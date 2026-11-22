package app

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type AppSession struct {
	ID, UserID, Token string
	Advanced          bool
	Expires           int64
}

// Remembered sessions expire after inactivity and refresh at most once a day.
// The cookie lifetime stays within browsers' 400-day persistent-cookie limit.
const SessionLifetime = 400 * 24 * time.Hour
const SessionRenewalInterval = 24 * time.Hour

func sessionNeedsRenewal(expires int64, now time.Time) bool {
	return expires <= now.Add(SessionLifetime-SessionRenewalInterval).Unix()
}

type ClientInfo struct{ Browser, OS, Device string }
type UsageRecord struct {
	ID         string    `json:"id"`
	UserID     string    `json:"user_id"`
	SessionID  string    `json:"session_id"`
	At         time.Time `json:"occurred_at"`
	Method     string    `json:"method"`
	Route      string    `json:"route"`
	Service    string    `json:"service"`
	View       string    `json:"app_view"`
	Background bool      `json:"is_background"`
	Status     int       `json:"status_code"`
	Duration   int64     `json:"duration_ms"`
	Bytes      int64     `json:"response_bytes"`
	Active     bool      `json:"-"`
	ViewEvent  bool      `json:"-"`
}
type AnalyticsQuery struct {
	Days                               int
	UserID, SessionID, Service, Cursor string
}
type UsageStore interface {
	Start(context.Context, Identity, ClientInfo, time.Time) (AppSession, error)
	Validate(context.Context, string, string, time.Time) (AppSession, error)
	End(context.Context, string, time.Time) error
	Write(context.Context, []UsageRecord) error
	Overview(context.Context, int, time.Time) (map[string]any, error)
	Page(context.Context, string, AnalyticsQuery, time.Time) (map[string]any, error)
}
type Usage struct {
	Store    UsageStore
	queue    chan UsageRecord
	stop     chan struct{}
	done     chan struct{}
	once     sync.Once
	Dropped  atomic.Int64
	Failures atomic.Int64
}

func NewUsage(store UsageStore) *Usage {
	u := &Usage{Store: store, queue: make(chan UsageRecord, 2048), stop: make(chan struct{}), done: make(chan struct{})}
	go u.worker()
	return u
}
func (u *Usage) Add(r UsageRecord) {
	if r.ID == "" {
		r.ID = newUUID()
	}
	select {
	case u.queue <- r:
	default:
		u.Dropped.Add(1)
	}
}
func (u *Usage) Close() { u.once.Do(func() { close(u.stop); <-u.done }) }
func (u *Usage) worker() {
	defer close(u.done)
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	batch := make([]UsageRecord, 0, 100)
	flush := func() {
		if len(batch) == 0 {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		err := u.Store.Write(ctx, batch)
		if err != nil && ctx.Err() == nil {
			// Writes use stable record IDs and monotonic activity timestamps.
			// A partial write can safely be retried without double counting.
			err = u.Store.Write(ctx, batch)
		}
		if err != nil {
			u.Failures.Add(1)
			u.Dropped.Add(int64(len(batch)))
			log.Print("Usage batch could not be saved; check analytics database permissions/connectivity")
		}
		batch = batch[:0]
	}
	for {
		select {
		case r := <-u.queue:
			batch = append(batch, r)
			if len(batch) >= 100 {
				flush()
			}
		case <-ticker.C:
			flush()
		case <-u.stop:
			for {
				select {
				case r := <-u.queue:
					batch = append(batch, r)
				default:
					flush()
					return
				}
			}
		}
	}
}
func newUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&15 | 64
	b[8] = b[8]&63 | 128
	s := hex.EncodeToString(b[:])
	return s[:8] + "-" + s[8:12] + "-" + s[12:16] + "-" + s[16:20] + "-" + s[20:]
}
func sessionHash(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}
func clientInfo(ua string) ClientInfo {
	c := ClientInfo{Browser: "Other", OS: "Other", Device: "desktop"}
	for _, v := range []struct{ needle, name string }{{"Edg/", "Edge"}, {"Firefox/", "Firefox"}, {"Chrome/", "Chrome"}, {"Safari/", "Safari"}} {
		if strings.Contains(ua, v.needle) {
			c.Browser = v.name
			break
		}
	}
	for _, v := range []struct{ needle, name string }{{"Android", "Android"}, {"iPhone", "iOS"}, {"iPad", "iPadOS"}, {"Windows", "Windows"}, {"Macintosh", "macOS"}, {"Linux", "Linux"}} {
		if strings.Contains(ua, v.needle) {
			c.OS = v.name
			break
		}
	}
	if strings.Contains(ua, "iPad") || strings.Contains(ua, "Tablet") {
		c.Device = "tablet"
	} else if strings.Contains(ua, "Mobile") || strings.Contains(ua, "iPhone") {
		c.Device = "mobile"
	}
	return c
}

type usageContextKey struct{}
type usageScope struct {
	UserID, SessionID string
	Active            bool
}

func markUsage(r *http.Request, u Identity) {
	if scope, ok := r.Context().Value(usageContextKey{}).(*usageScope); ok {
		scope.UserID = u.UserID
		scope.SessionID = u.SessionID
	}
}
func validView(s string) string {
	switch s {
	case "live", "journeys", "logs", "alerts", "analytics", "calls", "notifications", "app-usage":
		return s
	default:
		return "unknown"
	}
}
func serviceFor(route string) string {
	switch {
	case strings.Contains(route, "/api/usage/"):
		return "usage"
	case strings.Contains(route, "/api/analytics/"):
		return "analytics"
	case strings.Contains(route, "/api/history/calls"):
		return "calls"
	case strings.Contains(route, "/api/history/notifications"):
		return "notifications"
	case strings.Contains(route, "/api/history/app-usage"):
		return "app_usage"
	case strings.Contains(route, "/api/places/"):
		return "place_names"
	case strings.Contains(route, "/email/") || strings.Contains(route, "/auth/gmail/"):
		return "email"
	case strings.Contains(route, "/locations/latest"):
		return "live_locations"
	case strings.Contains(route, "/locations/history") || strings.Contains(route, "/activity/summary"):
		return "journeys"
	case strings.Contains(route, "/events"):
		return "activity_logs"
	case strings.Contains(route, "/devices"):
		return "devices"
	case strings.Contains(route, "/auth/") || strings.Contains(route, "/api/session"):
		return "authentication"
	default:
		return "other"
	}
}

type usageWriter struct {
	http.ResponseWriter
	status int
	bytes  int64
}

func (w *usageWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *usageWriter) WriteHeader(code int) {
	if w.status != 0 {
		return
	}
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}
func (w *usageWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(200)
	}
	n, e := w.ResponseWriter.Write(b)
	w.bytes += int64(n)
	return n, e
}
func (u *Usage) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/") && !strings.HasPrefix(r.URL.Path, "/auth/") {
			next.ServeHTTP(w, r)
			return
		}
		start := time.Now()
		scope := &usageScope{}
		r = r.WithContext(context.WithValue(r.Context(), usageContextKey{}, scope))
		rw := &usageWriter{ResponseWriter: w}
		defer func() {
			route := r.Pattern
			if len(route) == 0 || strings.HasSuffix(route, "/api/") {
				route = r.Method + " /api/unknown"
			}
			if len(route) > 100 {
				route = "unknown"
			}
			status := rw.status
			if status == 0 {
				status = 200
			}
			background := r.Header.Get("X-App-Source") == "background" || r.URL.Path == "/api/session" || r.URL.Path == "/api/events/changes" || strings.HasPrefix(r.URL.Path, "/api/usage/")
			u.Add(UsageRecord{UserID: scope.UserID, SessionID: scope.SessionID, At: start.UTC(), Method: r.Method, Route: route, Service: serviceFor(route), View: validView(r.Header.Get("X-App-View")), Background: background, Status: status, Duration: time.Since(start).Milliseconds(), Bytes: rw.bytes, Active: scope.Active || !background && scope.UserID != ""})
		}()
		next.ServeHTTP(rw, r)
	})
}
func (s *Server) usageHeartbeat(w http.ResponseWriter, r *http.Request, u Identity) {
	var body struct {
		Active *bool `json:"active"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 256)).Decode(&body) != nil || body.Active == nil {
		fail(w, APIError{400, "invalid_usage", "A heartbeat active flag is required."})
		return
	}
	if scope, ok := r.Context().Value(usageContextKey{}).(*usageScope); ok {
		scope.Active = *body.Active
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}
func (s *Server) usageView(w http.ResponseWriter, r *http.Request, u Identity) {
	var body struct {
		View string `json:"view"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 256)).Decode(&body) != nil || validView(body.View) == "unknown" {
		fail(w, APIError{400, "invalid_usage", "Choose a supported app view."})
		return
	}
	if s.Usage != nil {
		s.Usage.Add(UsageRecord{UserID: u.UserID, SessionID: u.SessionID, At: s.Now().UTC(), View: body.View, Active: true, ViewEvent: true})
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}
func (s *Server) analyticsQuery(r *http.Request) (AnalyticsQuery, error) {
	q := AnalyticsQuery{Days: 7, UserID: r.URL.Query().Get("user_id"), SessionID: r.URL.Query().Get("session_id"), Service: r.URL.Query().Get("service"), Cursor: r.URL.Query().Get("cursor")}
	if v := r.URL.Query().Get("days"); v != "" {
		var e error
		q.Days, e = strconv.Atoi(v)
		if e != nil {
			return q, errors.New("Invalid analytics period")
		}
	}
	if q.Days < 1 || q.Days > 90 || len(q.Cursor) > 1000 || q.UserID != "" && !ValidIDs([]string{q.UserID}) || q.SessionID != "" && !ValidIDs([]string{q.SessionID}) {
		return q, errors.New("Choose a valid analytics range and user/session IDs")
	}
	switch q.Service {
	case "", "usage", "analytics", "place_names", "email", "live_locations", "journeys", "activity_logs", "devices", "authentication", "other", "calls", "notifications", "app_usage":
	default:
		return q, errors.New("Unknown service")
	}
	return q, nil
}
func (s *Server) analyticsOverview(w http.ResponseWriter, r *http.Request, u Identity) {
	if !u.Advanced {
		fail(w, APIError{403, "advanced_required", "Advanced access is required to view app analytics."})
		return
	}
	if s.Usage == nil {
		fail(w, errors.New("Analytics are not configured"))
		return
	}
	q, e := s.analyticsQuery(r)
	if e != nil {
		fail(w, APIError{400, "invalid_analytics", e.Error()})
		return
	}
	data, e := s.Usage.Store.Overview(r.Context(), q.Days, s.Now())
	if e != nil {
		fail(w, e)
		return
	}
	data["pending_records"] = len(s.Usage.queue)
	data["dropped_records"] = s.Usage.Dropped.Load()
	data["write_failures"] = s.Usage.Failures.Load()
	writeJSON(w, 200, data)
}
func (s *Server) analyticsPage(kind string) func(http.ResponseWriter, *http.Request, Identity) {
	return func(w http.ResponseWriter, r *http.Request, u Identity) {
		if !u.Advanced {
			fail(w, APIError{403, "advanced_required", "Advanced access is required to view app analytics."})
			return
		}
		if s.Usage == nil {
			fail(w, errors.New("Analytics are not configured"))
			return
		}
		q, e := s.analyticsQuery(r)
		if e != nil {
			fail(w, APIError{400, "invalid_analytics", e.Error()})
			return
		}
		data, e := s.Usage.Store.Page(r.Context(), kind, q, s.Now())
		if e != nil {
			fail(w, e)
			return
		}
		writeJSON(w, 200, data)
	}
}
