package app

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
)

const PageSize = 20
const MaxDevices = 20
const MaxMapPoints = 5000
const Stamp = "2006-01-02T15:04:05.000Z"

var Reasons = []string{"legacy", "manual", "periodic", "screen_wake", "location_on", "location_off", "internet_on", "internet_off", "network_changed", "tracking_start", "boot_recovery", "location_available", "app_launch", "airplane_mode_on", "airplane_mode_off"}
var Outcomes = []string{"completed", "unavailable", "disabled", "permission_missing", "missed", "cancelled", "not_recorded"}
var Statuses = []string{"fresh", "last_known", "unavailable", "disabled"}
var Transports = []string{"unknown", "none", "wifi", "cellular", "other"}

type Device struct {
	ID               string `json:"device_id"`
	Name             string `json:"device_name"`
	Model            string `json:"detected_device_name"`
	Label            string `json:"label"`
	Revoked          bool   `json:"revoked"`
	Locked           bool   `json:"locked"`
	PINSetupRequired bool   `json:"pin_setup_required"`
	// Access metadata is read only on the server and never returned to browsers.
	TrackingEmail string `json:"-"`
	PINHash       string `json:"-"`
}
type Place struct {
	Label        string  `json:"label"`
	City         string  `json:"city,omitempty"`
	Country      string  `json:"country,omitempty"`
	Level        string  `json:"level"`
	Provider     string  `json:"provider"`
	LookedUp     string  `json:"looked_up_at"`
	Expires      string  `json:"expires_at"`
	Attribution  string  `json:"attribution"`
	ReferenceLat float64 `json:"-"`
	ReferenceLon float64 `json:"-"`
}
type Event struct {
	ID         string   `json:"event_id"`
	DeviceID   string   `json:"device_id"`
	Recorded   string   `json:"recorded_at"`
	Received   string   `json:"received_at"`
	Executed   *string  `json:"executed_at"`
	Outcome    *string  `json:"execution_status"`
	Scheduled  *string  `json:"scheduled_for"`
	Captured   *string  `json:"captured_at"`
	Lat        *float64 `json:"latitude"`
	Lon        *float64 `json:"longitude"`
	Accuracy   *float64 `json:"accuracy_m"`
	Battery    *int     `json:"battery_percent"`
	FixAge     *int64   `json:"fix_age_ms"`
	Mock       bool     `json:"is_mock"`
	Permission string   `json:"permission_accuracy"`
	Mode       string   `json:"accuracy_mode"`
	Reason     string   `json:"reason"`
	Status     string   `json:"location_status"`
	Transport  string   `json:"network_transport"`
	Airplane   *bool    `json:"airplane_mode_enabled"`
	Severity   string   `json:"severity"`
	Title      string   `json:"title"`
	Place      *Place   `json:"place,omitempty"`
}

func Ptr[T any](v T) *T             { return &v }
func ParseTime(s string) time.Time  { t, _ := time.Parse(time.RFC3339Nano, s); return t }
func FormatTime(t time.Time) string { return t.UTC().Format(Stamp) }
func (e Event) HasFix() bool {
	return e.Lat != nil && e.Lon != nil && e.Captured != nil && e.Accuracy != nil
}
func (e Event) IsAttention() bool {
	return slices.Contains([]string{"location_off", "internet_off", "airplane_mode_on"}, e.Reason) || e.Status == "disabled" || (e.Outcome != nil && slices.Contains([]string{"permission_missing", "missed", "disabled"}, *e.Outcome))
}
func (e Event) IsLimited() bool {
	return e.Mock || e.Status == "last_known" || e.Status == "unavailable" || e.Permission == "approximate" || (e.Accuracy != nil && *e.Accuracy > 200) || (e.Outcome != nil && *e.Outcome == "unavailable") || (e.Executed != nil && ParseTime(*e.Executed).Sub(ParseTime(e.Recorded)) > 5*time.Minute) || ParseTime(e.Received).Sub(ParseTime(e.Recorded)) > 15*time.Minute
}
func Classify(e Event) string {
	if e.IsAttention() {
		return "attention"
	}
	if e.IsLimited() {
		return "limited"
	}
	if slices.Contains([]string{"location_on", "internet_on", "airplane_mode_off"}, e.Reason) || (e.Reason == "periodic" && e.Status == "fresh" && e.HasFix() && e.Outcome != nil && *e.Outcome == "completed") {
		return "positive"
	}
	return "neutral"
}
func Title(reason string) string {
	m := map[string]string{"periodic": "Regular location report", "location_off": "Location turned off", "location_on": "Location turned on", "internet_off": "Internet became unavailable", "internet_on": "Internet became available", "airplane_mode_on": "Airplane mode enabled", "airplane_mode_off": "Airplane mode disabled", "network_changed": "Network changed", "manual": "Manual location report", "boot_recovery": "Device restarted", "app_launch": "App opened", "screen_wake": "Screen woke", "tracking_start": "Tracking started", "location_available": "Location became available", "legacy": "Location event"}
	if v, ok := m[reason]; ok {
		return v
	}
	return "Location event"
}
func Decorate(e Event) Event { e.Severity = Classify(e); e.Title = Title(e.Reason); return e }

type Identity struct {
	UserID        string `json:"user_id,omitempty"`
	SessionID     string `json:"-"`
	SessionToken  string `json:"-"`
	Advanced      bool   `json:"is_advanced"`
	Subject       string `json:"subject"`
	Name          string `json:"name"`
	Email         string `json:"email,omitempty"`
	EmailVerified bool   `json:"-"`
	HostedDomain  string `json:"-"`
	Admin         bool   `json:"admin"`
	Expires       int64  `json:"-"`
}
type Filter struct {
	DeviceIDs  []string `json:"device_ids"`
	From       string   `json:"from"`
	To         string   `json:"to"`
	Timezone   string   `json:"timezone"`
	Reasons    []string `json:"reasons"`
	Severity   []string `json:"severity"`
	Outcomes   []string `json:"outcomes"`
	Statuses   []string `json:"statuses"`
	Transports []string `json:"transports"`
}

func List(s string) []string {
	a := []string{}
	for _, v := range strings.Split(s, ",") {
		v = strings.TrimSpace(v)
		if v != "" && !slices.Contains(a, v) {
			a = append(a, v)
		}
	}
	slices.Sort(a)
	return a
}
func ValidIDs(ids []string) bool {
	for _, id := range ids {
		if len(id) != 36 {
			return false
		}
		for i, c := range id {
			if slices.Contains([]int{8, 13, 18, 23}, i) {
				if c != '-' {
					return false
				}
			} else if !strings.ContainsRune("0123456789abcdef", c) {
				return false
			}
		}
	}
	return true
}
func ReadFilter(q url.Values, now time.Time) (Filter, error) {
	for _, values := range q {
		if len(values) != 1 {
			return Filter{}, errors.New("A filter parameter may only appear once")
		}
	}
	f := Filter{DeviceIDs: List(q.Get("device_ids")), From: q.Get("from"), To: q.Get("to"), Timezone: q.Get("timezone"), Reasons: List(q.Get("reasons")), Severity: List(q.Get("severity")), Outcomes: List(q.Get("outcomes")), Statuses: List(q.Get("statuses")), Transports: List(q.Get("transports"))}
	if len(f.DeviceIDs) > MaxDevices || !ValidIDs(f.DeviceIDs) {
		return f, errors.New("Choose up to 20 valid device IDs")
	}
	if f.Timezone == "" {
		f.Timezone = "Asia/Kolkata"
	}
	zone, zoneErr := time.LoadLocation(f.Timezone)
	if zoneErr != nil {
		return f, errors.New("Unknown timezone")
	}
	if f.From == "" || f.To == "" {
		return f, errors.New("Both UTC date bounds are required")
	}
	start, err := time.Parse(time.RFC3339Nano, f.From)
	if err != nil {
		return f, errors.New("Invalid start time")
	}
	end, err := time.Parse(time.RFC3339Nano, f.To)
	if err != nil || !end.After(start) {
		return f, errors.New("Invalid end time")
	}
	// Ninety calendar days may include a DST boundary. Clamp to a rolling ninety-day window plus one calendar boundary.
	localNow := now.In(zone)
	midnight := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, zone)
	if end.Sub(start) > 90*24*time.Hour+time.Hour || end.After(midnight.AddDate(0, 0, 1)) || start.Before(midnight.AddDate(0, 0, -89)) {
		return f, errors.New("Choose dates within the most recent 90 days")
	}
	f.From = FormatTime(start)
	f.To = FormatTime(end)
	for _, pair := range []struct{ got, allowed []string }{{f.Reasons, Reasons}, {f.Severity, []string{"attention", "positive", "limited", "neutral"}}, {f.Outcomes, Outcomes}, {f.Statuses, Statuses}, {f.Transports, Transports}} {
		for _, v := range pair.got {
			if !slices.Contains(pair.allowed, v) {
				return f, errors.New("Unsupported filter value")
			}
		}
	}
	if q.Get("limit") != "" && q.Get("limit") != "20" {
		return f, errors.New("Event pages contain 20 records")
	}
	return f, nil
}
func (f Filter) Hash() string {
	b, _ := json.Marshal(f)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func Match(e Event, f Filter) bool {
	if !slices.Contains(f.DeviceIDs, e.DeviceID) || e.Recorded < f.From || e.Recorded >= f.To {
		return false
	}
	outcome := "not_recorded"
	if e.Outcome != nil {
		outcome = *e.Outcome
	}
	for _, pair := range []struct {
		set   []string
		value string
	}{{f.Reasons, e.Reason}, {f.Severity, Classify(e)}, {f.Outcomes, outcome}, {f.Statuses, e.Status}, {f.Transports, e.Transport}} {
		if len(pair.set) > 0 && !slices.Contains(pair.set, pair.value) {
			return false
		}
	}
	return true
}
func SortEvents(a []Event) {
	slices.SortFunc(a, func(a, b Event) int {
		if v := cmp.Compare(b.Recorded, a.Recorded); v != 0 {
			return v
		}
		return cmp.Compare(b.ID, a.ID)
	})
}
func Older(e Event, c Cursor) bool {
	return c.LastTime == "" || e.Recorded < c.LastTime || (e.Recorded == c.LastTime && e.ID < c.LastID)
}
func Distance(a, b, c, d float64) float64 {
	r := math.Pi / 180
	v := math.Pow(math.Sin((c-a)*r/2), 2) + math.Cos(a*r)*math.Cos(c*r)*math.Pow(math.Sin((d-b)*r/2), 2)
	return 6371000 * 2 * math.Asin(math.Sqrt(math.Min(1, v)))
}

type ReadPoint struct {
	ID string `json:"id"`
	At string `json:"at"`
}
type Repository interface {
	Devices(context.Context, Identity) ([]Device, error)
	Snapshot(context.Context) (ReadPoint, error)
	Events(context.Context, Filter, ReadPoint, Cursor, int) ([]Event, error)
	History(context.Context, Filter, ReadPoint, int) ([]Event, int, error)
	Latest(context.Context, []string) ([]Latest, error)
	EventsByID(context.Context, []string, []string) ([]Event, error)
	Summary(context.Context, Filter) ([]DaySummary, error)
	HasNew(context.Context, []string, string) (bool, error)
	Interruptions(context.Context, Filter, ReadPoint) ([]BreakPoint, error)
}
type BreakPoint struct {
	DeviceID string `json:"device_id"`
	Recorded string `json:"recorded_at"`
}
type DaySummary struct {
	Date      string `json:"date"`
	Events    int    `json:"events"`
	Fixes     int    `json:"fixes"`
	Attention int    `json:"attention"`
}
type Latest struct {
	DeviceID   string  `json:"device_id"`
	Event      *Event  `json:"latest_event"`
	Observed   *Event  `json:"latest_observed_event"`
	Fix        *Event  `json:"latest_fix"`
	Airplane   *bool   `json:"airplane_mode_enabled"`
	AirplaneAt *string `json:"airplane_observed_at"`
}
type Page struct {
	Items   []Event `json:"items"`
	Start   *string `json:"page_cursor"`
	Next    *string `json:"next_cursor"`
	More    bool    `json:"has_more"`
	AsOf    string  `json:"as_of"`
	Expires string  `json:"expires_at"`
	Order   string  `json:"order"`
}
type APIError struct {
	Status  int
	Code    string
	Message string
}

func (e APIError) Error() string { return e.Message }
func num(s string, fallback int) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return fallback
	}
	return n
}
func id(n int) string { return fmt.Sprintf("00000000-0000-4000-8000-%012d", n) }
