package postgres

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/deeprajsshetty/observability-platform/platform/internal/store"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// Migration is one forward-only schema step.
type Migration struct {
	Version  int
	Name     string
	SQL      string
	Checksum string
}

// Migrations returns the embedded migrations in version order.
func Migrations() ([]Migration, error) {
	entries, err := fs.ReadDir(migrationFS, "migrations")
	if err != nil {
		return nil, err
	}
	var out []Migration
	for _, e := range entries {
		name := e.Name()
		vs, rest, ok := strings.Cut(name, "_")
		v, err := strconv.Atoi(vs)
		if !ok || err != nil || !strings.HasSuffix(rest, ".sql") {
			return nil, fmt.Errorf("migration file %q must be named NNNN_name.sql", name)
		}
		b, err := migrationFS.ReadFile("migrations/" + name)
		if err != nil {
			return nil, err
		}
		sum := sha256.Sum256(b)
		out = append(out, Migration{Version: v, Name: strings.TrimSuffix(rest, ".sql"), SQL: string(b), Checksum: hex.EncodeToString(sum[:])})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	for i := range out {
		if out[i].Version != i+1 {
			return nil, fmt.Errorf("migration versions must be contiguous from 1; found %d at position %d", out[i].Version, i+1)
		}
	}
	return out, nil
}

// MigrationStatus is one row of `obsplatform migrate status`.
type MigrationStatus struct {
	Version   int
	Name      string
	AppliedAt *time.Time
}

const migrationLockID = 727_040_001 // arbitrary, stable advisory lock key

// Migrate applies pending migrations up to target (0 = latest). It holds a
// session advisory lock so concurrent replicas serialize, runs each migration
// in its own transaction, and refuses to continue if an applied migration's
// checksum differs from the embedded file (edited history).
func Migrate(ctx context.Context, pool *pgxpool.Pool, target int) ([]Migration, error) {
	ms, err := Migrations()
	if err != nil {
		return nil, err
	}
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, migrationLockID); err != nil {
		return nil, fmt.Errorf("acquiring migration lock: %w", err)
	}
	defer conn.Exec(context.WithoutCancel(ctx), `SELECT pg_advisory_unlock($1)`, migrationLockID) //nolint:errcheck

	if _, err := conn.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version integer PRIMARY KEY, name text NOT NULL, checksum text NOT NULL, applied_at timestamptz NOT NULL)`); err != nil {
		return nil, err
	}
	applied := map[int]string{}
	rows, err := conn.Query(ctx, `SELECT version, checksum FROM schema_migrations`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var v int
		var c string
		if err := rows.Scan(&v, &c); err != nil {
			return nil, err
		}
		applied[v] = c
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var ran []Migration
	for _, m := range ms {
		if target > 0 && m.Version > target {
			break
		}
		if c, ok := applied[m.Version]; ok {
			if c != m.Checksum {
				return ran, fmt.Errorf("migration %04d_%s was modified after being applied (checksum mismatch); write a new migration instead", m.Version, m.Name)
			}
			continue
		}
		err := pgx.BeginFunc(ctx, conn, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, m.SQL); err != nil {
				return fmt.Errorf("migration %04d_%s: %w", m.Version, m.Name, err)
			}
			_, err := tx.Exec(ctx, `INSERT INTO schema_migrations (version, name, checksum, applied_at) VALUES ($1, $2, $3, now())`, m.Version, m.Name, m.Checksum)
			return err
		})
		if err != nil {
			return ran, err
		}
		ran = append(ran, m)
	}
	for v := range applied {
		if v > len(ms) {
			return ran, fmt.Errorf("database has migration %d which this binary does not know; deploy a newer version", v)
		}
	}
	return ran, nil
}

// Status reports applied and pending migrations.
func Status(ctx context.Context, pool *pgxpool.Pool) ([]MigrationStatus, error) {
	ms, err := Migrations()
	if err != nil {
		return nil, err
	}
	applied := map[int]time.Time{}
	var exists bool
	if err := pool.QueryRow(ctx, `SELECT to_regclass('schema_migrations') IS NOT NULL`).Scan(&exists); err != nil {
		return nil, err
	}
	if exists {
		rows, err := pool.Query(ctx, `SELECT version, applied_at FROM schema_migrations`)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var v int
			var at time.Time
			if err := rows.Scan(&v, &at); err != nil {
				return nil, err
			}
			applied[v] = at
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	out := make([]MigrationStatus, 0, len(ms))
	for _, m := range ms {
		s := MigrationStatus{Version: m.Version, Name: m.Name}
		if at, ok := applied[m.Version]; ok {
			at := at
			s.AppliedAt = &at
		}
		out = append(out, s)
	}
	return out, nil
}

// Pending reports whether migrations remain to be applied (used by /readyz).
func Pending(ctx context.Context, pool *pgxpool.Pool) (int, error) {
	st, err := Status(ctx, pool)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, s := range st {
		if s.AppliedAt == nil {
			n++
		}
	}
	return n, nil
}

// Driver implements store.Backend.
func (s *Store) Driver() string { return "postgres" }

// MigrateUp implements store.Backend.
func (s *Store) MigrateUp(ctx context.Context) ([]store.MigrationState, error) {
	ran, err := Migrate(ctx, s.pool, 0)
	out := make([]store.MigrationState, len(ran))
	for i, m := range ran {
		out[i] = store.MigrationState{Version: m.Version, Name: m.Name}
	}
	return out, err
}

// MigrationStatus implements store.Backend.
func (s *Store) MigrationStatus(ctx context.Context) ([]store.MigrationState, error) {
	st, err := Status(ctx, s.pool)
	out := make([]store.MigrationState, len(st))
	for i, m := range st {
		out[i] = store.MigrationState{Version: m.Version, Name: m.Name, AppliedAt: m.AppliedAt}
	}
	return out, err
}

var _ store.Backend = (*Store)(nil)
