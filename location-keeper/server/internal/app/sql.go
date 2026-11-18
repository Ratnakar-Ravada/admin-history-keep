package app

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	_ "github.com/go-sql-driver/mysql"
	"slices"
	"strings"
	"time"
)

type SQLRepository struct {
	DB            *sql.DB
	Execution     bool
	Airplane      bool
	AllowAssigned bool
}

func NewSQLRepository(ctx context.Context, db *sql.DB, assigned bool) (*SQLRepository, error) {
	r := &SQLRepository{DB: db, AllowAssigned: assigned}
	if err := db.PingContext(ctx); err != nil {
		return nil, errors.New("TiDB connection failed; check private database configuration")
	}
	rows, err := db.QueryContext(ctx, "SELECT COLUMN_NAME FROM information_schema.COLUMNS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='tracker_locations'")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cols := map[string]bool{}
	for rows.Next() {
		var c string
		if err = rows.Scan(&c); err != nil {
			return nil, err
		}
		cols[c] = true
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if !cols["event_id"] || !cols["recorded_at"] {
		return nil, errors.New("Tracking schema is unavailable")
	}
	r.Execution = cols["executed_at"] && cols["execution_status"]
	r.Airplane = cols["airplane_mode_enabled"]
	return r, nil
}
func marks(n int) string { return strings.TrimSuffix(strings.Repeat("?,", n), ",") }
func stringsAny(a []string) []any {
	b := make([]any, len(a))
	for i, v := range a {
		b[i] = v
	}
	return b
}
func (r *SQLRepository) Devices(ctx context.Context, u Identity) ([]Device, error) {
	q := "SELECT d.device_id,COALESCE(NULLIF(d.device_name,''),NULLIF(d.detected_device_name,''),d.label),COALESCE(d.detected_device_name,''),d.label,(d.revoked_at IS NOT NULL) FROM tracker_devices d"
	args := []any{}
	if !u.Admin {
		if !r.AllowAssigned {
			return []Device{}, nil
		}
		q += " WHERE EXISTS(SELECT 1 FROM viewer_device_grants g WHERE g.device_id=d.device_id AND g.subject=?)"
		args = append(args, u.Subject)
	}
	q += " ORDER BY d.device_id"
	rows, err := r.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Device{}
	for rows.Next() {
		var d Device
		if err = rows.Scan(&d.ID, &d.Name, &d.Model, &d.Label, &d.Revoked); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
func (r *SQLRepository) Snapshot(ctx context.Context) (ReadPoint, error) {
	var p ReadPoint
	tx, err := r.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return p, err
	}
	defer tx.Rollback()
	err = tx.QueryRowContext(ctx, "SELECT CAST(TIDB_CURRENT_TSO() AS CHAR),DATE_FORMAT(UTC_TIMESTAMP(3),'%Y-%m-%dT%H:%i:%s.%fZ')").Scan(&p.ID, &p.At)
	if err != nil {
		return p, err
	}
	if p.ID == "0" || p.ID == "" {
		return p, errors.New("No valid TiDB snapshot timestamp")
	}
	p.At = FormatTime(ParseTime(p.At))
	return p, nil
}
func (r *SQLRepository) at(ctx context.Context, p ReadPoint, fn func(*sql.Conn) error) error {
	conn, err := r.DB.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if p.ID != "" {
		for _, c := range p.ID {
			if c < '0' || c > '9' {
				return errors.New("Invalid read point")
			}
		}
		if _, err = conn.ExecContext(ctx, "SET @@tidb_snapshot='"+p.ID+"'"); err != nil {
			return APIError{409, "cursor_expired", "The database read snapshot expired. Restart the event list."}
		}
		defer func() {
			resetCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			if _, e := conn.ExecContext(resetCtx, "SET @@tidb_snapshot='' "); e != nil {
				_ = conn.Raw(func(any) error { return driver.ErrBadConn })
			}
		}()
	}
	return fn(conn)
}
func (r *SQLRepository) projection() string {
	ex := "NULL AS executed_at,NULL AS execution_status"
	if r.Execution {
		ex = "l.executed_at,l.execution_status"
	}
	air := "NULL AS airplane_mode_enabled"
	if r.Airplane {
		air = "l.airplane_mode_enabled"
	}
	return "l.event_id,l.device_id,l.recorded_at,DATE_FORMAT(l.received_at,'%Y-%m-%dT%H:%i:%s.%fZ') AS received_at," + ex + ",l.scheduled_for,l.captured_at,l.latitude,l.longitude,l.accuracy_m,l.battery_percent,l.fix_age_ms,l.is_mock,l.permission_accuracy,l.accuracy_mode,l.reason,l.location_status,l.network_transport," + air
}
func scanEvents(rows *sql.Rows) ([]Event, error) {
	defer rows.Close()
	out := []Event{}
	for rows.Next() {
		var e Event
		var executed, outcome, scheduled, captured sql.NullString
		var lat, lon, accuracy sql.NullFloat64
		var battery, age sql.NullInt64
		var airplane sql.NullBool
		if err := rows.Scan(&e.ID, &e.DeviceID, &e.Recorded, &e.Received, &executed, &outcome, &scheduled, &captured, &lat, &lon, &accuracy, &battery, &age, &e.Mock, &e.Permission, &e.Mode, &e.Reason, &e.Status, &e.Transport, &airplane); err != nil {
			return nil, err
		}
		e.Received = FormatTime(ParseTime(e.Received))
		if executed.Valid {
			e.Executed = Ptr(executed.String)
		}
		if outcome.Valid {
			e.Outcome = Ptr(outcome.String)
		}
		if scheduled.Valid {
			e.Scheduled = Ptr(scheduled.String)
		}
		if captured.Valid {
			e.Captured = Ptr(captured.String)
		}
		if lat.Valid {
			e.Lat = Ptr(lat.Float64)
		}
		if lon.Valid {
			e.Lon = Ptr(lon.Float64)
		}
		if accuracy.Valid {
			e.Accuracy = Ptr(accuracy.Float64)
		}
		if battery.Valid {
			e.Battery = Ptr(int(battery.Int64))
		}
		if age.Valid {
			e.FixAge = Ptr(age.Int64)
		}
		if airplane.Valid {
			e.Airplane = Ptr(airplane.Bool)
		} else if e.Reason == "airplane_mode_on" {
			e.Airplane = Ptr(true)
		} else if e.Reason == "airplane_mode_off" {
			e.Airplane = Ptr(false)
		}
		out = append(out, Decorate(e))
	}
	return out, rows.Err()
}
func (r *SQLRepository) severitySQL() string {
	outcome := "NULL"
	executed := "NULL"
	if r.Execution {
		outcome = "l.execution_status"
		executed = "l.executed_at"
	}
	return "CASE WHEN l.reason IN ('location_off','internet_off','airplane_mode_on') OR l.location_status='disabled' OR " + outcome + " IN ('permission_missing','missed','disabled') THEN 'attention' WHEN l.is_mock=1 OR l.location_status IN ('last_known','unavailable') OR l.permission_accuracy='approximate' OR l.accuracy_m>200 OR " + outcome + "='unavailable' OR TIMESTAMPDIFF(MICROSECOND,STR_TO_DATE(l.recorded_at,'%Y-%m-%dT%H:%i:%s.%fZ'),STR_TO_DATE(" + executed + ",'%Y-%m-%dT%H:%i:%s.%fZ'))>300000000 OR TIMESTAMPDIFF(MICROSECOND,STR_TO_DATE(l.recorded_at,'%Y-%m-%dT%H:%i:%s.%fZ'),l.received_at)>900000000 THEN 'limited' WHEN l.reason IN ('location_on','internet_on','airplane_mode_off') OR (l.reason='periodic' AND l.location_status='fresh' AND l.latitude IS NOT NULL AND l.longitude IS NOT NULL AND l.captured_at IS NOT NULL AND l.accuracy_m IS NOT NULL AND " + outcome + "='completed') THEN 'positive' ELSE 'neutral' END"
}
func (r *SQLRepository) conditions(f Filter, device string, c Cursor) (string, []any) {
	where := "l.device_id=? AND l.recorded_at>=? AND l.recorded_at<?"
	args := []any{device, f.From, f.To}
	for _, p := range []struct {
		field string
		v     []string
	}{{"l.reason", f.Reasons}, {"(" + r.severitySQL() + ")", f.Severity}, {"COALESCE(l.execution_status,'not_recorded')", f.Outcomes}, {"l.location_status", f.Statuses}, {"l.network_transport", f.Transports}} {
		if len(p.v) > 0 {
			field := p.field
			if !r.Execution && strings.Contains(field, "execution_status") {
				field = "'not_recorded'"
			}
			where += " AND " + field + " IN (" + marks(len(p.v)) + ")"
			args = append(args, stringsAny(p.v)...)
		}
	}
	if c.LastTime != "" {
		where += " AND (l.recorded_at<? OR (l.recorded_at=? AND l.event_id<?))"
		args = append(args, c.LastTime, c.LastTime, c.LastID)
	}
	return where, args
}
func (r *SQLRepository) EventQuery(f Filter, c Cursor, limit int) (string, []any) {
	streams := []string{}
	args := []any{}
	for _, device := range f.DeviceIDs {
		where, partArgs := r.conditions(f, device, c)
		streams = append(streams, "(SELECT "+r.projection()+" FROM tracker_locations l WHERE "+where+" ORDER BY l.recorded_at DESC,l.event_id DESC LIMIT ?)")
		args = append(args, partArgs...)
		args = append(args, limit)
	}
	query := "SELECT * FROM (" + strings.Join(streams, " UNION ALL ") + ") event_streams ORDER BY recorded_at DESC,event_id DESC LIMIT ?"
	args = append(args, limit)
	return query, args
}
func (r *SQLRepository) Events(ctx context.Context, f Filter, p ReadPoint, c Cursor, limit int) ([]Event, error) {
	out := []Event{}
	if len(f.DeviceIDs) == 0 {
		return out, nil
	}
	query, args := r.EventQuery(f, c, limit)
	err := r.at(ctx, p, func(conn *sql.Conn) error {
		rows, e := conn.QueryContext(ctx, query, args...)
		if e != nil {
			return e
		}
		out, e = scanEvents(rows)
		return e
	})
	return out, err
}
func (r *SQLRepository) History(ctx context.Context, f Filter, p ReadPoint, limit int) ([]Event, int, error) {
	if len(f.DeviceIDs) == 0 {
		return []Event{}, 0, nil
	}
	where := "device_id IN (" + marks(len(f.DeviceIDs)) + ") AND captured_at>=? AND captured_at<? AND latitude IS NOT NULL AND longitude IS NOT NULL"
	args := append(stringsAny(f.DeviceIDs), f.From, f.To)
	count := 0
	out := []Event{}
	err := r.at(ctx, p, func(conn *sql.Conn) error {
		if e := conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM tracker_locations WHERE "+where, args...).Scan(&count); e != nil {
			return e
		}
		q := "SELECT " + r.projection() + " FROM tracker_locations l WHERE " + where + " ORDER BY l.captured_at DESC,l.event_id DESC LIMIT ?"
		queryArgs := append(slices.Clone(args), limit)
		if count > limit {
			q = "SELECT " + r.projection() + " FROM tracker_locations l JOIN (SELECT MAX(CONCAT(captured_at,'|',event_id)) k FROM tracker_locations WHERE " + where + " GROUP BY device_id,LEFT(captured_at,10)) samples ON CONCAT(l.captured_at,'|',l.event_id)=samples.k ORDER BY l.captured_at DESC,l.event_id DESC LIMIT ?"
		}
		rows, e := conn.QueryContext(ctx, q, queryArgs...)
		if e != nil {
			return e
		}
		out, e = scanEvents(rows)
		return e
	})
	return out, count, err
}
func (r *SQLRepository) Latest(ctx context.Context, ids []string) ([]Latest, error) {
	out := []Latest{}
	for _, device := range ids {
		x := Latest{DeviceID: device}
		for _, kind := range []string{"event", "observed", "fix", "airplane"} {
			where := "l.device_id=?"
			order := "l.recorded_at DESC,l.event_id DESC"
			if kind == "event" {
				order = "l.received_at DESC,l.event_id DESC"
			}
			if kind == "fix" {
				where += " AND l.captured_at IS NOT NULL AND l.latitude IS NOT NULL AND l.longitude IS NOT NULL"
				order = "l.captured_at DESC,l.event_id DESC"
			}
			if kind == "airplane" {
				where += " AND l.reason IN ('airplane_mode_on','airplane_mode_off')"
				if r.Airplane {
					where = "l.device_id=? AND (l.airplane_mode_enabled IS NOT NULL OR l.reason IN ('airplane_mode_on','airplane_mode_off'))"
				}
			}
			rows, e := r.DB.QueryContext(ctx, "SELECT "+r.projection()+" FROM tracker_locations l WHERE "+where+" ORDER BY "+order+" LIMIT 1", device)
			if e != nil {
				return nil, e
			}
			events, e := scanEvents(rows)
			if e != nil {
				return nil, e
			}
			if len(events) == 0 {
				continue
			}
			event := events[0]
			switch kind {
			case "event":
				x.Event = &event
			case "observed":
				x.Observed = &event
			case "fix":
				x.Fix = &event
			case "airplane":
				x.Airplane = event.Airplane
				x.AirplaneAt = Ptr(event.Recorded)
			}
		}
		out = append(out, x)
	}
	return out, nil
}
func (r *SQLRepository) EventsByID(ctx context.Context, ids, allowed []string) ([]Event, error) {
	if len(ids) == 0 || len(allowed) == 0 {
		return []Event{}, nil
	}
	args := append(stringsAny(ids), stringsAny(allowed)...)
	rows, e := r.DB.QueryContext(ctx, "SELECT "+r.projection()+" FROM tracker_locations l WHERE l.event_id IN ("+marks(len(ids))+") AND l.device_id IN ("+marks(len(allowed))+")", args...)
	if e != nil {
		return nil, e
	}
	return scanEvents(rows)
}
func (r *SQLRepository) Summary(ctx context.Context, f Filter) ([]DaySummary, error) {
	out := []DaySummary{}
	if len(f.DeviceIDs) == 0 {
		return out, nil
	}
	zone, _ := time.LoadLocation(f.Timezone)
	start := ParseTime(f.From).In(zone)
	end := ParseTime(f.To)
	local := time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, zone)
	expr := "CASE"
	args := []any{}
	for local.Before(end) {
		next := local.AddDate(0, 0, 1)
		expr += " WHEN l.recorded_at<? THEN ?"
		args = append(args, FormatTime(next), local.Format("2006-01-02"))
		local = next
	}
	expr += " END"
	query := "SELECT " + expr + " day,COUNT(*),SUM(l.captured_at IS NOT NULL),SUM((" + r.severitySQL() + ")='attention') FROM tracker_locations l WHERE l.device_id IN (" + marks(len(f.DeviceIDs)) + ") AND l.recorded_at>=? AND l.recorded_at<? GROUP BY day ORDER BY day"
	args = append(args, stringsAny(f.DeviceIDs)...)
	args = append(args, f.From, f.To)
	rows, err := r.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var d DaySummary
		if err = rows.Scan(&d.Date, &d.Events, &d.Fixes, &d.Attention); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
func (r *SQLRepository) Readiness(ctx context.Context) error { return r.DB.PingContext(ctx) }
func (r *SQLRepository) HasNew(ctx context.Context, ids []string, after string) (bool, error) {
	if len(ids) == 0 {
		return false, nil
	}
	var n int
	err := r.DB.QueryRowContext(ctx, "SELECT 1 FROM tracker_locations WHERE device_id IN ("+marks(len(ids))+") AND received_at>? LIMIT 1", append(stringsAny(ids), ParseTime(after))...).Scan(&n)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return n == 1, err
}
func (r *SQLRepository) Interruptions(ctx context.Context, f Filter, p ReadPoint) ([]BreakPoint, error) {
	out := []BreakPoint{}
	if len(f.DeviceIDs) == 0 || ParseTime(f.To).Sub(ParseTime(f.From)) > 48*time.Hour {
		return out, nil
	}
	args := append(stringsAny(f.DeviceIDs), f.From, f.To)
	err := r.at(ctx, p, func(conn *sql.Conn) error {
		rows, e := conn.QueryContext(ctx, "SELECT device_id,recorded_at FROM tracker_locations WHERE device_id IN ("+marks(len(f.DeviceIDs))+") AND recorded_at>=? AND recorded_at<? AND (location_status='disabled' OR reason='location_off') ORDER BY recorded_at LIMIT 10001", args...)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var b BreakPoint
			if e = rows.Scan(&b.DeviceID, &b.Recorded); e != nil {
				return e
			}
			out = append(out, b)
		}
		return rows.Err()
	})
	if len(out) > 10000 {
		return nil, APIError{413, "history_too_dense", "This day has too many interruptions. Select fewer devices."}
	}
	return out, err
}

var _ Repository = (*SQLRepository)(nil)
