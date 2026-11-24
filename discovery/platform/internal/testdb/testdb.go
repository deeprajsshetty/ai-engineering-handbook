// Package testdb provides isolated PostgreSQL databases for integration tests.
//
// If OBS_TEST_DATABASE_URL is set (CI uses a PostgreSQL service container), it
// is used. Otherwise, if initdb and pg_ctl are on PATH, a throwaway server is
// started in a temporary directory on a free loopback port and removed after
// the test binary finishes. If neither is available, tests are skipped with an
// explanation, never silently passed.
//
// Every call to New creates a fresh schema and applies all migrations, so
// tests are isolated from each other and from any developer data.
package testdb

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/deeprajsshetty/observability-platform/platform/internal/ids"
	"github.com/deeprajsshetty/observability-platform/platform/internal/store/postgres"
)

var (
	baseURL   string
	skipWhy   string
	schemaSeq atomic.Int64
)

// Main wraps testing.M: starts a server if needed, runs tests, stops it.
func Main(m *testing.M) {
	stop := setup()
	code := m.Run()
	stop()
	os.Exit(code)
}

func setup() func() {
	if u := os.Getenv("OBS_TEST_DATABASE_URL"); u != "" {
		baseURL = u
		return func() {}
	}
	initdb, err1 := exec.LookPath("initdb")
	pgctl, err2 := exec.LookPath("pg_ctl")
	if err1 != nil || err2 != nil {
		skipWhy = "PostgreSQL not available: set OBS_TEST_DATABASE_URL or put initdb and pg_ctl on PATH"
		return func() {}
	}
	dir, err := os.MkdirTemp("", "obs-testpg-")
	if err != nil {
		skipWhy = "cannot create temp dir: " + err.Error()
		return func() {}
	}
	data := filepath.Join(dir, "data")
	// macOS postmaster refuses to start without a valid locale in the environment.
	env := append(os.Environ(), "LC_ALL=C", "LANG=C")
	initCmd := exec.Command(initdb, "-D", data, "-U", "obs", "--auth=trust", "-E", "UTF8", "--no-locale")
	initCmd.Env = env
	if out, err := initCmd.CombinedOutput(); err != nil {
		skipWhy = "initdb failed: " + lastLine(out)
		_ = os.RemoveAll(dir)
		return func() {}
	}
	port := freePort()
	opts := fmt.Sprintf("-p %d -c listen_addresses=127.0.0.1 -c unix_socket_directories='' -c fsync=off -c full_page_writes=off", port)
	startCmd := exec.Command(pgctl, "-D", data, "-o", opts, "-l", filepath.Join(dir, "log"), "-w", "-t", "30", "start")
	startCmd.Env = env
	if out, err := startCmd.CombinedOutput(); err != nil {
		logTail, _ := os.ReadFile(filepath.Join(dir, "log"))
		skipWhy = "pg_ctl start failed: " + lastLine(out) + " / " + lastLine(logTail)
		_ = os.RemoveAll(dir)
		return func() {}
	}
	baseURL = fmt.Sprintf("postgres://obs@127.0.0.1:%d/postgres?sslmode=disable", port)
	return func() {
		_ = exec.Command(pgctl, "-D", data, "-m", "immediate", "-w", "stop").Run()
		_ = os.RemoveAll(dir)
	}
}

func freePort() int {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 55432
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func lastLine(b []byte) string {
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	return lines[len(lines)-1]
}

// URL returns the base database URL, skipping the test if none is available.
func URL(t testing.TB) string {
	t.Helper()
	if baseURL == "" {
		if os.Getenv("OBS_TEST_REQUIRE_DB") == "1" {
			t.Fatal("OBS_TEST_REQUIRE_DB=1 but no database: " + skipWhy)
		}
		t.Skip(skipWhy)
	}
	return baseURL
}

// NewSchema creates an empty schema and returns a pool bound to it via
// search_path. The schema is dropped when the test ends.
func NewSchema(t testing.TB) *pgxpool.Pool {
	t.Helper()
	u := URL(t)
	ctx := context.Background()
	admin, err := postgres.Open(ctx, u, 2, "")
	if err != nil {
		t.Fatalf("connecting to test database: %v", err)
	}
	schema := fmt.Sprintf("t_%d_%s", schemaSeq.Add(1), strings.TrimPrefix(ids.New("s"), "s_")[16:])
	if _, err := admin.Exec(ctx, `CREATE SCHEMA `+schema); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	pool, err := postgres.Open(ctx, u, 8, schema)
	if err != nil {
		t.Fatalf("connecting to test schema: %v", err)
	}
	t.Cleanup(func() {
		pool.Close()
		c, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = admin.Exec(c, `DROP SCHEMA `+schema+` CASCADE`)
		admin.Close()
	})
	return pool
}

// New returns a migrated store in a fresh schema.
func New(t testing.TB) *postgres.Store {
	t.Helper()
	pool := NewSchema(t)
	if _, err := postgres.Migrate(context.Background(), pool, 0); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return postgres.New(pool)
}
