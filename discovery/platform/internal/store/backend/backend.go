// Package backend opens the configured platform datastore (OBS_STORE_DRIVER):
// PostgreSQL (default) or Oracle (ADR 0011). Everything above it depends only
// on store.Backend.
package backend

import (
	"context"
	"fmt"

	"github.com/deeprajsshetty/observability-platform/platform/internal/config"
	"github.com/deeprajsshetty/observability-platform/platform/internal/store"
	"github.com/deeprajsshetty/observability-platform/platform/internal/store/oracle"
	"github.com/deeprajsshetty/observability-platform/platform/internal/store/postgres"
)

// Open connects to the configured store. It does not run migrations.
func Open(ctx context.Context, cfg config.Config) (store.Backend, error) {
	switch cfg.StoreDriver {
	case "", "postgres":
		pool, err := postgres.Open(ctx, cfg.DatabaseURL, cfg.DBMaxConns, "")
		if err != nil {
			return nil, err
		}
		return postgres.New(pool), nil
	case "oracle":
		return oracle.Open(ctx, cfg.DatabaseURL, int(cfg.DBMaxConns))
	default:
		return nil, fmt.Errorf("unsupported OBS_STORE_DRIVER %q", cfg.StoreDriver)
	}
}
