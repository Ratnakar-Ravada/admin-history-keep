package app

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"
)

// These fictional fixtures are unrelated to the user's private GPS exports.
type DemoRepository struct {
	Rows             []Event
	Registry         []Device
	Now              time.Time
	HistoryRows      map[string][]DeviceHistoryRecord
	mu               sync.Mutex
	snapshots        map[string][]Event
	historySnapshots map[string]map[string][]DeviceHistoryRecord
}

func NewDemo(now time.Time) *DemoRepository {
	r := &DemoRepository{Now: now, Registry: []Device{{ID: id(1), Name: "Alex’s phone", Model: "Demo Android", Label: "Personal"}, {ID: id(2), Name: "Sam’s phone", Model: "Demo Android", Label: "Personal"}, {ID: id(3), Name: "Travel phone", Model: "Demo Android", Label: "Spare"}}}
	zone, _ := time.LoadLocation("Asia/Kolkata")
	day := now.In(zone)
	day = time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, zone)
	reasons := []string{"periodic", "periodic", "internet_off", "location_off", "location_on", "internet_on", "airplane_mode_on", "airplane_mode_off", "periodic", "manual", "network_changed", "app_launch", "periodic", "periodic", "periodic", "periodic"}
	for d := 0; d < 90; d++ {
		for device := 0; device < 3; device++ {
			for slot := 0; slot < 48; slot++ {
				at := day.AddDate(0, 0, -d).Add(time.Duration(slot) * 30 * time.Minute)
				if at.After(now) {
					continue
				}
				n := (89-d)*144 + device*48 + slot + 100
				reason := reasons[(slot+device*3)%len(reasons)]
				status := "fresh"
				transport := "cellular"
				outcome := "completed"
				permission := "precise"
				if reason == "internet_off" || reason == "airplane_mode_on" {
					transport = "none"
				}
				if reason == "location_off" {
					status = "disabled"
					outcome = "disabled"
					permission = "none"
				}
				if slot%17 == 0 {
					status = "unavailable"
					outcome = "unavailable"
					permission = "none"
				}
				if status == "fresh" && slot%11 == 0 {
					status = "last_known"
				}
				e := Event{ID: id(n), DeviceID: id(device + 1), Recorded: FormatTime(at), Received: FormatTime(at.Add(3 * time.Second)), Executed: Ptr(FormatTime(at.Add(time.Second))), Outcome: Ptr(outcome), Reason: reason, Status: status, Transport: transport, Permission: permission, Mode: "high_precision", Battery: Ptr(92 - slot%50)}
				if reason == "periodic" {
					e.Scheduled = Ptr(e.Recorded)
				}
				if reason == "airplane_mode_on" {
					e.Airplane = Ptr(true)
				}
				if reason == "airplane_mode_off" {
					e.Airplane = Ptr(false)
				}
				if status == "fresh" || status == "last_known" {
					baseLat := 51.507 + float64(device)*.012
					baseLon := -.128 + float64(device)*.018
					t := float64(slot % 12)
					e.Lat = Ptr(baseLat + .0015*t)
					e.Lon = Ptr(baseLon + .002*t)
					e.Accuracy = Ptr(12.5 + float64(slot%5)*8)
					e.FixAge = Ptr(int64(700))
					e.Captured = Ptr(FormatTime(at.Add(1500 * time.Millisecond)))
					if status == "last_known" {
						e.Captured = Ptr(FormatTime(at.Add(-35 * time.Minute)))
						e.FixAge = Ptr(int64(2100000))
					}
				}
				r.Rows = append(r.Rows, Decorate(e))
			}
		}
	}
	SortEvents(r.Rows)
	r.HistoryRows = demoDeviceHistory(now, r.Registry)
	return r
}
func (r *DemoRepository) Devices(_ context.Context, u Identity) ([]Device, error) {
	out := []Device{}
	for _, d := range r.Registry {
		if u.Advanced || u.EmailVerified && u.Email != "" && strings.EqualFold(d.TrackingEmail, u.Email) && !d.Revoked {
			out = append(out, d)
		}
	}
	return out, nil
}
func (r *DemoRepository) Snapshot(_ context.Context) (ReadPoint, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.snapshots == nil {
		r.snapshots = map[string][]Event{}
	}
	p := ReadPoint{ID: fmt.Sprintf("%d-%d-%d-%d-%d", r.Now.UnixNano(), len(r.Rows), len(r.HistoryRows["calls"]), len(r.HistoryRows["notifications"]), len(r.HistoryRows["app-usage"])), At: FormatTime(r.Now)}
	if r.snapshots[p.ID] == nil {
		r.snapshots[p.ID] = slices.Clone(r.Rows)
	}
	if r.historySnapshots == nil {
		r.historySnapshots = map[string]map[string][]DeviceHistoryRecord{}
	}
	if _, ok := r.historySnapshots[p.ID]; !ok {
		r.historySnapshots[p.ID] = cloneDeviceHistory(r.HistoryRows)
	}
	return p, nil
}
func (r *DemoRepository) read(p ReadPoint) []Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	if rows, ok := r.snapshots[p.ID]; ok {
		return rows
	}
	return r.Rows
}
func (r *DemoRepository) Events(_ context.Context, f Filter, p ReadPoint, c Cursor, limit int) ([]Event, error) {
	out := []Event{}
	for _, e := range r.read(p) {
		if Match(e, f) && Older(e, c) {
			out = append(out, e)
			if len(out) == limit {
				break
			}
		}
	}
	return out, nil
}
func (r *DemoRepository) History(_ context.Context, f Filter, p ReadPoint, limit int) ([]Event, int, error) {
	out := []Event{}
	for _, e := range r.read(p) {
		if slices.Contains(f.DeviceIDs, e.DeviceID) && e.HasFix() && *e.Captured >= f.From && *e.Captured < f.To {
			out = append(out, e)
		}
	}
	return out, len(out), nil
}
func (r *DemoRepository) Latest(_ context.Context, ids []string) ([]Latest, error) {
	out := []Latest{}
	for _, d := range ids {
		x := Latest{DeviceID: d}
		for _, e := range r.Rows {
			if e.DeviceID != d {
				continue
			}
			e := e
			if x.Event == nil || e.Received > x.Event.Received {
				x.Event = &e
			}
			if x.Observed == nil {
				x.Observed = &e
			}
			if e.HasFix() && (x.Fix == nil || *e.Captured > *x.Fix.Captured) {
				x.Fix = &e
			}
			if x.AirplaneAt == nil && e.Airplane != nil {
				x.Airplane = e.Airplane
				x.AirplaneAt = Ptr(e.Recorded)
			}
		}
		out = append(out, x)
	}
	return out, nil
}
func (r *DemoRepository) EventsByID(_ context.Context, ids, allowed []string) ([]Event, error) {
	out := []Event{}
	for _, e := range r.Rows {
		if slices.Contains(ids, e.ID) && slices.Contains(allowed, e.DeviceID) {
			out = append(out, e)
		}
	}
	return out, nil
}
func (r *DemoRepository) Summary(_ context.Context, f Filter) ([]DaySummary, error) {
	zone, _ := time.LoadLocation(f.Timezone)
	days := map[string]*DaySummary{}
	for _, e := range r.Rows {
		if !slices.Contains(f.DeviceIDs, e.DeviceID) || e.Recorded < f.From || e.Recorded >= f.To {
			continue
		}
		day := ParseTime(e.Recorded).In(zone).Format("2006-01-02")
		if days[day] == nil {
			days[day] = &DaySummary{Date: day}
		}
		x := days[day]
		x.Events++
		if e.HasFix() {
			x.Fixes++
		}
		if Classify(e) == "attention" {
			x.Attention++
		}
	}
	out := []DaySummary{}
	for _, d := range days {
		out = append(out, *d)
	}
	slices.SortFunc(out, func(a, b DaySummary) int { return strings.Compare(a.Date, b.Date) })
	return out, nil
}
func (r *DemoRepository) HasNew(_ context.Context, ids []string, after string) (bool, error) {
	for _, e := range r.Rows {
		if slices.Contains(ids, e.DeviceID) && e.Received > after {
			return true, nil
		}
	}
	return false, nil
}
func (r *DemoRepository) Interruptions(_ context.Context, f Filter, p ReadPoint) ([]BreakPoint, error) {
	out := []BreakPoint{}
	for _, e := range r.read(p) {
		if slices.Contains(f.DeviceIDs, e.DeviceID) && e.Recorded >= f.From && e.Recorded < f.To && (e.Status == "disabled" || e.Reason == "location_off") {
			out = append(out, BreakPoint{e.DeviceID, e.Recorded})
		}
	}
	return out, nil
}
