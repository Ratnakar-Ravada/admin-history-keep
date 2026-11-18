// Package tidbhttp is an application-maintained database/sql adapter for TiDB
// Cloud Starter/Essential's preview HTTPS SQL protocol. Wire format and session
// handling follow PingCAP's documented serverless driver (@tidbcloud/serverless).
// No JavaScript service or additional runtime is required.
package tidbhttp

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-sql-driver/mysql"
)

type Error struct {
	HTTPStatus int
	Code       string
	Kind       string
}

func (e *Error) Error() string {
	return fmt.Sprintf("TiDB HTTPS %s (HTTP %d, code %s)", e.Kind, e.HTTPStatus, e.Code)
}

type connector struct {
	endpoint, user, password, database string
	client                             *http.Client
}
type httpDriver struct{}

func (httpDriver) Open(string) (driver.Conn, error) {
	return nil, errors.New("Use tidbhttp.Open with a private DSN")
}
func (c *connector) Driver() driver.Driver { return httpDriver{} }

func Open(dsn string) (*sql.DB, error) {
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		return nil, errors.New("Invalid TiDB MySQL DSN")
	}
	host, _, err := net.SplitHostPort(cfg.Addr)
	if err != nil || cfg.Net != "tcp" || !strings.HasSuffix(strings.ToLower(host), ".tidbcloud.com") || strings.ContainsAny(host, "/\\@?#\r\n") || cfg.User == "" || cfg.Passwd == "" || cfg.DBName == "" || strings.ContainsAny(cfg.DBName, "\r\n") {
		return nil, errors.New("HTTPS transport requires a TiDB Cloud Starter/Essential hostname and SQL credentials")
	}
	if !strings.HasPrefix(host, "http-") {
		host = "http-" + host
	}
	c := &connector{endpoint: "https://" + host + "/v1beta/sql", user: cfg.User, password: cfg.Passwd, database: cfg.DBName, client: &http.Client{Timeout: 12 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	return sql.OpenDB(c), nil
}
func (c *connector) Connect(ctx context.Context) (driver.Conn, error) {
	conn := &connection{config: c}
	if _, err := conn.request(ctx, "SET time_zone = '+00:00'"); err != nil {
		return nil, err
	}
	return conn, nil
}

type connection struct {
	config                   *connector
	session                  string
	broken, inTx, readOnlyTx bool
}
type response struct {
	Types []struct {
		Name string `json:"name"`
		Type string `json:"type"`
	} `json:"types"`
	Rows         [][]*string `json:"rows"`
	RowsAffected json.Number `json:"rowsAffected"`
	LastInsertID string      `json:"sLastInsertID"`
}

func (c *connection) request(ctx context.Context, query string) (*response, error) {
	if c.broken {
		return nil, errors.New("TiDB HTTPS session is unavailable")
	}
	body, _ := json.Marshal(map[string]string{"query": query})
	req, err := http.NewRequestWithContext(ctx, "POST", c.config.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, &Error{Kind: "request_error"}
	}
	req.SetBasicAuth(c.config.user, c.config.password)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("TiDB-Database", c.config.database)
	req.Header.Set("TiDB-Session", c.session)
	req.Header.Set("User-Agent", "location-keeper-go/1.0")
	resp, err := c.config.client.Do(req)
	if err != nil {
		c.broken = true
		return nil, &Error{Kind: "connection_error"}
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, (32<<20)+1))
	if err != nil || len(raw) > 32<<20 {
		c.broken = true
		return nil, &Error{HTTPStatus: resp.StatusCode, Kind: "response_error"}
	}
	if resp.StatusCode != 200 {
		var result struct {
			Code json.RawMessage `json:"code"`
		}
		_ = json.Unmarshal(raw, &result)
		code := strings.Trim(string(result.Code), "\"")
		if len(code) == 0 || len(code) > 32 {
			code = "unknown"
		}
		for _, r := range code {
			if r < '0' || r > '9' {
				code = "unknown"
				break
			}
		}
		// Provider messages may contain SQL, parameters or account details. Do not
		// expose them. Mark the connection unusable and never retry the statement.
		c.broken = true
		return nil, &Error{HTTPStatus: resp.StatusCode, Code: code, Kind: "query_rejected"}
	}
	var data response
	if json.Unmarshal(raw, &data) != nil {
		c.broken = true
		return nil, &Error{HTTPStatus: 200, Kind: "invalid_response"}
	}
	session := resp.Header.Get("TiDB-Session")
	if session == "" {
		c.broken = true
		return nil, &Error{HTTPStatus: 200, Kind: "missing_session"}
	}
	c.session = session
	// The preview API caps responses at 10k rows. Refuse the boundary rather than
	// silently treating a truncated interruption/registry result as complete.
	if len(data.Rows) >= 10000 {
		return nil, &Error{HTTPStatus: 200, Kind: "row_limit_exceeded"}
	}
	for _, row := range data.Rows {
		if len(row) != len(data.Types) {
			c.broken = true
			return nil, &Error{HTTPStatus: 200, Kind: "invalid_rows"}
		}
	}
	return &data, nil
}
func (c *connection) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("Prepared statements are unsupported; use QueryContext or ExecContext")
}
func (c *connection) IsValid() bool { return !c.broken }
func (c *connection) ResetSession(context.Context) error {
	if c.broken || c.inTx {
		return driver.ErrBadConn
	}
	return nil
}
func (c *connection) Close() error {
	if c.inTx && !c.broken {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_, _ = c.request(ctx, "ROLLBACK")
	}
	c.broken = true
	c.session = ""
	return nil
}
func (c *connection) Ping(ctx context.Context) error {
	_, err := c.request(ctx, "SELECT 1")
	return err
}
func (c *connection) Begin() (driver.Tx, error) {
	return c.BeginTx(context.Background(), driver.TxOptions{})
}
func (c *connection) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	if c.inTx || opts.Isolation != driver.IsolationLevel(sql.LevelDefault) && opts.Isolation != driver.IsolationLevel(sql.LevelRepeatableRead) {
		return nil, errors.New("Unsupported TiDB HTTPS transaction options")
	}
	// The HTTP API supports BEGIN, but rejects START TRANSACTION READ ONLY.
	// Enforce ReadOnly locally for this trusted-query adapter. Production viewer
	// credentials should additionally have SELECT-only database privileges.
	previousSession := c.session
	c.session = "" // Explicit transactions start a fresh HTTP session, as in the official SDK.
	if _, err := c.request(ctx, "BEGIN"); err != nil {
		return nil, err
	}
	c.inTx = true
	c.readOnlyTx = opts.ReadOnly
	return &transaction{conn: c, previousSession: previousSession}, nil
}

type transaction struct {
	conn            *connection
	previousSession string
}

func (t *transaction) Commit() error   { return t.finish("COMMIT") }
func (t *transaction) Rollback() error { return t.finish("ROLLBACK") }
func (t *transaction) finish(query string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := t.conn.request(ctx, query)
	t.conn.inTx = false
	t.conn.readOnlyTx = false
	t.conn.session = t.previousSession
	return err
}
func (c *connection) execute(ctx context.Context, query string, args []driver.NamedValue) (*response, error) {
	if c.inTx && c.readOnlyTx {
		words := strings.Fields(query)
		if len(words) == 0 {
			return nil, errors.New("Empty SQL statement")
		}
		word := strings.ToUpper(words[0])
		if word != "SELECT" && word != "SHOW" {
			return nil, errors.New("Mutation rejected in a read-only transaction")
		}
	}
	bound, err := bind(query, args)
	if err != nil {
		return nil, err
	}
	return c.request(ctx, bound)
}
func (c *connection) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	data, err := c.execute(ctx, query, args)
	if err != nil {
		return nil, err
	}
	return &rows{data: data}, nil
}
func (c *connection) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	data, err := c.execute(ctx, query, args)
	if err != nil {
		return nil, err
	}
	affected, _ := data.RowsAffected.Int64()
	id, _ := strconv.ParseInt(data.LastInsertID, 10, 64)
	return result{id, affected}, nil
}

type result struct{ id, affected int64 }

func (r result) LastInsertId() (int64, error) { return r.id, nil }
func (r result) RowsAffected() (int64, error) { return r.affected, nil }

type rows struct {
	data  *response
	index int
}

func (r *rows) Columns() []string {
	names := make([]string, len(r.data.Types))
	for i, t := range r.data.Types {
		names[i] = t.Name
	}
	return names
}
func (r *rows) Close() error { r.data.Rows = nil; return nil }
func (r *rows) Next(dest []driver.Value) error {
	if r.index >= len(r.data.Rows) {
		return io.EOF
	}
	for i, v := range r.data.Rows[r.index] {
		if v == nil {
			dest[i] = nil
		} else {
			dest[i] = *v
		}
	}
	r.index++
	return nil
}

// bind substitutes only SQL placeholders, respecting quoted text/comments.
// Text uses UTF-8 hex literals, avoiding dependence on backslash SQL modes.
func bind(query string, args []driver.NamedValue) (string, error) {
	var out strings.Builder
	index := 0
	for i := 0; i < len(query); {
		ch := query[i]
		if ch == '\'' || ch == '"' || ch == '`' {
			quote := ch
			start := i
			i++
			closed := false
			for i < len(query) {
				if query[i] == '\\' {
					i += 2
					continue
				}
				if query[i] == quote {
					if i+1 < len(query) && query[i+1] == quote {
						i += 2
						continue
					}
					i++
					closed = true
					break
				}
				i++
			}
			if !closed || i > len(query) {
				return "", errors.New("Unterminated SQL quote")
			}
			out.WriteString(query[start:i])
			continue
		}
		if ch == '#' || ch == '-' && i+2 < len(query) && query[i+1] == '-' && query[i+2] <= ' ' {
			end := strings.IndexByte(query[i:], '\n')
			if end < 0 {
				out.WriteString(query[i:])
				break
			}
			out.WriteString(query[i : i+end+1])
			i += end + 1
			continue
		}
		if ch == '/' && i+1 < len(query) && query[i+1] == '*' {
			end := strings.Index(query[i+2:], "*/")
			if end < 0 {
				return "", errors.New("Unterminated SQL comment")
			}
			if i+2 < len(query) && query[i+2] == '!' {
				return "", errors.New("Executable SQL comments are unsupported")
			}
			end += i + 4
			out.WriteString(query[i:end])
			i = end
			continue
		}
		if ch == '?' {
			if index >= len(args) {
				return "", errors.New("Not enough SQL parameters")
			}
			if args[index].Name != "" {
				return "", errors.New("Named SQL parameters are unsupported")
			}
			v, err := literal(args[index].Value)
			if err != nil {
				return "", err
			}
			out.WriteString(v)
			index++
			i++
			continue
		}
		if ch == ';' && strings.TrimSpace(query[i+1:]) != "" {
			return "", errors.New("Multiple SQL statements are unsupported")
		}
		out.WriteByte(ch)
		i++
	}
	if index != len(args) {
		return "", errors.New("Too many SQL parameters")
	}
	return out.String(), nil
}
func literal(v any) (string, error) {
	switch x := v.(type) {
	case nil:
		return "NULL", nil
	case string:
		if !utf8.ValidString(x) {
			return "", errors.New("SQL text must be valid UTF-8")
		}
		return "CONVERT(X'" + hex.EncodeToString([]byte(x)) + "' USING utf8mb4)", nil
	case []byte:
		return "X'" + hex.EncodeToString(x) + "'", nil
	case int64:
		return strconv.FormatInt(x, 10), nil
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return "", errors.New("SQL number must be finite")
		}
		return strconv.FormatFloat(x, 'g', -1, 64), nil
	case bool:
		if x {
			return "1", nil
		}
		return "0", nil
	case time.Time:
		return literal(x.UTC().Format("2006-01-02 15:04:05.000000"))
	default:
		return "", errors.New("Unsupported SQL parameter type")
	}
}

var _ driver.Connector = (*connector)(nil)
var _ driver.ConnBeginTx = (*connection)(nil)
var _ driver.QueryerContext = (*connection)(nil)
var _ driver.ExecerContext = (*connection)(nil)
var _ driver.Pinger = (*connection)(nil)
var _ driver.SessionResetter = (*connection)(nil)
var _ driver.Validator = (*connection)(nil)
