// Package postgresql integrates PostgreSQL servers (13 and later) through the
// pgx driver already used by the platform's own store. Sessions are opened
// with default_transaction_read_only=on and a statement timeout, so even a
// mis-granted monitoring role cannot change data through this path.
package postgresql

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"net/url"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"

	"github.com/deeprajsshetty/observability-platform/platform/internal/dbengine"
)

// Name is the engine name.
const Name = "postgresql"

// Engine implements dbengine.Engine.
type Engine struct{}

// Name returns "postgresql".
func (Engine) Name() string { return Name }

// DisplayName returns the product name.
func (Engine) DisplayName() string { return "PostgreSQL" }

// DefaultPort returns 5432.
func (Engine) DefaultPort() int { return 5432 }

// MinimumPrivileges documents the grants.
func (Engine) MinimumPrivileges() string {
	return "A login role that is a member of the predefined role pg_monitor (read-only statistics and sizes). No table privileges are needed."
}

// Open builds a lazily connecting pool. The password is set on the parsed
// config, never interpolated into a connection string.
func (Engine) Open(t dbengine.Target, password string, dial dbengine.DialFunc) (*sql.DB, error) {
	sslmode := map[dbengine.TLSMode]string{dbengine.TLSVerifyFull: "verify-full", dbengine.TLSRequire: "require", dbengine.TLSDisable: "disable"}[t.TLS]
	if sslmode == "" {
		return nil, fmt.Errorf("unsupported tls mode %q", t.TLS)
	}
	u := url.URL{Scheme: "postgres", User: url.User(t.Username), Host: hostPort(t), Path: "/" + t.Database}
	q := url.Values{}
	q.Set("sslmode", sslmode)
	q.Set("connect_timeout", "10")
	q.Set("application_name", "obsplatform-monitor")
	u.RawQuery = q.Encode()
	cfg, err := pgx.ParseConfig(u.String())
	if err != nil {
		return nil, fmt.Errorf("invalid postgresql target: %w", err)
	}
	cfg.Password = password
	cfg.RuntimeParams["default_transaction_read_only"] = "on"
	cfg.RuntimeParams["statement_timeout"] = "5000"
	cfg.DialFunc = func(ctx context.Context, network, addr string) (net.Conn, error) { return dial(ctx, network, addr) }
	cfg.Fallbacks = nil // no silent downgrade to plaintext
	db := stdlib.OpenDB(*cfg)
	db.SetMaxOpenConns(2)
	return db, nil
}

func hostPort(t dbengine.Target) string {
	port := t.Port
	if port == 0 {
		port = 5432
	}
	return net.JoinHostPort(t.Host, strconv.Itoa(port))
}

// Inventory reads the cluster, its databases and tablespaces.
func (Engine) Inventory(ctx context.Context, q dbengine.Querier) (dbengine.Inventory, error) {
	inv := dbengine.Inventory{Engine: Name}
	row, err := dbengine.One(ctx, q, `SELECT current_setting('server_version'), current_setting('server_version_num'),
		current_setting('max_connections'), pg_is_in_recovery()`)
	if err != nil {
		return inv, fmt.Errorf("reading server settings: %w", err)
	}
	inv.Instance = dbengine.Instance{Version: dbengine.Str(row[0]), Properties: map[string]any{
		"versionNum": dbengine.Str(row[1]), "maxConnections": dbengine.Str(row[2]), "inRecovery": dbengine.Bool(row[3]),
	}}
	// pg_control_system() needs EXECUTE (superuser or explicit grant). When
	// unavailable the provider falls back to the target address as identity.
	if r, err := dbengine.One(ctx, q, `SELECT system_identifier::text FROM pg_control_system()`); err == nil {
		inv.Instance.ID = dbengine.Str(r[0])
	}
	rows, err := q.Query(ctx, `SELECT d.datname, pg_encoding_to_char(d.encoding), d.datallowconn,
		CASE WHEN has_database_privilege(d.datname, 'CONNECT') OR pg_has_role('pg_read_all_stats', 'member') THEN pg_database_size(d.datname) END
		FROM pg_database d WHERE NOT d.datistemplate ORDER BY d.datname LIMIT 1000`)
	if err != nil {
		return inv, fmt.Errorf("listing databases: %w", err)
	}
	for _, r := range rows {
		p := map[string]any{"encoding": dbengine.Str(r[1]), "allowConnections": dbengine.Bool(r[2])}
		if n, ok := dbengine.Num(r[3]); ok {
			p["sizeBytes"] = n
		}
		inv.Databases = append(inv.Databases, dbengine.Database{Name: dbengine.Str(r[0]), Properties: p})
	}
	rows, err = q.Query(ctx, `SELECT spcname, pg_tablespace_size(oid) FROM pg_tablespace ORDER BY spcname LIMIT 1000`)
	if err != nil {
		return inv, fmt.Errorf("listing tablespaces: %w", err)
	}
	for _, r := range rows {
		p := map[string]any{}
		if n, ok := dbengine.Num(r[1]); ok {
			p["sizeBytes"] = n
		}
		inv.Tablespaces = append(inv.Tablespaces, dbengine.Tablespace{Name: dbengine.Str(r[0]), Properties: p})
	}
	return inv, nil
}

// Sample reads activity and statistics views.
func (Engine) Sample(ctx context.Context, q dbengine.Querier) (dbengine.Readings, error) {
	out := dbengine.NewReadings()
	inst := dbengine.ObjectRef{Kind: "instance"}
	row, err := dbengine.One(ctx, q, `SELECT count(*), count(*) FILTER (WHERE state = 'active')
		FROM pg_stat_activity WHERE backend_type = 'client backend'`)
	if err != nil {
		return out, fmt.Errorf("reading pg_stat_activity: %w", err)
	}
	if n, ok := dbengine.Num(row[0]); ok {
		out.Gauge(inst, "db.connections", n)
	}
	if n, ok := dbengine.Num(row[1]); ok {
		out.Gauge(inst, "db.sessions.active", n)
	}
	rows, err := q.Query(ctx, `SELECT datname, xact_commit + xact_rollback, blks_hit, blks_read,
		CASE WHEN has_database_privilege(datname, 'CONNECT') OR pg_has_role('pg_read_all_stats', 'member') THEN pg_database_size(datname) END
		FROM pg_stat_database WHERE datname IS NOT NULL ORDER BY datname LIMIT 1000`)
	if err != nil {
		return out, fmt.Errorf("reading pg_stat_database: %w", err)
	}
	var xacts, hit, read, size float64
	for _, r := range rows {
		db := dbengine.ObjectRef{Kind: "database", Name: dbengine.Str(r[0])}
		if n, ok := dbengine.Num(r[1]); ok {
			out.Counter(db, "db.transactions.rate", n)
			xacts += n
		}
		h, _ := dbengine.Num(r[2])
		rd, _ := dbengine.Num(r[3])
		hit, read = hit+h, read+rd
		if n, ok := dbengine.Num(r[4]); ok {
			out.Gauge(db, "storage.used", n)
			size += n
		}
	}
	out.Counter(inst, "db.transactions.rate", xacts)
	if hit+read > 0 {
		// Cumulative since the last statistics reset (pg_stat_reset).
		out.Gauge(inst, "db.cache.hit_ratio", 100*hit/(hit+read))
	}
	out.Gauge(inst, "storage.used", size)
	rows, err = q.Query(ctx, `SELECT spcname, pg_tablespace_size(oid) FROM pg_tablespace ORDER BY spcname LIMIT 1000`)
	if err != nil {
		return out, fmt.Errorf("reading tablespace sizes: %w", err)
	}
	for _, r := range rows {
		if n, ok := dbengine.Num(r[1]); ok {
			out.Gauge(dbengine.ObjectRef{Kind: "tablespace", Name: dbengine.Str(r[0])}, "storage.used", n)
		}
	}
	return out, nil
}
