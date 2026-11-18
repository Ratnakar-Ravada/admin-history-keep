// Emits credential-free SQL test cases or classifies JSON supplied over stdin.
package main

import (
	"encoding/json"
	"locationkeeper/server/internal/app"
	"os"
	"time"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "classify" {
		var events []app.Event
		if json.NewDecoder(os.Stdin).Decode(&events) != nil {
			os.Exit(1)
		}
		out := []string{}
		for _, e := range events {
			out = append(out, app.Classify(e))
		}
		_ = json.NewEncoder(os.Stdout).Encode(out)
		return
	}
	now := time.Now()
	f := app.Filter{DeviceIDs: []string{"fixture-device-1", "fixture-device-2"}, From: app.FormatTime(now.Add(-7 * 24 * time.Hour)), To: app.FormatTime(now.Add(time.Hour))}
	repo := &app.SQLRepository{Execution: true}
	out := map[string]any{}
	for _, severity := range []string{"", "attention", "positive", "limited", "neutral"} {
		f.Severity = nil
		if severity != "" {
			f.Severity = []string{severity}
		}
		query, args := repo.EventQuery(f, app.Cursor{}, 21)
		key := severity
		if key == "" {
			key = "first"
		}
		out[key] = map[string]any{"sql": query, "args": args}
	}
	f.Severity = nil
	q, a := repo.EventQuery(f, app.Cursor{LastTime: "fixture-cursor-time", LastID: "fixture-cursor-id"}, 21)
	out["next"] = map[string]any{"sql": q, "args": a}
	_ = json.NewEncoder(os.Stdout).Encode(out)
}
