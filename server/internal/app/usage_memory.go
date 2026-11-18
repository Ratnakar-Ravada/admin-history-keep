package app

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

type memoryAppUser struct {
	Identity             Identity
	ID                   string
	Created, Login, Seen time.Time
	Disabled             bool
}
type memoryAppSession struct {
	AppSession
	Subject, Hash                  string
	Started, Seen, Active, Expires time.Time
	Ended                          *time.Time
	Client                         ClientInfo
}
type MemoryUsageStore struct {
	mu       sync.Mutex
	Users    map[string]*memoryAppUser
	Sessions map[string]*memoryAppSession
	Records  []UsageRecord
	Secret   []byte
}

func NewMemoryUsageStore(secret []byte, seed bool) *MemoryUsageStore {
	m := &MemoryUsageStore{Users: map[string]*memoryAppUser{}, Sessions: map[string]*memoryAppSession{}, Secret: secret}
	if seed {
		now := time.Now().UTC()
		for i, name := range []string{"Demo workspace", "Alex Example", "Sam Example"} {
			subject := fmt.Sprintf("demo-%d", i)
			if i == 0 {
				subject = "synthetic-demo"
			}
			u := Identity{Subject: subject, Name: name, Email: fmt.Sprintf("demo%d@example.invalid", i), Admin: i == 0, Expires: now.Add(time.Hour).Unix()}
			session, _ := m.Start(context.Background(), u, ClientInfo{Browser: "Chrome", OS: "macOS", Device: "desktop"}, now.Add(-30*time.Minute))
			// Illustrate a manually granted role in the synthetic workspace.
			m.Users[subject].Identity.Advanced = i == 0
			for j := 0; j < 85; j++ {
				at := now.Add(-time.Duration(j) * time.Hour)
				m.Records = append(m.Records, UsageRecord{ID: newUUID(), UserID: session.UserID, SessionID: session.ID, At: at, Method: "GET", Route: "GET /api/events", Service: "activity_logs", View: "logs", Status: 200, Duration: int64(45 + j%30), Background: j%3 == 0})
			}
		}
	}
	return m
}
func (m *MemoryUsageStore) Start(_ context.Context, u Identity, c ClientInfo, now time.Time) (AppSession, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	user := m.Users[u.Subject]
	if user == nil {
		u.Advanced = false
		user = &memoryAppUser{Identity: u, ID: newUUID(), Created: now}
		m.Users[u.Subject] = user
	}
	if user.Disabled {
		return AppSession{}, errors.New("disabled")
	}
	u.Advanced = user.Identity.Advanced
	user.Identity = u
	user.Login = now
	user.Seen = now
	session := AppSession{ID: newUUID(), UserID: user.ID, Token: randomToken(), Advanced: user.Identity.Advanced}
	m.Sessions[session.ID] = &memoryAppSession{AppSession: session, Subject: u.Subject, Hash: sessionHash(session.Token), Started: now, Seen: now, Active: now, Expires: time.Unix(u.Expires, 0), Client: c}
	return session, nil
}
func (m *MemoryUsageStore) Validate(_ context.Context, subject, token string, now time.Time) (AppSession, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, s := range m.Sessions {
		if s.Subject == subject && s.Hash == sessionHash(token) && s.Ended == nil && s.Expires.After(now) {
			user := m.Users[subject]
			if user.Disabled {
				break
			}
			state := s.AppSession
			state.Advanced = user.Identity.Advanced
			return state, nil
		}
	}
	return AppSession{}, APIError{401, "session_expired", "Your app session ended. Sign in again."}
}
func (m *MemoryUsageStore) End(_ context.Context, id string, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s := m.Sessions[id]; s != nil {
		s.Ended = &now
	}
	return nil
}
func (m *MemoryUsageStore) Write(_ context.Context, records []UsageRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	known := make(map[string]bool, len(m.Records))
	for _, r := range m.Records {
		known[r.ID] = true
	}
	for _, r := range records {
		if !known[r.ID] {
			m.Records = append(m.Records, r)
			known[r.ID] = true
		}
	}
	for _, r := range records {
		for _, u := range m.Users {
			if u.ID == r.UserID && r.At.After(u.Seen) {
				u.Seen = r.At
			}
		}
		if s := m.Sessions[r.SessionID]; s != nil {
			if r.At.After(s.Seen) {
				s.Seen = r.At
			}
			if r.Active && r.At.After(s.Active) {
				s.Active = r.At
			}
		}
	}
	return nil
}
func (m *MemoryUsageStore) DemoIdentity() Identity {
	m.mu.Lock()
	defer m.mu.Unlock()
	u := m.Users["synthetic-demo"]
	if u == nil {
		return Identity{Subject: "synthetic-demo", Name: "Demo workspace", Admin: true, Advanced: true}
	}
	identity := u.Identity
	identity.UserID = u.ID
	for _, s := range m.Sessions {
		if s.UserID == u.ID {
			identity.SessionID = s.ID
			break
		}
	}
	return identity
}
func (m *MemoryUsageStore) Overview(_ context.Context, days int, now time.Time) (map[string]any, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	from, to := usageWindow(days, now)
	total, manual, failures, latency, sessionCount, active := 0, 0, 0, int64(0), 0, 0
	users := map[string]bool{}
	byDay, byService, byUser, byRoute, views := map[string]int{}, map[string]int{}, map[string]int{}, map[string]int{}, map[string]int{}
	userManual, serviceManual, serviceErrors := map[string]int{}, map[string]int{}, map[string]int{}
	routeLatency, routeMax := map[string]int64{}, map[string]int64{}
	routeErrors, routeMeta := map[string]int{}, map[string]UsageRecord{}
	userSessions := map[string]map[string]bool{}
	for _, r := range m.Records {
		if r.At.Before(from) || r.At.After(to) {
			continue
		}
		if r.ViewEvent {
			views[r.View]++
			continue
		}
		total++
		latency += r.Duration
		if !r.Background {
			manual++
			userManual[r.UserID]++
			serviceManual[r.Service]++
		}
		if r.Status >= 400 {
			failures++
			serviceErrors[r.Service]++
			routeErrors[r.Route]++
		}
		if r.UserID != "" {
			users[r.UserID] = true
			byUser[r.UserID]++
			if userSessions[r.UserID] == nil {
				userSessions[r.UserID] = map[string]bool{}
			}
			if r.SessionID != "" {
				userSessions[r.UserID][r.SessionID] = true
			}
		}
		byDay[r.At.Format("2006-01-02")]++
		byService[r.Service]++
		byRoute[r.Route]++
		routeLatency[r.Route] += r.Duration
		if r.Duration > routeMax[r.Route] {
			routeMax[r.Route] = r.Duration
		}
		routeMeta[r.Route] = r
	}
	for _, s := range m.Sessions {
		if !s.Started.Before(from) {
			sessionCount++
			if s.Ended == nil && s.Expires.After(now) && s.Active.After(now.Add(-5*time.Minute)) {
				active++
			}
		}
	}
	daily, services, top, routes, viewRows := []map[string]any{}, []map[string]any{}, []map[string]any{}, []map[string]any{}, []map[string]any{}
	for day, count := range byDay {
		daily = append(daily, map[string]any{"day": day, "requests": count})
	}
	sort.Slice(daily, func(i, j int) bool { return fmt.Sprint(daily[i]["day"]) < fmt.Sprint(daily[j]["day"]) })
	for key, count := range byService {
		services = append(services, map[string]any{"service": key, "requests": count, "interaction_requests": serviceManual[key], "errors": serviceErrors[key]})
	}
	for key, count := range byRoute {
		routes = append(routes, map[string]any{"route": key, "method": routeMeta[key].Method, "service": routeMeta[key].Service, "requests": count, "errors": routeErrors[key], "average_ms": float64(routeLatency[key]) / float64(count), "maximum_ms": routeMax[key]})
	}
	for key, count := range views {
		viewRows = append(viewRows, map[string]any{"app_view": key, "opens": count})
	}
	for _, u := range m.Users {
		if byUser[u.ID] > 0 {
			top = append(top, map[string]any{"id": u.ID, "email": u.Identity.Email, "display_name": u.Identity.Name, "requests": byUser[u.ID], "interaction_requests": userManual[u.ID], "sessions": len(userSessions[u.ID])})
		}
	}
	sort.Slice(services, func(i, j int) bool { return services[i]["requests"].(int) > services[j]["requests"].(int) })
	sort.Slice(routes, func(i, j int) bool { return routes[i]["requests"].(int) > routes[j]["requests"].(int) })
	sort.Slice(top, func(i, j int) bool {
		a, b := top[i]["interaction_requests"].(int), top[j]["interaction_requests"].(int)
		if a == b {
			return fmt.Sprint(top[i]["id"]) < fmt.Sprint(top[j]["id"])
		}
		return a > b
	})
	average := float64(0)
	if total > 0 {
		average = float64(latency) / float64(total)
	}
	return map[string]any{"from": FormatTime(from), "to": FormatTime(to), "timezone": "UTC", "totals": map[string]any{"requests": total, "interaction_requests": manual, "background_requests": total - manual, "errors": failures, "average_ms": average, "active_users": len(users)}, "accounts": map[string]any{"registered_users": len(m.Users), "new_users": len(m.Users)}, "sessions": map[string]any{"sessions": sessionCount, "active_sessions": active}, "daily": daily, "services": services, "top_users": top, "routes": routes, "views": viewRows}, nil
}
func (m *MemoryUsageStore) Page(_ context.Context, kind string, q AnalyticsQuery, now time.Time) (map[string]any, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	items := []map[string]any{}
	from, _ := usageWindow(q.Days, now)
	userRequests, sessionRequests := map[string]int{}, map[string]int{}
	for _, r := range m.Records {
		if r.ViewEvent {
			continue
		}
		sessionRequests[r.SessionID]++
		if !r.At.Before(from) && !r.At.After(now) {
			userRequests[r.UserID]++
		}
	}
	var c usageCursor
	var err error
	if q.Cursor != "" {
		c, err = decodeUsageCursor(m.Secret, q.Cursor, cursorScope(kind, q), now)
		if err != nil {
			return nil, err
		}
	}
	add := func(row map[string]any, at time.Time, id string) {
		if c.ID != "" && (at.After(ParseTime(c.At)) || at.Equal(ParseTime(c.At)) && id >= c.ID) {
			return
		}
		row["_at"] = FormatTime(at)
		items = append(items, row)
	}
	switch kind {
	case "users":
		for _, u := range m.Users {
			add(map[string]any{"id": u.ID, "email": u.Identity.Email, "display_name": u.Identity.Name, "is_advanced": u.Identity.Advanced, "created_at": FormatTime(u.Created), "last_login_at": FormatTime(u.Login), "last_seen_at": FormatTime(u.Seen), "requests": userRequests[u.ID]}, u.Created, u.ID)
		}
	case "sessions":
		for _, s := range m.Sessions {
			if s.Started.Before(from) || q.UserID != "" && s.UserID != q.UserID {
				continue
			}
			u := m.Users[s.Subject]
			var ended any
			if s.Ended != nil {
				ended = FormatTime(*s.Ended)
			}
			add(map[string]any{"id": s.ID, "user_id": s.UserID, "email": u.Identity.Email, "display_name": u.Identity.Name, "browser": s.Client.Browser, "operating_system": s.Client.OS, "device_type": s.Client.Device, "started_at": FormatTime(s.Started), "last_seen_at": FormatTime(s.Seen), "last_active_at": FormatTime(s.Active), "expires_at": FormatTime(s.Expires), "ended_at": ended, "requests": sessionRequests[s.ID]}, s.Started, s.ID)
		}
	case "requests":
		for _, r := range m.Records {
			if r.ViewEvent || r.At.Before(from) || q.UserID != "" && r.UserID != q.UserID || q.SessionID != "" && r.SessionID != q.SessionID || q.Service != "" && r.Service != q.Service {
				continue
			}
			email := "Anonymous"
			for _, u := range m.Users {
				if u.ID == r.UserID {
					email = u.Identity.Email
				}
			}
			add(map[string]any{"id": r.ID, "user_id": r.UserID, "session_id": r.SessionID, "email": email, "occurred_at": FormatTime(r.At), "method": r.Method, "route": r.Route, "service": r.Service, "app_view": r.View, "is_background": r.Background, "status_code": r.Status, "duration_ms": r.Duration, "response_bytes": r.Bytes}, r.At, r.ID)
		}
	default:
		return nil, errors.New("unknown analytics list")
	}
	sort.Slice(items, func(i, j int) bool {
		a, b := fmt.Sprint(items[i]["_at"]), fmt.Sprint(items[j]["_at"])
		if a == b {
			return fmt.Sprint(items[i]["id"]) > fmt.Sprint(items[j]["id"])
		}
		return a > b
	})
	more := len(items) > 20
	if more {
		items = items[:20]
	}
	var next *string
	if more {
		last := items[len(items)-1]
		next = Ptr(encodeUsageCursor(m.Secret, usageCursor{At: fmt.Sprint(last["_at"]), ID: fmt.Sprint(last["id"]), Scope: cursorScope(kind, q), Expiry: now.Add(15 * time.Minute).Unix()}))
	}
	for _, item := range items {
		delete(item, "_at")
	}
	return map[string]any{"items": items, "next_cursor": next, "has_more": more}, nil
}
