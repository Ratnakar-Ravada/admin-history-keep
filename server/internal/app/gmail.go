package app

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

const GmailSendScope = "https://www.googleapis.com/auth/gmail.send"

type gmailGrant struct {
	Subject   string       `json:"subject"`
	Email     string       `json:"email"`
	Connected string       `json:"connected_at"`
	Domain    string       `json:"hd,omitempty"`
	Token     oauth2.Token `json:"token"`
}

type GmailSender struct {
	mu           sync.Mutex
	auth         *Auth
	enabled      bool
	sender, file string
	seal         cipher.AEAD
	client       *http.Client
}

func NewGmailSender(a *Auth, c Config) *GmailSender {
	key := sha256.Sum256(append([]byte("gmail-sender:v1:"), c.Secret...))
	block, _ := aes.NewCipher(key[:])
	seal, _ := cipher.NewGCM(block)
	return &GmailSender{auth: a, enabled: c.GmailEnabled && !c.Demo, sender: strings.ToLower(c.GmailSenderEmail), file: c.GmailTokenFile, seal: seal, client: &http.Client{Timeout: 20 * time.Second}}
}
func (g *GmailSender) oauth() oauth2.Config {
	cfg := g.auth.OAuth
	cfg.RedirectURL = g.auth.BaseURL + "/auth/gmail/callback"
	cfg.Scopes = []string{oidc.ScopeOpenID, "email", "profile", GmailSendScope}
	return cfg
}
func (g *GmailSender) authorize(u Identity) error {
	if !u.Admin {
		return APIError{403, "admin_required", "Only an administrator can manage the alert sender."}
	}
	if !g.enabled {
		return APIError{409, "gmail_not_configured", "Configure Google sign-in and the Gmail sender before connecting email."}
	}
	if !strings.EqualFold(u.Email, g.sender) {
		return APIError{403, "sender_mismatch", "Sign in with the configured sender's Google account to manage email."}
	}
	return nil
}
func (g *GmailSender) aad() []byte {
	return []byte("location-keeper:gmail:" + g.auth.OAuth.ClientID + ":" + g.sender)
}
func (g *GmailSender) read() (gmailGrant, error) {
	var grant gmailGrant
	f, err := os.Open(g.file)
	if err != nil {
		return grant, err
	}
	defer f.Close()
	stat, err := f.Stat()
	if err != nil || !stat.Mode().IsRegular() || stat.Mode().Perm()&0077 != 0 {
		return grant, errors.New("Sender storage permissions are invalid")
	}
	raw, err := io.ReadAll(io.LimitReader(f, 65537))
	if err != nil || len(raw) > 65536 || len(raw) < g.seal.NonceSize() {
		return grant, errors.New("Sender storage is invalid")
	}
	raw, err = g.seal.Open(nil, raw[:g.seal.NonceSize()], raw[g.seal.NonceSize():], g.aad())
	if err != nil {
		return grant, errors.New("Reconnect the Gmail sender")
	}
	if json.Unmarshal(raw, &grant) != nil || grant.Email != g.sender || grant.Subject == "" || grant.Token.RefreshToken == "" {
		return grant, errors.New("Reconnect the Gmail sender")
	}
	return grant, nil
}
func (g *GmailSender) save(grant gmailGrant) error {
	raw, err := json.Marshal(grant)
	if err != nil {
		return err
	}
	nonce := make([]byte, g.seal.NonceSize())
	if _, err = io.ReadFull(rand.Reader, nonce); err != nil {
		return err
	}
	raw = g.seal.Seal(nonce, nonce, raw, g.aad())
	dir := filepath.Dir(g.file)
	if err = os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".gmail-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if _, err = f.Write(raw); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), g.file)
}
func (g *GmailSender) Status(w http.ResponseWriter, r *http.Request, u Identity) {
	if !u.Admin {
		fail(w, APIError{403, "admin_required", "Only an administrator can view the alert sender."})
		return
	}
	result := map[string]any{"enabled": g.enabled, "connected": false, "can_connect": g.enabled && strings.EqualFold(u.Email, g.sender), "sender_email": g.sender, "capabilities": []string{"plain_text", "html_cards", "attachments"}, "automated_alerts": false}
	if g.enabled {
		g.mu.Lock()
		grant, err := g.read()
		g.mu.Unlock()
		if err == nil {
			result["connected"] = true
			result["connected_at"] = grant.Connected
		} else if !errors.Is(err, os.ErrNotExist) {
			result["reconnect_required"] = true
		}
	}
	writeJSON(w, 200, result)
}

type gmailFlow struct {
	authFlow
	Subject, Email string
}

func (g *GmailSender) Connect(w http.ResponseWriter, r *http.Request, u Identity) {
	if err := g.authorize(u); err != nil {
		fail(w, err)
		return
	}
	f := gmailFlow{authFlow: authFlow{randomToken(), randomToken(), oauth2.GenerateVerifier(), g.auth.Now().Add(5 * time.Minute).Unix()}, Subject: u.Subject, Email: u.Email}
	g.auth.cookie(w, "lk_gmail_flow", g.auth.pack(f), 300)
	cfg := g.oauth()
	target := cfg.AuthCodeURL(f.State, oidc.Nonce(f.Nonce), oauth2.S256ChallengeOption(f.Verifier), oauth2.AccessTypeOffline, oauth2.SetAuthURLParam("prompt", "consent select_account"), oauth2.SetAuthURLParam("include_granted_scopes", "true"))
	writeJSON(w, 200, map[string]string{"authorization_url": target})
}
func (g *GmailSender) Callback(w http.ResponseWriter, r *http.Request) {
	done := func(ok bool) {
		g.auth.cookie(w, "lk_gmail_flow", "", -1)
		q := url.Values{"view": {"alerts"}}
		if ok {
			q.Set("email", "connected")
		} else {
			q.Set("email", "failed")
		}
		http.Redirect(w, r, "/?"+q.Encode(), http.StatusSeeOther)
	}
	u, err := g.auth.Identity(r)
	if err != nil || g.authorize(u) != nil {
		done(false)
		return
	}
	cookie, err := r.Cookie("lk_gmail_flow")
	if err != nil {
		done(false)
		return
	}
	var f gmailFlow
	if g.auth.unpack(cookie.Value, &f) != nil || f.Expires <= g.auth.Now().Unix() || f.State != r.URL.Query().Get("state") || f.Subject != u.Subject || f.Email != u.Email || r.URL.Query().Get("code") == "" {
		done(false)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	identity, token, err := g.auth.exchangeGoogleIdentity(ctx, g.oauth(), r.URL.Query().Get("code"), f.Verifier, f.Nonce)
	if err != nil || identity.Subject != u.Subject || identity.Email != g.sender || !g.auth.isAdmin(identity) {
		done(false)
		return
	}
	scopes, _ := token.Extra("scope").(string)
	if !slices.Contains(strings.Fields(scopes), GmailSendScope) {
		done(false)
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if token.RefreshToken == "" {
		if old, e := g.read(); e == nil && old.Subject == identity.Subject {
			token.RefreshToken = old.Token.RefreshToken
		}
	}
	if token.RefreshToken == "" {
		done(false)
		return
	}
	grant := gmailGrant{Subject: identity.Subject, Email: identity.Email, Domain: identity.HostedDomain, Connected: FormatTime(g.auth.Now()), Token: *token}
	if g.save(grant) != nil {
		done(false)
		return
	}
	done(true)
}
func (g *GmailSender) Disconnect(w http.ResponseWriter, r *http.Request, u Identity) {
	if err := g.authorize(u); err != nil {
		fail(w, err)
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if err := os.Remove(g.file); err != nil && !errors.Is(err, os.ErrNotExist) {
		fail(w, errors.New("Unable to remove sender credentials"))
		return
	}
	writeJSON(w, 200, map[string]bool{"disconnected": true})
}

// Send is for the future alert job. No public send endpoint or automatic retry
// is exposed: an uncertain POST result must not silently duplicate an alert.
func (g *GmailSender) Send(ctx context.Context, m OutgoingMail) (string, error) {
	if !g.enabled {
		return "", errors.New("Gmail sender is not configured")
	}
	raw, err := BuildEmail(g.sender, m)
	if err != nil {
		return "", err
	}
	g.mu.Lock()
	grant, err := g.read()
	if err != nil {
		g.mu.Unlock()
		return "", errors.New("Reconnect the Gmail sender")
	}
	if !g.auth.isAdmin(Identity{Subject: grant.Subject, Email: grant.Email, EmailVerified: true, HostedDomain: grant.Domain}) {
		g.mu.Unlock()
		return "", errors.New("The Gmail sender no longer has administrator access")
	}
	ctx = context.WithValue(ctx, oauth2.HTTPClient, g.client)
	cfg := g.oauth()
	token, err := cfg.TokenSource(ctx, &grant.Token).Token()
	if err != nil {
		g.mu.Unlock()
		return "", errors.New("Gmail authorization expired; reconnect the sender")
	}
	grant.Token = *token
	if err = g.save(grant); err != nil {
		g.mu.Unlock()
		return "", errors.New("Unable to preserve sender credentials")
	}
	g.mu.Unlock()
	return sendGmail(ctx, g.client, token.AccessToken, raw)
}
