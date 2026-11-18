package tidbhttp

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestBindPreservesQuotedSQLAndPreventsValueInjection(t *testing.T) {
	query := "SELECT ?, '?', `?`, \"?\", ? /* ? */ -- ?\n, ? # ?\n"
	got, err := bind(query, []driver.NamedValue{{Ordinal: 1, Value: "a'\\\x00😀"}, {Ordinal: 2, Value: int64(20)}, {Ordinal: 3, Value: nil}})
	want := "SELECT CONVERT(X'61275c00f09f9880' USING utf8mb4), '?', `?`, \"?\", 20 /* ? */ -- ?\n, NULL # ?\n"
	if err != nil || got != want {
		t.Fatalf("unsafe or incorrect binding: %q, %v", got, err)
	}
	for _, query := range []string{"SELECT ?", "SELECT 'unfinished", "SELECT 1; DELETE FROM x", "SELECT /*! ? */ 1"} {
		if _, err := bind(query, nil); err == nil {
			t.Fatalf("invalid SQL accepted: %s", query)
		}
	}
	if _, err := bind("SELECT 1", []driver.NamedValue{{Value: int64(1)}}); err == nil {
		t.Fatal("extra parameter accepted")
	}
}

func TestHTTPSPoolKeepsSnapshotsAndStartsFreshTransactions(t *testing.T) {
	type state struct {
		tx       bool
		snapshot string
	}
	var mu sync.Mutex
	states := map[string]state{}
	sequence := 0
	mutations := 0
	api := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		user, password, ok := r.BasicAuth()
		if !ok || user != "fixture-user" || password != "fixture-password" || r.Header.Get("TiDB-Database") != "fixture-db" {
			t.Error("SQL credentials or database missing")
		}
		var body struct {
			Query string `json:"query"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		header := r.Header.Get("TiDB-Session")
		s := states[header]
		rows := [][]*string{}
		types := []map[string]string{}
		add := func(name, value string) {
			types = append(types, map[string]string{"name": name, "type": "CHAR"})
			rows = [][]*string{{&value}}
		}
		switch {
		case body.Query == "BEGIN":
			if header != "" {
				t.Error("BEGIN must start a fresh session")
			}
			s = state{tx: true}
		case body.Query == "ROLLBACK" || body.Query == "COMMIT":
			if !s.tx {
				t.Error("transaction token was lost")
			}
			s.tx = false
		case strings.Contains(body.Query, "TIDB_CURRENT_TSO"):
			if !s.tx {
				t.Error("snapshot queried outside transaction")
			}
			add("tso", "471234567890123456")
		case strings.Contains(body.Query, "SELECT @@tidb_snapshot"):
			add("snapshot", s.snapshot)
		case strings.HasPrefix(body.Query, "SET @@tidb_snapshot="):
			s.snapshot = strings.Trim(strings.TrimSpace(strings.TrimPrefix(body.Query, "SET @@tidb_snapshot=")), "'")
		case strings.HasPrefix(body.Query, "INSERT"):
			mutations++
		case body.Query == "SELECT nullable_values":
			types = []map[string]string{{"name": "optional", "type": "VARCHAR"}, {"name": "id", "type": "BIGINT"}, {"name": "lat", "type": "DOUBLE"}, {"name": "flag", "type": "TINYINT"}}
			id, lat, flag := "471234567890123456", "17.437497", "1"
			rows = [][]*string{{nil, &id, &lat, &flag}}
		default:
			add("one", "1")
		}
		sequence++
		token := strings.Repeat("x", sequence)
		states[token] = s
		w.Header().Set("TiDB-Session", token)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"types": types, "rows": rows, "rowsAffected": 0})
	}))
	defer api.Close()
	db := sql.OpenDB(&connector{endpoint: api.URL, user: "fixture-user", password: "fixture-password", database: "fixture-db", client: api.Client()})
	defer db.Close()
	db.SetMaxOpenConns(1)
	ctx := context.Background()
	if err := db.PingContext(ctx); err != nil {
		t.Fatal(err)
	}
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	var tso string
	if err = tx.QueryRow("SELECT CAST(TIDB_CURRENT_TSO() AS CHAR)").Scan(&tso); err != nil || tso != "471234567890123456" {
		t.Fatal("TSO precision or transaction lost")
	}
	if _, err = tx.Exec("INSERT INTO forbidden VALUES(1)"); err == nil {
		t.Fatal("read-only transaction accepted mutation")
	}
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if mutations != 0 {
		t.Fatal("read-only rejection reached provider")
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = conn.ExecContext(ctx, "SET @@tidb_snapshot='471234567890123456'"); err != nil {
		t.Fatal(err)
	}
	var snapshot string
	if err = conn.QueryRowContext(ctx, "SELECT @@tidb_snapshot").Scan(&snapshot); err != nil || snapshot != tso {
		t.Fatal("snapshot state lost")
	}
	if _, err = conn.ExecContext(ctx, "SET @@tidb_snapshot=''"); err != nil {
		t.Fatal(err)
	}
	conn.Close()
	if err = db.QueryRow("SELECT @@tidb_snapshot").Scan(&snapshot); err != nil || snapshot != "" {
		t.Fatal("snapshot leaked into pool")
	}
	var optional sql.NullString
	var id string
	var latitude float64
	var flag bool
	if err = db.QueryRow("SELECT nullable_values").Scan(&optional, &id, &latitude, &flag); err != nil || optional.Valid || id != tso || latitude != 17.437497 || !flag {
		t.Fatal("SQL value types changed")
	}
}

func TestTransportFailureIsNotRetriedAndDoesNotExposeData(t *testing.T) {
	calls := 0
	client := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 503, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"code":"503","message":"PASSWORD fixture GPS and SQL"}`))}, nil
	})}
	db := sql.OpenDB(&connector{endpoint: "https://fixture.example/sql", client: client})
	defer db.Close()
	err := db.PingContext(context.Background())
	if err == nil || calls != 1 || strings.Contains(err.Error(), "PASSWORD") {
		t.Fatal("transport retried a request or exposed provider details")
	}
	var failure *Error
	if !errors.As(err, &failure) || failure.HTTPStatus != 503 {
		t.Fatal("missing redacted transport classification")
	}
}
func TestHTTPSRejectsMissingSessionsTruncationAndCancelledRequests(t *testing.T) {
	for _, tc := range []struct{ name, body, session, kind string }{{"missing token", `{"types":[],"rows":[]}`, "", "missing_session"}, {"bad body", `oops`, "token", "invalid_response"}, {"bad row width", `{"types":[],"rows":[["x"]]}`, "token", "invalid_rows"}, {"row cap", ``, "token", "row_limit_exceeded"}} {
		t.Run(tc.name, func(t *testing.T) {
			body := tc.body
			if tc.name == "row cap" {
				raw, _ := json.Marshal(map[string]any{"types": []any{}, "rows": make([][]string, 10000)})
				body = string(raw)
			}
			client := &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Header: http.Header{"Tidb-Session": {tc.session}}, Body: io.NopCloser(strings.NewReader(body))}, nil
			})}
			c := &connection{config: &connector{endpoint: "https://fixture.example/sql", client: client}}
			_, err := c.request(context.Background(), "SELECT 1")
			var failure *Error
			if !errors.As(err, &failure) || failure.Kind != tc.kind {
				t.Fatalf("wrong response classification: %v", err)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	client := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) { return nil, r.Context().Err() })}
	c := &connection{config: &connector{endpoint: "https://fixture.example/sql", client: client}}
	if _, err := c.request(ctx, "SELECT 1"); err == nil || c.IsValid() {
		t.Fatal("cancelled session was retained")
	}
}
func TestOpenRestrictsCredentialDestination(t *testing.T) {
	for _, dsn := range []string{"u:p@tcp(evil.example:4000)/db?tls=true", "u:p@tcp(tidbcloud.com.evil.example:4000)/db?tls=true", "u:p@unix(/tmp/socket)/db", "u:@tcp(gateway.tidbcloud.com:4000)/db?tls=true"} {
		if db, err := Open(dsn); err == nil {
			db.Close()
			t.Fatal("unsafe credential destination accepted")
		}
	}
	db, err := Open("u:p@tcp(gateway.tidbcloud.com:4000)/db?tls=true")
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
	if got, err := literal(time.Date(2026, 11, 18, 4, 0, 0, 0, time.FixedZone("IST", 19800))); err != nil || !strings.Contains(got, "CONVERT") {
		t.Fatal("timestamp parameter failed")
	}
}

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
