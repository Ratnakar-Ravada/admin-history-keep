package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

func historyFixture(t *testing.T, count int) (*Server, *DemoRepository, Filter) {
	t.Helper()
	s, repo, f := fixture(t, 0)
	repo.HistoryRows = map[string][]DeviceHistoryRecord{}
	for _, kind := range []string{"calls", "notifications", "app-usage"} {
		for i := 0; i < count; i++ {
			at := FormatTime(repo.Now.Add(-time.Duration(i/2+1) * time.Minute))
			typeName := map[string]string{"calls": "1", "notifications": "posted", "app-usage": "daily"}[kind]
			repo.HistoryRows[kind] = append(repo.HistoryRows[kind], DeviceHistoryRecord{ID: id(500 + i), DeviceID: id(i%2 + 1), Recorded: at, Observed: at, Received: at, EventType: typeName, CallID: id(1000 + i), SourceKey: id(2000 + i), Fields: map[string]*string{"name": nil, "text": Ptr("Hello"), "app_label": Ptr("Browser"), "foreground_ms": Ptr("5000")}})
		}
	}
	return s, repo, f
}

func historyURL(f Filter, kind, cursor string, extra url.Values) string {
	base, _ := url.Parse(query(f, cursor))
	q := base.Query()
	for key, values := range extra {
		q[key] = values
	}
	return "/api/history/" + kind + "?" + q.Encode()
}

func historyPage(t *testing.T, s *Server, f Filter, kind, cursor string, extra url.Values) DeviceHistoryPage {
	t.Helper()
	status, body := request(s, historyURL(f, kind, cursor, extra))
	if status != 200 {
		t.Fatalf("history status %d: %s", status, body)
	}
	var page DeviceHistoryPage
	if err := json.Unmarshal(body, &page); err != nil {
		t.Fatal(err)
	}
	return page
}

func TestDeviceHistorySnapshotPaginationAndScopedCursors(t *testing.T) {
	for _, kind := range []string{"calls", "notifications", "app-usage"} {
		t.Run(kind, func(t *testing.T) {
			s, repo, f := historyFixture(t, 45)
			first := historyPage(t, s, f, kind, "", nil)
			if len(first.Items) != 20 || first.Next == nil || first.Start == nil || !first.More {
				t.Fatal("expected complete first page and signed cursors")
			}
			if first.Items[0].Fields["name"] != nil {
				t.Fatal("null metadata not preserved")
			}
			// Backfilled history and revisions arriving after the first request
			// remain outside the existing snapshot, including page-one replay.
			late := repo.HistoryRows[kind][23]
			late.ID, late.Observed, late.Received = id(99999), FormatTime(repo.Now.Add(time.Second)), FormatTime(repo.Now.Add(time.Second))
			repo.HistoryRows[kind] = append(repo.HistoryRows[kind], late)
			seen := map[string]bool{}
			for _, row := range first.Items {
				seen[row.ID] = true
			}
			cursor := first.Next
			for cursor != nil {
				page := historyPage(t, s, f, kind, *cursor, nil)
				for _, row := range page.Items {
					if seen[row.ID] || row.ID == late.ID {
						t.Fatal("duplicate or post-snapshot revision")
					}
					seen[row.ID] = true
				}
				if len(page.Items) > 20 || page.More != (page.Next != nil) {
					t.Fatal("invalid page shape")
				}
				cursor = page.Next
			}
			if len(seen) != 45 {
				t.Fatalf("lost tied rows across devices: %d", len(seen))
			}
			replay := historyPage(t, s, f, kind, *first.Start, nil)
			if !reflect.DeepEqual(first.Items, replay.Items) {
				t.Fatal("page replay changed")
			}
			for _, extra := range []url.Values{{"q": {"hello"}}, {"event_type": {map[string]string{"calls": "3", "notifications": "active", "app-usage": "session"}[kind]}}} {
				if code, _ := request(s, historyURL(f, kind, *first.Next, extra)); code != 409 {
					t.Fatalf("changed filters accepted: %d", code)
				}
			}
			otherKind := "calls"
			if kind == otherKind {
				otherKind = "app-usage"
			}
			if code, _ := request(s, historyURL(f, otherKind, *first.Next, nil)); code != 409 {
				t.Fatal("cross-history cursor accepted")
			}
			if code, _ := request(s, historyURL(f, kind, *first.Next+"x", nil)); code != 400 {
				t.Fatal("tampered cursor accepted")
			}
			repo.Registry = repo.Registry[:1]
			f.DeviceIDs = []string{id(1)}
			if code, _ := request(s, historyURL(f, kind, *first.Next, nil)); code != 409 {
				t.Fatal("revoked device scope accepted")
			}
		})
	}
}

func TestDeviceHistoryLatestRevisionBeforeFiltering(t *testing.T) {
	s, repo, f := historyFixture(t, 1)
	for _, kind := range []string{"calls", "app-usage", "notifications"} {
		old := repo.HistoryRows[kind][0]
		old.Fields["name"], old.Fields["app_label"] = Ptr("Former label"), Ptr("Former label")
		newest := old
		newest.Fields = map[string]*string{"name": Ptr("Current label"), "app_label": Ptr("Current label"), "foreground_ms": Ptr("9000")}
		newest.ID, newest.Observed = id(9000), FormatTime(repo.Now)
		if kind == "calls" {
			newest.EventType = "3"
		}
		repo.HistoryRows[kind] = []DeviceHistoryRecord{old, newest}
		page := historyPage(t, s, f, kind, "", nil)
		want := 1
		if kind == "notifications" {
			want = 2 // Notification lifecycle revisions remain observations.
		}
		if len(page.Items) != want || page.Items[0].ID != newest.ID {
			t.Fatalf("%s latest revision failed: %+v", kind, page.Items)
		}
		if kind != "notifications" {
			if page := historyPage(t, s, f, kind, "", url.Values{"q": {"former"}}); len(page.Items) != 0 {
				t.Fatal("search resurrected an older revision")
			}
			page := historyPage(t, s, f, kind, "", url.Values{"q": {"CURRENT"}})
			if len(page.Items) != 1 || page.Items[0].ID != newest.ID {
				t.Fatal("case insensitive latest revision search failed")
			}
		}
	}
	if page := historyPage(t, s, f, "calls", "", url.Values{"event_type": {"1"}}); len(page.Items) != 0 {
		t.Fatal("type filter resurrected older call revision")
	}
	// A source key is scoped to the device and event type. Sessions and daily
	// totals must never suppress one another even in malformed legacy fixtures.
	row := repo.HistoryRows["app-usage"][1]
	row.ID, row.EventType = id(9200), "session"
	repo.HistoryRows["app-usage"] = append(repo.HistoryRows["app-usage"], row)
	if page := historyPage(t, s, f, "app-usage", "", nil); len(page.Items) != 2 {
		t.Fatal("daily and session records incorrectly combined")
	}
}

func TestDeviceHistoryAccessValidationExpiryAndEmptySelection(t *testing.T) {
	s, _, f := historyFixture(t, 45)
	for _, kind := range []string{"calls", "notifications", "app-usage"} {
		for _, extra := range []url.Values{{"event_type": {"invalid"}}, {"q": {strings.Repeat("a", 201)}}, {"q": {"a", "b"}}, {"severity": {"positive"}}} {
			if code, _ := request(s, historyURL(f, kind, "", extra)); code != 400 {
				t.Fatalf("invalid filters accepted for %s: %d", kind, code)
			}
		}
		unknown := f
		unknown.DeviceIDs = []string{id(99)}
		if code, _ := request(s, historyURL(unknown, kind, "", nil)); code != 403 {
			t.Fatal("unauthorized device accepted")
		}
		empty := f
		empty.DeviceIDs = nil
		page := historyPage(t, s, empty, kind, "", nil)
		if page.Items == nil || len(page.Items) != 0 || page.More || page.Next != nil {
			t.Fatal("empty selection did not return an empty list")
		}
	}
	page := historyPage(t, s, f, "calls", "", nil)
	now := s.Now()
	s.Now = func() time.Time { return now.Add(5 * time.Minute) }
	if code, _ := request(s, historyURL(f, "calls", *page.Next, nil)); code != 409 {
		t.Fatal("expired cursor accepted")
	}
	s.Auth.Demo = false
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, historyURL(f, "calls", "", nil), nil))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous history access: %d", w.Code)
	}
}

func TestDeviceHistorySQLUsesBoundValuesAndGlobalRevisionScope(t *testing.T) {
	_, _, f := historyFixture(t, 0)
	for _, kind := range []string{"calls", "app-usage", "notifications"} {
		filter := DeviceHistoryFilter{Filter: f, Kind: kind, Search: "%' OR 1=1 --", EventType: "1"}
		statement, args := deviceHistorySQL(filter, Cursor{LastTime: f.From, LastID: id(777)}, 21)
		if strings.Contains(statement, filter.Search) || !slices.Contains(args, any(filter.Search)) || len(args) != strings.Count(statement, "?") {
			t.Fatal("search/cursor values not safely bound")
		}
		if kind != "notifications" {
			// Latest revision lookup cannot use the current page's key, date,
			// search or type filters; that would resurrect obsolete revisions.
			start := strings.Index(statement, "NOT EXISTS(")
			if start < 0 {
				t.Fatal("missing latest revision selection")
			}
			end := strings.Index(statement[start:], ")))")
			if end < 0 || strings.Contains(statement[start:start+end], "?") {
				t.Fatal("revision lookup incorrectly limited to page filters")
			}
		}
	}
}

func TestDeviceHistoryNewViewsAreTrackedWithoutAdvancedAccess(t *testing.T) {
	s, store, cookie := trackedFixture(t)
	store.Users["fixture-google-user"].Identity.Advanced = false
	for _, view := range []string{"calls", "notifications", "app-usage"} {
		if w := trackedRequest(s, cookie, http.MethodPost, "/api/usage/view", `{"view":"`+view+`"}`); w.Code != 200 {
			t.Fatalf("new view not accepted: %s: %d", view, w.Code)
		}
		if serviceFor("GET /api/history/"+view) == "other" {
			t.Fatal("history requests missing service classification")
		}
	}
	// Tracked authenticated requests retain ordinary device authorization.
	_, _, f := historyFixture(t, 0)
	if w := trackedRequest(s, cookie, http.MethodGet, historyURL(f, "calls", "", nil), ""); w.Code != 200 {
		t.Fatalf("ordinary viewer blocked from device history: %d", w.Code)
	}
}
