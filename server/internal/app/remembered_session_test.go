package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func rememberedFixture(t *testing.T) (*Auth, *MemoryUsageStore, *http.Cookie, *time.Time) {
	t.Helper()
	s, store, _ := trackedFixture(t)
	now := s.Now()
	a := s.Auth
	a.Now = func() time.Time { return now }
	u := Identity{Subject: "remembered-user", Email: "viewer@gmail.com", EmailVerified: true, Expires: now.Add(SessionLifetime).Unix()}
	state, err := store.Start(context.Background(), u, ClientInfo{}, now)
	if err != nil {
		t.Fatal(err)
	}
	u.SessionToken = state.Token
	return a, store, &http.Cookie{Name: "lk_session", Value: a.pack(sessionPayload(u))}, &now
}

func rememberedRequest(cookie *http.Cookie) *http.Request {
	r := httptest.NewRequest("GET", "https://viewer.example/api/session", nil)
	r.AddCookie(cookie)
	return r
}

func TestRememberedSessionRenewsDailyAndSurvivesReturnVisits(t *testing.T) {
	a, store, cookie, now := rememberedFixture(t)
	start := *now
	var lastExpires int64
	for _, elapsed := range []time.Duration{0, time.Hour, 23 * time.Hour, 24 * time.Hour, 24*time.Hour + time.Second, 365 * 24 * time.Hour, 700 * 24 * time.Hour} {
		*now = start.Add(elapsed)
		w := httptest.NewRecorder()
		u, err := a.Authenticate(w, rememberedRequest(cookie))
		if err != nil {
			t.Fatalf("return visit at %s required sign-in: %v", elapsed, err)
		}
		renewal := elapsed == 24*time.Hour || elapsed >= 365*24*time.Hour
		cookies := w.Result().Cookies()
		if (len(cookies) == 1) != renewal {
			t.Fatalf("unexpected cookie renewal at %s: %v", elapsed, cookies)
		}
		if renewal {
			cookie = cookies[0]
			if cookie.MaxAge <= 0 || cookie.MaxAge > int(SessionLifetime.Seconds()) || !cookie.HttpOnly || cookie.SameSite != http.SameSiteLaxMode {
				t.Fatal("renewed cookie lost persistent session protections")
			}
			if u.Expires != now.Add(SessionLifetime).Unix() {
				t.Fatal("renewal did not roll the inactivity deadline forward")
			}
		} else if lastExpires != 0 && u.Expires != lastExpires {
			t.Fatal("session expiry changed before its daily renewal threshold")
		}
		state, err := store.Validate(context.Background(), u.Subject, u.SessionToken, *now)
		if err != nil || state.Expires != u.Expires {
			t.Fatal("cookie and durable session expiry disagree", err)
		}
		lastExpires = u.Expires
	}
}

func TestRememberedSessionRechecksRevocationAndCurrentRole(t *testing.T) {
	for _, change := range []string{"disabled", "revoked", "expired", "role removed"} {
		t.Run(change, func(t *testing.T) {
			a, store, cookie, now := rememberedFixture(t)
			store.Users["remembered-user"].Identity.Advanced = true
			u, err := a.Authenticate(httptest.NewRecorder(), rememberedRequest(cookie))
			if err != nil || !u.Advanced || u.Admin {
				t.Fatal("ordinary account did not use its current database role", err)
			}
			switch change {
			case "disabled":
				store.Users[u.Subject].Disabled = true
			case "revoked":
				if err := store.End(context.Background(), u.SessionID, *now); err != nil {
					t.Fatal(err)
				}
			case "expired":
				*now = now.Add(SessionLifetime)
			case "role removed":
				store.Users[u.Subject].Identity.Advanced = false
			}
			w := httptest.NewRecorder()
			after, err := a.Authenticate(w, rememberedRequest(cookie))
			if change == "role removed" {
				if err != nil || after.Advanced {
					t.Fatal("remembered cookie retained a revoked role", err)
				}
			} else if err == nil || len(w.Result().Cookies()) != 0 {
				t.Fatal("inactive or revoked session was accepted or renewed")
			}
		})
	}
}

func TestRememberedSessionLogoutRevokesCopiedCookieWithoutUsageWrapper(t *testing.T) {
	a, _, cookie, _ := rememberedFixture(t)
	w := httptest.NewRecorder()
	a.Logout(w, rememberedRequest(cookie))
	if w.Code != http.StatusOK || len(w.Result().Cookies()) != 1 || w.Result().Cookies()[0].MaxAge >= 0 {
		t.Fatal("logout did not clear the persistent cookie")
	}
	if _, err := a.Authenticate(httptest.NewRecorder(), rememberedRequest(cookie)); err == nil {
		t.Fatal("a copied remembered cookie survived logout")
	}
}

func TestRememberedSessionsRequireDurableValidation(t *testing.T) {
	a, _, cookie, _ := rememberedFixture(t)
	a.Usage = nil
	if _, err := a.Authenticate(httptest.NewRecorder(), rememberedRequest(cookie)); err == nil {
		t.Fatal("remembered cookie bypassed durable session validation")
	}
}

func TestExistingTrackedSessionUpgradesOnlyWhileStillValid(t *testing.T) {
	s, store, cookie := trackedFixture(t)
	w := httptest.NewRecorder()
	u, err := s.Auth.Authenticate(w, rememberedRequest(cookie))
	if err != nil || len(w.Result().Cookies()) != 1 || u.Expires != s.Now().Add(SessionLifetime).Unix() {
		t.Fatal("valid tracked session did not upgrade to the remembered lifetime", err)
	}
	if store.Sessions[u.SessionID].Expires.Unix() != u.Expires {
		t.Fatal("upgraded browser and durable expiry disagree")
	}
	// An old cookie cannot revive its durable session after that session expires.
	store.Sessions[u.SessionID].Expires = s.Now().Add(-time.Second)
	if _, err := s.Auth.Authenticate(httptest.NewRecorder(), rememberedRequest(cookie)); err == nil {
		t.Fatal("expired tracked session upgraded instead of requiring sign-in")
	}
}
