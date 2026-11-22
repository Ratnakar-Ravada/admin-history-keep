package app

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Unlocks belong to a revocable app session. Their version changes whenever the
// phone changes its tracking email or PIN, including when the same PIN is salted again.
type DeviceAccessStore interface {
	Unlocked(context.Context, Identity, []Device) (map[string]bool, error)
	ReserveAttempt(context.Context, string, time.Time) error
	Remember(context.Context, Identity, Device, time.Time) error
}

func deviceAccessVersion(secret []byte, d Device) string {
	m := hmac.New(sha256.New, secret)
	m.Write([]byte("device-access:v1\x00" + strings.ToLower(d.TrackingEmail) + "\x00" + d.PINHash))
	return hex.EncodeToString(m.Sum(nil))
}

func pinRateLimited() APIError {
	return APIError{429, "pin_rate_limited", "Too many PIN attempts. Try again in fifteen minutes."}
}

func (s *Server) accessibleDevices(ctx context.Context, u Identity) ([]Device, error) {
	devices, err := s.Repo.Devices(ctx, u)
	if err != nil {
		return nil, err
	}
	if u.Advanced {
		for i := range devices {
			devices[i].Locked, devices[i].PINSetupRequired = false, false
		}
		return devices, nil
	}
	if s.Access == nil || u.SessionID == "" {
		return nil, APIError{503, "device_access_unavailable", "Device access is temporarily unavailable. Please retry."}
	}
	// Enforce ownership here as well as in SQL, so every repository fails closed.
	owned := []Device{}
	for _, d := range devices {
		if u.EmailVerified && u.Email != "" && strings.EqualFold(d.TrackingEmail, u.Email) && !d.Revoked {
			owned = append(owned, d)
		}
	}
	unlocked, err := s.Access.Unlocked(ctx, u, owned)
	if err != nil {
		return nil, err
	}
	for i := range owned {
		owned[i].PINSetupRequired = owned[i].PINHash == ""
		owned[i].Locked = owned[i].PINSetupRequired || !unlocked[owned[i].ID]
	}
	return owned, nil
}

// Argon2 uses about 19 MiB per request; bound concurrent work across devices.
var devicePINSlots = make(chan struct{}, 4)

func (s *Server) unlockDevice(w http.ResponseWriter, r *http.Request, u Identity) {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		fail(w, APIError{415, "json_required", "Use application/json."})
		return
	}
	var body struct {
		DeviceID string `json:"device_id"`
		PIN      string `json:"pin"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 512))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&body) != nil || decoder.Decode(&struct{}{}) != io.EOF || !ValidIDs([]string{body.DeviceID}) || len(body.PIN) != 4 || strings.IndexFunc(body.PIN, func(r rune) bool { return r < '0' || r > '9' }) >= 0 {
		fail(w, APIError{400, "invalid_pin", "Choose a device and enter its four-digit PIN."})
		return
	}
	devices, err := s.accessibleDevices(r.Context(), u)
	if err != nil {
		fail(w, err)
		return
	}
	var selected *Device
	for i := range devices {
		if devices[i].ID == body.DeviceID {
			selected = &devices[i]
			break
		}
	}
	if selected == nil {
		fail(w, APIError{403, "access_denied", "This device is not available to this account."})
		return
	}
	if !selected.Locked {
		writeJSON(w, 200, map[string]any{"device_id": selected.ID, "unlocked": true})
		return
	}
	if selected.PINSetupRequired {
		fail(w, APIError{403, "pin_setup_required", "Complete this device's email and PIN setup in the mobile app."})
		return
	}
	if len(s.Config.PINPepper) < 32 {
		fail(w, APIError{503, "pin_verification_unavailable", "PIN verification is temporarily unavailable. Please retry later."})
		return
	}
	if err = s.Access.ReserveAttempt(r.Context(), selected.ID, s.Now()); err != nil {
		var apiErr APIError
		if errors.As(err, &apiErr) && apiErr.Status == 429 {
			w.Header().Set("Retry-After", "900")
		}
		fail(w, err)
		return
	}
	select {
	case devicePINSlots <- struct{}{}:
		defer func() { <-devicePINSlots }()
	case <-r.Context().Done():
		fail(w, r.Context().Err())
		return
	}
	if !VerifyDevicePIN(body.PIN, selected.PINHash, s.Config.PINPepper) {
		fail(w, APIError{403, "incorrect_pin", "That PIN is incorrect. Please try again."})
		return
	}
	if err = s.Access.Remember(r.Context(), u, *selected, s.Now()); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"device_id": selected.ID, "unlocked": true})
}

type pinAttempt struct {
	Count   int
	Started time.Time
}

type MemoryDeviceAccessStore struct {
	mu       sync.Mutex
	secret   []byte
	unlocks  map[string]map[string]string
	attempts map[string]pinAttempt
}

func NewMemoryDeviceAccessStore(secret []byte) *MemoryDeviceAccessStore {
	return &MemoryDeviceAccessStore{secret: secret, unlocks: map[string]map[string]string{}, attempts: map[string]pinAttempt{}}
}

func (m *MemoryDeviceAccessStore) Unlocked(_ context.Context, u Identity, devices []Device) (map[string]bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[string]bool{}
	for _, d := range devices {
		out[d.ID] = hmac.Equal([]byte(m.unlocks[u.SessionID][d.ID]), []byte(deviceAccessVersion(m.secret, d)))
	}
	return out, nil
}

func (m *MemoryDeviceAccessStore) ReserveAttempt(_ context.Context, deviceID string, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	a := m.attempts[deviceID]
	if a.Started.IsZero() || !now.Before(a.Started.Add(15*time.Minute)) {
		a = pinAttempt{Started: now}
	}
	if a.Count >= 5 {
		return pinRateLimited()
	}
	a.Count++
	m.attempts[deviceID] = a
	return nil
}

func (m *MemoryDeviceAccessStore) Remember(_ context.Context, u Identity, d Device, _ time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.unlocks[u.SessionID] == nil {
		m.unlocks[u.SessionID] = map[string]string{}
	}
	m.unlocks[u.SessionID][d.ID] = deviceAccessVersion(m.secret, d)
	return nil
}
