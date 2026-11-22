package app

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/mail"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	jose "github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
	"golang.org/x/oauth2"
)

type fixtureKeys struct{ key *rsa.PublicKey }

func (k fixtureKeys) VerifySignature(_ context.Context, raw string) ([]byte, error) {
	signed, err := jose.ParseSigned(raw, []jose.SignatureAlgorithm{jose.RS256})
	if err != nil {
		return nil, err
	}
	return signed.Verify(k.key)
}

func TestGmailConsentBindsAccountStateScopeAndOfflineGrant(t *testing.T) {
	s, _, _ := fixture(t, 1)
	a := s.Auth
	a.Demo = false
	a.Admins = []string{"owner"}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	signer, _ := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: key}, nil)
	nonce := ""
	scenario := ""
	requests := 0
	provider := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "application/json")
		_ = r.ParseForm()
		if r.Form.Get("code_verifier") == "" || r.Form.Get("redirect_uri") != "http://example.test/auth/gmail/callback" {
			t.Error("Gmail callback PKCE or redirect missing")
		}
		email, subject, n := "owner@gmail.com", "owner", nonce
		if scenario == "other email" {
			email = "another@gmail.com"
		}
		if scenario == "other account" {
			subject = "another"
		}
		if scenario == "wrong nonce" {
			n = "wrong"
		}
		id, _ := jwt.Signed(signer).Claims(jwt.Claims{Issuer: GoogleIssuer, Subject: subject, Audience: jwt.Audience{"fixture-client"}, Expiry: jwt.NewNumericDate(time.Now().Add(time.Hour))}).Claims(map[string]any{"nonce": n, "email": email, "email_verified": true}).Serialize()
		scope := "openid email profile " + GmailSendScope
		if scenario == "send scope declined" {
			scope = "openid email profile"
		}
		refresh := "fixture-refresh-secret"
		if scenario == "no offline grant" {
			refresh = ""
		}
		json.NewEncoder(w).Encode(map[string]any{"access_token": "fixture-access-secret", "token_type": "Bearer", "refresh_token": refresh, "expires_in": 3600, "scope": scope, "id_token": id})
	}))
	defer provider.Close()
	a.OAuth = oauth2.Config{ClientID: "fixture-client", ClientSecret: "fixture-secret", Endpoint: oauth2.Endpoint{AuthURL: GoogleIssuer + "/authorize", TokenURL: provider.URL, AuthStyle: oauth2.AuthStyleInParams}, RedirectURL: a.BaseURL + "/auth/callback"}
	a.Verifier = oidc.NewVerifier(GoogleIssuer, fixtureKeys{&key.PublicKey}, &oidc.Config{ClientID: "fixture-client"})
	u := Identity{Subject: "owner", Email: "owner@gmail.com", EmailVerified: true, Admin: true, Expires: time.Now().Add(time.Hour).Unix()}
	store := NewMemoryUsageStore(s.Config.Secret, false)
	a.Usage = NewUsage(store)
	t.Cleanup(a.Usage.Close)
	appSession, err := store.Start(context.Background(), u, ClientInfo{}, a.Now())
	if err != nil {
		t.Fatal(err)
	}
	u.SessionToken = appSession.Token
	for _, name := range []string{"accepted", "other email", "other account", "wrong nonce", "wrong state", "expired flow", "send scope declined", "no offline grant"} {
		t.Run(name, func(t *testing.T) {
			scenario = name
			requests = 0
			cfg := Config{Secret: []byte(strings.Repeat("s", 32)), GmailEnabled: true, GmailSenderEmail: u.Email, GmailTokenFile: filepath.Join(t.TempDir(), "sender.enc")}
			g := NewGmailSender(a, cfg)
			w := httptest.NewRecorder()
			g.Connect(w, httptest.NewRequest("POST", "http://example.test/api/email/connect", nil), u)
			if w.Code != 200 {
				t.Fatalf("connection status %d", w.Code)
			}
			var result map[string]string
			json.Unmarshal(w.Body.Bytes(), &result)
			target, _ := url.Parse(result["authorization_url"])
			if target.Query().Get("access_type") != "offline" || !strings.Contains(target.Query().Get("scope"), GmailSendScope) || target.Query().Get("prompt") != "consent select_account" || target.Query().Get("code_challenge_method") != "S256" {
				t.Fatal("missing Gmail consent controls")
			}
			nonce = target.Query().Get("nonce")
			flow := w.Result().Cookies()[0]
			if scenario == "expired flow" {
				var f gmailFlow
				a.unpack(flow.Value, &f)
				f.Expires = time.Now().Add(-time.Minute).Unix()
				flow.Value = a.pack(f)
			}
			state := target.Query().Get("state")
			if scenario == "wrong state" {
				state = "wrong"
			}
			r := httptest.NewRequest("GET", "http://example.test/auth/gmail/callback?code=fixture&state="+url.QueryEscape(state), nil).WithContext(oidc.ClientContext(context.Background(), provider.Client()))
			r.AddCookie(flow)
			r.AddCookie(&http.Cookie{Name: "lk_session", Value: a.pack(sessionPayload(u))})
			out := httptest.NewRecorder()
			g.Callback(out, r)
			if name == "accepted" {
				grant, e := g.read()
				if e != nil || grant.Subject != u.Subject || grant.Token.RefreshToken != "fixture-refresh-secret" {
					t.Fatal("offline sender grant not stored")
				}
				raw, _ := os.ReadFile(g.file)
				stat, _ := os.Stat(g.file)
				if bytes.Contains(raw, []byte("fixture-refresh-secret")) || bytes.Contains(raw, []byte("fixture-access-secret")) || stat.Mode().Perm() != 0600 {
					t.Fatal("sender credentials not private and encrypted")
				}
				status := httptest.NewRecorder()
				g.Status(status, r, u)
				if strings.Contains(status.Body.String(), "secret") || !strings.Contains(status.Body.String(), `"connected":true`) {
					t.Fatal("status exposed credentials or lost connection")
				}
			} else if _, err = os.Stat(g.file); !os.IsNotExist(err) {
				t.Fatal("rejected consent wrote credentials")
			}
			if (name == "wrong state" || name == "expired flow") && requests != 0 {
				t.Fatal("invalid flow reached Google token endpoint")
			}
		})
	}
}

func TestGmailRoutesEnforceOriginAdminAndConfiguredSender(t *testing.T) {
	s, _, _ := fixture(t, 1)
	s.Auth.Demo = false
	s.Auth.Admins = []string{"owner"}
	s.Gmail.enabled = true
	s.Gmail.sender = "owner@gmail.com"
	s.Gmail.file = filepath.Join(t.TempDir(), "token.enc")
	store := NewMemoryUsageStore(s.Config.Secret, false)
	s.Usage = NewUsage(store)
	s.Auth.Usage = s.Usage
	t.Cleanup(s.Usage.Close)
	for _, tc := range []struct {
		subject, email, origin string
		code                   int
	}{{"owner", "owner@gmail.com", "https://other.example", 403}, {"viewer", "owner@gmail.com", "http://example.test", 403}, {"owner", "someone@gmail.com", "http://example.test", 403}, {"owner", "owner@gmail.com", "http://example.test", 200}} {
		r := httptest.NewRequest("POST", "http://example.test/api/email/connect", nil)
		r.Header.Set("Origin", tc.origin)
		u := Identity{Subject: tc.subject, Email: tc.email, EmailVerified: true, Expires: time.Now().Add(time.Hour).Unix()}
		state, err := store.Start(context.Background(), u, ClientInfo{}, s.Auth.Now())
		if err != nil {
			t.Fatal(err)
		}
		u.SessionToken = state.Token
		r.AddCookie(&http.Cookie{Name: "lk_session", Value: s.Auth.pack(sessionPayload(u))})
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if w.Code != tc.code {
			t.Fatalf("status=%d want=%d", w.Code, tc.code)
		}
	}
}

func TestGmailStorageBindsSecretClientAndSender(t *testing.T) {
	s, _, _ := fixture(t, 1)
	g := NewGmailSender(s.Auth, Config{GmailEnabled: true, GmailSenderEmail: "owner@gmail.com", GmailTokenFile: filepath.Join(t.TempDir(), "token.enc"), Secret: []byte(strings.Repeat("k", 32))})
	grant := gmailGrant{Subject: "owner", Email: g.sender, Token: oauth2.Token{RefreshToken: "fixture-refresh", AccessToken: "fixture-access"}}
	if err := g.save(grant); err != nil {
		t.Fatal(err)
	}
	other := NewGmailSender(s.Auth, Config{GmailEnabled: true, GmailSenderEmail: g.sender, GmailTokenFile: g.file, Secret: []byte(strings.Repeat("x", 32))})
	if _, err := other.read(); err == nil {
		t.Fatal("wrong encryption secret accepted")
	}
	s.Auth.OAuth.ClientID = "changed-client"
	if _, err := g.read(); err == nil {
		t.Fatal("another OAuth client accepted stored credentials")
	}
	s.Auth.OAuth.ClientID = ""
	g.sender = "different@gmail.com"
	if _, err := g.read(); err == nil {
		t.Fatal("another sender accepted stored credentials")
	}
}

func TestGmailRefreshAndSendHaveNoAutomaticDeliveryRetry(t *testing.T) {
	s, _, _ := fixture(t, 1)
	s.Auth.Admins = []string{"owner"}
	s.Auth.OAuth = oauth2.Config{ClientID: "fixture", ClientSecret: "fixture-secret", Endpoint: oauth2.Endpoint{TokenURL: "https://oauth2.googleapis.com/token", AuthStyle: oauth2.AuthStyleInParams}}
	g := NewGmailSender(s.Auth, Config{GmailEnabled: true, GmailSenderEmail: "owner@gmail.com", GmailTokenFile: filepath.Join(t.TempDir(), "sender.enc"), Secret: []byte(strings.Repeat("s", 32))})
	grant := gmailGrant{Subject: "owner", Email: g.sender, Token: oauth2.Token{AccessToken: "old-access", RefreshToken: "fixture-refresh", Expiry: time.Now().Add(-time.Hour)}}
	if e := g.save(grant); e != nil {
		t.Fatal(e)
	}
	sends, refreshes := 0, 0
	g.client = &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		payload := `{"id":"fixture-message"}`
		code := 200
		if r.URL.Host == "oauth2.googleapis.com" {
			refreshes++
			_ = r.ParseForm()
			if r.Form.Get("refresh_token") != "fixture-refresh" || r.Form.Get("grant_type") != "refresh_token" {
				t.Error("invalid refresh")
			}
			payload = `{"access_token":"new-access","token_type":"Bearer","expires_in":3600}`
		} else if r.URL.Host == "gmail.googleapis.com" {
			sends++
			if r.Header.Get("Authorization") != "Bearer new-access" {
				t.Error("new access token missing")
			}
			if sends == 2 {
				code = 503
				payload = `{}`
			}
		} else {
			t.Error("unexpected outbound host")
		}
		return &http.Response{StatusCode: code, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(payload))}, nil
	})}
	message := OutgoingMail{To: []string{"recipient@example.com"}, Subject: "Example alert", Text: "Example", HTML: "<p>Example</p>", Attachments: []MailAttachment{{Name: "report.csv", ContentType: "text/csv", Data: []byte("a,b\n1,2")}}}
	id, err := g.Send(context.Background(), message)
	if err != nil || id != "fixture-message" || refreshes != 1 || sends != 1 {
		t.Fatal("refresh or send failed")
	}
	saved, err := g.read()
	if err != nil || saved.Token.RefreshToken != "fixture-refresh" || saved.Token.AccessToken != "new-access" {
		t.Fatal("refreshed credentials were not retained")
	}
	if _, err = g.Send(context.Background(), message); err == nil || sends != 2 {
		t.Fatal("uncertain send was retried or reported successful")
	}
}

func TestEmailCardAndAttachmentsRoundTrip(t *testing.T) {
	text, html, err := RenderAlertCard(AlertCard{Title: "<script>bad()</script>", Message: "Fictional report", Device: "Phone & tablet", Area: "Example area", When: "18 Nov", AppURL: "https://viewer.example/?view=logs"})
	if err != nil || strings.Contains(html, "<script>") || !strings.Contains(html, "&lt;script&gt;") || !strings.Contains(text, "Fictional report") {
		t.Fatal("card failed escaping or plain-text fallback")
	}
	data := []byte{0, 1, 255, 42}
	raw, err := BuildEmail("owner@gmail.com", OutgoingMail{To: []string{"recipient@example.com"}, Subject: "Example – report", Text: text, HTML: html, Attachments: []MailAttachment{{Name: "report.pdf", ContentType: "application/pdf", Data: data}}})
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	_, params, _ := mime.ParseMediaType(parsed.Header.Get("Content-Type"))
	mixed := multipart.NewReader(parsed.Body, params["boundary"])
	altPart, err := mixed.NextPart()
	if err != nil {
		t.Fatal(err)
	}
	typ, p, _ := mime.ParseMediaType(altPart.Header.Get("Content-Type"))
	if typ != "multipart/alternative" {
		t.Fatal("missing HTML/plain alternatives")
	}
	alt := multipart.NewReader(altPart, p["boundary"])
	for _, want := range []string{text, html} {
		part, e := alt.NextPart()
		if e != nil {
			t.Fatal(e)
		}
		body, e := io.ReadAll(base64.NewDecoder(base64.StdEncoding, part))
		if e != nil || string(body) != want {
			t.Fatal("email alternative changed")
		}
	}
	attachment, err := mixed.NextPart()
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(base64.NewDecoder(base64.StdEncoding, attachment))
	if err != nil || !bytes.Equal(body, data) || !strings.Contains(attachment.Header.Get("Content-Disposition"), "report.pdf") {
		t.Fatal("attachment changed")
	}
}
func TestEmailRejectsHeaderInjectionAndOversizedAttachments(t *testing.T) {
	for _, m := range []OutgoingMail{
		{To: []string{"ok@example.com"}, Subject: "x\r\nBcc: evil@example.com", Text: "x"},
		{To: []string{"ok@example.com\r\nBcc: evil@example.com"}, Subject: "x", Text: "x"},
		{To: []string{"ok@example.com"}, Subject: "x", Text: "x", Attachments: []MailAttachment{{Name: "x\r\nInjected:y", ContentType: "text/plain"}}},
		{To: []string{"ok@example.com"}, Subject: "x", Text: "x", Attachments: []MailAttachment{{Name: "x", ContentType: "text/plain", Data: make([]byte, MaxAttachmentBytes+1)}}},
	} {
		if _, err := BuildEmail("owner@gmail.com", m); err == nil {
			t.Fatal("unsafe email accepted")
		}
	}
}
