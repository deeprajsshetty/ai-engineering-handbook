// Package storetest is the driver-independent contract every store.Backend
// must satisfy (ADR 0011). store/postgres runs it against PostgreSQL and
// store/oracle against Oracle, so both implementations are held to the same
// observable behaviour: tenancy, idempotency, the one-active-job rule,
// exclusive claims, reconciliation, idempotent samples, bucketing and
// retention.
package storetest

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/deeprajsshetty/observability-platform/platform/internal/domain/metrics"
	"github.com/deeprajsshetty/observability-platform/platform/internal/domain/topology"
	"github.com/deeprajsshetty/observability-platform/platform/internal/ids"
	"github.com/deeprajsshetty/observability-platform/platform/internal/store"
)

var ctx = context.Background()

// Run executes the contract. newStore must return an empty, migrated store
// that is cleaned up when the test ends.
func Run(t *testing.T, newStore func(*testing.T) store.Store) {
	t.Run("ConnectionsTenantScopedAndUnique", func(t *testing.T) { testConnectionsTenantScopedAndUnique(t, newStore) })
	t.Run("JobLifecycleIdempotencyAndSingleActive", func(t *testing.T) { testJobLifecycleIdempotencyAndSingleActive(t, newStore) })
	t.Run("ClaimIsExclusiveUnderConcurrency", func(t *testing.T) { testClaimIsExclusiveUnderConcurrency(t, newStore) })
	t.Run("RecoverExpiredJobs", func(t *testing.T) { testRecoverExpiredJobs(t, newStore) })
	t.Run("JobEventsAreBounded", func(t *testing.T) { testJobEventsAreBounded(t, newStore) })
	t.Run("ApplySnapshotReconciliationEndToEnd", func(t *testing.T) { testApplySnapshotReconciliationEndToEnd(t, newStore) })
	t.Run("MetricSamplesIdempotentAndBucketed", func(t *testing.T) { testMetricSamplesIdempotentAndBucketed(t, newStore) })
	t.Run("PurgeMissingResources", func(t *testing.T) { testPurgeMissingResources(t, newStore) })
}

func mkConn(t *testing.T, st store.Store, tenant, name string) store.Connection {
	t.Helper()
	now := time.Now().UTC()
	c, err := st.CreateConnection(ctx, store.Connection{
		ID: ids.New("con"), TenantID: tenant, Name: name, Provider: "fixture",
		Config: map[string]any{"scenario": "retail-demo"}, CreatedBy: "test", CreatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func testConnectionsTenantScopedAndUnique(t *testing.T, newStore func(*testing.T) store.Store) {
	st := newStore(t)
	c := mkConn(t, st, "demo", "one")
	if _, err := st.CreateConnection(ctx, store.Connection{ID: ids.New("con"), TenantID: "demo", Name: "one", Provider: "fixture", CreatedBy: "x", CreatedAt: time.Now()}); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("duplicate name: want ErrConflict, got %v", err)
	}
	mkConn(t, st, "acme", "one") // same name, other tenant: allowed
	if _, err := st.GetConnection(ctx, "acme", c.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("cross-tenant read must be not found, got %v", err)
	}
	for i := 0; i < 5; i++ {
		mkConn(t, st, "demo", fmt.Sprintf("c%d", i))
	}
	var seen []string
	cur := ""
	for {
		page, next, err := st.ListConnections(ctx, "demo", store.Page{Limit: 2, Cursor: cur})
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range page {
			seen = append(seen, p.ID)
		}
		if next == "" {
			break
		}
		cur = next
	}
	if len(seen) != 6 {
		t.Fatalf("pagination returned %d, want 6", len(seen))
	}
	if _, _, err := st.ListConnections(ctx, "demo", store.Page{Limit: 2, Cursor: "garbage!"}); !errors.Is(err, store.ErrInvalidCursor) {
		t.Fatalf("bad cursor: %v", err)
	}
}

func newJob(tenant, conn, idem string) store.Job {
	return store.Job{ID: ids.New("job"), TenantID: tenant, ConnectionID: conn, Trigger: "manual", RequestedBy: "t", IdempotencyKey: idem, CreatedAt: time.Now().UTC()}
}

func testJobLifecycleIdempotencyAndSingleActive(t *testing.T, newStore func(*testing.T) store.Store) {
	st := newStore(t)
	c := mkConn(t, st, "demo", "c")
	j, created, err := st.CreateJob(ctx, newJob("demo", c.ID, "key-1"))
	if err != nil || !created {
		t.Fatalf("create: %v", err)
	}
	again, created, err := st.CreateJob(ctx, newJob("demo", c.ID, "key-1"))
	if err != nil || created || again.ID != j.ID {
		t.Fatalf("idempotent replay: created=%v id=%s err=%v", created, again.ID, err)
	}
	active, _, err := st.CreateJob(ctx, newJob("demo", c.ID, ""))
	if !errors.Is(err, store.ErrActiveJob) || active.ID != j.ID {
		t.Fatalf("second active job: err=%v active=%s", err, active.ID)
	}

	now := time.Now().UTC()
	claimed, ok, err := st.ClaimJob(ctx, "w1", now)
	if err != nil || !ok || claimed.ID != j.ID || claimed.Status != store.JobRunning || claimed.Attempt != 1 {
		t.Fatalf("claim: ok=%v job=%+v err=%v", ok, claimed, err)
	}
	if _, ok, _ := st.ClaimJob(ctx, "w2", now); ok {
		t.Fatal("a running job must not be claimed twice")
	}
	if _, err := st.Heartbeat(ctx, j.ID, "w2", store.JobProgress{}, now); !errors.Is(err, store.ErrNotOwner) {
		t.Fatalf("foreign heartbeat: %v", err)
	}
	if _, err := st.RequestCancel(ctx, "demo", j.ID, now); err != nil {
		t.Fatal(err)
	}
	cancel, err := st.Heartbeat(ctx, j.ID, "w1", store.JobProgress{Phase: "fetching", Pages: 2}, now)
	if err != nil || !cancel {
		t.Fatalf("heartbeat should report cancel: %v %v", cancel, err)
	}
	if err := st.FinishJob(ctx, j.ID, "w1", store.JobCancelled, topology.Counts{}, store.JobProgress{Phase: "cancelled"}, false, "", now); err != nil {
		t.Fatal(err)
	}
	if _, err := st.RequestCancel(ctx, "demo", j.ID, now); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("cancel terminal job: %v", err)
	}
	// With the previous job terminal, a new one may be queued; a queued job cancels immediately.
	j2, _, err := st.CreateJob(ctx, newJob("demo", c.ID, ""))
	if err != nil {
		t.Fatal(err)
	}
	j2c, err := st.RequestCancel(ctx, "demo", j2.ID, now)
	if err != nil || j2c.Status != store.JobCancelled {
		t.Fatalf("queued cancel: %+v %v", j2c, err)
	}
}

func testClaimIsExclusiveUnderConcurrency(t *testing.T, newStore func(*testing.T) store.Store) {
	st := newStore(t)
	const n = 12
	for i := 0; i < n; i++ {
		c := mkConn(t, st, "demo", fmt.Sprintf("c%d", i))
		if _, _, err := st.CreateJob(ctx, newJob("demo", c.ID, "")); err != nil {
			t.Fatal(err)
		}
	}
	var mu sync.Mutex
	claimed := map[string]string{}
	var wg sync.WaitGroup
	for w := 0; w < 6; w++ {
		wg.Add(1)
		go func(worker string) {
			defer wg.Done()
			for {
				j, ok, err := st.ClaimJob(ctx, worker, time.Now().UTC())
				if err != nil {
					t.Error(err)
					return
				}
				if !ok {
					return
				}
				mu.Lock()
				if prev, dup := claimed[j.ID]; dup {
					t.Errorf("job %s claimed by %s and %s", j.ID, prev, worker)
				}
				claimed[j.ID] = worker
				mu.Unlock()
			}
		}(fmt.Sprintf("w%d", w))
	}
	wg.Wait()
	if len(claimed) != n {
		t.Fatalf("claimed %d of %d", len(claimed), n)
	}
}

func testRecoverExpiredJobs(t *testing.T, newStore func(*testing.T) store.Store) {
	st := newStore(t)
	c := mkConn(t, st, "demo", "c")
	j, _, _ := st.CreateJob(ctx, newJob("demo", c.ID, ""))
	old := time.Now().UTC().Add(-time.Hour)
	if _, ok, _ := st.ClaimJob(ctx, "dead-worker", old); !ok {
		t.Fatal("claim failed")
	}
	requeued, failed, err := st.RecoverExpiredJobs(ctx, time.Now().UTC().Add(-time.Minute), 3, time.Now().UTC())
	if err != nil || requeued != 1 || failed != 0 {
		t.Fatalf("recover: requeued=%d failed=%d err=%v", requeued, failed, err)
	}
	got, _ := st.GetJob(ctx, "demo", j.ID)
	if got.Status != store.JobQueued {
		t.Fatalf("want queued, got %s", got.Status)
	}
	// Exhaust attempts.
	for i := 0; i < 2; i++ {
		st.ClaimJob(ctx, "dead", old)
		st.RecoverExpiredJobs(ctx, time.Now().UTC().Add(-time.Minute), 3, time.Now().UTC())
	}
	got, _ = st.GetJob(ctx, "demo", j.ID)
	if got.Status != store.JobFailed || got.Attempt != 3 {
		t.Fatalf("want failed after 3 attempts, got %s attempt=%d", got.Status, got.Attempt)
	}
}

func testJobEventsAreBounded(t *testing.T, newStore func(*testing.T) store.Store) {
	st := newStore(t)
	c := mkConn(t, st, "demo", "c")
	j, _, _ := st.CreateJob(ctx, newJob("demo", c.ID, ""))
	for i := 0; i < 15; i++ {
		if err := st.AppendJobEvent(ctx, "demo", j.ID, store.JobEvent{At: time.Now(), Level: "info", Message: fmt.Sprint(i)}, 10); err != nil {
			t.Fatal(err)
		}
	}
	evs, err := st.ListJobEvents(ctx, "demo", j.ID, 0, 100)
	if err != nil || len(evs) != 10 {
		t.Fatalf("events=%d err=%v", len(evs), err)
	}
	if evs, _ := st.ListJobEvents(ctx, "acme", j.ID, 0, 100); len(evs) != 0 {
		t.Fatal("events leaked across tenants")
	}
}

type world struct {
	st   store.Store
	conn store.Connection
}

func (w world) run(t *testing.T, complete bool, observe func(s *topology.Snapshot)) (topology.Counts, string) {
	t.Helper()
	jobID := ids.New("job")
	s := topology.NewSnapshot(w.conn.TenantID, w.conn.ID, 1000)
	observe(s)
	s.Finalize()
	resIDs := make([]string, 0)
	for _, r := range s.Resources() {
		resIDs = append(resIDs, r.ID)
	}
	edgeIDs := make([]string, 0)
	for _, e := range s.Relationships() {
		edgeIDs = append(edgeIDs, e.ID)
	}
	exR, exE, err := w.st.LoadExistingState(ctx, w.conn.TenantID, w.conn.ID, resIDs, edgeIDs)
	if err != nil {
		t.Fatal(err)
	}
	plan := topology.Reconcile(s, exR, exE, complete)
	err = w.st.ApplySnapshot(ctx, store.ApplyInput{TenantID: w.conn.TenantID, ConnectionID: w.conn.ID, JobID: jobID, Now: time.Now().UTC(),
		Resources: s.Resources(), Relationships: s.Relationships(), Plan: plan, Complete: complete})
	if err != nil {
		t.Fatal(err)
	}
	return plan.Counts(), jobID
}

func k(id string) topology.Key {
	return topology.Key{Provider: "fixture", Scope: "acct", ExternalID: id}
}

func add(t *testing.T, s *topology.Snapshot, id, typ string, labels map[string]string) {
	t.Helper()
	if err := s.AddResource(topology.Observation{Key: k(id), Type: typ, Name: id, Labels: labels}); err != nil {
		t.Fatal(err)
	}
}

func testApplySnapshotReconciliationEndToEnd(t *testing.T, newStore func(*testing.T) store.Store) {
	st := newStore(t)
	w := world{st, mkConn(t, st, "demo", "c")}
	base := func(s *topology.Snapshot) {
		add(t, s, "vpc", "network.vpc", nil)
		add(t, s, "vm1", "compute.instance", map[string]string{"v": "1"})
		add(t, s, "vm2", "compute.instance", nil)
		s.AddRelationship(topology.RelationshipObservation{Source: k("vpc"), Target: k("vm1"), Type: topology.RelContains})
		s.AddRelationship(topology.RelationshipObservation{Source: k("vpc"), Target: k("vm2"), Type: topology.RelContains})
	}
	c, _ := w.run(t, true, base)
	if c.Added != 3 || c.EdgesAdded != 2 {
		t.Fatalf("first run: %+v", c)
	}
	// Same snapshot again: idempotent.
	c, _ = w.run(t, true, base)
	if c.Unchanged != 3 || c.Added+c.Changed+c.Missing != 0 || c.EdgesUnchanged != 2 {
		t.Fatalf("replay: %+v", c)
	}
	// vm2 disappears, vm1 changes. A partial run must not mark vm2 missing.
	changed := func(s *topology.Snapshot) {
		add(t, s, "vpc", "network.vpc", nil)
		add(t, s, "vm1", "compute.instance", map[string]string{"v": "2"})
		s.AddRelationship(topology.RelationshipObservation{Source: k("vpc"), Target: k("vm1"), Type: topology.RelContains})
	}
	c, _ = w.run(t, false, changed)
	if c.Missing != 0 || c.Changed != 1 {
		t.Fatalf("partial run: %+v", c)
	}
	vm2 := topology.ResourceID("demo", k("vm2"))
	if r, _ := st.GetResource(ctx, "demo", vm2); r.Status != topology.StatusActive {
		t.Fatal("partial run marked vm2 missing")
	}
	c, job := w.run(t, true, changed)
	if c.Missing != 1 || c.EdgesMissing != 1 || c.Unchanged != 2 {
		t.Fatalf("complete run: %+v", c)
	}
	r, err := st.GetResource(ctx, "demo", vm2)
	if err != nil || r.Status != topology.StatusMissing || r.MissingSince == nil {
		t.Fatalf("vm2 must be retained as missing: %+v %v", r, err)
	}
	changes, _, _ := st.ListChanges(ctx, "demo", store.ChangeFilter{JobID: job}, store.Page{Limit: 10})
	if len(changes) != 1 || changes[0].Kind != "missing" || changes[0].Name != "vm2" {
		t.Fatalf("change log: %+v", changes)
	}
	// vm2 returns: reactivated, missing_since cleared.
	c, _ = w.run(t, true, base)
	if c.Reactivated != 1 || c.EdgesReactivated != 1 || c.Changed != 1 {
		t.Fatalf("reactivation: %+v", c)
	}
	if r, _ := st.GetResource(ctx, "demo", vm2); r.Status != topology.StatusActive || r.MissingSince != nil {
		t.Fatalf("vm2 not reactivated: %+v", r)
	}

	// Queries: search, filters, pagination, edges, BFS, summary, tenant isolation.
	list, _, err := st.ListResources(ctx, "demo", store.ResourceFilter{Query: "VM", Type: "compute.instance"}, store.Page{Limit: 1})
	if err != nil || len(list) != 1 || list[0].Name != "vm1" {
		t.Fatalf("search page 1: %+v %v", list, err)
	}
	if l, _, _ := st.ListResources(ctx, "demo", store.ResourceFilter{Query: "%"}, store.Page{Limit: 10}); len(l) != 0 {
		t.Fatal("LIKE wildcards in user input must be escaped")
	}
	if l, _, _ := st.ListResources(ctx, "demo", store.ResourceFilter{LabelKey: "v", LabelValue: "1"}, store.Page{Limit: 10}); len(l) != 1 {
		t.Fatalf("label filter: %d", len(l))
	}
	if l, _, _ := st.ListResources(ctx, "acme", store.ResourceFilter{}, store.Page{Limit: 10}); len(l) != 0 {
		t.Fatal("resources leaked across tenants")
	}
	rels, _, err := st.ListRelationships(ctx, "demo", store.RelationshipFilter{ResourceID: vm2}, store.Page{Limit: 10})
	if err != nil || len(rels) != 1 || rels[0].SourceName != "vpc" || rels[0].Type != topology.RelContains {
		t.Fatalf("relationships: %+v %v", rels, err)
	}
	vpc := topology.ResourceID("demo", k("vpc"))
	sub, err := topology.BFS(vpc, 2, 50, topology.DirOut, func(ids []string) ([]topology.Edge, error) {
		return st.EdgesTouching(ctx, "demo", ids, false)
	})
	if err != nil || len(sub.Nodes) != 3 || len(sub.Edges) != 2 {
		t.Fatalf("bfs: %+v %v", sub, err)
	}
	sum, err := st.Summary(ctx, "demo", "")
	if err != nil || sum.ActiveResources != 3 || sum.Relationships != 2 {
		t.Fatalf("summary: %+v %v", sum, err)
	}
	nodes, edges, trunc, err := st.Graph(ctx, "demo", store.ResourceFilter{}, 2)
	if err != nil || len(nodes) != 2 || !trunc {
		t.Fatalf("graph cap: n=%d e=%d trunc=%v err=%v", len(nodes), len(edges), trunc, err)
	}
}

func testMetricSamplesIdempotentAndBucketed(t *testing.T, newStore func(*testing.T) store.Store) {
	st := newStore(t)
	w := world{st, mkConn(t, st, "demo", "c")}
	w.run(t, true, func(s *topology.Snapshot) { add(t, s, "vm", "compute.instance", nil) })
	rid := topology.ResourceID("demo", k("vm"))
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var samples []metrics.Sample
	for i := 0; i < 10; i++ {
		samples = append(samples, metrics.Sample{TenantID: "demo", ResourceID: rid, Metric: "cpu.utilization", Timestamp: t0.Add(time.Duration(i) * time.Minute), Value: float64(i), Source: metrics.SourceSimulated, Collector: "test"})
	}
	n, err := st.InsertSamples(ctx, samples)
	if err != nil || n != 10 {
		t.Fatalf("insert: %d %v", n, err)
	}
	if n, _ := st.InsertSamples(ctx, samples); n != 0 {
		t.Fatalf("re-insert must be a no-op, inserted %d", n)
	}
	// A sample claiming another tenant's resource is dropped.
	bad := samples[0]
	bad.TenantID = "acme"
	bad.Timestamp = t0.Add(time.Hour)
	if n, _ := st.InsertSamples(ctx, []metrics.Sample{bad}); n != 0 {
		t.Fatal("cross-tenant sample accepted")
	}
	s, err := st.QuerySeries(ctx, "demo", rid, "cpu.utilization", t0, t0.Add(10*time.Minute), 5*time.Minute)
	if err != nil || len(s.Points) != 2 {
		t.Fatalf("series: %+v %v", s, err)
	}
	if s.Points[0].Avg != 2 || s.Points[0].Min != 0 || s.Points[0].Max != 4 || s.Points[0].Count != 5 {
		t.Fatalf("bucket 0: %+v", s.Points[0])
	}
	if len(s.Sources) != 1 || s.Sources[0] != metrics.SourceSimulated {
		t.Fatalf("sources: %v", s.Sources)
	}
	latest, _ := st.LatestSamples(ctx, "demo", rid)
	if latest["cpu.utilization"].Value != 9 {
		t.Fatalf("latest: %+v", latest)
	}
	// Fleet lookup: newest value per (resource, metric); unknown IDs, metrics
	// without samples and other tenants yield nothing.
	lf, err := st.LatestSamplesFor(ctx, "demo", []string{rid, "res_unknown"}, []string{"cpu.utilization", "memory.utilization"})
	if err != nil || len(lf) != 1 || len(lf[rid]) != 1 || lf[rid]["cpu.utilization"].Value != 9 || lf[rid]["cpu.utilization"].Source != metrics.SourceSimulated {
		t.Fatalf("latest for fleet: %+v %v", lf, err)
	}
	if other, _ := st.LatestSamplesFor(ctx, "acme", []string{rid}, []string{"cpu.utilization"}); len(other) != 0 {
		t.Fatalf("cross-tenant fleet lookup returned data: %+v", other)
	}
	// Retention removes old samples in batches.
	if n, err := st.PurgeMetricSamples(ctx, t0.Add(5*time.Minute), 3); err != nil || n != 5 {
		t.Fatalf("purge: %d %v", n, err)
	}
}

func testPurgeMissingResources(t *testing.T, newStore func(*testing.T) store.Store) {
	st := newStore(t)
	w := world{st, mkConn(t, st, "demo", "c")}
	w.run(t, true, func(s *topology.Snapshot) {
		add(t, s, "a", "compute.instance", nil)
		add(t, s, "b", "compute.instance", nil)
	})
	w.run(t, true, func(s *topology.Snapshot) { add(t, s, "a", "compute.instance", nil) })
	if n, _ := st.PurgeMissingResources(ctx, time.Now().UTC().Add(-time.Hour)); n != 0 {
		t.Fatal("purged before the window elapsed")
	}
	if n, _ := st.PurgeMissingResources(ctx, time.Now().UTC().Add(time.Hour)); n != 1 {
		t.Fatal("did not purge after the window")
	}
}
