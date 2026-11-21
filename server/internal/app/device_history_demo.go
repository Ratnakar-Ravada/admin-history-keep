package app

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"time"
)

func cloneDeviceHistory(source map[string][]DeviceHistoryRecord) map[string][]DeviceHistoryRecord {
	out := make(map[string][]DeviceHistoryRecord, len(source))
	for kind, rows := range source {
		out[kind] = slices.Clone(rows)
		for i := range out[kind] {
			out[kind][i].Fields = maps.Clone(rows[i].Fields)
		}
	}
	return out
}

func latestDeviceHistory(rows []DeviceHistoryRecord, kind string) []DeviceHistoryRecord {
	if kind == "notifications" {
		return slices.Clone(rows)
	}
	latest := map[string]DeviceHistoryRecord{}
	for _, row := range rows {
		key := row.DeviceID + ":" + row.CallID
		if kind == "app-usage" {
			key = row.DeviceID + ":" + row.EventType + ":" + row.SourceKey
		}
		previous, ok := latest[key]
		if !ok || row.Observed > previous.Observed || row.Observed == previous.Observed && row.ID > previous.ID {
			latest[key] = row
		}
	}
	out := make([]DeviceHistoryRecord, 0, len(latest))
	for _, row := range latest {
		out = append(out, row)
	}
	return out
}

func (r *DemoRepository) DeviceHistory(_ context.Context, f DeviceHistoryFilter, p ReadPoint, c Cursor, limit int) ([]DeviceHistoryRecord, error) {
	r.mu.Lock()
	var rows []DeviceHistoryRecord
	if snapshot, ok := r.historySnapshots[p.ID]; ok {
		rows = snapshot[f.Kind]
	} else {
		rows = cloneDeviceHistory(r.HistoryRows)[f.Kind]
	}
	r.mu.Unlock()
	rows = latestDeviceHistory(rows, f.Kind)
	slices.SortFunc(rows, func(a, b DeviceHistoryRecord) int {
		if v := cmp.Compare(b.Recorded, a.Recorded); v != 0 {
			return v
		}
		return cmp.Compare(b.ID, a.ID)
	})
	out := []DeviceHistoryRecord{}
	for _, row := range rows {
		older := c.LastTime == "" || row.Recorded < c.LastTime || row.Recorded == c.LastTime && row.ID < c.LastID
		if historyMatch(row, f) && older {
			out = append(out, row)
			if len(out) == limit {
				break
			}
		}
	}
	return out, nil
}

func demoDeviceHistory(now time.Time, devices []Device) map[string][]DeviceHistoryRecord {
	rows := map[string][]DeviceHistoryRecord{"calls": {}, "notifications": {}, "app-usage": {}}
	zone, _ := time.LoadLocation("Asia/Kolkata")
	local := now.In(zone)
	today := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, zone)
	appNames := []string{"Messages", "Browser", "Maps", "Music"}
	packages := []string{"com.example.messages", "com.example.browser", "com.example.maps", "com.example.music"}
	n := 100000
	makeRow := func(device, kind, eventType string, at time.Time, fields map[string]*string) DeviceHistoryRecord {
		n++
		observed := at.Add(20 * time.Second)
		if observed.After(now) {
			observed = now
		}
		return DeviceHistoryRecord{ID: id(n), DeviceID: device, Recorded: FormatTime(at), Observed: FormatTime(observed), Received: FormatTime(observed), EventType: eventType, SourceKey: fmt.Sprintf("%064x", n), Fields: fields}
	}
	for day := 0; day < 90; day++ {
		start := today.AddDate(0, 0, -day)
		for deviceIndex, device := range devices {
			for slot := 0; slot < 12; slot++ {
				at := start.Add(time.Duration(slot*2) * time.Hour)
				if at.After(now) {
					continue
				}
				typeNumber := strconv.Itoa((slot+deviceIndex)%7 + 1)
				fields := map[string]*string{"name": Ptr([]string{"Jamie Taylor", "Morgan Lee", "Home", "Work"}[slot%4]), "number": Ptr(fmt.Sprintf("+1 202-555-%04d", 100+slot)), "type": Ptr(typeNumber), "duration": Ptr(strconv.Itoa(45 + slot*37)), "countryiso": Ptr("US"), "presentation": Ptr("1"), "is_read": Ptr("1"), "features": Ptr("0")}
				if typeNumber == "3" || typeNumber == "5" || typeNumber == "6" || typeNumber == "7" {
					fields["duration"] = Ptr("0")
				}
				if slot%4 == 3 {
					fields["name"] = nil
				}
				call := makeRow(device.ID, "calls", typeNumber, at, fields)
				call.CallID, call.SourceID, call.SourceKey = id(n+500000), strconv.Itoa(n), ""
				rows["calls"] = append(rows["calls"], call)
				app := slot % len(appNames)
				notificationType := []string{"posted", "active", "removed"}[slot%3]
				posted := at
				if notificationType == "removed" {
					posted = at.Add(-10 * time.Minute)
				}
				notification := makeRow(device.ID, "notifications", notificationType, at, map[string]*string{"app_label": Ptr(appNames[app]), "title": Ptr([]string{"A message from Jamie", "Your reading list", "Traffic update", "Now playing"}[app]), "text": Ptr([]string{"See you at the cafe at 6.", "Your saved article is ready.", "A faster route is available.", "Evening playlist"}[app]), "big_text": nil, "posted_at": Ptr(FormatTime(posted)), "category": Ptr("status"), "channel_id": Ptr("general"), "ongoing": Ptr("false"), "group_summary": Ptr("false")})
				notification.SourcePackage = packages[app]
				if slot%5 == 4 {
					notification.Fields["title"], notification.Fields["text"] = nil, nil
				}
				if notificationType == "removed" {
					notification.Fields["removal_reason"] = Ptr("2")
				}
				rows["notifications"] = append(rows["notifications"], notification)
			}
			for app, name := range appNames {
				if start.After(now) {
					continue
				}
				duration := int64((app+1)*900000 + deviceIndex*120000)
				daily := makeRow(device.ID, "app-usage", "daily", start, map[string]*string{"app_label": Ptr(name), "interval_start": Ptr(FormatTime(start)), "foreground_ms": Ptr(strconv.FormatInt(duration, 10)), "visible_ms": Ptr(strconv.FormatInt(duration+120000, 10)), "foreground_service_ms": Ptr("0"), "last_used_at": Ptr(FormatTime(start))})
				daily.SourcePackage = packages[app]
				rows["app-usage"] = append(rows["app-usage"], daily)
				for _, metric := range []string{"foreground", "visible"} {
					at := start.Add(time.Duration(8+app) * time.Hour)
					end := at.Add(time.Duration(duration) * time.Millisecond)
					if end.After(now) {
						continue
					}
					session := makeRow(device.ID, "app-usage", "session", at, map[string]*string{"app_label": Ptr(name), "metric": Ptr(metric), "start_at": Ptr(FormatTime(at)), "end_at": Ptr(FormatTime(end)), "duration_ms": Ptr(strconv.FormatInt(duration, 10)), "end_reason": Ptr("activity"), "estimated": Ptr("true")})
					session.SourcePackage = packages[app]
					rows["app-usage"] = append(rows["app-usage"], session)
				}
			}
		}
	}
	return rows
}
