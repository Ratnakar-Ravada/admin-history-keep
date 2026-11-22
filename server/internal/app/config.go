package app

import (
	"bufio"
	"crypto/rand"
	"errors"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	AppDBDSN            string
	TiDBTransport       string
	Addr                string
	BaseURL             string
	Demo                bool
	DSN                 string
	CacheDSN            string
	Secret              []byte
	GoogleClientID      string
	GoogleClientSecret  string
	GoogleAdminSubjects string
	GoogleAdminEmails   string
	Assigned            bool
	GeoKey              string
	MapKey              string
	WebDir              string
	CursorTTL           time.Duration
	GmailEnabled        bool
	GmailSenderEmail    string
	GmailTokenFile      string
	PINPepper           string
}

func LoadEnv(path string) error {
	if path == "" {
		return nil
	}
	f, e := os.Open(path)
	if e != nil {
		return e
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		key = strings.TrimSpace(strings.TrimPrefix(key, "export "))
		value = strings.Trim(strings.TrimSpace(value), "\"'")
		if ok && os.Getenv(key) == "" {
			_ = os.Setenv(key, value)
		}
	}
	return sc.Err()
}
func ConfigFromEnv() (Config, error) {
	get := func(k, d string) string {
		if v := os.Getenv(k); v != "" {
			return v
		}
		return d
	}
	c := Config{Addr: get("LISTEN_ADDR", "127.0.0.1:8080"), BaseURL: get("PUBLIC_BASE_URL", "http://127.0.0.1:5173"), Demo: get("DATA_SOURCE", "demo") == "demo", DSN: os.Getenv("TIDB_DSN"), CacheDSN: os.Getenv("PLACE_CACHE_DSN"), Secret: []byte(os.Getenv("SESSION_SECRET")), GoogleClientID: os.Getenv("GOOGLE_CLIENT_ID"), GoogleClientSecret: os.Getenv("GOOGLE_CLIENT_SECRET"), GoogleAdminSubjects: os.Getenv("GOOGLE_ADMIN_SUBJECTS"), GoogleAdminEmails: os.Getenv("GOOGLE_ADMIN_EMAILS"), Assigned: os.Getenv("ALLOW_ASSIGNED_USERS") == "true", GeoKey: os.Getenv("GEOAPIFY_API_KEY"), MapKey: os.Getenv("MAP_PUBLIC_KEY"), WebDir: get("WEB_DIR", "../web/dist"), CursorTTL: 4 * time.Minute}
	c.TiDBTransport = get("TIDB_TRANSPORT", "mysql")
	if c.TiDBTransport != "mysql" && c.TiDBTransport != "https" {
		return c, errors.New("TIDB_TRANSPORT must be mysql or https")
	}
	c.AppDBDSN = get("APP_DB_DSN", c.DSN)
	c.PINPepper = os.Getenv("PIN_PEPPER")
	if c.PINPepper != "" && len(c.PINPepper) < 32 {
		return c, errors.New("PIN_PEPPER must match the registration Worker's secret and contain at least 32 bytes")
	}
	c.GmailEnabled = os.Getenv("GMAIL_ENABLED") == "true"
	c.GmailSenderEmail = strings.ToLower(strings.TrimSpace(os.Getenv("GMAIL_SENDER_EMAIL")))
	c.GmailTokenFile = get("GMAIL_TOKEN_FILE", "../.private/gmail-sender.enc")
	if c.GmailEnabled && !validEmail(c.GmailSenderEmail) {
		return c, errors.New("GMAIL_SENDER_EMAIL must be an exact sender address when Gmail is enabled")
	}

	if v := os.Getenv("CURSOR_TTL_SECONDS"); v != "" {
		n, e := strconv.Atoi(v)
		if e != nil || n < 30 || n > 240 {
			return c, errors.New("CURSOR_TTL_SECONDS must be between 30 and 240 and shorter than the database GC window")
		}
		c.CursorTTL = time.Duration(n) * time.Second
	}
	base, e := url.Parse(c.BaseURL)
	if e != nil || base.Host == "" || (base.Scheme != "http" && base.Scheme != "https") || base.User != nil || base.Fragment != "" || base.RawQuery != "" || base.Path != "" && base.Path != "/" {
		return c, errors.New("PUBLIC_BASE_URL must be an absolute origin")
	}
	if c.Demo {
		host, _, e := net.SplitHostPort(c.Addr)
		if e != nil || !(host == "localhost" || net.ParseIP(host).IsLoopback()) {
			return c, errors.New("Demo mode must bind to loopback")
		}
		if c.DSN != "" || c.CacheDSN != "" {
			return c, errors.New("Demo mode cannot connect to a private database")
		}
		if len(c.Secret) < 32 {
			c.Secret = make([]byte, 32)
			_, _ = rand.Read(c.Secret)
		}
	} else {
		if os.Getenv("DATA_SOURCE") != "tidb" {
			return c, errors.New("DATA_SOURCE must be demo or tidb")
		}
		if c.DSN == "" || len(c.Secret) < 32 {
			return c, errors.New("TiDB mode requires TIDB_DSN and a SESSION_SECRET of at least 32 bytes")
		}
		if base.Scheme != "https" && base.Hostname() != "localhost" && !net.ParseIP(base.Hostname()).IsLoopback() {
			return c, errors.New("PUBLIC_BASE_URL must use HTTPS outside local development")
		}
	}
	return c, nil
}
