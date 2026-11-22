package app

import (
	"context"
	"crypto/hmac"
	"database/sql"
	"errors"
	"time"
)

// Writes use APP_DB_DSN and app-owned tables; tracker data remains SELECT-only.
type SQLDeviceAccessStore struct {
	DB     *sql.DB
	Secret []byte
}

func NewSQLDeviceAccessStore(ctx context.Context, db *sql.DB, secret []byte) (*SQLDeviceAccessStore, error) {
	var count int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM information_schema.TABLES WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME IN ('app_device_unlocks','app_device_pin_attempts')").Scan(&count); err != nil || count != 2 {
		return nil, errors.New("Device access tables are missing; run go run ./cmd/migrateapp -apply")
	}
	return &SQLDeviceAccessStore{DB: db, Secret: secret}, nil
}

func (s *SQLDeviceAccessStore) Unlocked(ctx context.Context, u Identity, devices []Device) (map[string]bool, error) {
	out := map[string]bool{}
	if len(devices) == 0 {
		return out, nil
	}
	rows, err := s.DB.QueryContext(ctx, "SELECT device_id,pin_version FROM app_device_unlocks WHERE session_id=?", u.SessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	versions := map[string]string{}
	for rows.Next() {
		var deviceID, version string
		if err := rows.Scan(&deviceID, &version); err != nil {
			return nil, err
		}
		versions[deviceID] = version
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, d := range devices {
		out[d.ID] = hmac.Equal([]byte(versions[d.ID]), []byte(deviceAccessVersion(s.Secret, d)))
	}
	return out, nil
}

func (s *SQLDeviceAccessStore) ReserveAttempt(ctx context.Context, deviceID string, now time.Time) error {
	nonce := newUUID()
	cutoff := now.Add(-15 * time.Minute).UnixMilli()
	// Set the nonce before incrementing attempts: SQL assignments evaluate left to
	// right. Only the first five reservations in a window can acquire a nonce.
	_, err := s.DB.ExecContext(ctx, `INSERT INTO app_device_pin_attempts(device_id,attempts,window_started_ms,attempt_nonce) VALUES(?,1,?,?)
ON DUPLICATE KEY UPDATE
attempt_nonce=IF(window_started_ms<=? OR attempts<5,VALUES(attempt_nonce),attempt_nonce),
attempts=IF(window_started_ms<=?,1,LEAST(attempts+1,5)),
window_started_ms=IF(window_started_ms<=?,VALUES(window_started_ms),window_started_ms)`, deviceID, now.UnixMilli(), nonce, cutoff, cutoff, cutoff)
	if err != nil {
		return err
	}
	var saved string
	if err := s.DB.QueryRowContext(ctx, "SELECT attempt_nonce FROM app_device_pin_attempts WHERE device_id=?", deviceID).Scan(&saved); err != nil {
		return err
	}
	if saved != nonce {
		return pinRateLimited()
	}
	return nil
}

func (s *SQLDeviceAccessStore) Remember(ctx context.Context, u Identity, d Device, now time.Time) error {
	_, err := s.DB.ExecContext(ctx, "INSERT INTO app_device_unlocks(session_id,device_id,pin_version,unlocked_at) VALUES(?,?,?,?) ON DUPLICATE KEY UPDATE pin_version=VALUES(pin_version),unlocked_at=VALUES(unlocked_at)", u.SessionID, d.ID, deviceAccessVersion(s.Secret, d), now.UTC())
	return err
}
