package app

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

type Server struct {
	Repo   Repository
	Auth   *Auth
	Places *Places
	Signer Signer
	Config Config
	Now    func() time.Time
	Gmail  *GmailSender
	Usage  *Usage
	Access DeviceAccessStore
}

func NewServer(repo Repository, auth *Auth, places *Places, c Config) *Server {
	s := &Server{Repo: repo, Auth: auth, Places: places, Signer: Signer{c.Secret, c.CursorTTL}, Config: c, Now: time.Now, Gmail: NewGmailSender(auth, c)}
	if c.Demo {
		s.Access = NewMemoryDeviceAccessStore(c.Secret)
	}
	return s
}
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "private, no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func fail(w http.ResponseWriter, err error) {
	var a APIError
	if errors.As(err, &a) {
		writeJSON(w, a.Status, map[string]string{"error": a.Message, "code": a.Code})
		return
	}
	writeJSON(w, 503, map[string]string{"error": "The data service is temporarily unavailable. Please retry.", "code": "service_unavailable"})
}

type identityKey struct{}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, map[string]bool{"ok": true}) })
	mux.HandleFunc("GET /api/session", s.session)
	mux.HandleFunc("GET /auth/login", s.Auth.Login)
	mux.HandleFunc("GET /auth/callback", s.Auth.Callback)
	mux.HandleFunc("GET /auth/gmail/callback", s.Gmail.Callback)
	protected := func(fn func(http.ResponseWriter, *http.Request, Identity)) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
			defer cancel()
			r = r.WithContext(ctx)
			u, e := s.Auth.Authenticate(w, r)
			if e != nil {
				fail(w, e)
				return
			}
			if r.Method == "POST" {
				if r.Header.Get("Origin") != strings.TrimRight(s.Config.BaseURL, "/") {
					fail(w, APIError{403, "origin_rejected", "This request origin is not allowed."})
					return
				}
			}
			fn(w, r, u)
		}
	}
	mux.HandleFunc("POST /auth/logout", protected(func(w http.ResponseWriter, r *http.Request, _ Identity) { s.Auth.Logout(w, r) }))
	mux.HandleFunc("GET /api/devices", protected(s.devices))
	mux.HandleFunc("POST /api/devices/unlock", protected(s.unlockDevice))
	mux.HandleFunc("POST /api/usage/heartbeat", protected(s.usageHeartbeat))
	mux.HandleFunc("POST /api/usage/view", protected(s.usageView))
	mux.HandleFunc("GET /api/analytics/overview", protected(s.analyticsOverview))
	mux.HandleFunc("GET /api/analytics/users", protected(s.analyticsPage("users")))
	mux.HandleFunc("GET /api/analytics/sessions", protected(s.analyticsPage("sessions")))
	mux.HandleFunc("GET /api/analytics/requests", protected(s.analyticsPage("requests")))
	mux.HandleFunc("GET /api/email/status", protected(s.Gmail.Status))
	mux.HandleFunc("POST /api/email/connect", protected(s.Gmail.Connect))
	mux.HandleFunc("POST /api/email/disconnect", protected(s.Gmail.Disconnect))
	mux.HandleFunc("GET /api/events", protected(s.events))
	mux.HandleFunc("GET /api/events/changes", protected(s.changes))
	for _, kind := range []string{"calls", "notifications", "app-usage"} {
		mux.HandleFunc("GET /api/history/"+kind, protected(s.deviceHistory(kind)))
	}
	mux.HandleFunc("GET /api/locations/latest", protected(s.latest))
	mux.HandleFunc("GET /api/locations/history", protected(s.history))
	mux.HandleFunc("GET /api/activity/summary", protected(s.summary))
	mux.HandleFunc("POST /api/places/resolve", protected(s.resolve))
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		fail(w, APIError{404, "not_found", "API route not found."})
	})
	mux.HandleFunc("/", s.static)
	base := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("X-Frame-Options", "DENY")
		defer func() {
			if recover() != nil {
				fail(w, errors.New("request failed"))
			}
		}()
		mux.ServeHTTP(w, r)
	})
	if s.Usage != nil {
		return s.Usage.Wrap(base)
	}
	return base
}
func (s *Server) session(w http.ResponseWriter, r *http.Request) {
	u, e := s.Auth.Authenticate(w, r)
	var apiErr APIError
	if errors.As(e, &apiErr) && apiErr.Status >= 500 {
		fail(w, e)
		return
	}
	style := "https://tiles.openfreemap.org/styles/liberty"
	if s.Config.MapKey != "" {
		style = "https://maps.geoapify.com/v1/styles/osm-bright/style.json?apiKey=" + url.QueryEscape(s.Config.MapKey)
	}
	result := map[string]any{"authenticated": e == nil, "demo": s.Config.Demo, "now": FormatTime(s.Now()), "map_style": style, "geocoding_enabled": s.Config.GeoKey != "" || s.Config.Demo, "max_devices": MaxDevices, "login_url": "/auth/login", "analytics_enabled": s.Usage != nil, "reasons": Reasons, "outcomes": Outcomes, "statuses": Statuses, "transports": Transports}
	if e == nil {
		result["user"] = u
	}
	writeJSON(w, 200, result)
}
func (s *Server) authorised(r *http.Request, u Identity, ids []string) ([]Device, error) {
	if len(ids) > MaxDevices || !ValidIDs(ids) {
		return nil, APIError{400, "invalid_devices", "Choose up to 20 valid device IDs."}
	}
	devices, e := s.accessibleDevices(r.Context(), u)
	if e != nil {
		return nil, e
	}
	for _, id := range ids {
		if !slices.ContainsFunc(devices, func(d Device) bool { return d.ID == id }) {
			return nil, APIError{403, "access_denied", "A selected device is not available to this account."}
		}
		if slices.ContainsFunc(devices, func(d Device) bool { return d.ID == id && d.Locked }) {
			return nil, APIError{403, "device_locked", "Enter this device's PIN to view its history."}
		}
	}
	devices = slices.DeleteFunc(devices, func(d Device) bool { return d.Locked })
	slices.SortFunc(devices, func(a, b Device) int { return strings.Compare(a.ID, b.ID) })
	return devices, nil
}
func (s *Server) filter(r *http.Request, u Identity) (Filter, []Device, error) {
	f, e := ReadFilter(r.URL.Query(), s.Now())
	if e != nil {
		return f, nil, APIError{400, "invalid_filters", e.Error()}
	}
	d, e := s.authorised(r, u, f.DeviceIDs)
	return f, d, e
}
func (s *Server) devices(w http.ResponseWriter, r *http.Request, u Identity) {
	d, e := s.accessibleDevices(r.Context(), u)
	if e != nil {
		fail(w, e)
		return
	}
	query := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	if len(query) > 200 {
		fail(w, APIError{400, "invalid_search", "Search text is too long."})
		return
	}
	out := []Device{}
	for _, v := range d {
		if strings.Contains(strings.ToLower(v.Name+" "+v.Model+" "+v.Label), query) {
			out = append(out, v)
		}
	}
	writeJSON(w, 200, map[string]any{"items": out})
}
func (s *Server) events(w http.ResponseWriter, r *http.Request, u Identity) {
	f, devices, e := s.filter(r, u)
	if e != nil {
		fail(w, e)
		return
	}
	now := s.Now()
	c := Cursor{Version: 1, Subject: u.Subject, Scope: ScopeHash(u.Subject, devices), Filter: f.Hash(), Expiry: now.Add(s.Signer.TTL).Unix()}
	if token := r.URL.Query().Get("cursor"); token != "" {
		previous, err := s.Signer.Decode(token, now)
		if err != nil {
			fail(w, err)
			return
		}
		if previous.Subject != u.Subject || previous.Scope != c.Scope || previous.Filter != c.Filter {
			fail(w, APIError{409, "cursor_scope_changed", "The filters or access permissions changed. Restart the event list."})
			return
		}
		c = previous
	} else if len(f.DeviceIDs) > 0 {
		c.Read, e = s.Repo.Snapshot(r.Context())
		if e != nil {
			fail(w, e)
			return
		}
	} else {
		c.Read = ReadPoint{At: FormatTime(now)}
	}
	rows := []Event{}
	if len(f.DeviceIDs) > 0 {
		rows, e = s.Repo.Events(r.Context(), f, c.Read, c, PageSize+1)
	}
	if e != nil {
		fail(w, e)
		return
	}
	var start *string
	if c.Read.ID != "" {
		start = Ptr(s.Signer.Encode(c))
	}
	more := len(rows) > PageSize
	if more {
		rows = rows[:PageSize]
	}
	s.Places.Enrich(rows)
	var next *string
	if more {
		last := rows[len(rows)-1]
		c.LastTime = last.Recorded
		c.LastID = last.ID
		next = Ptr(s.Signer.Encode(c))
	}
	writeJSON(w, 200, Page{Items: rows, Start: start, Next: next, More: more, AsOf: c.Read.At, Expires: FormatTime(time.Unix(c.Expiry, 0)), Order: "recorded_at_desc,event_id_desc"})
}
func (s *Server) latest(w http.ResponseWriter, r *http.Request, u Identity) {
	ids := List(r.URL.Query().Get("device_ids"))
	if _, e := s.authorised(r, u, ids); e != nil {
		fail(w, e)
		return
	}
	rows, e := s.Repo.Latest(r.Context(), ids)
	if e != nil {
		fail(w, e)
		return
	}
	for i := range rows {
		if rows[i].Fix != nil {
			rows[i].Fix.Place, _ = s.Places.lookup(*rows[i].Fix)
		}
	}
	writeJSON(w, 200, map[string]any{"items": rows, "as_of": FormatTime(s.Now())})
}
func (s *Server) history(w http.ResponseWriter, r *http.Request, u Identity) {
	f, _, e := s.filter(r, u)
	if e != nil {
		fail(w, e)
		return
	}
	p, e := s.Repo.Snapshot(r.Context())
	if e != nil {
		fail(w, e)
		return
	}
	rows, total, e := s.Repo.History(r.Context(), f, p, MaxMapPoints)
	if e != nil {
		fail(w, e)
		return
	}
	simplified := total > MaxMapPoints
	resolution := "observations"
	if len(rows) > MaxMapPoints {
		out := []Event{}
		seen := map[string]bool{}
		SortEvents(rows)
		for _, row := range rows {
			key := row.DeviceID + (*row.Captured)[:10]
			if !seen[key] {
				seen[key] = true
				out = append(out, row)
			}
		}
		rows = out
	}
	if simplified {
		resolution = "daily_representatives_utc"
	}
	breaks, e := s.Repo.Interruptions(r.Context(), f, p)
	if e != nil {
		fail(w, e)
		return
	}
	s.Places.Enrich(rows, true)
	writeJSON(w, 200, map[string]any{"interruptions": breaks, "items": rows, "total_observations": total, "returned_observations": len(rows), "simplified": simplified, "resolution": resolution, "limit": MaxMapPoints, "as_of": p.At})
}
func (s *Server) summary(w http.ResponseWriter, r *http.Request, u Identity) {
	f, _, e := s.filter(r, u)
	if e != nil {
		fail(w, e)
		return
	}
	rows, e := s.Repo.Summary(r.Context(), f)
	if e != nil {
		fail(w, e)
		return
	}
	writeJSON(w, 200, map[string]any{"days": rows, "timezone": f.Timezone})
}
func (s *Server) changes(w http.ResponseWriter, r *http.Request, u Identity) {
	ids := List(r.URL.Query().Get("device_ids"))
	if _, e := s.authorised(r, u, ids); e != nil {
		fail(w, e)
		return
	}
	after := r.URL.Query().Get("as_of")
	if ParseTime(after).IsZero() {
		fail(w, APIError{400, "invalid_time", "A valid read time is required."})
		return
	}
	has, e := s.Repo.HasNew(r.Context(), ids, FormatTime(ParseTime(after)))
	if e != nil {
		fail(w, e)
		return
	}
	writeJSON(w, 200, map[string]bool{"available": has})
}
func (s *Server) resolve(w http.ResponseWriter, r *http.Request, u Identity) {
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		fail(w, APIError{415, "json_required", "Use application/json."})
		return
	}
	var b struct {
		IDs     []string `json:"event_ids"`
		Refresh bool     `json:"refresh"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 12000))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&b) != nil || len(b.IDs) > 100 || (b.Refresh && len(b.IDs) != 1) || !ValidIDs(b.IDs) {
		fail(w, APIError{400, "invalid_events", "Choose up to 100 valid events."})
		return
	}
	devices, e := s.accessibleDevices(r.Context(), u)
	if e != nil {
		fail(w, e)
		return
	}
	allowed := []string{}
	for _, d := range devices {
		if !d.Locked {
			allowed = append(allowed, d.ID)
		}
	}
	rows, e := s.Repo.EventsByID(r.Context(), b.IDs, allowed)
	if e != nil {
		fail(w, e)
		return
	}
	if len(rows) != len(List(strings.Join(b.IDs, ","))) {
		fail(w, APIError{403, "access_denied", "One or more events are unavailable to this account."})
		return
	}
	writeJSON(w, 200, map[string]any{"items": s.Places.Resolve(rows, b.Refresh)})
}
func (s *Server) static(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" && r.Method != "HEAD" {
		w.WriteHeader(405)
		return
	}
	root, e := filepath.Abs(s.Config.WebDir)
	if e != nil {
		http.NotFound(w, r)
		return
	}
	clean := filepath.Clean("/" + r.URL.Path)
	path := filepath.Join(root, clean)
	if !strings.HasPrefix(path, root+string(os.PathSeparator)) && path != root {
		http.NotFound(w, r)
		return
	}
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		if strings.Contains(filepath.Base(path), ".") && !errors.Is(err, fs.ErrNotExist) {
			http.NotFound(w, r)
			return
		}
		path = filepath.Join(root, "index.html")
	}
	if _, e = os.Stat(path); e != nil {
		writeJSON(w, 404, map[string]string{"error": "Build the web app, or run the Vite development server.", "code": "web_not_built"})
		return
	}
	if filepath.Base(path) == "index.html" {
		w.Header().Set("Cache-Control", "no-store")
	}
	http.ServeFile(w, r, path)
}
