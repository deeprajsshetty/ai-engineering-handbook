package metrics_test

import (
	"context"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	dm "github.com/deeprajsshetty/observability-platform/platform/internal/domain/metrics"
	"github.com/deeprajsshetty/observability-platform/platform/internal/domain/topology"
	"github.com/deeprajsshetty/observability-platform/platform/internal/metrics"
	"github.com/deeprajsshetty/observability-platform/platform/internal/metrics/kubemetrics"
	"github.com/deeprajsshetty/observability-platform/platform/internal/metrics/simulated"
	"github.com/deeprajsshetty/observability-platform/platform/internal/netguard"
	"github.com/deeprajsshetty/observability-platform/platform/internal/retry"
	"github.com/deeprajsshetty/observability-platform/platform/internal/secrets"
	"github.com/deeprajsshetty/observability-platform/platform/internal/store"
)

func TestAlignedSteps(t *testing.T) {
	now := time.Date(2026, 1, 1, 10, 30, 42, 0, time.UTC)
	steps := metrics.AlignedSteps(time.Time{}, false, now, time.Minute, 10*time.Minute)
	if len(steps) != 11 || !steps[0].Equal(time.Date(2026, 1, 1, 10, 20, 0, 0, time.UTC)) || !steps[10].Equal(now.Truncate(time.Minute)) {
		t.Fatalf("backfill steps: %v", steps)
	}
	last := now.Add(-3 * time.Minute)
	steps = metrics.AlignedSteps(last, true, now, time.Minute, 10*time.Minute)
	if len(steps) != 3 {
		t.Fatalf("incremental steps after last sample: %v", steps)
	}
	if s := metrics.AlignedSteps(now, true, now, time.Minute, time.Hour); len(s) != 0 {
		t.Fatalf("no new steps expected: %v", s)
	}
}

func TestSimulatedValuesDeterministicBoundedAndLabelled(t *testing.T) {
	cpu, _ := dm.Lookup("cpu.utilization")
	ts := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	a := simulated.Value(cpu, "res_1", ts)
	if a != simulated.Value(cpu, "res_1", ts) {
		t.Fatal("value must be a pure function of (resource, metric, ts)")
	}
	if a == simulated.Value(cpu, "res_2", ts) {
		t.Fatal("different resources should differ")
	}
	for i := 0; i < 2000; i++ {
		v := simulated.Value(cpu, "res_x", ts.Add(time.Duration(i)*time.Minute))
		if v < 0 || v > 100 || math.IsNaN(v) {
			t.Fatalf("cpu out of range: %v", v)
		}
	}
	req := metrics.Request{TenantID: "t", Now: ts, Interval: time.Minute, Backfill: 5 * time.Minute,
		Resources: []topology.Resource{{ID: "res_1", Type: "compute.instance"}, {ID: "res_2", Type: "network.vpc"}}}
	samples, err := simulated.Collector{}.Collect(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	perMetric := len(dm.ForType("compute.instance"))
	if len(samples) != perMetric*6 {
		t.Fatalf("samples=%d want %d (vpc has no metrics)", len(samples), perMetric*6)
	}
	for _, s := range samples {
		if s.Source != dm.SourceSimulated || s.Collector != simulated.Name || s.Timestamp.Location() != time.UTC {
			t.Fatalf("provenance not recorded: %+v", s)
		}
	}
}

func TestCatalogConsistency(t *testing.T) {
	for _, d := range dm.All() {
		if d.Unit == "" || len(d.ResourceTypes) == 0 || !topology.ValidType(d.ResourceTypes[0]) {
			t.Fatalf("incomplete definition %+v", d)
		}
	}
	if dm.Supported("network.vpc") || !dm.Supported("k8s.pod") {
		t.Fatal("capability mapping wrong")
	}
}

func TestKubernetesMetricsContract(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(401)
			return
		}
		switch r.URL.Path {
		case "/apis/metrics.k8s.io/v1beta1/nodes":
			w.Write([]byte(`{"metadata":{},"items":[{"metadata":{"name":"node-1"},"timestamp":"2026-11-17T10:00:00Z","usage":{"cpu":"250m","memory":"1Gi"}}]}`))
		case "/apis/metrics.k8s.io/v1beta1/pods":
			w.Write([]byte(`{"metadata":{},"items":[{"metadata":{"name":"web","namespace":"apps"},"timestamp":"2026-11-17T10:00:00Z","containers":[{"usage":{"cpu":"100m","memory":"64Mi"}},{"usage":{"cpu":"50000000n","memory":"32Mi"}}]}]}`))
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	c := &kubemetrics.Collector{Policy: netguard.Policy{Allowlist: []string{"127.0.0.1"}, AllowInsecure: true}, Timeout: 5 * time.Second,
		Retry: retry.Policy{MaxAttempts: 1}}
	cred, _ := (&secrets.Resolver{Getenv: func(string) string { return "tok" }}).Resolve("env:OBS_SECRET_T")
	req := metrics.Request{TenantID: "t", Credential: cred, Now: time.Now().UTC(),
		Connection: store.Connection{Config: map[string]any{"server": srv.URL}},
		Resources: []topology.Resource{
			{ID: "res_node", Type: "k8s.node", Properties: map[string]any{"name": "node-1", "capacityCpu": "2", "capacityMemory": "4Gi"}},
			{ID: "res_pod", Type: "k8s.pod", Properties: map[string]any{"name": "web", "namespace": "apps"}},
		}}
	samples, err := c.Collect(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]float64{}
	for _, s := range samples {
		if s.Source != dm.SourceMeasured {
			t.Fatal("kubernetes samples must be measured")
		}
		got[s.ResourceID+"/"+s.Metric] = s.Value
	}
	want := map[string]float64{
		"res_node/node.cpu.usage": 0.25, "res_node/cpu.utilization": 12.5,
		"res_node/node.memory.usage": 1 << 30, "res_node/memory.utilization": 25,
		"res_pod/container.cpu.usage": 0.15, "res_pod/container.memory.usage": 96 << 20,
	}
	for k, v := range want {
		if math.Abs(got[k]-v) > 1e-9 {
			t.Errorf("%s = %v, want %v", k, got[k], v)
		}
	}
}
