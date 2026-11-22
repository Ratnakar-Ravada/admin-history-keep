package app

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
	"io"
	"net/http"
	"net/mail"
	"slices"
	"strings"
	"time"
)

type Auth struct {
	Demo        bool
	BaseURL     string
	Provider    *oidc.Provider
	OAuth       oauth2.Config
	Verifier    *oidc.IDTokenVerifier
	Admins      []string
	AdminEmails []string
	Assigned    bool
	seal        cipher.AEAD
	Now         func() time.Time
	Usage       *Usage
}

const GoogleIssuer = "https://accounts.google.com"

func NewAuth(ctx context.Context, c Config) (*Auth, error) {
	return newGoogleAuth(ctx, c, GoogleIssuer)
}

// The issuer parameter is internal to support local protocol tests. There is no
// environment override: deployed sign-in always verifies Google's issuer.
func newGoogleAuth(ctx context.Context, c Config, issuer string) (*Auth, error) {
	// Changing providers deliberately invalidates sessions issued by the old app.
	key := sha256.Sum256(append([]byte("google-session:v1:"), c.Secret...))
	block, _ := aes.NewCipher(key[:])
	seal, _ := cipher.NewGCM(block)
	a := &Auth{Demo: c.Demo, BaseURL: strings.TrimRight(c.BaseURL, "/"), Admins: List(c.GoogleAdminSubjects), Assigned: c.Assigned, seal: seal, Now: time.Now}
	if c.Demo {
		return a, nil
	}
	for _, email := range List(c.GoogleAdminEmails) {
		email = strings.ToLower(email)
		if !validEmail(email) {
			return nil, errors.New("GOOGLE_ADMIN_EMAILS must contain exact email addresses")
		}
		a.AdminEmails = append(a.AdminEmails, email)
	}
	if c.GoogleClientID == "" || c.GoogleClientSecret == "" {
		return nil, errors.New("Live mode requires GOOGLE_CLIENT_ID and GOOGLE_CLIENT_SECRET")
	}
	p, err := oidc.NewProvider(ctx, issuer)
	if err != nil {
		return nil, errors.New("Google sign-in discovery failed")
	}
	a.Provider = p
	endpoint := p.Endpoint()
	endpoint.AuthStyle = oauth2.AuthStyleInParams
	a.OAuth = oauth2.Config{ClientID: c.GoogleClientID, ClientSecret: c.GoogleClientSecret, Endpoint: endpoint, RedirectURL: a.BaseURL + "/auth/callback", Scopes: []string{oidc.ScopeOpenID, "email", "profile"}}
	a.Verifier = p.Verifier(&oidc.Config{ClientID: c.GoogleClientID})
	return a, nil
}

func validEmail(email string) bool {
	if strings.ContainsAny(email, "*?[]\r\n") {
		return false
	}
	a, err := mail.ParseAddress(email)
	return err == nil && a.Address == email && len(email) <= 254
}

func (a *Auth) isAdmin(u Identity) bool {
	if slices.Contains(a.Admins, u.Subject) {
		return true
	}
	email := strings.ToLower(u.Email)
	// For non-Gmail consumer accounts Google may no longer control the email
	// address. Such accounts require explicit immutable subject authorization.
	authoritative := strings.HasSuffix(email, "@gmail.com") || u.HostedDomain != ""
	return u.EmailVerified && authoritative && slices.Contains(a.AdminEmails, email)
}

func (a *Auth) pack(v any) string {
	b, _ := json.Marshal(v)
	nonce := make([]byte, a.seal.NonceSize())
	_, _ = io.ReadFull(rand.Reader, nonce)
	return base64.RawURLEncoding.EncodeToString(a.seal.Seal(nonce, nonce, b, nil))
}
func (a *Auth) unpack(s string, v any) error {
	b, e := base64.RawURLEncoding.DecodeString(s)
	if e != nil || len(b) < a.seal.NonceSize() {
		return errors.New("Invalid session")
	}
	b, e = a.seal.Open(nil, b[:a.seal.NonceSize()], b[a.seal.NonceSize():], nil)
	if e != nil {
		return errors.New("Invalid session")
	}
	return json.Unmarshal(b, v)
}
func (a *Auth) cookie(w http.ResponseWriter, name, value string, age int) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: value, Path: "/", HttpOnly: true, Secure: strings.HasPrefix(a.BaseURL, "https://"), SameSite: http.SameSiteLaxMode, MaxAge: age})
}
func (a *Auth) Identity(r *http.Request) (Identity, error) {
	return a.Authenticate(nil, r)
}

// Authenticate validates durable session and account state on every request.
// A response writer lets active browsers receive the periodically renewed cookie.
func (a *Auth) Authenticate(w http.ResponseWriter, r *http.Request) (Identity, error) {
	if a.Demo {
		u := Identity{Subject: "synthetic-demo", Name: "Demo workspace", Admin: true, Advanced: true}
		if a.Usage != nil {
			if memory, ok := a.Usage.Store.(*MemoryUsageStore); ok {
				u = memory.DemoIdentity()
			}
		}
		markUsage(r, u)
		return u, nil
	}
	c, err := r.Cookie("lk_session")
	if err != nil {
		return Identity{}, APIError{401, "sign_in_required", "Sign in to view your devices."}
	}
	var u Identity
	err = a.unpack(c.Value, &u)
	if err != nil || u.Expires <= a.Now().Unix() || u.Subject == "" || !u.EmailVerified || !validEmail(u.Email) {
		return Identity{}, APIError{401, "sign_in_required", "Your sign-in session has expired."}
	}
	if a.Usage == nil || a.Usage.Store == nil {
		return Identity{}, APIError{503, "session_service_unavailable", "Session verification is temporarily unavailable."}
	}
	if u.SessionToken == "" {
		return Identity{}, APIError{401, "session_upgrade_required", "Sign in again to create your tracked app session."}
	}
	state, err := a.Usage.Store.Validate(r.Context(), u.Subject, u.SessionToken, a.Now())
	if err != nil {
		var api APIError
		if errors.As(err, &api) {
			return Identity{}, api
		}
		return Identity{}, APIError{503, "session_service_unavailable", "Session verification is temporarily unavailable."}
	}
	u.UserID = state.UserID
	u.SessionID = state.ID
	u.Advanced = state.Advanced
	u.Admin = a.isAdmin(u)
	renewCookie := state.Expires > u.Expires
	u.Expires = state.Expires
	if w != nil && renewCookie {
		a.cookie(w, "lk_session", a.pack(sessionPayload(u)), int(time.Unix(u.Expires, 0).Sub(a.Now()).Seconds()))
	}
	markUsage(r, u)
	return u, nil
}

type authFlow struct {
	State    string
	Nonce    string
	Verifier string
	Expires  int64
}

func randomToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}
func (a *Auth) Login(w http.ResponseWriter, r *http.Request) {
	if a.Demo {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	f := authFlow{randomToken(), randomToken(), oauth2.GenerateVerifier(), a.Now().Add(5 * time.Minute).Unix()}
	a.cookie(w, "lk_flow", a.pack(f), 300)
	http.Redirect(w, r, a.OAuth.AuthCodeURL(f.State, oidc.Nonce(f.Nonce), oauth2.S256ChallengeOption(f.Verifier), oauth2.SetAuthURLParam("prompt", "select_account")), http.StatusFound)
}
func (a *Auth) Callback(w http.ResponseWriter, r *http.Request) {
	fail := func() {
		a.cookie(w, "lk_flow", "", -1)
		a.cookie(w, "lk_session", "", -1)
		http.Redirect(w, r, "/?signin=failed", http.StatusSeeOther)
	}
	if a.Demo {
		fail()
		return
	}
	cookie, e := r.Cookie("lk_flow")
	if e != nil {
		fail()
		return
	}
	var f authFlow
	if a.unpack(cookie.Value, &f) != nil || f.Expires <= a.Now().Unix() || r.URL.Query().Get("state") != f.State || r.URL.Query().Get("code") == "" {
		fail()
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	u, _, e := a.exchangeGoogleIdentity(ctx, a.OAuth, r.URL.Query().Get("code"), f.Verifier, f.Nonce)
	if e != nil {
		fail()
		return
	}
	if a.Usage == nil || a.Usage.Store == nil {
		fail()
		return
	}
	// Google's token authenticates the sign-in. The app session has its own
	// rolling lifetime and never stores Google's access, ID or refresh tokens.
	expiry := a.Now().Add(SessionLifetime)
	u.Admin = a.isAdmin(u)
	u.Expires = expiry.Unix()
	if u.Name == "" {
		u.Name = "Location Keeper viewer"
	}
	state, err := a.Usage.Store.Start(ctx, u, clientInfo(r.UserAgent()), a.Now().UTC())
	if err != nil {
		fail()
		return
	}
	u.UserID = state.UserID
	u.SessionID = state.ID
	u.SessionToken = state.Token
	u.Advanced = state.Advanced
	markUsage(r, u)
	a.cookie(w, "lk_session", a.pack(sessionPayload(u)), int(expiry.Sub(a.Now()).Seconds()))
	a.cookie(w, "lk_flow", "", -1)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// Both Google flows verify identity tokens before accepting account permissions.
func (a *Auth) exchangeGoogleIdentity(ctx context.Context, cfg oauth2.Config, code, verifier, nonce string) (Identity, *oauth2.Token, error) {
	denied := errors.New("Google account could not be verified")
	token, err := cfg.Exchange(ctx, code, oauth2.VerifierOption(verifier))
	if err != nil {
		return Identity{}, nil, denied
	}
	raw, ok := token.Extra("id_token").(string)
	if !ok {
		return Identity{}, nil, denied
	}
	verified, err := a.Verifier.Verify(ctx, raw)
	if err != nil || verified.Nonce != nonce || verified.Subject == "" || len(verified.Subject) > 255 {
		return Identity{}, nil, denied
	}
	var claims struct {
		Name     string `json:"name"`
		Email    string `json:"email"`
		Verified bool   `json:"email_verified"`
		Domain   string `json:"hd"`
	}
	if verified.Claims(&claims) != nil || !claims.Verified || !validEmail(claims.Email) {
		return Identity{}, nil, denied
	}
	return Identity{Subject: verified.Subject, Name: claims.Name, Email: strings.ToLower(claims.Email), EmailVerified: true, HostedDomain: claims.Domain, Expires: verified.Expiry.Unix()}, token, nil
}

// Explicit session projection includes expiry, which is never returned by the session API.
type sessionPayload Identity

func (s sessionPayload) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Subject       string `json:"subject"`
		Name          string `json:"name"`
		Email         string `json:"email"`
		EmailVerified bool   `json:"email_verified"`
		HostedDomain  string `json:"hd,omitempty"`
		Expires       int64  `json:"exp"`
		SID           string `json:"sid,omitempty"`
	}{s.Subject, s.Name, s.Email, s.EmailVerified, s.HostedDomain, s.Expires, s.SessionToken})
}
func (u *Identity) UnmarshalJSON(b []byte) error {
	var v struct {
		SID           string `json:"sid"`
		Subject       string `json:"subject"`
		Name          string `json:"name"`
		Email         string `json:"email"`
		EmailVerified bool   `json:"email_verified"`
		HostedDomain  string `json:"hd"`
		Expires       int64  `json:"exp"`
	}
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	u.SessionToken = v.SID
	u.Subject = v.Subject
	u.Name = v.Name
	u.Email = v.Email
	u.EmailVerified = v.EmailVerified
	u.HostedDomain = v.HostedDomain
	u.Expires = v.Expires
	return nil
}
func (a *Auth) Logout(w http.ResponseWriter, r *http.Request) {
	if !a.Demo {
		u, err := a.Identity(r)
		if err != nil {
			fail(w, err)
			return
		}
		if err := a.Usage.Store.End(r.Context(), u.SessionID, a.Now().UTC()); err != nil {
			fail(w, err)
			return
		}
	}

	a.cookie(w, "lk_session", "", -1)
	writeJSON(w, 200, map[string]bool{"signed_out": true})
}
