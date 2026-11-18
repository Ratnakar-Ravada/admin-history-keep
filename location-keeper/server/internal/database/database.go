// Package database configures the explicit SQL transport shared by the API and
// read-only diagnostic. Statements are never retried through another transport.
package database

import (
	"database/sql"
	"errors"
	"github.com/go-sql-driver/mysql"
	"locationkeeper/server/internal/tidbhttp"
	"time"
)

func Open(dsn, transport string) (*sql.DB, error) {
	var db *sql.DB
	var err error
	switch transport {
	case "https":
		db, err = tidbhttp.Open(dsn)
	case "", "mysql":
		c, e := mysql.ParseDSN(dsn)
		if e != nil {
			return nil, errors.New("Invalid database DSN; use Go MySQL format and tls=true")
		}
		c.ParseTime = true
		c.Loc = time.UTC
		c.TLSConfig = "true"
		c.Timeout = 8 * time.Second
		c.ReadTimeout = 12 * time.Second
		c.WriteTimeout = 8 * time.Second
		if c.Params == nil {
			c.Params = map[string]string{}
		}
		c.Params["time_zone"] = "'+00:00'"
		db, err = sql.Open("mysql", c.FormatDSN())
	default:
		return nil, errors.New("TIDB_TRANSPORT must be mysql or https")
	}
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(8)
	db.SetMaxIdleConns(4)
	db.SetConnMaxLifetime(3 * time.Minute)
	return db, nil
}
