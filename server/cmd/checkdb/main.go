// checkdb performs only schema inspection, SELECTs, and read-only snapshot sessions.
// It prints aggregate verification results, never credentials, names, UUIDs or coordinates.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/go-sql-driver/mysql"
	"locationkeeper/server/internal/app"
	"locationkeeper/server/internal/database"
	"locationkeeper/server/internal/tidbhttp"
	"net"
	"os"
	"strings"
	"time"
)

func main() {
	stage := "configuration"
	report := map[string]any{}
	fail := func() {
		report["ok"] = false
		report["failed_stage"] = stage
		_ = json.NewEncoder(os.Stdout).Encode(report)
		os.Exit(1)
	}
	if app.LoadEnv(os.Getenv("ENV_FILE")) != nil {
		fail()
	}
	cfg := mysql.NewConfig()
	cfg.User = os.Getenv("TIDB_USERNAME")
	cfg.Passwd = os.Getenv("TIDB_PASSWORD")
	cfg.Net = "tcp"
	cfg.Addr = os.Getenv("TIDB_HOST") + ":4000"
	cfg.DBName = os.Getenv("TIDB_DATABASE")
	if raw := os.Getenv("TIDB_DSN"); raw != "" {
		var err error
		cfg, err = mysql.ParseDSN(raw)
		if err != nil {
			fail()
		}
	}
	cfg.TLSConfig = "true"
	cfg.ParseTime = true
	cfg.Loc = time.UTC
	cfg.Timeout = 8 * time.Second
	cfg.ReadTimeout = 10 * time.Second
	cfg.Params = map[string]string{"time_zone": "'+00:00'"}
	dsn := cfg.FormatDSN()
	transport := os.Getenv("TIDB_TRANSPORT")
	if transport == "" {
		transport = "mysql"
	}
	report["transport"] = transport
	db, e := database.Open(dsn, transport)
	if e != nil {
		fail()
	}
	defer db.Close()
	db.SetMaxOpenConns(2)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Second)
	defer cancel()
	stage = "connection"
	if err := db.PingContext(ctx); err != nil {
		message := err.Error()
		host, _, _ := net.SplitHostPort(cfg.Addr)
		for _, secret := range []string{cfg.Passwd, cfg.User, cfg.Addr, host, cfg.DBName} {
			if secret != "" {
				message = strings.ReplaceAll(message, secret, "[redacted]")
			}
		}
		report["connection_error"] = message
		fail()
	}
	stage = "schema"
	repo, e := app.NewSQLRepository(ctx, db, false)
	if e != nil {
		fail()
	}
	report["execution_metadata"] = repo.Execution
	report["airplane_column"] = repo.Airplane
	stage = "snapshot"
	p, e := repo.Snapshot(ctx)
	if e != nil {
		var transportError *tidbhttp.Error
		if errors.As(e, &transportError) {
			report["transport_error"] = transportError.Error()
		}
		if e.Error() == "No valid TiDB snapshot timestamp" {
			report["snapshot_error"] = "no_valid_tso"
		}
		fail()
	}
	devices, e := repo.Devices(ctx, app.Identity{Admin: true})
	if e != nil {
		fail()
	}
	ids := []string{}
	for i, d := range devices {
		if i == 3 {
			break
		}
		ids = append(ids, d.ID)
	}
	f := app.Filter{DeviceIDs: ids, From: app.FormatTime(time.Now().Add(-7 * 24 * time.Hour)), To: app.FormatTime(time.Now().Add(time.Hour)), Timezone: "Asia/Kolkata"}
	stage = "keyset/merge"
	first, e := repo.Events(ctx, f, p, app.Cursor{}, 21)
	if e != nil {
		fail()
	}
	report["first_page_rows"] = len(first)
	if len(first) > 20 {
		last := first[19]
		next, e := repo.Events(ctx, f, p, app.Cursor{LastTime: last.Recorded, LastID: last.ID}, 21)
		if e != nil {
			fail()
		}
		for _, a := range first[:20] {
			for _, b := range next {
				if a.ID == b.ID {
					fail()
				}
			}
		}
		report["next_page_rows"] = len(next)
	}
	stage = "severity filters"
	for _, v := range []string{"attention", "positive", "limited", "neutral"} {
		f.Severity = []string{v}
		events, e := repo.Events(ctx, f, p, app.Cursor{}, 21)
		if e != nil {
			fail()
		}
		for _, event := range events {
			if event.Severity != v {
				fail()
			}
		}
	}
	f.Severity = nil
	stage = "map/summary/latest"
	if _, _, e = repo.History(ctx, f, p, 5000); e != nil {
		fail()
	}
	if _, e = repo.Interruptions(ctx, f, p); e != nil {
		fail()
	}
	if _, e = repo.Summary(ctx, f); e != nil {
		fail()
	}
	if _, e = repo.Latest(ctx, ids); e != nil {
		fail()
	}
	stage = "pooled snapshot cleanup"
	var snapshot string
	if e = db.QueryRowContext(ctx, "SELECT @@tidb_snapshot").Scan(&snapshot); e != nil || snapshot != "" {
		fail()
	}
	report["snapshot_cleanup_verified"] = true
	report["read_only"] = true
	report["ok"] = true
	_ = json.NewEncoder(os.Stdout).Encode(report)
}
