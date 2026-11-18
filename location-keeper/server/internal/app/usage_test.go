package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"locationkeeper/server/internal/database"
)

func trackedFixture(t *testing.T) (*Server, *MemoryUsageStore, *http.Cookie) {
	t.Helper()
	s, _, _ := fixture(t, 0)
	s.Auth.Demo = false
	s.Auth.Assigned = true
	s.Auth.AdminEmails = []string{"person@gmail.com"}
	s.Auth.Now = s.Now
	store := NewMemoryUsageStore(s.Config.Secret, false)
	usage := NewUsage(store)
	t.Cleanup(usage.Close)
	s.Usage, s.Auth.Usage = usage, usage
	u := Identity{Subject: "fixture-google-user", Email: "person@gmail.com", Name: "Fixture person", EmailVerified: true, Admin: true, Advanced: true, Expires: s.Now().Add(time.Hour).Unix()}
	state, err := store.Start(context.Background(), u, ClientInfo{Browser: "Chrome", OS: "macOS", Device: "desktop"}, s.Now())
	if err != nil {
		t.Fatal(err)
	}
	store.Users[u.Subject].Identity.Advanced = true // Simulate a role granted in TiDB.
	u.SessionToken = state.Token
	return s, store, &http.Cookie{Name: "lk_session", Value: s.Auth.pack(sessionPayload(u))}
}

func trackedRequest(s *Server, cookie *http.Cookie, method, path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Origin", s.Config.BaseURL)
	if cookie != nil {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	return w
}

func TestPersistentSessionRoleAndLogout(t *testing.T) {
	s, store, cookie := trackedFixture(t)
	store.mu.Lock()
	store.Users["fixture-google-user"].Identity.Advanced = true
	store.mu.Unlock()
	if w := trackedRequest(s, cookie, "GET", "/api/analytics/overview", ""); w.Code != 200 {
		t.Fatalf("database-granted advanced access rejected: %d", w.Code)
	}
	store.mu.Lock()
	store.Users["fixture-google-user"].Identity.Advanced = false
	store.mu.Unlock()
	for _, path := range []string{"/api/analytics/overview", "/api/analytics/users", "/api/analytics/sessions", "/api/analytics/requests"} {
		if w := trackedRequest(s, cookie, "GET", path, ""); w.Code != 403 {
			t.Fatalf("role revocation did not gate %s: %d", path, w.Code)
		}
	}
	w := trackedRequest(s, cookie, "GET", "/api/session", "")
	if strings.Contains(w.Body.String(), "\"is_advanced\":true") || strings.Contains(w.Body.String(), "\"sid\"") || strings.Contains(w.Body.String(), "token_hash") {
		t.Fatal("session exposes a secret or trusts a stale privilege")
	}
	if w = trackedRequest(s, cookie, "POST", "/auth/logout", ""); w.Code != 200 {
		t.Fatalf("logout: %d", w.Code)
	}
	if w = trackedRequest(s, cookie, "GET", "/api/devices", ""); w.Code != 401 {
		t.Fatal("old cookie accepted after logout")
	}
	legacy := Identity{Subject: "fixture-google-user", Email: "person@gmail.com", EmailVerified: true, Expires: s.Now().Add(time.Hour).Unix()}
	old := &http.Cookie{Name: "lk_session", Value: s.Auth.pack(sessionPayload(legacy))}
	if w = trackedRequest(s, old, "GET", "/api/devices", ""); w.Code != 401 {
		t.Fatal("stateless legacy cookie bypassed persistent session")
	}
}

func TestAdvancedAccessUsesDatabaseInsteadOfGoogleAdminEmails(t *testing.T) {
	s, store, cookie := trackedFixture(t)
	s.Auth.AdminEmails = nil
	for _, path := range []string{"/api/analytics/overview", "/api/analytics/users", "/api/analytics/sessions", "/api/analytics/requests"} {
		if w := trackedRequest(s, cookie, "GET", path, ""); w.Code != 200 {
			t.Fatalf("database-granted ordinary viewer rejected at %s: %d", path, w.Code)
		}
	}
	r := httptest.NewRequest("GET", "/api/session", nil)
	r.AddCookie(cookie)
	identity, err := s.Auth.Identity(r)
	if err != nil || identity.Admin || !identity.Advanced {
		t.Fatal("advanced role is coupled to Google administrator status", err)
	}
	// Sign-in refreshes the profile but cannot overwrite a manually granted role.
	identity.Advanced = false
	state, err := store.Start(context.Background(), identity, ClientInfo{}, s.Now())
	if err != nil || !state.Advanced {
		t.Fatal("sign-in reset a manually granted advanced role", err)
	}
	store.mu.Lock()
	store.Users[identity.Subject].Identity.Advanced = false
	store.mu.Unlock()
	identity.Advanced = true
	state, err = store.Start(context.Background(), identity, ClientInfo{}, s.Now())
	if err != nil || state.Advanced {
		t.Fatal("sign-in overwrote a manually revoked advanced role", err)
	}
}

func TestUsageRecordsNormalizedRoutesAndSeparatesBackground(t *testing.T) {
	s, store, cookie := trackedFixture(t)
	r := httptest.NewRequest("GET", "/api/devices?q=private-device-name", nil)
	r.AddCookie(cookie)
	r.Header.Set("Authorization", "Bearer must-not-record")
	r.Header.Set("X-App-View", "logs")
	s.Handler().ServeHTTP(httptest.NewRecorder(), r)
	trackedRequest(s, cookie, "GET", "/api/session", "")
	trackedRequest(s, cookie, "POST", "/api/usage/heartbeat", `{"active":true}`)
	trackedRequest(s, cookie, "POST", "/api/usage/view", `{"view":"journeys"}`)
	if w := trackedRequest(s, cookie, "POST", "/api/usage/view", `{"view":"private-arbitrary-string"}`); w.Code != 400 {
		t.Fatal("arbitrary view accepted")
	}
	for _, path := range []string{"/api/analytics/overview?days=7junk", "/api/analytics/requests?user_id=bad", "/api/analytics/requests?service=not-a-service"} {
		if w := trackedRequest(s, cookie, "GET", path, ""); w.Code != 400 {
			t.Fatalf("invalid filter accepted %s", path)
		}
	}
	s.Usage.Close()
	store.mu.Lock()
	defer store.mu.Unlock()
	manual, background, views := false, false, 0
	for _, rec := range store.Records {
		if rec.ViewEvent {
			views++
			if rec.View != "journeys" {
				t.Fatal("invalid view persisted")
			}
			continue
		}
		if rec.UserID == "" || rec.SessionID == "" {
			t.Fatal("authenticated API call missing actor")
		}
		if rec.Route == "GET /api/devices" {
			manual = !rec.Background && rec.Active && rec.View == "logs" && rec.Service == "devices" && rec.Status == 200
		}
		if rec.Route == "GET /api/session" {
			background = rec.Background && !rec.Active
		}
	}
	raw, _ := json.Marshal(store.Records)
	if !manual || !background || views != 1 {
		t.Fatalf("incorrect classification: manual=%v background=%v views=%d", manual, background, views)
	}
	for _, value := range []string{"private-device-name", "must-not-record", cookie.Value, "private-arbitrary-string"} {
		if strings.Contains(string(raw), value) {
			t.Fatal("sensitive request data stored in usage")
		}
	}
}

func TestUsageCursorPagingAndScope(t *testing.T) {
	s, store, _ := trackedFixture(t)
	store.mu.Lock()
	var session *memoryAppSession
	for _, state := range store.Sessions {
		session = state
	}
	records := []UsageRecord{}
	for i := 0; i < 45; i++ {
		records = append(records, UsageRecord{ID: newUUID(), UserID: session.UserID, SessionID: session.ID, At: s.Now().Add(-time.Duration(i/3) * time.Second), Method: "GET", Route: "GET /api/events", Service: "activity_logs", Status: 200})
	}
	store.mu.Unlock()
	if err := store.Write(context.Background(), records); err != nil {
		t.Fatal(err)
	}
	q := AnalyticsQuery{Days: 7, UserID: session.UserID, Service: "activity_logs"}
	seen := map[string]bool{}
	for page := 0; page < 3; page++ {
		result, err := store.Page(context.Background(), "requests", q, s.Now())
		if err != nil {
			t.Fatal(err)
		}
		rows := result["items"].([]map[string]any)
		want := 20
		if page == 2 {
			want = 5
		}
		if len(rows) != want {
			t.Fatalf("page size %d want %d", len(rows), want)
		}
		for _, row := range rows {
			id := fmt.Sprint(row["id"])
			if seen[id] {
				t.Fatal("duplicate across cursor boundary")
			}
			seen[id] = true
		}
		next := result["next_cursor"].(*string)
		if page < 2 {
			q.Cursor = *next
			changed := q
			changed.Days = 30
			if _, e := store.Page(context.Background(), "requests", changed, s.Now()); e == nil {
				t.Fatal("cursor reused under new filters")
			}
		}
	}
	if len(seen) != 45 {
		t.Fatal("lost rows")
	}
	if _, e := store.Page(context.Background(), "requests", q, s.Now().Add(16*time.Minute)); e == nil {
		t.Fatal("expired cursor accepted")
	}
	q.Cursor += "tampered"
	if _, e := store.Page(context.Background(), "requests", q, s.Now()); e == nil {
		t.Fatal("tampered cursor accepted")
	}
}

type retryUsageStore struct {
	*MemoryUsageStore
	calls int
}

func (s *retryUsageStore) Write(ctx context.Context, records []UsageRecord) error {
	s.calls++
	if s.calls == 1 {
		return errors.New("temporary fixture failure")
	}
	return s.MemoryUsageStore.Write(ctx, records)
}
func TestUsageFlushRetriesAndDrainsOnClose(t *testing.T) {
	store := &retryUsageStore{MemoryUsageStore: NewMemoryUsageStore([]byte("fixture"), false)}
	usage := NewUsage(store)
	for i := 0; i < 75; i++ {
		usage.Add(UsageRecord{At: time.Now(), Route: "GET /api/devices"})
	}
	usage.Close()
	if len(store.Records) != 75 || store.calls != 2 || usage.Dropped.Load() != 0 {
		t.Fatal("shutdown did not safely retry/drain usage")
	}
}

// This opt-in test writes and removes only its own synthetic app analytics rows.
func TestUsageSQLIntegration(t *testing.T) {
	if os.Getenv("RUN_APP_DB_INTEGRATION") != "1" {
		t.Skip("set RUN_APP_DB_INTEGRATION=1 and ENV_FILE to verify the configured app database")
	}
	if err := LoadEnv(os.Getenv("ENV_FILE")); err != nil {
		t.Fatal("cannot read environment file")
	}
	cfg, err := ConfigFromEnv()
	if err != nil {
		t.Fatal("invalid configuration")
	}
	db, err := database.Open(cfg.AppDBDSN, cfg.TiDBTransport)
	if err != nil {
		t.Fatal("cannot open app database")
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	store, err := NewSQLUsageStore(ctx, db, cfg.Secret)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	subject := "synthetic-usage-test-" + newUUID()
	identity := Identity{Subject: subject, Email: "usage-test@example.invalid", Name: "Temporary analytics verification", Admin: true, Advanced: true, Expires: now.Add(time.Hour).Unix()}
	defer func() {
		clean, stop := context.WithTimeout(context.Background(), 30*time.Second)
		defer stop()
		var userID string
		if e := db.QueryRowContext(clean, "SELECT id FROM app_users WHERE google_subject=?", subject).Scan(&userID); e != nil {
			return
		}
		for _, table := range []string{"app_view_events", "app_api_requests", "app_sessions"} {
			if _, e := db.ExecContext(clean, "DELETE FROM "+table+" WHERE user_id=?", userID); e != nil {
				t.Errorf("fixture cleanup failed for %s", table)
			}
		}
		if _, e := db.ExecContext(clean, "DELETE FROM app_users WHERE id=?", userID); e != nil {
			t.Error("fixture user cleanup failed")
		}
	}()
	state, err := store.Start(ctx, identity, ClientInfo{Browser: "Fixture", OS: "Fixture", Device: "desktop"}, now)
	if err != nil {
		t.Fatal("persistent sign-in:", err)
	}
	if state.Advanced {
		t.Fatal("new users must use the database FALSE default, including Google administrators")
	}
	if _, err = store.Validate(ctx, subject, state.Token, now); err != nil {
		t.Fatal("session validation:", err)
	}
	if _, err = db.ExecContext(ctx, "UPDATE app_users SET is_advanced=1 WHERE id=?", state.UserID); err != nil {
		t.Fatal("manual role grant failed")
	}
	granted, err := store.Validate(ctx, subject, state.Token, now)
	if err != nil || !granted.Advanced {
		t.Fatal("database grant was not reflected in existing session", err)
	}
	identity.Advanced = false
	second, err := store.Start(ctx, identity, ClientInfo{}, now)
	if err != nil || !second.Advanced || second.UserID != state.UserID {
		t.Fatal("sign-in reset a manually granted role or created a duplicate user", err)
	}
	if _, err = db.ExecContext(ctx, "UPDATE app_users SET is_advanced=0 WHERE id=?", state.UserID); err != nil {
		t.Fatal("manual role revocation failed")
	}
	revoked, err := store.Validate(ctx, subject, state.Token, now)
	if err != nil || revoked.Advanced {
		t.Fatal("database revocation was not reflected in existing session", err)
	}
	identity.Advanced = true
	ordinary, err := store.Start(ctx, identity, ClientInfo{}, now)
	if err != nil || ordinary.Advanced || ordinary.UserID != state.UserID {
		t.Fatal("sign-in overwrote a manually revoked role", err)
	}
	var hash string
	if err = db.QueryRowContext(ctx, "SELECT token_hash FROM app_sessions WHERE id=?", state.ID).Scan(&hash); err != nil || hash == state.Token || hash != sessionHash(state.Token) {
		t.Fatal("raw session token stored or hash mismatch")
	}
	records := []UsageRecord{}
	for i := 0; i < 25; i++ {
		records = append(records, UsageRecord{ID: newUUID(), UserID: state.UserID, SessionID: state.ID, At: now.Add(-time.Duration(i/3) * time.Second), Method: "GET", Route: "GET /api/events", Service: "activity_logs", View: "logs", Status: 200, Duration: 20, Active: true})
	}
	records = append(records, UsageRecord{ID: newUUID(), UserID: state.UserID, SessionID: state.ID, At: now, View: "logs", Active: true, ViewEvent: true})
	if err = store.Write(ctx, records); err != nil {
		t.Fatal("usage write:", err)
	}
	if err = store.Write(ctx, records); err != nil {
		t.Fatal("idempotent usage retry:", err)
	}
	q := AnalyticsQuery{Days: 7, UserID: state.UserID}
	result, err := store.Page(ctx, "requests", q, now)
	if err != nil {
		t.Fatal("requests page:", err)
	}
	rows := result["items"].([]map[string]any)
	if len(rows) != 20 || result["has_more"] != true {
		t.Fatal("first SQL cursor page incorrect")
	}
	q.Cursor = *result["next_cursor"].(*string)
	result, err = store.Page(ctx, "requests", q, now)
	if err != nil || len(result["items"].([]map[string]any)) != 5 || result["has_more"] != false {
		t.Fatal("SQL cursor boundary or deduplication failed", err)
	}
	for _, kind := range []string{"users", "sessions"} {
		if _, err = store.Page(ctx, kind, AnalyticsQuery{Days: 7, UserID: state.UserID}, now); err != nil {
			t.Fatalf("%s SQL list: %v", kind, err)
		}
	}
	if _, err = store.Overview(ctx, 7, now); err != nil {
		t.Fatal("overview aggregation:", err)
	}
	if err = store.End(ctx, state.ID, now); err != nil {
		t.Fatal("session logout:", err)
	}
	if _, err = store.Validate(ctx, subject, state.Token, now); err == nil {
		t.Fatal("ended SQL session accepted")
	}
	t.Log("Verified user upsert, persistent sessions, database role grants/revocation/preservation, idempotent metrics, aggregation and 20-record cursor pages; fixture rows removed on cleanup")
}
