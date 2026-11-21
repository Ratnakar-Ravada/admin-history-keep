package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

// DeviceHistoryRecord preserves the phone's distinct recorded, observed and
// database receipt timestamps. Metadata values are strings or null, as uploaded.
type DeviceHistoryRecord struct {
	ID            string             `json:"event_id"`
	DeviceID      string             `json:"device_id"`
	Recorded      string             `json:"recorded_at"`
	Observed      string             `json:"observed_at"`
	Received      string             `json:"received_at"`
	EventType     string             `json:"event_type"`
	SourcePackage string             `json:"source_package,omitempty"`
	CallID        string             `json:"call_id,omitempty"`
	SourceID      string             `json:"source_id,omitempty"`
	SourceKey     string             `json:"source_key,omitempty"`
	Fields        map[string]*string `json:"fields"`
}

type DeviceHistoryFilter struct {
	Filter
	Kind      string `json:"kind"`
	EventType string `json:"event_type"`
	Search    string `json:"q"`
}

func (f DeviceHistoryFilter) Hash() string {
	b, _ := json.Marshal(f)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

type DeviceHistoryPage struct {
	Items   []DeviceHistoryRecord `json:"items"`
	Start   *string               `json:"page_cursor"`
	Next    *string               `json:"next_cursor"`
	More    bool                  `json:"has_more"`
	AsOf    string                `json:"as_of"`
	Expires string                `json:"expires_at"`
}

type DeviceHistoryRepository interface {
	DeviceHistory(context.Context, DeviceHistoryFilter, ReadPoint, Cursor, int) ([]DeviceHistoryRecord, error)
}

func historyUnavailable() APIError {
	return APIError{503, "history_unavailable", "This history is unavailable. Check that its history-keep table exists and the admin API database account has SELECT access, then retry."}
}

func (s *Server) deviceHistory(kind string) func(http.ResponseWriter, *http.Request, Identity) {
	return func(w http.ResponseWriter, r *http.Request, u Identity) {
		base, devices, err := s.filter(r, u)
		if err != nil {
			fail(w, err)
			return
		}
		f := DeviceHistoryFilter{Filter: base, Kind: kind, EventType: r.URL.Query().Get("event_type"), Search: strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))}
		allowed := map[string][]string{"calls": {"1", "2", "3", "4", "5", "6", "7"}, "notifications": {"active", "posted", "removed"}, "app-usage": {"daily", "session"}}
		if utf8.RuneCountInString(f.Search) > 200 || f.EventType != "" && !slices.Contains(allowed[kind], f.EventType) || len(base.Reasons)+len(base.Severity)+len(base.Outcomes)+len(base.Statuses)+len(base.Transports) > 0 {
			fail(w, APIError{400, "invalid_filters", "Choose a supported history type and search text up to 200 characters."})
			return
		}
		now := s.Now()
		c := Cursor{Version: 1, Subject: u.Subject, Scope: ScopeHash(u.Subject, devices), Filter: f.Hash(), Expiry: now.Add(s.Signer.TTL).Unix()}
		if token := r.URL.Query().Get("cursor"); token != "" {
			previous, e := s.Signer.Decode(token, now)
			if e != nil {
				fail(w, e)
				return
			}
			if previous.Subject != c.Subject || previous.Scope != c.Scope || previous.Filter != c.Filter {
				fail(w, APIError{409, "cursor_scope_changed", "The filters or access permissions changed. Restart this history list."})
				return
			}
			c = previous
		} else if len(f.DeviceIDs) > 0 {
			c.Read, err = s.Repo.Snapshot(r.Context())
			if err != nil {
				fail(w, err)
				return
			}
		} else {
			c.Read.At = FormatTime(now)
		}
		rows := []DeviceHistoryRecord{}
		if len(f.DeviceIDs) > 0 {
			repo, ok := s.Repo.(DeviceHistoryRepository)
			if !ok {
				fail(w, historyUnavailable())
				return
			}
			rows, err = repo.DeviceHistory(r.Context(), f, c.Read, c, PageSize+1)
			if err != nil {
				fail(w, err)
				return
			}
		}
		var start, next *string
		if c.Read.ID != "" {
			start = Ptr(s.Signer.Encode(c))
		}
		more := len(rows) > PageSize
		if more {
			rows = rows[:PageSize]
			last := rows[len(rows)-1]
			c.LastTime, c.LastID = last.Recorded, last.ID
			next = Ptr(s.Signer.Encode(c))
		}
		writeJSON(w, 200, DeviceHistoryPage{Items: rows, Start: start, Next: next, More: more, AsOf: c.Read.At, Expires: FormatTime(time.Unix(c.Expiry, 0))})
	}
}

func historyMatch(e DeviceHistoryRecord, f DeviceHistoryFilter) bool {
	if !slices.Contains(f.DeviceIDs, e.DeviceID) || e.Recorded < f.From || e.Recorded >= f.To || f.EventType != "" && e.EventType != f.EventType {
		return false
	}
	if f.Search == "" {
		return true
	}
	values := []string{e.SourcePackage}
	for _, key := range historySearchFields(f.Kind) {
		value := e.Fields[key]
		if value != nil && strings.Contains(strings.ToLower(*value), f.Search) {
			return true
		}
		if value != nil {
			values = append(values, *value)
		}
	}
	return strings.Contains(strings.ToLower(strings.Join(values, " ")), f.Search)
}
