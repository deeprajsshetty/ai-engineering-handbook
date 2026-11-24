package discovery

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/deeprajsshetty/observability-platform/platform/internal/domain/topology"
	"github.com/deeprajsshetty/observability-platform/platform/internal/ids"
	"github.com/deeprajsshetty/observability-platform/platform/internal/secrets"
	"github.com/deeprajsshetty/observability-platform/platform/internal/store"
	"github.com/deeprajsshetty/observability-platform/platform/internal/telemetry"
)

// RunnerConfig bounds worker behaviour.
type RunnerConfig struct {
	Workers       int
	JobTimeout    time.Duration
	MaxResources  int
	MaxAttempts   int
	PollInterval  time.Duration
	LeaseTimeout  time.Duration
	MaxJobEvents  int
	SchedulerTick time.Duration
}

// Runner executes discovery jobs from the database-backed queue. Any number
// of runners (replicas) may run concurrently: claiming uses SKIP LOCKED and a
// lease, so each job runs on exactly one worker at a time.
type Runner struct {
	Store     store.Store
	Providers *Registry
	Secrets   *secrets.Resolver
	Log       *slog.Logger
	Telemetry *telemetry.Registry
	Config    RunnerConfig
	// OnSucceeded is called after a successful job (used to trigger metrics collection).
	OnSucceeded func(tenantID, connectionID string)
	Now         func() time.Time

	workerID string
	wake     chan struct{}
	once     sync.Once
}

func (r *Runner) init() {
	r.once.Do(func() {
		host, _ := os.Hostname()
		r.workerID = fmt.Sprintf("%s-%s", host, ids.New("wkr")[4:16])
		r.wake = make(chan struct{}, 1)
		if r.Now == nil {
			r.Now = func() time.Time { return time.Now().UTC() }
		}
		if r.Config.SchedulerTick == 0 {
			r.Config.SchedulerTick = 30 * time.Second
		}
	})
}

// Notify wakes an idle worker (non-blocking).
func (r *Runner) Notify() {
	r.init()
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

// Run starts workers, lease recovery and the interval scheduler; it returns
// when ctx is cancelled and every in-flight job has stopped.
func (r *Runner) Run(ctx context.Context) {
	r.init()
	var wg sync.WaitGroup
	for i := 0; i < r.Config.Workers; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); r.workerLoop(ctx) }()
	}
	wg.Add(2)
	go func() { defer wg.Done(); r.recoveryLoop(ctx) }()
	go func() { defer wg.Done(); r.schedulerLoop(ctx) }()
	wg.Wait()
}

func (r *Runner) workerLoop(ctx context.Context) {
	for ctx.Err() == nil {
		job, ok, err := r.Store.ClaimJob(ctx, r.workerID, r.Now())
		if err != nil && ctx.Err() == nil {
			r.Log.Error("claiming discovery job failed", "error", err)
		}
		if ok {
			r.Execute(ctx, job)
			continue
		}
		t := time.NewTimer(r.Config.PollInterval)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-r.wake:
			t.Stop()
		case <-t.C:
		}
	}
}

func (r *Runner) recoveryLoop(ctx context.Context) {
	t := time.NewTicker(r.Config.LeaseTimeout / 2)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			now := r.Now()
			requeued, failed, err := r.Store.RecoverExpiredJobs(ctx, now.Add(-r.Config.LeaseTimeout), r.Config.MaxAttempts, now)
			if err != nil && ctx.Err() == nil {
				r.Log.Error("recovering expired jobs failed", "error", err)
			}
			if requeued+failed > 0 {
				r.Log.Warn("recovered discovery jobs with expired leases", "requeued", requeued, "failed", failed)
				r.Notify()
			}
		}
	}
}

// schedulerLoop enqueues jobs for connections with a discovery interval.
func (r *Runner) schedulerLoop(ctx context.Context) {
	t := time.NewTicker(r.Config.SchedulerTick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := r.ScheduleDue(ctx); err != nil && ctx.Err() == nil {
				r.Log.Error("discovery scheduler failed", "error", err)
			}
		}
	}
}

// ScheduleDue creates scheduled jobs for connections whose interval elapsed.
func (r *Runner) ScheduleDue(ctx context.Context) error {
	r.init()
	conns, err := r.Store.AllConnections(ctx)
	if err != nil {
		return err
	}
	latestByTenant := map[string]map[string]store.Job{}
	now := r.Now()
	for _, c := range conns {
		if c.DiscoveryIntervalMinutes <= 0 {
			continue
		}
		latest, ok := latestByTenant[c.TenantID]
		if !ok {
			if latest, err = r.Store.LatestJobs(ctx, c.TenantID); err != nil {
				return err
			}
			latestByTenant[c.TenantID] = latest
		}
		if j, ok := latest[c.ID]; ok && (!j.Status.Terminal() || j.CreatedAt.Add(time.Duration(c.DiscoveryIntervalMinutes)*time.Minute).After(now)) {
			continue
		}
		_, created, err := r.Store.CreateJob(ctx, store.Job{ID: ids.New("job"), TenantID: c.TenantID, ConnectionID: c.ID, Trigger: "schedule", RequestedBy: "scheduler", CreatedAt: now})
		if err != nil && !errors.Is(err, store.ErrActiveJob) {
			return err
		}
		if created {
			r.Log.Info("scheduled discovery job", "tenant", c.TenantID, "connection", c.ID)
			r.Notify()
		}
	}
	return nil
}

// errCancelled marks a user-requested cancellation.
var errCancelled = errors.New("cancelled by request")

// errLeaseLost marks loss of the job lease (another worker took over).
var errLeaseLost = errors.New("job lease lost")

type progress struct {
	mu sync.Mutex
	p  store.JobProgress
}

func (p *progress) get() store.JobProgress { p.mu.Lock(); defer p.mu.Unlock(); return p.p }
func (p *progress) update(f func(*store.JobProgress)) {
	p.mu.Lock()
	f(&p.p)
	p.mu.Unlock()
}

// Execute runs one claimed job to a terminal state.
func (r *Runner) Execute(parent context.Context, job store.Job) {
	r.init()
	log := r.Log.With("job", job.ID, "tenant", job.TenantID, "connection", job.ConnectionID, "attempt", job.Attempt)
	ctx, cancel := context.WithCancelCause(parent)
	defer cancel(nil)
	ctx, cancelTimeout := context.WithTimeout(ctx, r.Config.JobTimeout)
	defer cancelTimeout()

	pr := &progress{p: store.JobProgress{Phase: "starting"}}
	event := func(level, msg string, details map[string]any) {
		e := store.JobEvent{At: r.Now(), Level: level, Message: msg, Details: details}
		if err := r.Store.AppendJobEvent(context.WithoutCancel(ctx), job.TenantID, job.ID, e, r.Config.MaxJobEvents); err != nil {
			log.Warn("writing job event failed", "error", err)
		}
	}

	// Heartbeat: extends the lease, publishes progress, observes cancellation.
	hbDone := make(chan struct{})
	go func() {
		defer close(hbDone)
		t := time.NewTicker(max(r.Config.LeaseTimeout/3, 200*time.Millisecond))
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				cancelReq, err := r.Store.Heartbeat(ctx, job.ID, r.workerID, pr.get(), r.Now())
				switch {
				case errors.Is(err, store.ErrNotOwner):
					cancel(errLeaseLost)
					return
				case err != nil:
					log.Warn("heartbeat failed", "error", err)
				case cancelReq:
					cancel(errCancelled)
					return
				}
			}
		}
	}()

	status, counts, complete, errMsg := r.run(ctx, job, pr, event, log)
	cancel(nil)
	<-hbDone

	if errors.Is(context.Cause(ctx), errLeaseLost) {
		log.Warn("lease lost; another worker owns the job now")
		return
	}
	p := pr.get()
	p.Phase = string(status)
	finishCtx, fc := context.WithTimeout(context.WithoutCancel(parent), 10*time.Second)
	defer fc()
	if err := r.Store.FinishJob(finishCtx, job.ID, r.workerID, status, counts, p, complete, errMsg, r.Now()); err != nil {
		log.Error("finishing job failed", "error", err)
		return
	}
	event(map[bool]string{true: "info", false: "error"}[status == store.JobSucceeded || status == store.JobCancelled],
		"job "+string(status), map[string]any{"added": counts.Added, "changed": counts.Changed, "unchanged": counts.Unchanged,
			"reactivated": counts.Reactivated, "missing": counts.Missing})
	if r.Telemetry != nil {
		r.Telemetry.Add("obs_discovery_jobs_total", 1, "status", string(status))
		r.Telemetry.Add("obs_discovery_resources_observed_total", int64(p.Resources))
	}
	log.Info("discovery job finished", "status", status, "resources", p.Resources, "relationships", p.Relationships,
		"added", counts.Added, "changed", counts.Changed, "missing", counts.Missing, "error", errMsg)
	if status == store.JobSucceeded && r.OnSucceeded != nil {
		r.OnSucceeded(job.TenantID, job.ConnectionID)
	}
}

const maxWarningEvents = 20

func (r *Runner) run(ctx context.Context, job store.Job, pr *progress, event func(string, string, map[string]any), log *slog.Logger) (store.JobStatus, topology.Counts, bool, string) {
	conn, err := r.Store.GetConnection(ctx, job.TenantID, job.ConnectionID)
	if err != nil {
		return store.JobFailed, topology.Counts{}, false, "loading connection: " + err.Error()
	}
	prov, ok := r.Providers.Get(conn.Provider)
	if !ok {
		return store.JobFailed, topology.Counts{}, false, fmt.Sprintf("provider %q is not enabled in this deployment", conn.Provider)
	}
	cred, err := r.Secrets.Resolve(conn.CredentialRef)
	if err != nil {
		// The error names the reference, never the value.
		return store.JobFailed, topology.Counts{}, false, "resolving credential: " + err.Error()
	}
	prev, err := r.Store.CountSucceededJobs(ctx, job.TenantID, job.ConnectionID)
	if err != nil {
		return store.JobFailed, topology.Counts{}, false, err.Error()
	}
	event("info", "discovery started", map[string]any{"provider": conn.Provider, "worker": r.workerID, "revision": prev + 1})

	snap := topology.NewSnapshot(job.TenantID, job.ConnectionID, r.Config.MaxResources)
	pr.update(func(p *store.JobProgress) { p.Phase = "fetching" })
	warnings := 0
	emit := func(pg Page) error {
		if err := context.Cause(ctx); err != nil {
			return err
		}
		for _, o := range pg.Resources {
			if err := snap.AddResource(o); err != nil {
				if errors.Is(err, topology.ErrSnapshotLimit) {
					return err
				}
				warnings++
				if warnings <= maxWarningEvents {
					event("warn", "invalid resource observation skipped", map[string]any{"reason": err.Error()})
				}
			}
		}
		for _, o := range pg.Relationships {
			if err := snap.AddRelationship(o); err != nil {
				warnings++
				if warnings <= maxWarningEvents {
					event("warn", "invalid relationship observation skipped", map[string]any{"reason": err.Error()})
				}
			}
		}
		pr.update(func(p *store.JobProgress) {
			p.Pages++
			p.Resources = snap.ResourceCount()
			p.Relationships = snap.RelationshipCount()
			p.Warnings = warnings
		})
		if pages := pr.get().Pages; pages <= 50 {
			event("info", fmt.Sprintf("page %d processed", pages), map[string]any{"resources": len(pg.Resources), "relationships": len(pg.Relationships)})
		}
		return nil
	}
	discErr := prov.Discover(ctx, Request{TenantID: job.TenantID, Connection: conn, Credential: cred, Revision: prev + 1}, emit)
	cause := context.Cause(ctx)

	status := store.JobSucceeded
	errMsg := ""
	switch {
	case errors.Is(cause, errLeaseLost):
		return store.JobFailed, topology.Counts{}, false, "lease lost"
	case errors.Is(cause, errCancelled):
		status, errMsg = store.JobCancelled, "cancelled by request"
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		status, errMsg = store.JobFailed, fmt.Sprintf("job exceeded timeout of %s", r.Config.JobTimeout)
	case errors.Is(discErr, topology.ErrSnapshotLimit):
		status, errMsg = store.JobFailed, discErr.Error()+"; raise OBS_DISCOVERY_MAX_RESOURCES or narrow the connection scope"
	case discErr != nil:
		status, errMsg = store.JobFailed, discErr.Error()
	}
	complete := status == store.JobSucceeded

	// Persist what was observed, even for partial runs: upserts are
	// idempotent and partial runs never mark anything missing.
	if snap.ResourceCount() == 0 {
		return status, topology.Counts{}, complete, errMsg
	}
	pr.update(func(p *store.JobProgress) { p.Phase = "reconciling" })
	rep := snap.Finalize()
	if n := len(rep.DanglingEdges); n > 0 {
		event("warn", "relationships to unobserved resources dropped", map[string]any{"count": n})
	}
	if n := len(rep.CycleEdges); n > 0 {
		event("warn", "relationships that would create a containment cycle dropped", map[string]any{"count": n})
	}
	pr.update(func(p *store.JobProgress) {
		p.Relationships = snap.RelationshipCount()
		p.Warnings = warnings + len(rep.DanglingEdges) + len(rep.CycleEdges)
	})

	pctx, pcancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
	defer pcancel()
	resources := snap.Resources()
	edges := snap.Relationships()
	resIDs := make([]string, len(resources))
	for i, x := range resources {
		resIDs[i] = x.ID
	}
	edgeIDs := make([]string, len(edges))
	for i, x := range edges {
		edgeIDs[i] = x.ID
	}
	exR, exE, err := r.Store.LoadExistingState(pctx, job.TenantID, job.ConnectionID, resIDs, edgeIDs)
	if err != nil {
		return store.JobFailed, topology.Counts{}, false, "loading existing topology: " + err.Error()
	}
	plan := topology.Reconcile(snap, exR, exE, complete)
	if err := r.Store.ApplySnapshot(pctx, store.ApplyInput{TenantID: job.TenantID, ConnectionID: job.ConnectionID, JobID: job.ID,
		Now: r.Now(), Resources: resources, Relationships: edges, Plan: plan, Complete: complete}); err != nil {
		return store.JobFailed, topology.Counts{}, false, "persisting topology: " + err.Error()
	}
	c := plan.Counts()
	event("info", "topology reconciled", map[string]any{"complete": complete, "added": c.Added, "changed": c.Changed,
		"unchanged": c.Unchanged, "reactivated": c.Reactivated, "missing": c.Missing, "edgesMissing": c.EdgesMissing})
	_ = log
	return status, c, complete, errMsg
}
