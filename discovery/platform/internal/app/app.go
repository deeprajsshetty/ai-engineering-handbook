// Package app wires configuration, store, providers, collectors, workers and
// the HTTP server into one process. Roles (OBS_ROLES) select whether this
// process serves the API, runs background workers, or both, so the same image
// can be deployed as one Deployment or split into api and worker Deployments.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/deeprajsshetty/observability-platform/platform/internal/api"
	"github.com/deeprajsshetty/observability-platform/platform/internal/auth"
	"github.com/deeprajsshetty/observability-platform/platform/internal/config"
	"github.com/deeprajsshetty/observability-platform/platform/internal/discovery"
	"github.com/deeprajsshetty/observability-platform/platform/internal/metrics"
	"github.com/deeprajsshetty/observability-platform/platform/internal/modules"
	"github.com/deeprajsshetty/observability-platform/platform/internal/netguard"
	"github.com/deeprajsshetty/observability-platform/platform/internal/secrets"
	"github.com/deeprajsshetty/observability-platform/platform/internal/store"
	"github.com/deeprajsshetty/observability-platform/platform/internal/telemetry"
)

// App is a wired process.
type App struct {
	Config    config.Config
	Log       *slog.Logger
	Store     store.Backend
	API       *api.API
	Runner    *discovery.Runner
	Metrics   *metrics.Service
	Telemetry *telemetry.Registry

	shuttingDown atomic.Bool
}

// Modules builds the integrations compiled into this binary.
func Modules(cfg config.Config) (modules.Installed, error) {
	policy := netguard.Policy{Allowlist: cfg.EgressAllowlist, AllowInsecure: cfg.AllowInsecureEndpoints}
	return modules.Install(modules.Default(), modules.Deps{Config: cfg, Egress: policy})
}

// New wires the application against an already-open store.
func New(cfg config.Config, log *slog.Logger, st store.Backend) (*App, error) {
	var authn auth.Authenticator
	switch cfg.AuthMode {
	case "dev":
		a, err := auth.NewDevAuthenticator(cfg.DevTokens)
		if err != nil {
			return nil, err
		}
		authn = a
		log.Warn("DEVELOPMENT AUTH MODE: static bearer tokens are accepted; never use this mode in production")
	case "oidc":
		return nil, auth.OIDCNotImplemented
	}
	tel := telemetry.NewRegistry()
	tel.Help("obs_http_requests_total", "HTTP requests by method, route and status code.")
	tel.Help("obs_discovery_jobs_total", "Finished discovery jobs by status.")
	tel.Help("obs_metric_samples_written_total", "Metric samples newly stored, by source.")
	resolver := secrets.NewResolver(cfg.SecretDir)
	installed, err := Modules(cfg)
	if err != nil {
		return nil, err
	}
	providers := installed.Providers
	ms := &metrics.Service{
		Store: st, Collectors: installed.Collectors, Secrets: resolver, Log: log, Telemetry: tel,
		Config: metrics.Config{Interval: cfg.MetricsInterval, Backfill: cfg.MetricsBackfill, Concurrency: cfg.MetricsConcurrency},
	}
	runner := &discovery.Runner{
		Store: st, Providers: providers, Secrets: resolver, Log: log, Telemetry: tel,
		Config: discovery.RunnerConfig{
			Workers: cfg.DiscoveryWorkers, JobTimeout: cfg.DiscoveryJobTimeout, MaxResources: cfg.DiscoveryMaxResources,
			MaxAttempts: cfg.DiscoveryMaxAttempts, PollInterval: cfg.DiscoveryPollInterval, LeaseTimeout: cfg.JobLeaseTimeout,
			MaxJobEvents: cfg.MaxJobEvents,
		},
	}
	a := &App{Config: cfg, Log: log, Store: st, Runner: runner, Metrics: ms, Telemetry: tel}
	// Discovery drives metrics: a successful job triggers collection for the
	// connection's topology. Only workers collect; API-only replicas do not.
	runner.OnSucceeded = func(tenantID, connectionID string) {
		conn, err := st.GetConnection(context.Background(), tenantID, connectionID)
		if err != nil {
			return
		}
		if _, err := ms.Trigger(context.Background(), conn, "discovery", "discovery"); err != nil && !errors.Is(err, metrics.ErrInProgress) && !errors.Is(err, metrics.ErrNoCollector) {
			log.Warn("triggering post-discovery collection failed", "connection", connectionID, "error", err)
		}
	}
	a.API = &api.API{
		Store: st, Providers: providers, Jobs: runner, Metrics: ms, Auth: authn, Secrets: resolver,
		Config: cfg, Log: log, Telemetry: tel, Ready: a.ready, Modules: installed.Modules,
	}
	return a, nil
}

func (a *App) ready(ctx context.Context) error {
	if a.shuttingDown.Load() {
		return errors.New("shutting down")
	}
	if err := a.Store.Ping(ctx); err != nil {
		return errors.New("database unreachable")
	}
	n, err := store.PendingMigrations(ctx, a.Store)
	if err != nil {
		return errors.New("cannot read migration state")
	}
	if n > 0 {
		return fmt.Errorf("%d database migrations pending; run `obsplatform migrate up`", n)
	}
	return nil
}

// Run serves until ctx is cancelled, then shuts down gracefully: readiness
// flips to 503, the server drains in-flight requests, workers stop claiming
// and finish or release their current job.
func (a *App) Run(ctx context.Context) error {
	workerCtx, stopWorkers := context.WithCancel(context.WithoutCancel(ctx))
	defer stopWorkers()
	var wg sync.WaitGroup
	if a.Config.Roles["worker"] {
		wg.Add(3)
		go func() { defer wg.Done(); a.Runner.Run(workerCtx) }()
		go func() { defer wg.Done(); a.Metrics.Loop(workerCtx) }()
		go func() { defer wg.Done(); a.retentionLoop(workerCtx) }()
		a.Log.Info("background workers started", "discoveryWorkers", a.Config.DiscoveryWorkers)
	}
	var srv *http.Server
	errCh := make(chan error, 1)
	if a.Config.Roles["api"] {
		srv = &http.Server{
			Addr: a.Config.HTTPAddr, Handler: a.API.Handler(),
			ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 60 * time.Second,
			IdleTimeout: 120 * time.Second, MaxHeaderBytes: 64 << 10,
		}
		go func() {
			a.Log.Info("http server listening", "addr", a.Config.HTTPAddr)
			if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				errCh <- err
			}
		}()
	}
	var runErr error
	select {
	case <-ctx.Done():
	case runErr = <-errCh:
	}
	a.shuttingDown.Store(true)
	a.Log.Info("shutting down")
	sctx, cancel := context.WithTimeout(context.Background(), a.Config.ShutdownTimeout)
	defer cancel()
	if srv != nil {
		if err := srv.Shutdown(sctx); err != nil {
			a.Log.Error("http shutdown incomplete", "error", err)
		}
	}
	stopWorkers()
	done := make(chan struct{})
	go func() { wg.Wait(); a.Metrics.Wait(); close(done) }()
	select {
	case <-done:
	case <-sctx.Done():
		a.Log.Warn("workers did not stop before the shutdown timeout; leases will expire and jobs will be requeued")
	}
	return runErr
}

// retentionLoop enforces data retention.
func (a *App) retentionLoop(ctx context.Context) {
	run := func() {
		now := time.Now().UTC()
		c := a.Config
		var st store.Retention = a.Store
		res := map[string]int64{}
		var errs []error
		do := func(name string, f func() (int64, error)) {
			n, err := f()
			res[name] = n
			if err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", name, err))
			}
		}
		do("metricSamples", func() (int64, error) { return st.PurgeMetricSamples(ctx, now.Add(-c.MetricRetention), 10000) })
		do("missingResources", func() (int64, error) { return st.PurgeMissingResources(ctx, now.Add(-c.TopologyPurgeAfter)) })
		do("jobs", func() (int64, error) { return st.PurgeJobs(ctx, now.Add(-c.JobRetention)) })
		do("changes", func() (int64, error) { return st.PurgeChanges(ctx, now.Add(-c.JobRetention)) })
		do("collectionRuns", func() (int64, error) { return st.PurgeCollectionRuns(ctx, now.Add(-c.JobRetention)) })
		do("audit", func() (int64, error) { return st.PurgeAudit(ctx, now.Add(-c.AuditRetention)) })
		if len(errs) > 0 && ctx.Err() == nil {
			a.Log.Error("retention pass failed", "error", errors.Join(errs...))
		}
		a.Log.Info("retention pass", "deleted", res)
	}
	t := time.NewTicker(a.Config.RetentionInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			run()
		}
	}
}
