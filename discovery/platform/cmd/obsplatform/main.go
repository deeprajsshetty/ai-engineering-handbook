// Command obsplatform is the platform binary.
//
//	obsplatform serve            run API and/or workers (OBS_ROLES)
//	obsplatform migrate up       apply pending database migrations
//	obsplatform migrate status   list applied and pending migrations
//	obsplatform seed             create the demo connection (idempotent)
//	obsplatform version          print version
package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/deeprajsshetty/observability-platform/platform/internal/api"
	"github.com/deeprajsshetty/observability-platform/platform/internal/app"
	"github.com/deeprajsshetty/observability-platform/platform/internal/config"
	"github.com/deeprajsshetty/observability-platform/platform/internal/ids"
	"github.com/deeprajsshetty/observability-platform/platform/internal/store"
	"github.com/deeprajsshetty/observability-platform/platform/internal/store/backend"
	"github.com/deeprajsshetty/observability-platform/platform/internal/telemetry"
)

const usage = `usage: obsplatform <command>

commands:
  serve            run the API and/or background workers (see OBS_ROLES)
  migrate up       apply pending database migrations
  migrate status   show applied and pending migrations
  seed             create the demo connection for tenant "demo" (idempotent)
  version          print the version

configuration is read from OBS_* environment variables; see docs/operations/configuration.md`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
	if os.Args[1] == "version" {
		fmt.Printf("obsplatform %s (%s)\n", api.Version, api.Commit)
		return
	}
	if err := run(os.Args[1], os.Args[2:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(cmd string, args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("invalid configuration:\n%w", err)
	}
	log := telemetry.NewLogger(cfg.LogLevel, cfg.LogFormat)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	st, err := backend.Open(ctx, cfg)
	if err != nil {
		return err
	}
	defer st.Close()

	switch cmd {
	case "serve":
		log.Info("starting", "version", api.Version, "commit", api.Commit, "config", cfg.Redacted())
		a, err := app.New(cfg, log, st)
		if err != nil {
			return err
		}
		return a.Run(ctx)
	case "migrate":
		sub := "up"
		if len(args) > 0 {
			sub = args[0]
		}
		switch sub {
		case "up":
			ran, err := st.MigrateUp(ctx)
			for _, m := range ran {
				fmt.Printf("applied %04d_%s (%s)\n", m.Version, m.Name, st.Driver())
			}
			if err == nil && len(ran) == 0 {
				fmt.Println("database is up to date")
			}
			return err
		case "status":
			sts, err := st.MigrationStatus(ctx)
			if err != nil {
				return err
			}
			for _, s := range sts {
				state := "pending"
				if s.AppliedAt != nil {
					state = "applied " + s.AppliedAt.UTC().Format(time.RFC3339)
				}
				fmt.Printf("%04d_%-20s %s\n", s.Version, s.Name, state)
			}
			return nil
		default:
			return errors.New("migrate takes up or status")
		}
	case "seed":
		return seed(ctx, st)
	default:
		return fmt.Errorf("unknown command %q\n\n%s", cmd, usage)
	}
}

// DemoConnectionName is the seeded connection's name.
const DemoConnectionName = "Demo estate (simulated)"

// DemoDatabaseConnectionName is the optional measured database connection.
const DemoDatabaseConnectionName = "Local PostgreSQL (measured)"

// seed creates the simulated demo connection and, when
// OBS_SEED_DATABASE_TARGET=host:port/database is set (scripts/demo.sh sets it
// to the local development server), a real database connection that is
// discovered and measured through the generic database integration.
func seed(ctx context.Context, st store.Store) error {
	if err := seedOne(ctx, st, store.Connection{Name: DemoConnectionName, Provider: "fixture",
		Config: map[string]any{"scenario": "retail-demo", "account": "sim-acct-0001", "pageSize": float64(10), "pageDelayMs": float64(150)}}); err != nil {
		return err
	}
	target := os.Getenv("OBS_SEED_DATABASE_TARGET")
	if target == "" {
		return nil
	}
	hostPort, dbname, ok := strings.Cut(target, "/")
	host, portS, err := net.SplitHostPort(hostPort)
	port, perr := strconv.Atoi(portS)
	if !ok || err != nil || perr != nil {
		return errors.New("OBS_SEED_DATABASE_TARGET must be host:port/database")
	}
	return seedOne(ctx, st, store.Connection{Name: DemoDatabaseConnectionName, Provider: "database",
		CredentialRef: env("OBS_SEED_DATABASE_CREDENTIAL_REF", "file:demo-db-password"),
		Config: map[string]any{"engine": "postgresql", "host": host, "port": float64(port), "database": dbname,
			"username": env("OBS_SEED_DATABASE_USER", "obs_monitor"), "tls": "disable", "location": "local-dev", "environment": "dev"}})
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func seedOne(ctx context.Context, st store.Store, c store.Connection) error {
	c.ID, c.TenantID, c.CreatedBy, c.CreatedAt = ids.New("con"), "demo", "seed", time.Now().UTC()
	created, err := st.CreateConnection(ctx, c)
	if errors.Is(err, store.ErrConflict) {
		fmt.Printf("connection %q already exists\n", c.Name)
		return nil
	}
	if err != nil {
		return err
	}
	fmt.Printf("created connection %q (%s) for tenant demo\n", created.Name, created.ID)
	return nil
}
