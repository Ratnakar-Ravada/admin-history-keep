package main

import (
	"context"
	"database/sql"
	"locationkeeper/server/internal/app"
	"locationkeeper/server/internal/database"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
	_ "time/tzdata"
)

func main() {
	if e := app.LoadEnv(os.Getenv("ENV_FILE")); e != nil {
		log.Fatal("Unable to load ENV_FILE")
	}
	c, e := app.ConfigFromEnv()
	if e != nil {
		log.Fatal(e)
	}
	listener, e := net.Listen("tcp", c.Addr)
	if e != nil {
		log.Fatalf("Unable to listen on %s: %v", c.Addr, e)
	}
	defer listener.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	auth, e := app.NewAuth(ctx, c)
	if e != nil {
		log.Fatal(e)
	}
	var repo app.Repository
	var cache *sql.DB
	if c.Demo {
		repo = app.NewDemo(time.Now())
	} else {
		db, err := database.Open(c.DSN, c.TiDBTransport)
		if err != nil {
			log.Fatal("Invalid database configuration")
		}
		defer db.Close()
		repo, e = app.NewSQLRepository(ctx, db, c.Assigned)
		if e != nil {
			log.Fatal("TiDB schema/connection could not be verified")
		}
		if c.CacheDSN != "" {
			cache, e = database.Open(c.CacheDSN, c.TiDBTransport)
			if e != nil {
				log.Fatal("Invalid place-cache database configuration")
			}
			defer cache.Close()
		}
	}
	places := app.NewPlaces(c.GeoKey, c.Demo, cache)
	defer places.Close()
	var store app.UsageStore
	var access app.DeviceAccessStore
	if c.Demo {
		store = app.NewMemoryUsageStore(c.Secret, true)
		access = app.NewMemoryDeviceAccessStore(c.Secret)
	} else {
		appDB, err := database.Open(c.AppDBDSN, c.TiDBTransport)
		if err != nil {
			log.Fatal("Invalid APP_DB_DSN")
		}
		defer appDB.Close()
		sqlStore, err := app.NewSQLUsageStore(ctx, appDB, c.Secret)
		if err != nil {
			log.Fatal("App analytics tables unavailable; run ENV_FILE=../.env go run ./cmd/migrateapp -apply")
		}
		store = sqlStore
		access, err = app.NewSQLDeviceAccessStore(ctx, appDB, c.Secret)
		if err != nil {
			log.Fatal("Device access tables unavailable; run ENV_FILE=../.env go run ./cmd/migrateapp -apply")
		}
	}
	usage := app.NewUsage(store)
	defer usage.Close()
	auth.Usage = usage
	server := app.NewServer(repo, auth, places, c)
	server.Usage = usage
	server.Access = access
	httpServer := &http.Server{Addr: c.Addr, Handler: server.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 20 * time.Second, WriteTimeout: 25 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 1 << 16}
	done := make(chan os.Signal, 1)
	signal.Notify(done, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-done
		ctx, stop := context.WithTimeout(context.Background(), 8*time.Second)
		defer stop()
		_ = httpServer.Shutdown(ctx)
	}()
	log.Printf("Location Keeper listening on %s (synthetic demo: %t)", listener.Addr(), c.Demo)
	if e = httpServer.Serve(listener); e != nil && e != http.ErrServerClosed {
		log.Fatalf("HTTP server stopped: %v", e)
	}
}
