// Package oracle integrates Oracle Database (12c and later, CDB and non-CDB)
// through go-ora, a pure-Go implementation of the Oracle wire protocol (MIT
// licence, no Oracle Instant Client needed, so the image stays static).
//
// Verification status: unit-tested against canned results, and run against
// a live Oracle Database Free 23ai container database (TestLiveCDB) as a
// least-privilege common user. Not yet run against 19c or a non-CDB.
package oracle

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"strings"

	goora "github.com/sijms/go-ora/v2"

	"github.com/deeprajsshetty/observability-platform/platform/internal/dbengine"
)

// Name is the engine name.
const Name = "oracle"

// Engine implements dbengine.Engine.
type Engine struct{}

// Name returns "oracle".
func (Engine) Name() string { return Name }

// DisplayName returns the product name.
func (Engine) DisplayName() string { return "Oracle Database" }

// DefaultPort returns 1521.
func (Engine) DefaultPort() int { return 1521 }

// MinimumPrivileges documents the grants.
func (Engine) MinimumPrivileges() string {
	return "CREATE SESSION plus SELECT_CATALOG_ROLE (or SELECT on V_$DATABASE, V_$INSTANCE, V_$PDBS, V_$SESSION, V_$SYSMETRIC, " +
		"CDB_/DBA_TABLESPACES and CDB_/DBA_TABLESPACE_USAGE_METRICS). For a CDB, create a common user (C##...), grant CONTAINER=ALL, " +
		"run ALTER USER <user> SET CONTAINER_DATA=ALL CONTAINER=CURRENT (otherwise only the root container is visible), and connect to the root service; " +
		"connecting to a PDB service shows that PDB only."
}

type dialer struct{ f dbengine.DialFunc }

func (d dialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	return d.f(ctx, network, address)
}

// Open builds a lazily connecting pool through the guarded dialer.
func (Engine) Open(t dbengine.Target, password string, dial dbengine.DialFunc) (*sql.DB, error) {
	port := t.Port
	if port == 0 {
		port = 1521
	}
	opts := map[string]string{"CONNECTION TIMEOUT": "10", "TIMEOUT": "15"}
	switch t.TLS {
	case dbengine.TLSVerifyFull:
		opts["SSL"] = "true"
		opts["SSL VERIFY"] = "true"
	case dbengine.TLSRequire:
		opts["SSL"] = "true"
		opts["SSL VERIFY"] = "false"
	case dbengine.TLSDisable:
	default:
		return nil, fmt.Errorf("unsupported tls mode %q", t.TLS)
	}
	c, ok := goora.NewConnector(goora.BuildUrl(t.Host, port, t.Database, t.Username, password, opts)).(*goora.OracleConnector)
	if !ok {
		return nil, fmt.Errorf("unexpected go-ora connector type")
	}
	c.Dialer(dialer{dial})
	db := sql.OpenDB(c)
	db.SetMaxOpenConns(2)
	return db, nil
}

const tablespaceCDB = `SELECT c.name, m.tablespace_name, m.used_space * t.block_size, m.tablespace_size * t.block_size, m.used_percent, t.contents
	FROM cdb_tablespace_usage_metrics m
	JOIN cdb_tablespaces t ON t.tablespace_name = m.tablespace_name AND t.con_id = m.con_id
	JOIN v$containers c ON c.con_id = m.con_id
	WHERE c.name <> 'PDB$SEED'
	ORDER BY c.name, m.tablespace_name FETCH FIRST 2000 ROWS ONLY`

const tablespaceNonCDB = `SELECT (SELECT name FROM v$database), m.tablespace_name, m.used_space * t.block_size, m.tablespace_size * t.block_size, m.used_percent, t.contents
	FROM dba_tablespace_usage_metrics m
	JOIN dba_tablespaces t ON t.tablespace_name = m.tablespace_name
	ORDER BY m.tablespace_name FETCH FIRST 2000 ROWS ONLY`

type tsRow struct {
	db, name, contents  string
	used, size, percent float64
}

func tablespaces(ctx context.Context, q dbengine.Querier, cdb bool) ([]tsRow, error) {
	query := tablespaceNonCDB
	if cdb {
		query = tablespaceCDB
	}
	rows, err := q.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("reading tablespace usage: %w", err)
	}
	out := make([]tsRow, 0, len(rows))
	for _, r := range rows {
		used, _ := dbengine.Num(r[2])
		size, _ := dbengine.Num(r[3])
		pct, _ := dbengine.Num(r[4])
		out = append(out, tsRow{db: dbengine.Str(r[0]), name: dbengine.Str(r[1]), used: used, size: size, percent: pct, contents: dbengine.Str(r[5])})
	}
	return out, nil
}

func isCDB(ctx context.Context, q dbengine.Querier) (bool, error) {
	r, err := dbengine.One(ctx, q, `SELECT cdb FROM v$database`)
	if err != nil {
		return false, fmt.Errorf("reading v$database: %w", err)
	}
	return strings.EqualFold(dbengine.Str(r[0]), "YES"), nil
}

// Inventory reads the instance, PDBs (or the non-CDB database) and tablespaces.
func (Engine) Inventory(ctx context.Context, q dbengine.Querier) (dbengine.Inventory, error) {
	inv := dbengine.Inventory{Engine: Name}
	r, err := dbengine.One(ctx, q, `SELECT d.dbid, d.name, d.cdb, d.database_role, d.log_mode, i.instance_name, i.version, i.host_name, i.status
		FROM v$database d CROSS JOIN v$instance i`)
	if err != nil {
		return inv, fmt.Errorf("reading v$database/v$instance: %w", err)
	}
	cdb := strings.EqualFold(dbengine.Str(r[2]), "YES")
	inv.Instance = dbengine.Instance{
		ID: dbengine.Str(r[0]) + ":" + strings.ToLower(dbengine.Str(r[5])), Name: dbengine.Str(r[5]), Version: dbengine.Str(r[6]),
		Properties: map[string]any{"dbName": dbengine.Str(r[1]), "cdb": cdb, "databaseRole": dbengine.Str(r[3]),
			"logMode": dbengine.Str(r[4]), "hostName": dbengine.Str(r[7]), "instanceStatus": dbengine.Str(r[8])},
	}
	if cdb {
		rows, err := q.Query(ctx, `SELECT name, open_mode, con_id FROM v$pdbs WHERE name <> 'PDB$SEED' ORDER BY name FETCH FIRST 1000 ROWS ONLY`)
		if err != nil {
			return inv, fmt.Errorf("listing PDBs: %w", err)
		}
		for _, p := range rows {
			inv.Databases = append(inv.Databases, dbengine.Database{Name: dbengine.Str(p[0]),
				Properties: map[string]any{"openMode": dbengine.Str(p[1]), "conId": dbengine.Str(p[2]), "kind": "pdb"}})
		}
	} else {
		inv.Databases = append(inv.Databases, dbengine.Database{Name: dbengine.Str(r[1]), Properties: map[string]any{"kind": "non-cdb"}})
	}
	ts, err := tablespaces(ctx, q, cdb)
	if err != nil {
		return inv, err
	}
	for _, t := range ts {
		db := t.db
		if strings.EqualFold(db, "CDB$ROOT") {
			db = "" // root tablespaces belong to the instance
		}
		inv.Tablespaces = append(inv.Tablespaces, dbengine.Tablespace{Name: t.name, Database: db,
			Properties: map[string]any{"contents": t.contents, "sizeBytes": t.size}})
	}
	return inv, nil
}

var sysmetrics = map[string]string{
	"Buffer Cache Hit Ratio":   "db.cache.hit_ratio",
	"User Transaction Per Sec": "db.transactions.rate",
	"Host CPU Utilization (%)": "cpu.utilization",
}

// Sample reads session counts, 60-second system metrics and tablespace usage.
func (Engine) Sample(ctx context.Context, q dbengine.Querier) (dbengine.Readings, error) {
	out := dbengine.NewReadings()
	inst := dbengine.ObjectRef{Kind: "instance"}
	r, err := dbengine.One(ctx, q, `SELECT count(*), sum(CASE WHEN status = 'ACTIVE' THEN 1 ELSE 0 END) FROM v$session WHERE type = 'USER'`)
	if err != nil {
		return out, fmt.Errorf("reading v$session: %w", err)
	}
	if n, ok := dbengine.Num(r[0]); ok {
		out.Gauge(inst, "db.connections", n)
	}
	if n, ok := dbengine.Num(r[1]); ok {
		out.Gauge(inst, "db.sessions.active", n)
	}
	// group_id 2 is the long-duration (60 s) system metric group. These are
	// already rates and ratios computed by the server, so they are gauges.
	rows, err := q.Query(ctx, `SELECT metric_name, value FROM v$sysmetric WHERE group_id = 2
		AND metric_name IN ('Buffer Cache Hit Ratio', 'User Transaction Per Sec', 'Host CPU Utilization (%)')`)
	if err != nil {
		return out, fmt.Errorf("reading v$sysmetric: %w", err)
	}
	for _, m := range rows {
		if name, ok := sysmetrics[dbengine.Str(m[0])]; ok {
			if v, ok := dbengine.Num(m[1]); ok {
				out.Gauge(inst, name, v)
			}
		}
	}
	cdb, err := isCDB(ctx, q)
	if err != nil {
		return out, err
	}
	ts, err := tablespaces(ctx, q, cdb)
	if err != nil {
		return out, err
	}
	perDB := map[string]float64{}
	var total float64
	for _, t := range ts {
		db := t.db
		if strings.EqualFold(db, "CDB$ROOT") {
			db = ""
		}
		ref := dbengine.ObjectRef{Kind: "tablespace", Database: db, Name: t.name}
		out.Gauge(ref, "storage.used", t.used)
		out.Gauge(ref, "db.storage.utilization", t.percent)
		total += t.used
		if db != "" {
			perDB[db] += t.used
		}
	}
	for db, v := range perDB {
		out.Gauge(dbengine.ObjectRef{Kind: "database", Name: db}, "storage.used", v)
	}
	out.Gauge(inst, "storage.used", total)
	return out, nil
}
