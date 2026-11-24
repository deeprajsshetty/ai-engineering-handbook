// Package oracle implements store.Backend on Oracle Database 19c or later
// (ADR 0011), through database/sql and the pure-Go go-ora driver, so the
// platform's own data (connections, jobs, topology, metrics, audit) can live
// in Oracle where a customer mandates it.
//
// Behaviour matches store/postgres; both are held to the same contract tests
// (store/storetest). Oracle-specific choices:
//   - Oracle treats ” as NULL: optional text columns are nullable and are
//     scanned through nullStr; filters are assembled in Go so an empty filter
//     adds no predicate (a "= ”" test would never be true in Oracle).
//   - Binds are always named (:name via sql.Named); identifiers are never
//     interpolated from input.
//   - Arrays are bound as IN lists in chunks of at most 1000 (Oracle's limit).
//   - Timestamps are TIMESTAMP(6) holding UTC; values are bound as t.UTC()
//     and re-labelled UTC when scanned.
//   - JSON documents are CLOBs constrained IS JSON and bound as go-ora Clob.
package oracle

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	goora "github.com/sijms/go-ora/v2"

	"github.com/deeprajsshetty/observability-platform/platform/internal/store"
)

// Store is the Oracle implementation.
type Store struct {
	db *sql.DB
}

var _ store.Backend = (*Store)(nil)

// Open connects with a bounded pool. url is oracle://user:password@host:port/service.
func Open(ctx context.Context, rawURL string, maxConns int) (*Store, error) {
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme != "oracle" || u.Host == "" {
		return nil, errors.New("invalid Oracle database URL") // never echo: it may hold a password
	}
	q := u.Query()
	if q.Get("TIMEOUT") == "" {
		q.Set("TIMEOUT", "30") // per-call timeout, seconds
	}
	u.RawQuery = q.Encode()
	db := sql.OpenDB(utcConnector{goora.NewConnector(u.String())})
	db.SetMaxOpenConns(maxConns)
	db.SetMaxIdleConns(maxConns)
	db.SetConnMaxIdleTime(5 * time.Minute)
	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := db.PingContext(cctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("connecting to Oracle: %s", redact(err.Error(), u))
	}
	return &Store{db: db}, nil
}

// utcConnector pins every session to UTC. go-ora otherwise sets the session
// time zone from the client host, and Oracle then shifts TIMESTAMP columns
// when comparing them with bound time values (found by the contract tests
// on a host in +05:30).
type utcConnector struct{ driver.Connector }

func (c utcConnector) Connect(ctx context.Context) (driver.Conn, error) {
	conn, err := c.Connector.Connect(ctx)
	if err != nil {
		return nil, err
	}
	ex, ok := conn.(driver.ExecerContext)
	if !ok {
		conn.Close()
		return nil, errors.New("oracle driver does not support ExecContext")
	}
	if _, err := ex.ExecContext(ctx, `ALTER SESSION SET TIME_ZONE = 'UTC'`, nil); err != nil {
		conn.Close()
		return nil, err
	}
	return conn, nil
}

// redact removes the password from driver messages.
func redact(msg string, u *url.URL) string {
	if p, ok := u.User.Password(); ok && p != "" {
		msg = strings.ReplaceAll(msg, p, "***")
	}
	return msg
}

// NewFromDB wraps an existing pool (tests).
func NewFromDB(db *sql.DB) *Store { return &Store{db: db} }

// DB exposes the pool (tests).
func (s *Store) DB() *sql.DB { return s.db }

// Driver implements store.Backend.
func (s *Store) Driver() string { return "oracle" }

// Ping checks connectivity.
func (s *Store) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }

// Close closes the pool.
func (s *Store) Close() { _ = s.db.Close() }

// ------------------------------------------------------------------ helpers

// qb builds a statement with named binds.
type qb struct {
	sb   strings.Builder
	args []any
}

func (q *qb) w(s string) *qb { q.sb.WriteString(s); return q }

// b adds a bind and returns its placeholder.
func (q *qb) b(v any) string {
	name := "p" + strconv.Itoa(len(q.args))
	q.args = append(q.args, sql.Named(name, v))
	return ":" + name
}

// in writes "col IN (:a,:b,...)" chunked by 1000, OR-joined, in parentheses.
// An empty list yields a false predicate.
func (q *qb) in(col string, vals []string) *qb {
	if len(vals) == 0 {
		return q.w("1=0")
	}
	q.w("(")
	for start := 0; start < len(vals); start += 1000 {
		if start > 0 {
			q.w(" OR ")
		}
		q.w(col + " IN (")
		for i, v := range vals[start:min(start+1000, len(vals))] {
			if i > 0 {
				q.w(",")
			}
			q.w(q.b(v))
		}
		q.w(")")
	}
	return q.w(")")
}

func (q *qb) String() string { return q.sb.String() }

// ts binds a time as TIMESTAMP holding its UTC wall clock. go-ora encodes a
// time.Time's own wall clock, so converting to UTC first is sufficient
// (verified against Oracle 23ai with a non-UTC host time zone).
func ts(t time.Time) any { return t.UTC() }

// tsp binds an optional time.
func tsp(t *time.Time) any {
	if t == nil {
		return nil
	}
	return ts(*t)
}

// clob binds a JSON document (or NULL for nil).
func clob(v any) any {
	if v == nil {
		return goora.Clob{Valid: false}
	}
	b, err := json.Marshal(v)
	if err != nil {
		b = []byte("{}")
	}
	return goora.Clob{String: string(b), Valid: true}
}

// str binds "" as NULL explicitly (Oracle would do so anyway).
func str(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func boolNum(b bool) int {
	if b {
		return 1
	}
	return 0
}

// nullStr scans NULL as "".
type nullStr struct{ p *string }

func (n nullStr) Scan(v any) error {
	switch x := v.(type) {
	case nil:
		*n.p = ""
	case string:
		*n.p = x
	case []byte:
		*n.p = string(x)
	case goora.Clob:
		*n.p = x.String
	default:
		*n.p = fmt.Sprint(x)
	}
	return nil
}

// utc scans a TIMESTAMP and re-labels its wall clock as UTC.
type utc struct{ p *time.Time }

func (u utc) Scan(v any) error {
	t, ok := v.(time.Time)
	if !ok {
		if v == nil {
			return errors.New("unexpected NULL timestamp")
		}
		return fmt.Errorf("unexpected timestamp type %T", v)
	}
	*u.p = time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), time.UTC)
	return nil
}

// utcPtr scans a nullable TIMESTAMP.
type utcPtr struct{ p **time.Time }

func (u utcPtr) Scan(v any) error {
	if v == nil {
		*u.p = nil
		return nil
	}
	var t time.Time
	if err := (utc{&t}).Scan(v); err != nil {
		return err
	}
	*u.p = &t
	return nil
}

// boolScan scans NUMBER(1).
type boolScan struct{ p *bool }

func (b boolScan) Scan(v any) error {
	switch x := v.(type) {
	case int64:
		*b.p = x != 0
	case float64:
		*b.p = x != 0
	case bool:
		*b.p = x
	case string:
		*b.p = x == "1"
	case nil:
		*b.p = false
	default:
		return fmt.Errorf("unexpected boolean type %T", v)
	}
	return nil
}

// jsonScan decodes a CLOB JSON document into dst.
type jsonScan struct{ dst any }

func (j jsonScan) Scan(v any) error {
	var s string
	if err := (nullStr{&s}).Scan(v); err != nil {
		return err
	}
	if s == "" {
		return nil
	}
	return json.Unmarshal([]byte(s), j.dst)
}

func isUnique(err error, name string) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "ORA-00001") && (name == "" || strings.Contains(strings.ToUpper(msg), strings.ToUpper(name)))
}

func notFound(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return store.ErrNotFound
	}
	return err
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

type scanner interface{ Scan(dest ...any) error }

func collect[T any](rows *sql.Rows, err error, scan func(scanner) (T, error)) ([]T, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []T
	for rows.Next() {
		v, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *Store) query(ctx context.Context, q *qb) (*sql.Rows, error) {
	return s.db.QueryContext(ctx, q.String(), q.args...)
}

func (s *Store) exec(ctx context.Context, q *qb) (int64, error) {
	res, err := s.db.ExecContext(ctx, q.String(), q.args...)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// tx runs fn in a transaction.
func (s *Store) tx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func txExec(ctx context.Context, tx *sql.Tx, q *qb) (int64, error) {
	res, err := tx.ExecContext(ctx, q.String(), q.args...)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func pageLimit(q *qb, n int) {
	q.w(" FETCH FIRST " + strconv.Itoa(n) + " ROWS ONLY")
}
