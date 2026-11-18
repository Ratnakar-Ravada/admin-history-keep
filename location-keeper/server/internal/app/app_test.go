package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func fixture(t *testing.T, n int) (*Server, *DemoRepository, Filter) {
	t.Helper()
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	repo := &DemoRepository{Now: now, Registry: []Device{{ID: id(1), Name: "Demo one"}, {ID: id(2), Name: "Demo two"}}}
	for i := 0; i < n; i++ {
		at := FormatTime(now.Add(-time.Duration(i/2+1) * time.Minute))
		repo.Rows = append(repo.Rows, Decorate(Event{ID: id(100 + i), DeviceID: id(i%2 + 1), Recorded: at, Received: at, Reason: "periodic", Status: "fresh", Outcome: Ptr("completed"), Captured: Ptr(at), Lat: Ptr(51.5), Lon: Ptr(-.1), Accuracy: Ptr(10.0), Permission: "precise"}))
	}
	SortEvents(repo.Rows)
	cfg := Config{Demo: true, BaseURL: "http://example.test", Secret: []byte(strings.Repeat("s", 32)), CursorTTL: 4 * time.Minute, WebDir: t.TempDir()}
	auth, e := NewAuth(context.Background(), cfg)
	if e != nil {
		t.Fatal(e)
	}
	places := NewPlaces("", true, nil)
	t.Cleanup(places.Close)
	server := NewServer(repo, auth, places, cfg)
	server.Now = func() time.Time { return now }
	f := Filter{DeviceIDs: []string{id(1), id(2)}, From: FormatTime(now.Add(-24 * time.Hour)), To: FormatTime(now.Add(time.Hour)), Timezone: "UTC", Reasons: []string{}, Severity: []string{}, Outcomes: []string{}, Statuses: []string{}, Transports: []string{}}
	return server, repo, f
}
func query(f Filter, cursor string) string {
	q := url.Values{"device_ids": {strings.Join(f.DeviceIDs, ",")}, "from": {f.From}, "to": {f.To}, "timezone": {f.Timezone}}
	for k, v := range map[string][]string{"reasons": f.Reasons, "severity": f.Severity, "outcomes": f.Outcomes, "statuses": f.Statuses, "transports": f.Transports} {
		if len(v) > 0 {
			q.Set(k, strings.Join(v, ","))
		}
	}
	if cursor != "" {
		q.Set("cursor", cursor)
	}
	return "/api/events?" + q.Encode()
}
func request(s *Server, path string) (int, []byte) {
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest("GET", path, nil))
	return w.Code, w.Body.Bytes()
}
func page(t *testing.T, s *Server, f Filter, cursor string) Page {
	t.Helper()
	code, b := request(s, query(f, cursor))
	if code != 200 {
		t.Fatalf("status %d: %s", code, b)
	}
	var p Page
	if e := json.Unmarshal(b, &p); e != nil {
		t.Fatal(e)
	}
	return p
}
func TestTwentyRecordPages(t *testing.T) {
	for _, n := range []int{0, 20, 23, 40, 61} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			s, _, f := fixture(t, n)
			cursor := ""
			ids := map[string]bool{}
			pages := 0
			for {
				p := page(t, s, f, cursor)
				pages++
				if len(p.Items) > 20 {
					t.Fatal("page too large")
				}
				for _, e := range p.Items {
					if ids[e.ID] {
						t.Fatal("duplicate event")
					}
					ids[e.ID] = true
				}
				if !p.More {
					if p.Next != nil {
						t.Fatal("terminal cursor")
					}
					break
				}
				if p.Next == nil {
					t.Fatal("missing cursor")
				}
				cursor = *p.Next
			}
			if len(ids) != n {
				t.Fatalf("got %d of %d", len(ids), n)
			}
			if n == 40 && pages != 2 {
				t.Fatal("unnecessary empty page")
			}
		})
	}
}
func TestTiesAcrossDevicesUseIDOrdering(t *testing.T) {
	s, _, f := fixture(t, 50)
	p := page(t, s, f, "")
	for i := 1; i < len(p.Items); i++ {
		prev, now := p.Items[i-1], p.Items[i]
		if prev.Recorded < now.Recorded || prev.Recorded == now.Recorded && prev.ID < now.ID {
			t.Fatal("unstable sort")
		}
	}
	if len(p.Items) != 20 {
		t.Fatal("must be 20 combined, not per device")
	}
}
func TestCursorRejectsChangedFiltersTamperingAndRevokedScope(t *testing.T) {
	s, r, f := fixture(t, 50)
	p := page(t, s, f, "")
	token := *p.Next
	f.Severity = []string{"positive"}
	if code, _ := request(s, query(f, token)); code != 409 {
		t.Fatal("filter mismatch accepted")
	}
	f.Severity = []string{}
	if code, _ := request(s, query(f, token+"a")); code != 400 {
		t.Fatal("tampered cursor accepted")
	}
	r.Registry = r.Registry[:1]
	f.DeviceIDs = []string{id(1)}
	if code, _ := request(s, query(f, token)); code != 409 {
		t.Fatal("revoked scope accepted")
	}
}
func TestCursorExpiryAndRetry(t *testing.T) {
	s, _, f := fixture(t, 50)
	p := page(t, s, f, "")
	a := page(t, s, f, *p.Next)
	b := page(t, s, f, *p.Next)
	if a.Items[0].ID != b.Items[0].ID {
		t.Fatal("retry not deterministic")
	}
	now := s.Now()
	s.Now = func() time.Time { return now.Add(5 * time.Minute) }
	if code, _ := request(s, query(f, *p.Next)); code != 409 {
		t.Fatal("expired cursor accepted")
	}
}
func TestLateBackfillExcludedUntilRefresh(t *testing.T) {
	s, r, f := fixture(t, 40)
	p := page(t, s, f, "")
	r.Rows = append(r.Rows, Decorate(Event{ID: id(9999), DeviceID: id(1), Recorded: FormatTime(r.Now.Add(-15 * time.Minute)), Received: FormatTime(r.Now.Add(time.Second))}))
	SortEvents(r.Rows)
	next := page(t, s, f, *p.Next)
	if next.More || len(next.Items) != 20 {
		t.Fatal("new backfill altered snapshot")
	}
	for _, e := range next.Items {
		if e.ID == id(9999) {
			t.Fatal("late backfill entered existing snapshot")
		}
	}
	r.Now = r.Now.Add(time.Second)
	refresh := page(t, s, f, "")
	next = page(t, s, f, *refresh.Next)
	if !next.More {
		t.Fatal("refresh failed to include new arrival")
	}
}
func TestFirstPageCanBeRecoveredAtItsOriginalSnapshot(t *testing.T) {
	s, r, f := fixture(t, 40)
	p := page(t, s, f, "")
	if p.Start == nil {
		t.Fatal("missing first-page replay cursor")
	}
	r.Rows = append(r.Rows, Decorate(Event{ID: id(9999), DeviceID: id(1), Recorded: FormatTime(r.Now), Received: FormatTime(r.Now)}))
	SortEvents(r.Rows)
	r.Now = r.Now.Add(time.Second)
	replay := page(t, s, f, *p.Start)
	if replay.Items[0].ID != p.Items[0].ID {
		t.Fatal("replayed first page mixed snapshots")
	}
	refreshed := page(t, s, f, "")
	if refreshed.Items[0].ID != id(9999) {
		t.Fatal("explicit refresh did not include arrival")
	}
}
func TestClassification(t *testing.T) {
	base := Event{Reason: "periodic", Status: "fresh", Outcome: Ptr("completed"), Lat: Ptr(51.5), Lon: Ptr(-.1), Captured: Ptr("2026-11-18T00:00:00.000Z"), Recorded: "2026-11-18T00:00:00.000Z", Received: "2026-11-18T00:00:01.000Z", Accuracy: Ptr(10.0), Permission: "precise"}
	cases := []struct {
		name, want string
		change     func(*Event)
	}{{"healthy", "positive", func(*Event) {}}, {"disabled periodic", "attention", func(e *Event) { e.Status = "disabled" }}, {"internet loss", "attention", func(e *Event) { e.Reason = "internet_off" }}, {"airplane on", "attention", func(e *Event) { e.Reason = "airplane_mode_on" }}, {"approximate", "limited", func(e *Event) { e.Permission = "approximate" }}, {"legacy outcome", "neutral", func(e *Event) { e.Outcome = nil }}, {"stale fix", "limited", func(e *Event) { e.Status = "last_known" }}, {"delayed upload", "limited", func(e *Event) { e.Received = "2026-11-18T01:00:00.000Z" }}}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := base
			c.change(&e)
			if got := Classify(e); got != c.want {
				t.Fatalf("%s != %s", got, c.want)
			}
		})
	}
}
func TestNoDevicesDoesNotReadAllEvents(t *testing.T) {
	s, _, f := fixture(t, 50)
	f.DeviceIDs = []string{}
	if p := page(t, s, f, ""); len(p.Items) != 0 {
		t.Fatal("empty selection leaked records")
	}
}
func TestUnsupportedFiltersAndPageSizes(t *testing.T) {
	s, _, f := fixture(t, 50)
	for _, suffix := range []string{"&severity=positive%27%20OR%201=1", "&limit=100", "&timezone=Invalid%2FZone"} {
		if code, _ := request(s, query(f, "")+suffix); code != 400 {
			t.Fatalf("invalid filter accepted: %s", suffix)
		}
	}
}
func TestPlaceEndpointRequiresSameOriginAndAccess(t *testing.T) {
	s, _, _ := fixture(t, 5)
	for _, origin := range []string{"http://malicious.test", "http://example.test"} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "/api/places/resolve", strings.NewReader(`{"event_ids":["`+id(9999)+`"]}`))
		r.Header.Set("Origin", origin)
		r.Header.Set("Content-Type", "application/json")
		s.Handler().ServeHTTP(w, r)
		if w.Code != 403 {
			t.Fatal("unavailable event/origin accepted")
		}
	}
}
func TestNoPrivateDatabaseInDemoMode(t *testing.T) {
	t.Setenv("DATA_SOURCE", "demo")
	t.Setenv("TIDB_DSN", "private-database")
	if _, e := ConfigFromEnv(); e == nil {
		t.Fatal("demo accepted private database")
	}
	t.Setenv("TIDB_DSN", "")
	t.Setenv("LISTEN_ADDR", "0.0.0.0:8080")
	if _, e := ConfigFromEnv(); e == nil {
		t.Fatal("public demo binding accepted")
	}
}
func TestSessionRoundTripAndTampering(t *testing.T) {
	s, _, _ := fixture(t, 1)
	u := Identity{Subject: "owner", Name: "Owner", Expires: time.Now().Add(time.Hour).Unix()}
	value := s.Auth.pack(sessionPayload(u))
	var decoded Identity
	if s.Auth.unpack(value, &decoded) != nil || decoded.Expires != u.Expires {
		t.Fatal("session expiry lost")
	}
	if s.Auth.unpack(value+"a", &decoded) == nil {
		t.Fatal("tampered session accepted")
	}
}
func TestLiveAPICannotUseDemoAuthentication(t *testing.T) {
	s, _, _ := fixture(t, 1)
	s.Auth.Demo = false
	code, _ := request(s, "/api/devices")
	if code != http.StatusUnauthorized {
		t.Fatal("unauthenticated private data accepted")
	}
}
