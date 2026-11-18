package app

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestPlaceLookupsCoalesceAndCache(t *testing.T) {
	p := NewPlaces("test-key", false, nil)
	defer p.Close()
	var count atomic.Int32
	p.client = &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		count.Add(1)
		if r.URL.Host != "api.geoapify.com" {
			t.Error("unexpected provider")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"results":[{"suburb":"Example area","city":"Example city","country":"Example country"}]}`)), Header: http.Header{}}, nil
	})}
	e := Event{ID: id(1), Lat: Ptr(51.5), Lon: Ptr(-.1), Captured: Ptr("2026-11-18T00:00:00.000Z"), Accuracy: Ptr(15.0), Permission: "precise"}
	for i := 0; i < 20; i++ {
		p.lookup(e)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		place, status := p.lookup(e)
		if status == "ready" {
			if !strings.Contains(place.Label, "Example area") {
				t.Fatal("area label missing")
			}
			if count.Load() != 1 {
				t.Fatal("duplicate provider requests")
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("lookup did not complete")
}
func TestCoarseFixDoesNotClaimStreetOrBuilding(t *testing.T) {
	p := NewPlaces("test-key", false, nil)
	defer p.Close()
	p.client = &http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"results":[{"suburb":"Tiny block","city":"Example city","country":"Country","name":"A business","street":"A street"}]}`)), Header: http.Header{}}, nil
	})}
	e := Event{Lat: Ptr(51.5), Lon: Ptr(-.1), Captured: Ptr("2026-11-18T00:00:00.000Z"), Accuracy: Ptr(2000.0), Permission: "approximate"}
	entry := p.load(context.Background(), placeJob{Key: placeKey(e), Event: e})
	if entry.Place == nil || strings.Contains(entry.Place.Label, "Tiny block") || entry.Place.Level != "city" {
		t.Fatal("coarse fix was over-specified")
	}
}
func TestProviderRateLimitsAreNegativeCached(t *testing.T) {
	p := NewPlaces("test-key", false, nil)
	defer p.Close()
	p.client = &http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 429, Body: io.NopCloser(strings.NewReader("")), Header: http.Header{}}, nil
	})}
	e := Event{Lat: Ptr(51.5), Lon: Ptr(-.1), Captured: Ptr("2026-11-18T00:00:00.000Z"), Accuracy: Ptr(10.0)}
	entry := p.load(context.Background(), placeJob{Event: e})
	if entry.Status != "unavailable" || entry.Until.Sub(time.Now()) < 4*time.Minute {
		t.Fatal("rate limit was not backed off")
	}
}
func TestNoCoordinatesNeverEnqueuesGeocoding(t *testing.T) {
	p := NewPlaces("test-key", false, nil)
	defer p.Close()
	_, status := p.lookup(Event{Reason: "location_off", Status: "disabled"})
	if status != "no_fix" || len(p.queue) != 0 {
		t.Fatal("geocoding unavailable position")
	}
}

func TestBroadHistoryDoesNotConsumeProviderQuota(t *testing.T) {
	p := NewPlaces("test-key", false, nil)
	defer p.Close()
	var calls atomic.Int32
	p.client = &http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return nil, context.Canceled
	})}
	rows := make([]Event, 5000)
	for i := range rows {
		rows[i] = Event{Lat: Ptr(51.0 + float64(i)/10000), Lon: Ptr(-.1), Captured: Ptr("2026-11-18T00:00:00.000Z"), Accuracy: Ptr(15.0)}
	}
	p.Enrich(rows, true)
	p.mu.Lock()
	pending := len(p.pending)
	p.mu.Unlock()
	if pending != 0 || calls.Load() != 0 {
		t.Fatal("broad history scheduled provider requests")
	}
}

func TestManualAreaRefreshReplacesCachedLabel(t *testing.T) {
	p := NewPlaces("test-key", false, nil)
	defer p.Close()
	p.client = &http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"results":[{"city":"Updated city","country":"Country"}]}`)), Header: http.Header{}}, nil
	})}
	e := Event{Lat: Ptr(51.5), Lon: Ptr(-.1), Captured: Ptr("2026-11-18T00:00:00.000Z"), Accuracy: Ptr(15.0)}
	p.cache[placeKey(e)] = placeEntry{&Place{Label: "Old label", ReferenceLat: 51.5, ReferenceLon: -.1}, "ready", time.Now().Add(time.Hour)}
	if _, status := p.lookupForce(e, true); status != "pending" {
		t.Fatal("manual refresh did not queue")
	}
	if _, status := p.lookup(e); status != "pending" {
		t.Fatal("old label concealed pending refresh")
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		place, status := p.lookup(e)
		if status == "ready" {
			if !strings.Contains(place.Label, "Updated city") {
				t.Fatal("refresh returned old label")
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("manual refresh did not complete")
}
