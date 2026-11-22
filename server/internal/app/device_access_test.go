package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func deviceAccessFixture(t *testing.T) (*Server, *MemoryUsageStore, *http.Cookie, Filter) {
	t.Helper()
	s, store, cookie := trackedFixture(t)
	r := s.Repo.(*DemoRepository)
	r.Registry = []Device{
		{ID: id(1), Name: "Everyday phone", TrackingEmail: "person@gmail.com", PINHash: devicePINGolden},
		{ID: id(2), Name: "Travel phone", TrackingEmail: "person@gmail.com", PINHash: devicePINGolden},
		{ID: id(3), Name: "Other person's phone", TrackingEmail: "other@gmail.com", PINHash: devicePINGolden},
	}
	store.Users["fixture-google-user"].Identity.Advanced = false
	s.Config.PINPepper = devicePINTestPepper
	// The environment administrator is deliberately not an advanced user.
	f := Filter{DeviceIDs: []string{id(1)}, From: FormatTime(s.Now().Add(-time.Hour)), To: FormatTime(s.Now().Add(time.Hour)), Timezone: "UTC"}
	return s, store, cookie, f
}

func unlockRequest(s *Server, cookie *http.Cookie, deviceID, pin string) *http.Response {
	r := httptestUnlock(s, cookie, fmt.Sprintf(`{"device_id":%q,"pin":%q}`, deviceID, pin))
	return r
}

func httptestUnlock(s *Server, cookie *http.Cookie, body string) *http.Response {
	r := httptest.NewRequest("POST", "/api/devices/unlock", strings.NewReader(body))
	r.Header.Set("Origin", s.Config.BaseURL)
	r.Header.Set("Content-Type", "application/json")
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	return w.Result()
}

func readDeviceList(t *testing.T, s *Server, cookie *http.Cookie) []Device {
	t.Helper()
	w := trackedRequest(s, cookie, "GET", "/api/devices", "")
	if w.Code != 200 {
		t.Fatalf("device list: %d %s", w.Code, w.Body.String())
	}
	for _, secret := range []string{"pin_hash", "PINHash", "tracking_email", devicePINGolden, devicePINTestPepper} {
		if strings.Contains(w.Body.String(), secret) {
			t.Fatalf("device list exposed %q", secret)
		}
	}
	var result struct {
		Items []Device `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result.Items
}

func TestDeviceDiscoveryIsEmailScopedAndAdminDoesNotBypassPIN(t *testing.T) {
	s, _, cookie, _ := deviceAccessFixture(t)
	devices := readDeviceList(t, s, cookie)
	if len(devices) != 2 || !devices[0].Locked || !devices[1].Locked {
		t.Fatalf("expected only two locked email-linked devices: %+v", devices)
	}
	if response := unlockRequest(s, cookie, id(3), "0042"); response.StatusCode != 403 {
		t.Fatalf("another email's device unlocked: %d", response.StatusCode)
	}
}

func TestDevicePINUnlockGatesEveryTrackingEndpoint(t *testing.T) {
	s, _, cookie, f := deviceAccessFixture(t)
	args := strings.TrimPrefix(query(f, ""), "/api/events?")
	for _, path := range []string{
		"/api/events?" + args,
		"/api/locations/latest?device_ids=" + id(1),
		"/api/locations/history?" + args,
		"/api/activity/summary?" + args,
		"/api/events/changes?device_ids=" + id(1) + "&as_of=" + f.From,
		"/api/history/calls?" + args,
		"/api/history/notifications?" + args,
		"/api/history/app-usage?" + args,
	} {
		w := trackedRequest(s, cookie, "GET", path, "")
		if w.Code != 403 || !strings.Contains(w.Body.String(), "device_locked") {
			t.Fatalf("locked device allowed by %s: %d %s", path, w.Code, w.Body.String())
		}
	}
	// Place resolution accepts event IDs, so verify its separate access path too.
	r := s.Repo.(*DemoRepository)
	r.Rows = append(r.Rows, Event{ID: id(999), DeviceID: id(1), Recorded: f.From})
	req := httptest.NewRequest("POST", "/api/places/resolve", strings.NewReader(fmt.Sprintf(`{"event_ids":[%q]}`, id(999))))
	req.Header.Set("Origin", s.Config.BaseURL)
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	if w.Code != 403 {
		t.Fatalf("place resolution exposed a locked event: %d", w.Code)
	}
	if response := unlockRequest(s, cookie, id(1), "0042"); response.StatusCode != 200 {
		t.Fatalf("correct leading-zero PIN failed: %d", response.StatusCode)
	}
	if w := trackedRequest(s, cookie, "GET", "/api/locations/latest?device_ids="+id(1), ""); w.Code != 200 {
		t.Fatalf("unlocked history unavailable: %d", w.Code)
	}
	devices := readDeviceList(t, s, cookie)
	if devices[0].Locked || !devices[1].Locked {
		t.Fatal("same PIN on two devices incorrectly unlocked both")
	}
}

func TestRememberedDeviceUnlockSurvivesServerRecreationButNotPINOrEmailChanges(t *testing.T) {
	s, store, cookie, _ := deviceAccessFixture(t)
	if response := unlockRequest(s, cookie, id(1), "0042"); response.StatusCode != 200 {
		t.Fatal(response.StatusCode)
	}
	restarted := NewServer(s.Repo, s.Auth, s.Places, s.Config)
	restarted.Now, restarted.Usage, restarted.Access = s.Now, s.Usage, s.Access
	if readDeviceList(t, restarted, cookie)[0].Locked {
		t.Fatal("unlock was kept only in handler memory")
	}
	r := s.Repo.(*DemoRepository)
	r.Registry[0].PINHash += "changed"
	if !readDeviceList(t, restarted, cookie)[0].Locked {
		t.Fatal("PIN change retained access")
	}
	r.Registry[0].PINHash = devicePINGolden
	r.Registry[0].TrackingEmail = "new.owner@gmail.com"
	if len(readDeviceList(t, restarted, cookie)) != 1 {
		t.Fatal("ownership change retained device visibility")
	}
	r.Registry[1].Revoked = true
	if len(readDeviceList(t, restarted, cookie)) != 0 {
		t.Fatal("revoked device remained visible")
	}
	store.Users["fixture-google-user"].Disabled = true
	if w := trackedRequest(s, cookie, "GET", "/api/devices", ""); w.Code != 401 {
		t.Fatal("disabled account kept remembered access")
	}
}

func TestAdvancedDeviceBypassUsesCurrentDatabaseRole(t *testing.T) {
	s, store, cookie, _ := deviceAccessFixture(t)
	store.Users["fixture-google-user"].Identity.Advanced = true
	devices := readDeviceList(t, s, cookie)
	if len(devices) != 3 {
		t.Fatal("advanced user did not see all devices")
	}
	for _, d := range devices {
		if d.Locked {
			t.Fatal("advanced user asked for a PIN")
		}
	}
	store.Users["fixture-google-user"].Identity.Advanced = false
	devices = readDeviceList(t, s, cookie)
	if len(devices) != 2 || !devices[0].Locked {
		t.Fatal("role removal kept bypass or advanced access created an unlock")
	}
}

func TestDevicePINAttemptLimitsAndUnavailableSetup(t *testing.T) {
	s, _, cookie, _ := deviceAccessFixture(t)
	for i := 0; i < 5; i++ {
		if response := unlockRequest(s, cookie, id(1), "0000"); response.StatusCode != 403 {
			t.Fatalf("attempt %d: %d", i+1, response.StatusCode)
		}
	}
	response := unlockRequest(s, cookie, id(1), "0042")
	if response.StatusCode != 429 || response.Header.Get("Retry-After") != "900" {
		t.Fatal("sixth attempt was not throttled")
	}
	now := s.Now().Add(16 * time.Minute)
	s.Now, s.Auth.Now = func() time.Time { return now }, func() time.Time { return now }
	if response := unlockRequest(s, cookie, id(1), "0042"); response.StatusCode != 200 {
		t.Fatal("attempt window did not expire")
	}
	s.Config.PINPepper = ""
	if response := unlockRequest(s, cookie, id(2), "0042"); response.StatusCode != 503 {
		t.Fatal("missing pepper did not fail closed")
	}
	s.Repo.(*DemoRepository).Registry[1].PINHash = ""
	devices := readDeviceList(t, s, cookie)
	if !devices[1].PINSetupRequired || !devices[1].Locked {
		t.Fatal("unenrolled device was not locked")
	}
}

func TestDevicePINRequestValidationAndOrigin(t *testing.T) {
	s, _, cookie, _ := deviceAccessFixture(t)
	for _, body := range []string{
		fmt.Sprintf(`{"device_id":%q,"pin":42}`, id(1)),
		fmt.Sprintf(`{"device_id":%q,"pin":"42"}`, id(1)),
		fmt.Sprintf(`{"device_id":%q,"pin":"００４２"}`, id(1)),
		fmt.Sprintf(`{"device_id":%q,"pin":"0042","extra":true}`, id(1)),
		fmt.Sprintf(`{"device_id":%q,"pin":"0042"} {}`, id(1)),
	} {
		if response := httptestUnlock(s, cookie, body); response.StatusCode != 400 {
			t.Fatalf("invalid input accepted: %d", response.StatusCode)
		}
	}
	req := httptest.NewRequest("POST", "/api/devices/unlock", strings.NewReader(fmt.Sprintf(`{"device_id":%q,"pin":"0042"}`, id(1))))
	req.AddCookie(cookie)
	req.Header.Set("Origin", "https://unrelated.example")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	if w.Code != 403 || !strings.Contains(w.Body.String(), "origin_rejected") {
		t.Fatal("cross-origin unlock was accepted")
	}
}

func TestDevicePINReservationsAreAtomic(t *testing.T) {
	store := NewMemoryDeviceAccessStore([]byte("synthetic-test-key"))
	var permitted atomic.Int32
	var wg sync.WaitGroup
	now := time.Now()
	for i := 0; i < 30; i++ {
		wg.Go(func() {
			if store.ReserveAttempt(context.Background(), id(1), now) == nil {
				permitted.Add(1)
			}
		})
	}
	wg.Wait()
	if permitted.Load() != 5 {
		t.Fatalf("admitted %d concurrent attempts", permitted.Load())
	}
}

func TestRememberedUnlockIsBrowserSessionScopedAndLogoutRevokesIt(t *testing.T) {
	s, store, cookie, _ := deviceAccessFixture(t)
	if response := unlockRequest(s, cookie, id(1), "0042"); response.StatusCode != 200 {
		t.Fatal(response.StatusCode)
	}
	var u Identity
	if err := s.Auth.unpack(cookie.Value, &u); err != nil {
		t.Fatal(err)
	}
	state, err := store.Start(context.Background(), u, ClientInfo{}, s.Now())
	if err != nil {
		t.Fatal(err)
	}
	u.SessionToken = state.Token
	otherBrowser := &http.Cookie{Name: "lk_session", Value: s.Auth.pack(sessionPayload(u))}
	if !readDeviceList(t, s, otherBrowser)[0].Locked {
		t.Fatal("another browser inherited the first browser's PIN unlock")
	}
	if readDeviceList(t, s, cookie)[0].Locked {
		t.Fatal("original browser lost its unlock")
	}
	if w := trackedRequest(s, cookie, "POST", "/auth/logout", ""); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if w := trackedRequest(s, cookie, "GET", "/api/locations/latest?device_ids="+id(1), ""); w.Code != 401 {
		t.Fatal("copied remembered cookie survived logout")
	}
}

func TestMixedSelectionAndOldCursorCannotBypassRelocking(t *testing.T) {
	s, _, cookie, f := deviceAccessFixture(t)
	if response := unlockRequest(s, cookie, id(1), "0042"); response.StatusCode != 200 {
		t.Fatal(response.StatusCode)
	}
	mixed := "/api/locations/latest?device_ids=" + id(1) + "," + id(2)
	if w := trackedRequest(s, cookie, "GET", mixed, ""); w.Code != 403 {
		t.Fatal("mixed unlocked/locked selection exposed data")
	}
	r := s.Repo.(*DemoRepository)
	for i := 0; i < 41; i++ {
		r.Rows = append(r.Rows, Event{ID: id(100 + i), DeviceID: id(1), Recorded: FormatTime(s.Now().Add(-time.Duration(i) * time.Minute))})
	}
	SortEvents(r.Rows)
	w := trackedRequest(s, cookie, "GET", query(f, ""), "")
	var p Page
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &p) != nil || p.Next == nil {
		t.Fatalf("cursor fixture failed: %d %s", w.Code, w.Body.String())
	}
	r.Registry[0].PINHash += "changed"
	if w := trackedRequest(s, cookie, "GET", query(f, *p.Next), ""); w.Code != 403 {
		t.Fatal("old cursor bypassed PIN change")
	}
	r.Registry[0].TrackingEmail = "new.owner@gmail.com"
	if w := trackedRequest(s, cookie, "GET", query(f, *p.Next), ""); w.Code != 403 {
		t.Fatal("old cursor bypassed ownership change")
	}
}

func TestRememberedDeviceAccessSurvivesLongAbsenceWithoutAnotherPIN(t *testing.T) {
	s, _, cookie, _ := deviceAccessFixture(t)
	response := unlockRequest(s, cookie, id(1), "0042")
	if response.StatusCode != 200 {
		t.Fatal(response.StatusCode)
	}
	for _, saved := range response.Cookies() {
		if saved.Name == "lk_session" {
			cookie = saved
		}
	}
	if cookie.MaxAge < int((399 * 24 * time.Hour).Seconds()) {
		t.Fatal("PIN unlock did not retain the automatic long-term sign-in")
	}
	later := s.Now().Add(365 * 24 * time.Hour)
	s.Now, s.Auth.Now = func() time.Time { return later }, func() time.Time { return later }
	if readDeviceList(t, s, cookie)[0].Locked {
		t.Fatal("returning user was asked for the same device PIN again")
	}
}
