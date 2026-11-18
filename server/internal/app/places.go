package app

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

type placeEntry struct {
	Place  *Place
	Status string
	Until  time.Time
}
type placeJob struct {
	Key   string
	Event Event
	Force bool
}
type Places struct {
	mu        sync.Mutex
	cache     map[string]placeEntry
	pending   map[string]bool
	refreshed map[string]time.Time
	queue     chan placeJob
	key       string
	Demo      bool
	DB        *sql.DB
	client    *http.Client
	cancel    context.CancelFunc
	now       func() time.Time
}

func NewPlaces(key string, demo bool, db *sql.DB) *Places {
	p := &Places{cache: map[string]placeEntry{}, pending: map[string]bool{}, refreshed: map[string]time.Time{}, queue: make(chan placeJob, 100), key: key, Demo: demo, DB: db, client: &http.Client{Timeout: 6 * time.Second}, now: time.Now}
	ctx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel
	go p.worker(ctx)
	return p
}
func (p *Places) Close() { p.cancel() }
func placeKey(e Event) string {
	if !e.HasFix() {
		return ""
	}
	level := "area"
	precision := 10000.0
	if e.Permission == "approximate" || *e.Accuracy > 200 {
		level = "city"
		precision = 100.0
	}
	v := fmt.Sprintf("geoapify:en:%s:%.4f:%.4f", level, math.Floor(*e.Lat*precision)/precision, math.Floor(*e.Lon*precision)/precision)
	h := sha256.Sum256([]byte(v))
	return hex.EncodeToString(h[:])
}
func (p *Places) lookup(e Event) (*Place, string) { return p.lookupForce(e, false) }
func (p *Places) lookupForce(e Event, force bool) (*Place, string) {
	if !e.HasFix() {
		return nil, "no_fix"
	}
	if p.Demo {
		return &Place{Label: "London area · illustrative", Level: "area", Provider: "synthetic", LookedUp: e.Recorded, Expires: FormatTime(p.now().Add(30 * 24 * time.Hour)), Attribution: "Fictional demo location"}, "ready"
	}
	if p.key == "" {
		return nil, "not_configured"
	}
	key := placeKey(e)
	p.mu.Lock()
	defer p.mu.Unlock()
	if force && p.now().Sub(p.refreshed[key]) < 10*time.Second {
		force = false
	}
	if p.pending[key] {
		return nil, "pending"
	}
	if !force {
		if c, ok := p.cache[key]; ok && p.now().Before(c.Until) {
			if c.Place == nil || Distance(*e.Lat, *e.Lon, c.Place.ReferenceLat, c.Place.ReferenceLon) <= 2000 {
				return c.Place, c.Status
			}
		}
	}
	p.pending[key] = true
	if force {
		p.refreshed[key] = p.now()
	}
	select {
	case p.queue <- placeJob{Key: key, Event: e, Force: force}:
		return nil, "pending"
	default:
		delete(p.pending, key)
		return nil, "busy"
	}
}

// Enrich only schedules lookups for bounded event pages. History can ask for
// cached labels; the client resolves the few observations actually in view.
func (p *Places) Enrich(events []Event, cachedOnly ...bool) {
	for i := range events {
		if len(cachedOnly) == 0 || !cachedOnly[0] || p.Demo {
			events[i].Place, _ = p.lookup(events[i])
			continue
		}
		p.mu.Lock()
		if c, ok := p.cache[placeKey(events[i])]; ok && p.now().Before(c.Until) {
			events[i].Place = c.Place
		}
		p.mu.Unlock()
	}
}

type PlaceResult struct {
	Status string `json:"status"`
	Place  *Place `json:"place,omitempty"`
}

func (p *Places) Resolve(events []Event, refresh ...bool) map[string]PlaceResult {
	out := map[string]PlaceResult{}
	force := len(refresh) > 0 && refresh[0]
	for _, e := range events {
		place, status := p.lookupForce(e, force)
		out[e.ID] = PlaceResult{status, place}
	}
	return out
}
func (p *Places) worker(ctx context.Context) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case job := <-p.queue:
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			entry := p.load(ctx, job)
			p.mu.Lock()
			if len(p.cache) > 10000 {
				for k, v := range p.cache {
					if p.now().After(v.Until) {
						delete(p.cache, k)
					}
				}
				if len(p.cache) > 10000 {
					p.cache = map[string]placeEntry{}
				}
			}
			p.cache[job.Key] = entry
			delete(p.pending, job.Key)
			p.mu.Unlock()
		}
	}
}
func (p *Places) load(ctx context.Context, job placeJob) placeEntry {
	now := p.now()
	if p.DB != nil && !job.Force {
		var raw string
		err := p.DB.QueryRowContext(ctx, "SELECT payload FROM place_cache WHERE cache_key=? AND expires_at>UTC_TIMESTAMP(3)", job.Key).Scan(&raw)
		if err == nil {
			var saved struct {
				Place *Place  `json:"place"`
				Lat   float64 `json:"reference_latitude"`
				Lon   float64 `json:"reference_longitude"`
			}
			if json.Unmarshal([]byte(raw), &saved) == nil && saved.Place != nil && Distance(*job.Event.Lat, *job.Event.Lon, saved.Lat, saved.Lon) <= 2000 {
				saved.Place.ReferenceLat = saved.Lat
				saved.Place.ReferenceLon = saved.Lon
				return placeEntry{saved.Place, "ready", ParseTime(saved.Place.Expires)}
			}
		}
	}
	failure := placeEntry{nil, "unavailable", now.Add(time.Minute)}
	u := url.URL{Scheme: "https", Host: "api.geoapify.com", Path: "/v1/geocode/reverse"}
	q := url.Values{"lat": {fmt.Sprint(*job.Event.Lat)}, "lon": {fmt.Sprint(*job.Event.Lon)}, "lang": {"en"}, "limit": {"1"}, "format": {"json"}, "apiKey": {p.key}}
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	if err != nil {
		return failure
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return failure
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		if resp.StatusCode == 429 {
			failure.Until = now.Add(5 * time.Minute)
		}
		return failure
	}
	var payload struct {
		Results []struct {
			Suburb   string `json:"suburb"`
			District string `json:"district"`
			City     string `json:"city"`
			State    string `json:"state"`
			Country  string `json:"country"`
		} `json:"results"`
	}
	if json.NewDecoder(http.MaxBytesReader(nil, resp.Body, 1<<20)).Decode(&payload) != nil || len(payload.Results) == 0 {
		return failure
	}
	v := payload.Results[0]
	level := "area"
	parts := []string{}
	if job.Event.Permission != "approximate" && *job.Event.Accuracy <= 200 {
		if v.Suburb != "" {
			parts = append(parts, v.Suburb)
		} else if v.District != "" {
			parts = append(parts, v.District)
		}
	} else {
		level = "city"
	}
	for _, part := range []string{v.City, v.State, v.Country} {
		if part != "" && (len(parts) == 0 || parts[len(parts)-1] != part) {
			parts = append(parts, part)
		}
	}
	if len(parts) == 0 {
		return failure
	}
	place := &Place{Label: strings.Join(parts, ", "), City: v.City, Country: v.Country, Level: level, Provider: "Geoapify", LookedUp: FormatTime(now), Expires: FormatTime(now.Add(30 * 24 * time.Hour)), Attribution: "Powered by Geoapify · © OpenStreetMap contributors", ReferenceLat: *job.Event.Lat, ReferenceLon: *job.Event.Lon}
	if p.DB != nil {
		raw, _ := json.Marshal(map[string]any{"place": place, "reference_latitude": place.ReferenceLat, "reference_longitude": place.ReferenceLon})
		_, _ = p.DB.ExecContext(ctx, "INSERT INTO place_cache(cache_key,payload,expires_at) VALUES(?,?,?) ON DUPLICATE KEY UPDATE payload=VALUES(payload),expires_at=VALUES(expires_at)", job.Key, string(raw), ParseTime(place.Expires))
	}
	return placeEntry{place, "ready", ParseTime(place.Expires)}
}
