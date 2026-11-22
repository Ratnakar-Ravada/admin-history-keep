// migrateapp applies additive app analytics and device access tables, never tracking DDL.
package main

import (
	"context"
	"flag"
	"fmt"
	"locationkeeper/server/internal/app"
	"locationkeeper/server/internal/database"
	"log"
	"os"
	"strings"
	"time"
)

func main() {
	apply := flag.Bool("apply", false, "Apply the six additive app analytics/device access CREATE TABLE statements")
	flag.Parse()
	if app.LoadEnv(os.Getenv("ENV_FILE")) != nil {
		log.Fatal("Unable to load ENV_FILE")
	}
	cfg, err := app.ConfigFromEnv()
	if err != nil {
		log.Fatal("Check environment configuration")
	}
	lines := []string{}
	for _, path := range []string{"../db/migrations/004_app_analytics.sql", "../db/migrations/005_device_access.sql"} {
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			log.Fatal("Run this command from server/")
		}
		for _, line := range strings.Split(string(raw), "\n") {
			if !strings.HasPrefix(strings.TrimSpace(line), "--") {
				lines = append(lines, line)
			}
		}
	}
	statements := []string{}
	for _, q := range strings.Split(strings.Join(lines, "\n"), ";") {
		q = strings.TrimSpace(q)
		if q == "" {
			continue
		}
		if !strings.HasPrefix(q, "CREATE TABLE IF NOT EXISTS app_") {
			log.Fatal("Unexpected migration statement")
		}
		statements = append(statements, q)
	}
	if len(statements) != 6 {
		log.Fatal("Expected six additive tables")
	}
	if !*apply {
		fmt.Println("Ready: six additive app analytics/device access tables. Pass -apply to create them.")
		return
	}
	db, err := database.Open(cfg.AppDBDSN, cfg.TiDBTransport)
	if err != nil {
		log.Fatal("Invalid app database configuration")
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	for _, q := range statements {
		if _, err = db.ExecContext(ctx, q); err != nil {
			log.Fatal("App schema migration failed; verify CREATE privileges")
		}
	}
	fmt.Println("Created/verified six app analytics/device access tables. Tracking tables were unchanged.")
}
