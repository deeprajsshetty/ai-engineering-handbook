package oracle

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/deeprajsshetty/observability-platform/platform/internal/store"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

type migration struct {
	version  int
	name     string
	stmts    []string
	checksum string
}

func migrations() ([]migration, error) {
	entries, err := fs.ReadDir(migrationFS, "migrations")
	if err != nil {
		return nil, err
	}
	var out []migration
	for _, e := range entries {
		vs, rest, ok := strings.Cut(e.Name(), "_")
		v, err := strconv.Atoi(vs)
		if !ok || err != nil || !strings.HasSuffix(rest, ".sql") {
			return nil, fmt.Errorf("migration file %q must be named NNNN_name.sql", e.Name())
		}
		b, err := migrationFS.ReadFile("migrations/" + e.Name())
		if err != nil {
			return nil, err
		}
		sum := sha256.Sum256(b)
		out = append(out, migration{version: v, name: strings.TrimSuffix(rest, ".sql"), stmts: splitStatements(string(b)), checksum: hex.EncodeToString(sum[:])})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].version < out[j].version })
	for i := range out {
		if out[i].version != i+1 {
			return nil, fmt.Errorf("migration versions must be contiguous from 1; found %d at position %d", out[i].version, i+1)
		}
	}
	return out, nil
}

// splitStatements splits on lines containing only ";" and drops comments.
func splitStatements(src string) []string {
	var out []string
	var cur strings.Builder
	flush := func() {
		if s := strings.TrimSpace(cur.String()); s != "" {
			out = append(out, s)
		}
		cur.Reset()
	}
	for _, line := range strings.Split(src, "\n") {
		t := strings.TrimSpace(line)
		switch {
		case t == ";":
			flush()
		case strings.HasPrefix(t, "--"):
		default:
			cur.WriteString(line)
			cur.WriteString("\n")
		}
	}
	flush()
	return out
}

// MigrateUp applies pending migrations. Oracle DDL commits implicitly, so a
// migration cannot be rolled back as a unit: each statement is applied in
// order and the migration is recorded only after all of them succeed. A
// failed migration must be fixed forward (see the runbook). Concurrent
// replicas serialize on an exclusive lock of schema_migration_lock, held by
// one session for the whole run while DDL and bookkeeping run on a second
// session (DDL would otherwise commit and release the lock).
func (s *Store) MigrateUp(ctx context.Context) ([]store.MigrationState, error) {
	ms, err := migrations()
	if err != nil {
		return nil, err
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	for _, t := range []struct{ name, ddl string }{
		{"SCHEMA_MIGRATIONS", `CREATE TABLE schema_migrations (version NUMBER(10) PRIMARY KEY, name VARCHAR2(128) NOT NULL,
			checksum VARCHAR2(64) NOT NULL, applied_at TIMESTAMP(6) NOT NULL)`},
		{"SCHEMA_MIGRATION_LOCK", `CREATE TABLE schema_migration_lock (id NUMBER(1) PRIMARY KEY)`},
	} {
		var n int
		if err := conn.QueryRowContext(ctx, `SELECT count(*) FROM user_tables WHERE table_name = :t`, sql.Named("t", t.name)).Scan(&n); err != nil {
			return nil, err
		}
		if n == 0 {
			// ORA-00955: another replica created it concurrently.
			if _, err := conn.ExecContext(ctx, t.ddl); err != nil && !strings.Contains(err.Error(), "ORA-00955") {
				return nil, err
			}
		}
	}
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint:errcheck
	if _, err := tx.ExecContext(ctx, `LOCK TABLE schema_migration_lock IN EXCLUSIVE MODE`); err != nil {
		return nil, fmt.Errorf("acquiring migration lock: %w", err)
	}
	applied := map[int]string{}
	rows, err := tx.QueryContext(ctx, `SELECT version, checksum FROM schema_migrations`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var v int
		var c string
		if err := rows.Scan(&v, &c); err != nil {
			rows.Close()
			return nil, err
		}
		applied[v] = c
	}
	rows.Close()
	var ran []store.MigrationState
	ddl, err := s.db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	defer ddl.Close()
	for _, m := range ms {
		if c, ok := applied[m.version]; ok {
			if c != m.checksum {
				return ran, fmt.Errorf("migration %04d_%s was modified after being applied (checksum mismatch); write a new migration instead", m.version, m.name)
			}
			continue
		}
		for i, st := range m.stmts {
			if _, err := ddl.ExecContext(ctx, st); err != nil {
				return ran, fmt.Errorf("migration %04d_%s statement %d: %w", m.version, m.name, i+1, err)
			}
		}
		if _, err := ddl.ExecContext(ctx, `INSERT INTO schema_migrations (version, name, checksum, applied_at) VALUES (:v, :n, :c, :t)`,
			sql.Named("v", m.version), sql.Named("n", m.name), sql.Named("c", m.checksum), sql.Named("t", ts(time.Now()))); err != nil {
			return ran, err
		}
		ran = append(ran, store.MigrationState{Version: m.version, Name: m.name})
	}
	for v := range applied {
		if v > len(ms) {
			return ran, fmt.Errorf("database has migration %d which this binary does not know; deploy a newer version", v)
		}
	}
	return ran, tx.Commit()
}

// MigrationStatus lists migrations and their state.
func (s *Store) MigrationStatus(ctx context.Context) ([]store.MigrationState, error) {
	ms, err := migrations()
	if err != nil {
		return nil, err
	}
	applied := map[int]time.Time{}
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM user_tables WHERE table_name = 'SCHEMA_MIGRATIONS'`).Scan(&n); err != nil {
		return nil, err
	}
	if n > 0 {
		rows, err := s.db.QueryContext(ctx, `SELECT version, applied_at FROM schema_migrations`)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var v int
			var at time.Time
			if err := rows.Scan(&v, utc{&at}); err != nil {
				rows.Close()
				return nil, err
			}
			applied[v] = at
		}
		rows.Close()
	}
	out := make([]store.MigrationState, 0, len(ms))
	for _, m := range ms {
		st := store.MigrationState{Version: m.version, Name: m.name}
		if at, ok := applied[m.version]; ok {
			at := at
			st.AppliedAt = &at
		}
		out = append(out, st)
	}
	return out, nil
}
