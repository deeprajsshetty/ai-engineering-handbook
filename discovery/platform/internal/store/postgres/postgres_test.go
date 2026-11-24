package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/deeprajsshetty/observability-platform/platform/internal/ids"
	"github.com/deeprajsshetty/observability-platform/platform/internal/store"
	"github.com/deeprajsshetty/observability-platform/platform/internal/store/postgres"
	"github.com/deeprajsshetty/observability-platform/platform/internal/store/storetest"
	"github.com/deeprajsshetty/observability-platform/platform/internal/testdb"
)

func TestMain(m *testing.M) { testdb.Main(m) }

// TestContract runs the shared store contract against PostgreSQL.
func TestContract(t *testing.T) {
	storetest.Run(t, func(t *testing.T) store.Store { return testdb.New(t) })
}

var ctx = context.Background()

func TestMigrationsFromEmptyAndUpgradeFromBaseline(t *testing.T) {
	pool := testdb.NewSchema(t)
	// Baseline: the first released schema only.
	ran, err := postgres.Migrate(ctx, pool, 1)
	if err != nil || len(ran) != 1 {
		t.Fatalf("migrate to 1: ran=%d err=%v", len(ran), err)
	}
	st := postgres.New(pool)
	c := mkConn(t, st, "demo", "baseline")
	// Upgrade to latest with existing data.
	ran, err = postgres.Migrate(ctx, pool, 0)
	if err != nil {
		t.Fatal(err)
	}
	all, _ := postgres.Migrations()
	if len(ran) != len(all)-1 {
		t.Fatalf("upgrade ran %d, want %d", len(ran), len(all)-1)
	}
	if _, err := st.GetConnection(ctx, "demo", c.ID); err != nil {
		t.Fatalf("data lost across upgrade: %v", err)
	}
	// Re-running is a no-op.
	if ran, err := postgres.Migrate(ctx, pool, 0); err != nil || len(ran) != 0 {
		t.Fatalf("re-run: ran=%d err=%v", len(ran), err)
	}
	pending, err := postgres.Pending(ctx, pool)
	if err != nil || pending != 0 {
		t.Fatalf("pending=%d err=%v", pending, err)
	}
	// Tampered history is detected.
	if _, err := pool.Exec(ctx, `UPDATE schema_migrations SET checksum='x' WHERE version=1`); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.Migrate(ctx, pool, 0); err == nil {
		t.Fatal("checksum mismatch not detected")
	}
}

func mkConn(t *testing.T, st store.Store, tenant, name string) store.Connection {
	t.Helper()
	c, err := st.CreateConnection(ctx, store.Connection{
		ID: ids.New("con"), TenantID: tenant, Name: name, Provider: "fixture",
		Config: map[string]any{"scenario": "retail-demo"}, CreatedBy: "test", CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return c
}
