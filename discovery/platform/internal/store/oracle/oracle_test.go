package oracle_test

import (
	"context"
	"strings"
	"testing"

	"github.com/deeprajsshetty/observability-platform/platform/internal/store"
	"github.com/deeprajsshetty/observability-platform/platform/internal/store/storetest"
	"github.com/deeprajsshetty/observability-platform/platform/internal/testdb/oratest"
)

// TestContract runs the shared store contract against Oracle.
func TestContract(t *testing.T) {
	storetest.Run(t, func(t *testing.T) store.Store { return oratest.New(t) })
}

func TestMigrations(t *testing.T) {
	ctx := context.Background()
	st := oratest.NewSchema(t)
	before, err := st.MigrationStatus(ctx)
	if err != nil || len(before) < 2 || before[0].AppliedAt != nil {
		t.Fatalf("status before: %v %v", before, err)
	}
	ran, err := st.MigrateUp(ctx)
	if err != nil || len(ran) != len(before) {
		t.Fatalf("migrate: ran=%d err=%v", len(ran), err)
	}
	if n, err := store.PendingMigrations(ctx, st); err != nil || n != 0 {
		t.Fatalf("pending=%d err=%v", n, err)
	}
	if ran, err := st.MigrateUp(ctx); err != nil || len(ran) != 0 {
		t.Fatalf("re-run must be a no-op: ran=%d err=%v", len(ran), err)
	}
	if _, err := st.DB().ExecContext(ctx, `UPDATE schema_migrations SET checksum='x' WHERE version=1`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.MigrateUp(ctx); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("tampered history not detected: %v", err)
	}
}
