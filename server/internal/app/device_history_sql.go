package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
)

func historySearchFields(kind string) []string {
	switch kind {
	case "calls":
		return []string{"name", "number", "formatted_number", "normalized_number", "matched_number", "asserted_display_name"}
	case "notifications":
		return []string{"app_label", "title", "text", "big_text", "sub_text", "text_lines", "messages"}
	default:
		return []string{"app_label"}
	}
}

// Only fixed table, column and metadata names enter this statement. All viewer
// input is passed as parameters. Deduplication precedes type/text filtering so
// an older call revision cannot reappear after the latest revision changes.
func deviceHistorySQL(f DeviceHistoryFilter, c Cursor, limit int) (string, []any) {
	table, recorded, eventType := "tracker_app_usage", "t.recorded_at", "t.event_type"
	identity, sourceID, sourcePackage, sourceKey := "''", "''", "t.source_package", "t.source_key"
	if f.Kind == "calls" {
		table, recorded = "tracker_call_logs", "t.call_started_at"
		eventType = "COALESCE(JSON_UNQUOTE(JSON_EXTRACT(t.fields_json,'$.type')),'')"
		identity, sourceID, sourcePackage, sourceKey = "t.call_id", "t.source_id", "''", "''"
	} else if f.Kind == "notifications" {
		table = "tracker_notifications"
	}
	q := "SELECT t.event_id,t.device_id," + recorded + ",t.observed_at,DATE_FORMAT(t.received_at,'%Y-%m-%dT%H:%i:%s.%fZ')," + eventType + "," + sourcePackage + "," + identity + "," + sourceID + "," + sourceKey + ",t.fields_json FROM " + table + " t WHERE t.device_id IN (" + marks(len(f.DeviceIDs)) + ") AND " + recorded + ">=? AND " + recorded + "<?"
	args := append(stringsAny(f.DeviceIDs), f.From, f.To)
	if f.Kind != "notifications" {
		group := "n.source_key=t.source_key AND n.event_type=t.event_type"
		if f.Kind == "calls" {
			group = "n.call_id=t.call_id"
		}
		q += " AND NOT EXISTS(SELECT 1 FROM " + table + " n WHERE n.device_id=t.device_id AND " + group + " AND (n.observed_at>t.observed_at OR (n.observed_at=t.observed_at AND n.event_id>t.event_id)))"
	}
	if f.EventType != "" {
		q += " AND " + eventType + "=?"
		args = append(args, f.EventType)
	}
	if f.Search != "" {
		parts := []string{sourcePackage}
		for _, key := range historySearchFields(f.Kind) {
			parts = append(parts, "JSON_UNQUOTE(JSON_EXTRACT(t.fields_json,'$."+key+"'))")
		}
		q += " AND LOCATE(?,LOWER(CONCAT_WS(' '," + strings.Join(parts, ",") + ")))>0"
		args = append(args, f.Search)
	}
	if c.LastTime != "" {
		q += " AND (" + recorded + "<? OR (" + recorded + "=? AND t.event_id<?))"
		args = append(args, c.LastTime, c.LastTime, c.LastID)
	}
	q += " ORDER BY " + recorded + " DESC,t.event_id DESC LIMIT ?"
	args = append(args, limit)
	return q, args
}

func (r *SQLRepository) DeviceHistory(ctx context.Context, f DeviceHistoryFilter, p ReadPoint, c Cursor, limit int) ([]DeviceHistoryRecord, error) {
	out := []DeviceHistoryRecord{}
	q, args := deviceHistorySQL(f, c, limit)
	err := r.at(ctx, p, func(conn *sql.Conn) error {
		rows, err := conn.QueryContext(ctx, q, args...)
		if err != nil {
			return historyUnavailable()
		}
		defer rows.Close()
		for rows.Next() {
			var e DeviceHistoryRecord
			var fields []byte
			if err := rows.Scan(&e.ID, &e.DeviceID, &e.Recorded, &e.Observed, &e.Received, &e.EventType, &e.SourcePackage, &e.CallID, &e.SourceID, &e.SourceKey, &fields); err != nil {
				return err
			}
			if err := json.Unmarshal(fields, &e.Fields); err != nil {
				return err
			}
			if e.Fields == nil {
				e.Fields = map[string]*string{}
			}
			e.Recorded, e.Observed, e.Received = FormatTime(ParseTime(e.Recorded)), FormatTime(ParseTime(e.Observed)), FormatTime(ParseTime(e.Received))
			out = append(out, e)
		}
		return rows.Err()
	})
	if err != nil {
		var apiErr APIError
		if errors.As(err, &apiErr) {
			return nil, err
		}
		return nil, historyUnavailable()
	}
	return out, nil
}
