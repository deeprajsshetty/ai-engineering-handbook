// Package dbengine is the engine-neutral contract for integrating database
// servers (PostgreSQL, Oracle, and any engine added later). One Engine
// implementation per database product turns its catalog views into a
// provider-neutral Inventory (for topology) and Readings (for metrics).
//
// Everything above this package — the "database" discovery provider, the
// measured metrics collector, the API and the UI — is engine-agnostic: adding
// an engine means adding one package that implements Engine and registering
// it in internal/modules. See docs/guides/extending.md.
//
// Engines only issue read-only catalog and statistics queries. They never
// read application tables and never change server state.
package dbengine

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"sort"
	"strconv"
	"time"
)

// TLSMode is the engine-neutral transport security setting.
type TLSMode string

const (
	TLSVerifyFull TLSMode = "verify-full" // encrypt and verify certificate and host name (default)
	TLSRequire    TLSMode = "require"     // encrypt, do not verify (private networks with self-signed certificates)
	TLSDisable    TLSMode = "disable"     // plaintext; only allowed when insecure endpoints are enabled (development)
)

// Target identifies a database server. It holds no secret.
type Target struct {
	Host     string
	Port     int
	Database string // PostgreSQL database name; Oracle service name
	Username string
	TLS      TLSMode
}

// DialFunc is a guarded TCP dialer (see netguard.Policy.Dialer).
type DialFunc func(ctx context.Context, network, address string) (net.Conn, error)

// Row is one result row; values are driver-native (int64, float64, string,
// []byte, time.Time, nil).
type Row []any

// Querier runs read-only queries. Engines depend on this interface rather
// than *sql.DB so they can be unit-tested with canned results.
type Querier interface {
	Query(ctx context.Context, query string, args ...any) ([]Row, error)
}

// Instance is the server (PostgreSQL cluster, Oracle instance).
type Instance struct {
	ID         string // stable identity: PostgreSQL system identifier; Oracle DBID + instance name
	Name       string
	Version    string
	Properties map[string]any
}

// Database is a logical database (PostgreSQL database, Oracle PDB or non-CDB).
type Database struct {
	Name       string
	Properties map[string]any
}

// Tablespace is a storage container. Database is empty for engines whose
// tablespaces belong to the instance (PostgreSQL).
type Tablespace struct {
	Name       string
	Database   string
	Properties map[string]any
}

// Inventory is what discovery reads from one server.
type Inventory struct {
	Engine      string
	Instance    Instance
	Databases   []Database
	Tablespaces []Tablespace
}

// ObjectRef addresses the object a reading belongs to.
type ObjectRef struct {
	Kind     string // "instance" | "database" | "tablespace"
	Database string // for tablespaces of engines with per-database tablespaces
	Name     string // empty for the instance
}

// Readings is one statistics snapshot. Gauges are point-in-time values;
// Counters are cumulative and are turned into per-second rates by the
// collector from two consecutive snapshots. Keys are metric names from the
// domain metrics catalog.
type Readings struct {
	Gauges   map[ObjectRef]map[string]float64
	Counters map[ObjectRef]map[string]float64
}

// NewReadings returns empty readings.
func NewReadings() Readings {
	return Readings{Gauges: map[ObjectRef]map[string]float64{}, Counters: map[ObjectRef]map[string]float64{}}
}

// Gauge records a gauge value.
func (r Readings) Gauge(o ObjectRef, metric string, v float64) { put(r.Gauges, o, metric, v) }

// Counter records a cumulative counter value.
func (r Readings) Counter(o ObjectRef, metric string, v float64) { put(r.Counters, o, metric, v) }

func put(m map[ObjectRef]map[string]float64, o ObjectRef, metric string, v float64) {
	if m[o] == nil {
		m[o] = map[string]float64{}
	}
	m[o][metric] = v
}

// Engine integrates one database product.
type Engine interface {
	Name() string        // stable identifier used in configuration and labels, e.g. "oracle"
	DisplayName() string // e.g. "Oracle Database"
	DefaultPort() int
	// Open returns a connection pool that dials only through dial. It must
	// not contact the server (sql.DB connects lazily).
	Open(t Target, password string, dial DialFunc) (*sql.DB, error)
	// MinimumPrivileges documents the read-only grants the monitoring user needs.
	MinimumPrivileges() string
	Inventory(ctx context.Context, q Querier) (Inventory, error)
	Sample(ctx context.Context, q Querier) (Readings, error)
}

// Registry holds the compiled-in engines.
type Registry struct{ engines map[string]Engine }

// NewRegistry builds a registry.
func NewRegistry(es ...Engine) *Registry {
	r := &Registry{engines: map[string]Engine{}}
	for _, e := range es {
		r.engines[e.Name()] = e
	}
	return r
}

// Get returns an engine by name.
func (r *Registry) Get(name string) (Engine, bool) { e, ok := r.engines[name]; return e, ok }

// Names returns engine names sorted.
func (r *Registry) Names() []string {
	out := make([]string, 0, len(r.engines))
	for n := range r.engines {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// SQLQuerier adapts *sql.DB to Querier.
type SQLQuerier struct{ DB *sql.DB }

// Query runs a query and materialises all rows (catalog queries are small;
// callers bound them with LIMIT/FETCH FIRST).
func (s SQLQuerier) Query(ctx context.Context, query string, args ...any) ([]Row, error) {
	rows, err := s.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	var out []Row
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		out = append(out, Row(vals))
	}
	return out, rows.Err()
}

// ErrNoRows is returned by One when a query that must return a row did not.
var ErrNoRows = errors.New("query returned no rows")

// One runs a query that must return exactly one row.
func One(ctx context.Context, q Querier, query string, args ...any) (Row, error) {
	rows, err := q.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, ErrNoRows
	}
	return rows[0], nil
}

// Str converts a driver value to a string.
func Str(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case []byte:
		return string(x)
	case time.Time:
		return x.UTC().Format(time.RFC3339)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	default:
		return fmt.Sprint(x)
	}
}

// Num converts a driver value to a float64; ok is false for NULL or non-numeric values.
func Num(v any) (float64, bool) {
	switch x := v.(type) {
	case int64:
		return float64(x), true
	case int32:
		return float64(x), true
	case int:
		return float64(x), true
	case float64:
		return x, true
	case float32:
		return float64(x), true
	case string:
		f, err := strconv.ParseFloat(x, 64)
		return f, err == nil
	case []byte:
		f, err := strconv.ParseFloat(string(x), 64)
		return f, err == nil
	default:
		return 0, false
	}
}

// Bool converts a driver value to a boolean (true, "t", "YES", 1).
func Bool(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case int64:
		return x != 0
	default:
		s := Str(v)
		return s == "t" || s == "true" || s == "YES" || s == "TRUE" || s == "1"
	}
}
