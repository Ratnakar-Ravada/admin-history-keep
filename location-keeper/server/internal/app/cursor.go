package app

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"
)

type Cursor struct {
	Version  int       `json:"v"`
	Subject  string    `json:"sub"`
	Scope    string    `json:"scope"`
	Filter   string    `json:"filter"`
	LastTime string    `json:"last_time"`
	LastID   string    `json:"last_id"`
	Read     ReadPoint `json:"read"`
	Expiry   int64     `json:"exp"`
}
type Signer struct {
	Key []byte
	TTL time.Duration
}

func (s Signer) Encode(c Cursor) string {
	b, _ := json.Marshal(c)
	payload := base64.RawURLEncoding.EncodeToString(b)
	m := hmac.New(sha256.New, s.Key)
	m.Write([]byte(payload))
	return payload + "." + hex.EncodeToString(m.Sum(nil))
}
func (s Signer) Decode(token string, now time.Time) (Cursor, error) {
	var c Cursor
	fail := APIError{400, "invalid_cursor", "The page cursor is invalid. Restart the event list."}
	if len(token) > 4096 {
		return c, fail
	}
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return c, fail
	}
	sig, err := hex.DecodeString(parts[1])
	if err != nil {
		return c, fail
	}
	m := hmac.New(sha256.New, s.Key)
	m.Write([]byte(parts[0]))
	if !hmac.Equal(sig, m.Sum(nil)) {
		return c, fail
	}
	b, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil || json.Unmarshal(b, &c) != nil || c.Version != 1 || c.Read.ID == "" || c.Read.At == "" || (c.LastTime == "") != (c.LastID == "") || (c.LastTime != "" && ParseTime(c.LastTime).IsZero()) {
		return c, fail
	}
	if now.Unix() >= c.Expiry {
		return c, APIError{409, "cursor_expired", "This browsing session expired. Restart with the same filters."}
	}
	return c, nil
}
func ScopeHash(subject string, devices []Device) string {
	ids := []string{}
	for _, d := range devices {
		ids = append(ids, d.ID)
	}
	b, _ := json.Marshal(struct {
		Subject string
		Devices []string
	}{subject, ids})
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
