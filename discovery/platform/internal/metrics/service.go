// Package metrics drives topology-aware metric collection: for each
// connection it selects the active resources whose type has metric
// capabilities, asks the connection's collector for samples, and stores them
// idempotently. Collection runs on a schedule, after successful discovery,
// and on demand.
package metrics

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	dm "github.com/deeprajsshetty/observability-platform/platform/internal/domain/metrics"
	"github.com/deeprajsshetty/observability-platform/platform/internal/domain/topology"
	"github.com/deeprajsshetty/observability-platform/platform/internal/ids"
	"github.com/deeprajsshetty/observability-platform/platform/internal/secrets"
	"github.com/deeprajsshetty/observability-platform/platform/internal/store"
	"github.com/deeprajsshetty/observability-platform/platform/internal/telemetry"
)

// Request is the input to a collector.
type Request struct {
	TenantID   string
	Connection store.Connection
	Credential secrets.Value
	Resources  []topology.Resource  // active resources with metric capabilities
	LastSample map[string]time.Time // newest stored sample per resource
	Now        time.Time
	Interval   time.Duration
	Backfill   time.Duration
}

// Collector produces samples for resources of one provider.
type Collector interface {
	Name() string
	Source() dm.Source
	Collect(ctx context.Context, req Request) ([]dm.Sample, error)
}

// Config bounds collection.
type Config struct {
	Interval     time.Duration
	Backfill     time.Duration
	Concurrency  int
	MaxResources int
	RunTimeout   time.Duration
}

// ErrInProgress is returned when a collection for the connection is running.
var ErrInProgress = errors.New("a metrics collection is already running for this connection")

// ErrNoCollector is returned for providers without a collector.
var ErrNoCollector = errors.New("no metrics collector is available for this provider")

// Service schedules and runs collections.
type Service struct {
	Store      store.Store
	Collectors map[string]Collector // keyed by provider name
	Secrets    *secrets.Resolver
	Log        *slog.Logger
	Telemetry  *telemetry.Registry
	Config     Config
	Now        func() time.Time

	once     sync.Once
	sem      chan struct{}
	mu       sync.Mutex
	inFlight map[string]bool
	wg       sync.WaitGroup
}

func (s *Service) init() {
	s.once.Do(func() {
		s.sem = make(chan struct{}, max(s.Config.Concurrency, 1))
		s.inFlight = map[string]bool{}
		if s.Now == nil {
			s.Now = func() time.Time { return time.Now().UTC() }
		}
		if s.Config.RunTimeout == 0 {
			s.Config.RunTimeout = 2 * time.Minute
		}
		if s.Config.MaxResources == 0 {
			s.Config.MaxResources = 10000
		}
	})
}

// CollectorFor returns the collector of a provider.
func (s *Service) CollectorFor(provider string) (Collector, bool) {
	c, ok := s.Collectors[provider]
	return c, ok
}

func (s *Service) acquire(connID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.inFlight[connID] {
		return false
	}
	s.inFlight[connID] = true
	return true
}

func (s *Service) release(connID string) {
	s.mu.Lock()
	delete(s.inFlight, connID)
	s.mu.Unlock()
}

// Trigger starts a collection asynchronously and returns the run record.
func (s *Service) Trigger(ctx context.Context, conn store.Connection, trigger, requestedBy string) (store.CollectionRun, error) {
	s.init()
	col, ok := s.CollectorFor(conn.Provider)
	if !ok {
		return store.CollectionRun{}, ErrNoCollector
	}
	if !s.acquire(conn.ID) {
		return store.CollectionRun{}, ErrInProgress
	}
	run := store.CollectionRun{ID: ids.New("col"), TenantID: conn.TenantID, ConnectionID: conn.ID, Trigger: trigger,
		Status: "running", RequestedBy: requestedBy, Collector: col.Name(), StartedAt: s.Now()}
	if err := s.Store.CreateCollectionRun(ctx, run); err != nil {
		s.release(conn.ID)
		return store.CollectionRun{}, err
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer s.release(conn.ID)
		s.sem <- struct{}{}
		defer func() { <-s.sem }()
		bg, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.Config.RunTimeout)
		defer cancel()
		s.execute(bg, conn, col, run)
	}()
	return run, nil
}

// Wait blocks until background collections finish (graceful shutdown, tests).
func (s *Service) Wait() { s.wg.Wait() }

// CollectSync runs one collection synchronously (scheduler and tests).
func (s *Service) CollectSync(ctx context.Context, conn store.Connection, trigger, requestedBy string) (store.CollectionRun, error) {
	s.init()
	col, ok := s.CollectorFor(conn.Provider)
	if !ok {
		return store.CollectionRun{}, ErrNoCollector
	}
	if !s.acquire(conn.ID) {
		return store.CollectionRun{}, ErrInProgress
	}
	defer s.release(conn.ID)
	run := store.CollectionRun{ID: ids.New("col"), TenantID: conn.TenantID, ConnectionID: conn.ID, Trigger: trigger,
		Status: "running", RequestedBy: requestedBy, Collector: col.Name(), StartedAt: s.Now()}
	if err := s.Store.CreateCollectionRun(ctx, run); err != nil {
		return run, err
	}
	select {
	case s.sem <- struct{}{}:
	case <-ctx.Done():
		return run, ctx.Err()
	}
	defer func() { <-s.sem }()
	rctx, cancel := context.WithTimeout(ctx, s.Config.RunTimeout)
	defer cancel()
	s.execute(rctx, conn, col, run)
	return s.Store.GetCollectionRun(ctx, conn.TenantID, run.ID)
}

func (s *Service) execute(ctx context.Context, conn store.Connection, col Collector, run store.CollectionRun) {
	log := s.Log.With("collection", run.ID, "tenant", conn.TenantID, "connection", conn.ID, "collector", col.Name())
	finish := func(status string, resources, samples int, msg string) {
		fctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		if err := s.Store.FinishCollectionRun(fctx, conn.TenantID, run.ID, status, resources, samples, msg, s.Now()); err != nil {
			log.Error("recording collection result failed", "error", err)
		}
		if s.Telemetry != nil {
			s.Telemetry.Add("obs_metric_collections_total", 1, "status", status, "collector", col.Name())
			s.Telemetry.Add("obs_metric_samples_written_total", int64(samples), "source", string(col.Source()))
		}
		log.Info("metrics collection finished", "status", status, "resources", resources, "samples", samples, "error", msg)
	}
	resources, err := s.Store.ActiveResourcesOfTypes(ctx, conn.TenantID, conn.ID, dm.SupportedTypes(), s.Config.MaxResources)
	if err != nil {
		finish("failed", 0, 0, "listing resources: "+err.Error())
		return
	}
	if len(resources) == 0 {
		finish("succeeded", 0, 0, "")
		return
	}
	cred, err := s.Secrets.Resolve(conn.CredentialRef)
	if err != nil {
		finish("failed", len(resources), 0, "resolving credential: "+err.Error())
		return
	}
	idsList := make([]string, len(resources))
	for i, r := range resources {
		idsList[i] = r.ID
	}
	last, err := s.Store.LatestSampleTimes(ctx, conn.TenantID, idsList)
	if err != nil {
		finish("failed", len(resources), 0, "reading latest samples: "+err.Error())
		return
	}
	samples, err := col.Collect(ctx, Request{TenantID: conn.TenantID, Connection: conn, Credential: cred, Resources: resources,
		LastSample: last, Now: s.Now(), Interval: s.Config.Interval, Backfill: s.Config.Backfill})
	if err != nil {
		finish("failed", len(resources), 0, err.Error())
		return
	}
	n, err := s.Store.InsertSamples(ctx, samples)
	if err != nil {
		finish("failed", len(resources), int(n), "storing samples: "+err.Error())
		return
	}
	finish("succeeded", len(resources), int(n), "")
}

// Loop collects every interval for every connection that has a collector.
func (s *Service) Loop(ctx context.Context) {
	s.init()
	t := time.NewTicker(s.Config.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			s.Wait()
			return
		case <-t.C:
			conns, err := s.Store.AllConnections(ctx)
			if err != nil {
				if ctx.Err() == nil {
					s.Log.Error("listing connections for metrics failed", "error", err)
				}
				continue
			}
			for _, c := range conns {
				if _, ok := s.CollectorFor(c.Provider); !ok {
					continue
				}
				if _, err := s.Trigger(ctx, c, "schedule", "scheduler"); err != nil && !errors.Is(err, ErrInProgress) {
					s.Log.Error("scheduling metrics collection failed", "connection", c.ID, "error", err)
				}
			}
		}
	}
}

// AlignedSteps returns UTC timestamps aligned to interval, from just after
// last (or now-backfill when there is no sample) up to now inclusive.
func AlignedSteps(last time.Time, hasLast bool, now time.Time, interval, backfill time.Duration) []time.Time {
	end := now.UTC().Truncate(interval)
	start := end.Add(-backfill)
	if hasLast {
		next := last.UTC().Truncate(interval).Add(interval)
		if next.After(start) {
			start = next
		}
	}
	var out []time.Time
	for t := start; !t.After(end); t = t.Add(interval) {
		out = append(out, t)
	}
	return out
}

// String helps logging.
func (r Request) String() string {
	return fmt.Sprintf("collect tenant=%s connection=%s resources=%d", r.TenantID, r.Connection.ID, len(r.Resources))
}
