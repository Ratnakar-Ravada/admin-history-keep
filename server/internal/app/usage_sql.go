package app

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

type SQLUsageStore struct {
	DB     *sql.DB
	Secret []byte
}

func NewSQLUsageStore(ctx context.Context, db *sql.DB, secret []byte) (*SQLUsageStore, error) {
	var count int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM information_schema.TABLES WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME IN ('app_users','app_sessions','app_api_requests','app_view_events')").Scan(&count); err != nil || count != 4 {
		return nil, errors.New("App analytics tables are missing; run go run ./cmd/migrateapp -apply")
	}
	return &SQLUsageStore{db, secret}, nil
}

func (s *SQLUsageStore) Start(ctx context.Context, u Identity, c ClientInfo, now time.Time) (AppSession, error) {
	result := AppSession{ID: newUUID(), Token: randomToken(), Expires: u.Expires}
	expires := time.Unix(u.Expires, 0).UTC()
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	// Advanced access is managed directly in TiDB. New users use the schema's
	// FALSE default; profile updates must preserve any manually assigned value.
	_, err = tx.ExecContext(ctx, "INSERT INTO app_users(id,google_subject,email,display_name,hosted_domain,created_at,last_login_at,last_seen_at) VALUES(?,?,?,?,?,?,?,?) ON DUPLICATE KEY UPDATE email=VALUES(email),display_name=VALUES(display_name),hosted_domain=VALUES(hosted_domain),last_login_at=VALUES(last_login_at),last_seen_at=VALUES(last_seen_at)", newUUID(), u.Subject, u.Email, bounded(u.Name, 255), bounded(u.HostedDomain, 255), now, now, now)
	if err != nil {
		return result, err
	}
	var disabled bool
	err = tx.QueryRowContext(ctx, "SELECT id,is_advanced,is_disabled FROM app_users WHERE google_subject=?", u.Subject).Scan(&result.UserID, &result.Advanced, &disabled)
	if err != nil {
		return result, err
	}
	if disabled {
		return result, APIError{403, "account_disabled", "This app account is disabled."}
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO app_sessions(id,user_id,token_hash,started_at,last_seen_at,last_active_at,expires_at,browser,operating_system,device_type) VALUES(?,?,?,?,?,?,?,?,?,?)", result.ID, result.UserID, sessionHash(result.Token), now, now, now, expires, c.Browser, c.OS, c.Device)
	if err != nil {
		return result, err
	}
	err = tx.Commit()
	return result, err
}
func bounded(v string, n int) string {
	r := []rune(v)
	if len(r) > n {
		return string(r[:n])
	}
	return v
}
func (s *SQLUsageStore) Validate(ctx context.Context, subject, token string, now time.Time) (AppSession, error) {
	result, err := s.readSession(ctx, subject, token, now)
	if err != nil || !sessionNeedsRenewal(result.Expires, now) {
		return result, err
	}
	// Renew only an unexpired, non-revoked session belonging to an enabled
	// account. Concurrent renewals cannot shorten its lifetime or undo logout.
	_, err = s.DB.ExecContext(ctx, "UPDATE app_sessions s JOIN app_users u ON u.id=s.user_id SET s.expires_at=GREATEST(s.expires_at,?) WHERE s.id=? AND s.token_hash=? AND u.google_subject=? AND s.ended_at IS NULL AND s.expires_at>? AND u.is_disabled=0", now.Add(SessionLifetime).UTC(), result.ID, sessionHash(token), subject, now)
	if err != nil {
		return AppSession{}, err
	}
	return s.readSession(ctx, subject, token, now)
}

func (s *SQLUsageStore) readSession(ctx context.Context, subject, token string, now time.Time) (AppSession, error) {
	var result AppSession
	var disabled bool
	var expires string
	err := s.DB.QueryRowContext(ctx, "SELECT s.id,u.id,u.is_advanced,u.is_disabled,"+dateSQL("s.expires_at")+" FROM app_sessions s JOIN app_users u ON u.id=s.user_id WHERE s.token_hash=? AND u.google_subject=? AND s.ended_at IS NULL AND s.expires_at>?", sessionHash(token), subject, now).Scan(&result.ID, &result.UserID, &result.Advanced, &disabled, &expires)
	if errors.Is(err, sql.ErrNoRows) || disabled {
		return result, APIError{401, "session_expired", "Your app session ended. Sign in again."}
	}
	if err == nil {
		result.Expires = ParseTime(expires).Unix()
		if result.Expires <= now.Unix() {
			return AppSession{}, APIError{401, "session_expired", "Your app session ended. Sign in again."}
		}
	}
	return result, err
}
func (s *SQLUsageStore) End(ctx context.Context, id string, now time.Time) error {
	_, err := s.DB.ExecContext(ctx, "UPDATE app_sessions SET ended_at=?,end_reason='logout',last_seen_at=GREATEST(last_seen_at,?) WHERE id=? AND ended_at IS NULL", now, now, id)
	return err
}
func nullableID(v string) any {
	if v == "" {
		return nil
	}
	return v
}
func (s *SQLUsageStore) Write(ctx context.Context, records []UsageRecord) error {
	requests, views := []UsageRecord{}, []UsageRecord{}
	users, sessions, active := map[string]time.Time{}, map[string]time.Time{}, map[string]time.Time{}
	latest := func(m map[string]time.Time, k string, t time.Time) {
		if k != "" && t.After(m[k]) {
			m[k] = t
		}
	}
	for _, r := range records {
		if r.ViewEvent {
			if r.UserID != "" && r.SessionID != "" {
				views = append(views, r)
			}
		} else {
			requests = append(requests, r)
		}
		latest(users, r.UserID, r.At)
		latest(sessions, r.SessionID, r.At)
		if r.Active {
			latest(active, r.SessionID, r.At)
		}
	}
	if len(requests) > 0 {
		values, args := []string{}, []any{}
		for _, r := range requests {
			values = append(values, "(?,?,?,?,?,?,?,?,?,?,?,?)")
			args = append(args, r.ID, nullableID(r.UserID), nullableID(r.SessionID), r.At, r.Method, r.Route, r.Service, r.View, r.Background, r.Status, r.Duration, r.Bytes)
		}
		if _, err := s.DB.ExecContext(ctx, "INSERT IGNORE INTO app_api_requests(id,user_id,session_id,occurred_at,method,route,service,app_view,is_background,status_code,duration_ms,response_bytes) VALUES "+strings.Join(values, ","), args...); err != nil {
			return err
		}
	}
	if len(views) > 0 {
		values, args := []string{}, []any{}
		for _, r := range views {
			values = append(values, "(?,?,?,?,?)")
			args = append(args, r.ID, r.UserID, r.SessionID, r.At, r.View)
		}
		if _, err := s.DB.ExecContext(ctx, "INSERT IGNORE INTO app_view_events(id,user_id,session_id,occurred_at,app_view) VALUES "+strings.Join(values, ","), args...); err != nil {
			return err
		}
	}
	for _, update := range []struct {
		table, column string
		values        map[string]time.Time
	}{{"app_users", "last_seen_at", users}, {"app_sessions", "last_seen_at", sessions}, {"app_sessions", "last_active_at", active}} {
		if len(update.values) == 0 {
			continue
		}
		expr, args, ids := "CASE id", []any{}, []string{}
		for id, at := range update.values {
			expr += " WHEN ? THEN GREATEST(" + update.column + ",?)"
			args = append(args, id, at)
			ids = append(ids, id)
		}
		expr += " ELSE " + update.column + " END"
		args = append(args, stringsAny(ids)...)
		if _, err := s.DB.ExecContext(ctx, "UPDATE "+update.table+" SET "+update.column+"="+expr+" WHERE id IN ("+marks(len(ids))+")", args...); err != nil {
			return err
		}
	}
	return nil
}
func usageWindow(days int, now time.Time) (time.Time, time.Time) {
	now = now.UTC()
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, 1-days), now
}
func dateSQL(v string) string { return "DATE_FORMAT(" + v + ",'%Y-%m-%dT%H:%i:%s.%fZ')" }
func readMaps(ctx context.Context, db *sql.DB, query string, args ...any) ([]map[string]any, error) {
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	names, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	out := []map[string]any{}
	for rows.Next() {
		values := make([]any, len(names))
		dest := make([]any, len(names))
		for i := range values {
			dest[i] = &values[i]
		}
		if err = rows.Scan(dest...); err != nil {
			return nil, err
		}
		entry := map[string]any{}
		for i, name := range names {
			v := values[i]
			if b, ok := v.([]byte); ok {
				v = string(b)
			}
			entry[name] = v
		}
		out = append(out, entry)
	}
	return out, rows.Err()
}
func (s *SQLUsageStore) Overview(ctx context.Context, days int, now time.Time) (map[string]any, error) {
	from, to := usageWindow(days, now)
	out := map[string]any{"from": FormatTime(from), "to": FormatTime(to), "timezone": "UTC"}
	queries := []struct {
		name, query string
		args        []any
	}{
		{"totals", "SELECT COUNT(*) requests,COALESCE(SUM(is_background=0),0) interaction_requests,COALESCE(SUM(is_background=1),0) background_requests,COALESCE(SUM(status_code>=400),0) errors,COALESCE(AVG(duration_ms),0) average_ms,COUNT(DISTINCT user_id) active_users FROM app_api_requests WHERE occurred_at>=? AND occurred_at<=?", []any{from, to}},
		{"accounts", "SELECT COUNT(*) registered_users,COALESCE(SUM(created_at>=?),0) new_users FROM app_users", []any{from}},
		{"sessions", "SELECT COUNT(*) sessions,COALESCE(SUM(ended_at IS NULL AND expires_at>? AND last_active_at>=?),0) active_sessions FROM app_sessions WHERE started_at>=? AND started_at<=?", []any{now, now.Add(-5 * time.Minute), from, to}},
		{"daily", "SELECT DATE(occurred_at) day,COUNT(*) requests,COALESCE(SUM(is_background=0),0) interaction_requests,COALESCE(SUM(status_code>=400),0) errors FROM app_api_requests WHERE occurred_at>=? AND occurred_at<=? GROUP BY day ORDER BY day", []any{from, to}},
		{"top_users", "SELECT u.id,u.email,u.display_name,COUNT(*) requests,SUM(r.is_background=0) interaction_requests,COUNT(DISTINCT r.session_id) sessions FROM app_api_requests r JOIN app_users u ON u.id=r.user_id WHERE r.occurred_at>=? AND r.occurred_at<=? GROUP BY u.id,u.email,u.display_name ORDER BY interaction_requests DESC,requests DESC,u.id LIMIT 10", []any{from, to}},
		{"routes", "SELECT method,route,service,COUNT(*) requests,SUM(status_code>=400) errors,AVG(duration_ms) average_ms,MAX(duration_ms) maximum_ms FROM app_api_requests WHERE occurred_at>=? AND occurred_at<=? GROUP BY method,route,service ORDER BY requests DESC,route LIMIT 20", []any{from, to}},
		{"services", "SELECT service,COUNT(*) requests,SUM(is_background=0) interaction_requests,SUM(status_code>=400) errors FROM app_api_requests WHERE occurred_at>=? AND occurred_at<=? GROUP BY service ORDER BY requests DESC", []any{from, to}},
		{"views", "SELECT app_view,COUNT(*) opens,COUNT(DISTINCT user_id) users FROM app_view_events WHERE occurred_at>=? AND occurred_at<=? GROUP BY app_view ORDER BY opens DESC", []any{from, to}},
	}
	for _, q := range queries {
		rows, err := readMaps(ctx, s.DB, q.query, q.args...)
		if err != nil {
			return nil, err
		}
		if q.name == "totals" || q.name == "accounts" || q.name == "sessions" {
			if len(rows) > 0 {
				out[q.name] = rows[0]
			} else {
				out[q.name] = map[string]any{}
			}
		} else {
			out[q.name] = rows
		}
	}
	return out, nil
}

type usageCursor struct {
	At, ID, Scope string
	Expiry        int64
}

func cursorScope(kind string, q AnalyticsQuery) string {
	raw, _ := json.Marshal([]any{kind, q.Days, q.UserID, q.SessionID, q.Service})
	h := sha256.Sum256(raw)
	return hex.EncodeToString(h[:])
}
func encodeUsageCursor(secret []byte, c usageCursor) string {
	raw, _ := json.Marshal(c)
	mac := hmac.New(sha256.New, secret)
	mac.Write(raw)
	return base64.RawURLEncoding.EncodeToString(raw) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
func decodeUsageCursor(secret []byte, token, scope string, now time.Time) (usageCursor, error) {
	var c usageCursor
	parts := strings.Split(token, ".")
	invalid := APIError{409, "cursor_expired", "Analytics browsing expired or filters changed. Refresh this list."}
	if len(parts) != 2 {
		return c, invalid
	}
	raw, e := base64.RawURLEncoding.DecodeString(parts[0])
	if e != nil {
		return c, invalid
	}
	sig, e := base64.RawURLEncoding.DecodeString(parts[1])
	if e != nil {
		return c, invalid
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write(raw)
	if !hmac.Equal(sig, mac.Sum(nil)) || json.Unmarshal(raw, &c) != nil || c.Scope != scope || c.Expiry <= now.Unix() || ParseTime(c.At).IsZero() || !ValidIDs([]string{c.ID}) {
		return c, invalid
	}
	return c, nil
}
func (s *SQLUsageStore) Page(ctx context.Context, kind string, q AnalyticsQuery, now time.Time) (map[string]any, error) {
	from, to := usageWindow(q.Days, now)
	where, args := "", []any{}
	query, order, column := "", "", ""
	scope := cursorScope(kind, q)
	switch kind {
	case "users":
		query = "SELECT u.id,u.email,u.display_name,u.is_advanced," + dateSQL("u.created_at") + " created_at," + dateSQL("u.last_login_at") + " last_login_at," + dateSQL("u.last_seen_at") + " last_seen_at,(SELECT COUNT(*) FROM app_api_requests r WHERE r.user_id=u.id AND r.occurred_at>=?) requests FROM app_users u"
		args = append(args, from)
		where = "1=1"
		column = "u.created_at"
		order = "u.created_at DESC,u.id DESC"
	case "sessions":
		query = "SELECT s.id,s.user_id,u.email,u.display_name,s.browser,s.operating_system,s.device_type," + dateSQL("s.started_at") + " started_at," + dateSQL("s.last_seen_at") + " last_seen_at," + dateSQL("s.last_active_at") + " last_active_at," + dateSQL("s.expires_at") + " expires_at," + dateSQL("s.ended_at") + " ended_at,s.end_reason,(SELECT COUNT(*) FROM app_api_requests r WHERE r.session_id=s.id) requests FROM app_sessions s JOIN app_users u ON u.id=s.user_id"
		where = "s.started_at>=? AND s.started_at<=?"
		args = append(args, from, to)
		column = "s.started_at"
		order = "s.started_at DESC,s.id DESC"
		if q.UserID != "" {
			where += " AND s.user_id=?"
			args = append(args, q.UserID)
		}
	case "requests":
		query = "SELECT r.id,r.user_id,r.session_id,COALESCE(u.email,'Anonymous') email," + dateSQL("r.occurred_at") + " occurred_at,r.method,r.route,r.service,r.app_view,r.is_background,r.status_code,r.duration_ms,r.response_bytes FROM app_api_requests r LEFT JOIN app_users u ON u.id=r.user_id"
		where = "r.occurred_at>=? AND r.occurred_at<=?"
		args = append(args, from, to)
		column = "r.occurred_at"
		order = "r.occurred_at DESC,r.id DESC"
		for _, filter := range []struct{ col, value string }{{"r.user_id", q.UserID}, {"r.session_id", q.SessionID}, {"r.service", q.Service}} {
			if filter.value != "" {
				where += " AND " + filter.col + "=?"
				args = append(args, filter.value)
			}
		}
	default:
		return nil, errors.New("Unknown analytics list")
	}
	alias := strings.Split(column, ".")[0]
	if q.Cursor != "" {
		c, e := decodeUsageCursor(s.Secret, q.Cursor, scope, now)
		if e != nil {
			return nil, e
		}
		where += " AND (" + column + "<? OR (" + column + "=? AND " + alias + ".id<?))"
		at := ParseTime(c.At)
		args = append(args, at, at, c.ID)
	}
	rows, err := readMaps(ctx, s.DB, query+" WHERE "+where+" ORDER BY "+order+" LIMIT 21", args...)
	if err != nil {
		return nil, err
	}
	more := len(rows) > 20
	if more {
		rows = rows[:20]
	}
	var next *string
	if more {
		last := rows[len(rows)-1]
		field := map[string]string{"users": "created_at", "sessions": "started_at", "requests": "occurred_at"}[kind]
		next = Ptr(encodeUsageCursor(s.Secret, usageCursor{At: fmt.Sprint(last[field]), ID: fmt.Sprint(last["id"]), Scope: scope, Expiry: now.Add(15 * time.Minute).Unix()}))
	}
	return map[string]any{"items": rows, "next_cursor": next, "has_more": more}, nil
}
