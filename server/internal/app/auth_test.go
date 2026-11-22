package app

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	jose "github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

func TestGoogleCodeFlowAndAccountAuthorization(t *testing.T) {
	key, e := rsa.GenerateKey(rand.Reader, 2048)
	if e != nil {
		t.Fatal(e)
	}
	signer, e := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: key}, (&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", "fixture"))
	if e != nil {
		t.Fatal(e)
	}
	type scenario struct {
		name, email, domain, subject                                                                                                      string
		unverified, wrongNonce, wrongState, wrongAudience, wrongIssuer, expiredToken, expiredFlow, missingEmail, assigned, allowed, admin bool
	}
	var current scenario
	nonce, challenge := "", ""
	var issuer *httptest.Server
	issuer = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			json.NewEncoder(w).Encode(map[string]any{"issuer": issuer.URL, "authorization_endpoint": issuer.URL + "/authorize", "token_endpoint": issuer.URL + "/token", "jwks_uri": issuer.URL + "/keys", "response_types_supported": []string{"code"}, "subject_types_supported": []string{"public"}, "id_token_signing_alg_values_supported": []string{"RS256"}})
		case "/keys":
			json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &key.PublicKey, KeyID: "fixture", Algorithm: "RS256", Use: "sig"}}})
		case "/token":
			_ = r.ParseForm()
			hash := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
			if r.Form.Get("code_verifier") == "" || base64.RawURLEncoding.EncodeToString(hash[:]) != challenge {
				t.Error("PKCE verifier does not match login challenge")
			}
			if r.Form.Get("client_id") != "fixture-client" || r.Form.Get("client_secret") != "fixture-secret" {
				t.Error("Google client authentication missing")
			}
			n := nonce
			if current.wrongNonce {
				n = "wrong-nonce"
			}
			audience, iss, expiry := "fixture-client", issuer.URL, time.Now().Add(time.Hour)
			if current.wrongAudience {
				audience = "another-client"
			}
			if current.wrongIssuer {
				iss = "https://another-provider.example"
			}
			if current.expiredToken {
				expiry = time.Now().Add(-time.Hour)
			}
			email := current.email
			if email == "" {
				email = "owner@gmail.com"
			}
			if current.missingEmail {
				email = ""
			}
			subject := current.subject
			if subject == "" {
				subject = "google-owner"
			}
			token, e := jwt.Signed(signer).Claims(jwt.Claims{Issuer: iss, Subject: subject, Audience: jwt.Audience{audience}, Expiry: jwt.NewNumericDate(expiry), IssuedAt: jwt.NewNumericDate(time.Now())}).Claims(map[string]any{"nonce": n, "name": "Fictional owner", "email": email, "email_verified": !current.unverified, "hd": current.domain}).Serialize()
			if e != nil {
				t.Error(e)
			}
			json.NewEncoder(w).Encode(map[string]any{"access_token": "fixture-access", "token_type": "Bearer", "id_token": token, "expires_in": 3600})
		default:
			w.WriteHeader(404)
		}
	}))
	defer issuer.Close()
	ctx := oidc.ClientContext(context.Background(), issuer.Client())
	cfg := Config{BaseURL: "https://viewer.example", Secret: []byte(strings.Repeat("k", 32)), GoogleClientID: "fixture-client", GoogleClientSecret: "fixture-secret", GoogleAdminEmails: "OWNER@gmail.com, owner@work.example, person@external.example", GoogleAdminSubjects: "pinned-owner"}
	a, e := newGoogleAuth(ctx, cfg, issuer.URL)
	if e != nil {
		t.Fatal(e)
	}
	for _, tc := range []scenario{
		{name: "verified Gmail administrator", allowed: true, admin: true},
		{name: "Workspace administrator", email: "owner@work.example", domain: "work.example", allowed: true, admin: true},
		{name: "unverified email", unverified: true},
		{name: "missing email", missingEmail: true},
		{name: "ordinary verified account", email: "someone@gmail.com", allowed: true},
		{name: "external email does not grant administrator access", email: "person@external.example", allowed: true},
		{name: "external account pinned by subject", email: "person@external.example", subject: "pinned-owner", allowed: true, admin: true},
		{name: "subject administrator starts without advanced access", email: "other@gmail.com", subject: "pinned-owner", allowed: true, admin: true},
		{name: "assigned non-administrator", email: "someone@gmail.com", assigned: true, allowed: true},
		{name: "wrong nonce", wrongNonce: true},
		{name: "wrong state", wrongState: true},
		{name: "wrong audience", wrongAudience: true},
		{name: "wrong issuer", wrongIssuer: true},
		{name: "expired ID token", expiredToken: true},
		{name: "expired login flow", expiredFlow: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			current = tc
			usageStore := NewMemoryUsageStore(cfg.Secret, false)
			a.Usage = NewUsage(usageStore)
			defer a.Usage.Close()
			a.Assigned = tc.assigned
			w := httptest.NewRecorder()
			a.Login(w, httptest.NewRequest("GET", "https://viewer.example/auth/login", nil))
			target, e := url.Parse(w.Header().Get("Location"))
			if e != nil {
				t.Fatal(e)
			}
			nonce = target.Query().Get("nonce")
			challenge = target.Query().Get("code_challenge")
			if target.Query().Get("code_challenge_method") != "S256" || nonce == "" || target.Query().Get("prompt") != "select_account" {
				t.Fatal("missing Google account chooser, nonce or PKCE challenge")
			}
			scopes := strings.Fields(target.Query().Get("scope"))
			slices.Sort(scopes)
			if !slices.Equal(scopes, []string{"email", "openid", "profile"}) || target.Query().Get("access_type") == "offline" {
				t.Fatal("sign-in requested unnecessary Google API or offline access")
			}
			flow := w.Result().Cookies()[0]
			if tc.expiredFlow {
				var f authFlow
				if a.unpack(flow.Value, &f) != nil {
					t.Fatal("invalid fixture flow")
				}
				f.Expires = time.Now().Add(-time.Minute).Unix()
				flow.Value = a.pack(f)
			}
			state := target.Query().Get("state")
			if tc.wrongState {
				state = "wrong-state"
			}
			callback := httptest.NewRequest("GET", "https://viewer.example/auth/callback?code=fixture-code&state="+url.QueryEscape(state), nil).WithContext(ctx)
			callback.AddCookie(flow)
			out := httptest.NewRecorder()
			a.Callback(out, callback)
			hasSession := false
			for _, cookie := range out.Result().Cookies() {
				if cookie.Name != "lk_session" || cookie.MaxAge <= 0 {
					continue
				}
				hasSession = true
				if !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteLaxMode {
					t.Fatal("insecure session cookie")
				}
				if cookie.MaxAge < int((SessionLifetime-time.Minute).Seconds()) || cookie.MaxAge > int(SessionLifetime.Seconds()) {
					t.Fatal("sign-in did not automatically create a remembered session")
				}
				r := httptest.NewRequest("GET", "https://viewer.example/api/devices", nil)
				r.AddCookie(cookie)
				identity, e := a.Identity(r)
				if e != nil || !identity.EmailVerified || identity.Email == "" || identity.Admin != tc.admin {
					t.Fatal("valid Google identity not recognised")
				}
				if identity.UserID == "" || identity.SessionID == "" || identity.Advanced {
					t.Fatal("Google callback did not create persistent app identity and session")
				}
				raw, _ := json.Marshal(identity)
				if strings.Contains(string(raw), "fixture-access") || strings.Contains(string(raw), "email_verified") || strings.Contains(string(raw), "\"exp\"") {
					t.Fatal("private authentication metadata leaked into API identity")
				}
				// Google's original ID token has expired by this point. Only the
				// independently revocable app session is needed for return visits.
				originalNow := a.Now
				later := a.Now().Add(48 * time.Hour)
				a.Now = func() time.Time { return later }
				renewed := httptest.NewRecorder()
				_, rememberedErr := a.Authenticate(renewed, r)
				a.Now = originalNow
				if rememberedErr != nil || len(renewed.Result().Cookies()) != 1 {
					t.Fatal("remembered app session depended on the expired Google token", rememberedErr)
				}
			}
			if hasSession != tc.allowed {
				t.Fatalf("session issued=%v, want=%v", hasSession, tc.allowed)
			}
			if len(usageStore.Users) != len(usageStore.Sessions) || len(usageStore.Users) != map[bool]int{true: 1, false: 0}[tc.allowed] {
				t.Fatal("persistent records were not limited to authorized Google sign-ins")
			}
			if !tc.allowed && out.Header().Get("Location") != "/?signin=failed" {
				t.Fatal("failed sign-in did not return to login")
			}
		})
	}
}

func TestGoogleProviderIsPinnedAndLegacyConfigurationIgnored(t *testing.T) {
	t.Setenv("DATA_SOURCE", "tidb")
	t.Setenv("TIDB_DSN", "fixture")
	t.Setenv("SESSION_SECRET", strings.Repeat("k", 32))
	t.Setenv("OIDC_ISSUER", "https://untrusted.example")
	t.Setenv("OIDC_CLIENT_ID", "legacy-client")
	t.Setenv("OIDC_ADMIN_SUBJECTS", "legacy-admin")
	t.Setenv("GOOGLE_CLIENT_ID", "google-client")
	t.Setenv("GOOGLE_CLIENT_SECRET", "google-secret")
	t.Setenv("GOOGLE_ADMIN_EMAILS", "owner@gmail.com")
	cfg, e := ConfigFromEnv()
	if e != nil {
		t.Fatal(e)
	}
	calls := 0
	client := &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.String() != GoogleIssuer+"/.well-known/openid-configuration" {
			t.Fatal("Google issuer was overridden")
		}
		body := `{"issuer":"https://accounts.google.com","authorization_endpoint":"https://accounts.google.com/o/oauth2/v2/auth","token_endpoint":"https://oauth2.googleapis.com/token","jwks_uri":"https://www.googleapis.com/oauth2/v3/certs"}`
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	// Device ownership comes from the verified email and PIN. Gmail sender
	// administrator lists are optional and must not gate ordinary sign-in.
	cfg.GoogleAdminEmails, cfg.GoogleAdminSubjects = "", ""
	a, e := NewAuth(oidc.ClientContext(context.Background(), client), cfg)
	if e != nil || calls != 1 || a.OAuth.ClientID != "google-client" || a.OAuth.RedirectURL != strings.TrimRight(cfg.BaseURL, "/")+"/auth/callback" {
		t.Fatal("Google configuration was not applied")
	}
}

func TestGoogleSessionsRecheckAdministratorAllowlist(t *testing.T) {
	s, _, cookie := trackedFixture(t)
	a := s.Auth
	r := httptest.NewRequest("GET", "http://example.test/api/devices", nil)
	r.AddCookie(cookie)
	if u, e := a.Identity(r); e != nil || !u.Admin {
		t.Fatal("administrator rejected")
	}
	a.AdminEmails = nil
	if u, e := a.Identity(r); e != nil || u.Admin {
		t.Fatal("administrator removal did not preserve ordinary account sign-in")
	}
}

func TestGoogleSettingsRequiredBeforeDiscovery(t *testing.T) {
	for _, cfg := range []Config{
		{GoogleClientID: "client", GoogleAdminEmails: "owner@gmail.com"},
		{GoogleClientSecret: "secret"},
		{GoogleClientID: "client", GoogleClientSecret: "secret", GoogleAdminEmails: "*@gmail.com"},
	} {
		if _, e := NewAuth(context.Background(), cfg); e == nil {
			t.Fatal("incomplete Google configuration accepted")
		}
	}
}
