package kubernetes

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/deeprajsshetty/observability-platform/platform/internal/discovery"
	"github.com/deeprajsshetty/observability-platform/platform/internal/domain/topology"
	"github.com/deeprajsshetty/observability-platform/platform/internal/kubeclient"
	"github.com/deeprajsshetty/observability-platform/platform/internal/netguard"
	"github.com/deeprajsshetty/observability-platform/platform/internal/retry"
	"github.com/deeprajsshetty/observability-platform/platform/internal/secrets"
	"github.com/deeprajsshetty/observability-platform/platform/internal/store"
)

// Recorded-shape fixtures of the Kubernetes list API (trimmed to the fields
// the adapter reads). Nodes are split across two pages to exercise
// limit/continue; the first pods call returns 429 to exercise retry.
var fixtures = map[string]string{
	"/version":                       `{"gitVersion":"v1.34.1"}`,
	"/api/v1/namespaces/kube-system": `{"metadata":{"name":"kube-system","uid":"ks-uid"}}`,
	"/api/v1/nodes|":                 `{"metadata":{"continue":"page2"},"items":[{"metadata":{"name":"node-1","uid":"n1","labels":{"topology.kubernetes.io/region":"eu-1"}},"status":{"capacity":{"cpu":"2","memory":"8Gi"},"nodeInfo":{"kubeletVersion":"v1.34.1"}}}]}`,
	"/api/v1/nodes|page2":            `{"metadata":{},"items":[{"metadata":{"name":"node-2","uid":"n2"},"status":{"capacity":{"cpu":"4","memory":"16Gi"}}}]}`,
	"/api/v1/namespaces":             `{"metadata":{},"items":[{"metadata":{"name":"default","uid":"ns-default"}},{"metadata":{"name":"apps","uid":"ns-apps"}}]}`,
	"/apis/apps/v1/replicasets":      `{"metadata":{},"items":[{"metadata":{"name":"web-5d9","uid":"rs1","namespace":"apps","ownerReferences":[{"kind":"Deployment","name":"web","uid":"dep1"}]}}]}`,
	"/apis/apps/v1/deployments":      `{"metadata":{},"items":[{"metadata":{"name":"web","uid":"dep1","namespace":"apps"},"spec":{"replicas":1}}]}`,
	"/api/v1/pods":                   `{"metadata":{},"items":[{"metadata":{"name":"web-5d9-abc","uid":"p1","namespace":"apps","labels":{"app":"web"},"ownerReferences":[{"kind":"ReplicaSet","name":"web-5d9","uid":"rs1"}]},"spec":{"nodeName":"node-1"},"status":{"phase":"Running"}},{"metadata":{"name":"db-0","uid":"p2","namespace":"apps","labels":{"app":"db"}},"spec":{"nodeName":"node-2"},"status":{"phase":"Running"}}]}`,
	"/api/v1/services":               `{"metadata":{},"items":[{"metadata":{"name":"web","uid":"svc1","namespace":"apps"},"spec":{"type":"ClusterIP","selector":{"app":"web"}}}]}`,
}

type fakeAPI struct {
	srv        *httptest.Server
	podCalls   atomic.Int32
	nodeCalls  atomic.Int32
	forbidNode bool
}

func newFakeAPI(t *testing.T) *fakeAPI {
	f := &fakeAPI{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("adapter must be read-only; got %s", r.Method)
		}
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		key := r.URL.Path
		switch r.URL.Path {
		case "/api/v1/nodes":
			f.nodeCalls.Add(1)
			if f.forbidNode {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			if r.URL.Query().Get("limit") == "" {
				t.Error("list calls must send limit")
			}
			key += "|" + r.URL.Query().Get("continue")
		case "/api/v1/pods":
			if f.podCalls.Add(1) == 1 {
				w.Header().Set("Retry-After", "0")
				w.WriteHeader(http.StatusTooManyRequests)
				return
			}
		}
		body, ok := fixtures[key]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func provider() *Provider {
	return &Provider{
		Policy:      netguard.Policy{Allowlist: []string{"127.0.0.1"}, AllowInsecure: true},
		PageTimeout: 5 * time.Second,
		Retry:       retry.Policy{MaxAttempts: 3, BaseDelay: time.Millisecond, MaxDelay: 5 * time.Millisecond},
	}
}

func request(server, token string) discovery.Request {
	r := secrets.Resolver{Getenv: func(string) string { return token }}
	v, _ := r.Resolve("env:OBS_SECRET_K8S")
	return discovery.Request{TenantID: "t", Credential: v, Revision: 1, Connection: store.Connection{ID: "c",
		Config: map[string]any{"server": server, "clusterName": "test", "pageSize": float64(1)}}}
}

func TestDiscoverContract(t *testing.T) {
	f := newFakeAPI(t)
	p := provider()
	if err := p.Check(context.Background(), request(f.srv.URL, "tok")); err != nil {
		t.Fatalf("check: %v", err)
	}
	s := topology.NewSnapshot("t", "c", 100)
	err := p.Discover(context.Background(), request(f.srv.URL, "tok"), func(pg discovery.Page) error {
		for _, o := range pg.Resources {
			if err := s.AddResource(o); err != nil {
				t.Fatalf("invalid observation: %v", err)
			}
		}
		for _, o := range pg.Relationships {
			if err := s.AddRelationship(o); err != nil {
				t.Fatalf("invalid relationship: %v", err)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	rep := s.Finalize()
	if len(rep.DanglingEdges)+len(rep.CycleEdges) > 0 {
		t.Fatalf("unexpected dropped edges: %+v", rep)
	}
	if s.ResourceCount() != 9 {
		t.Fatalf("resources=%d want 9", s.ResourceCount())
	}
	byType := map[topology.RelationType]int{}
	for _, e := range s.Relationships() {
		byType[e.Type]++
	}
	if byType[topology.RelContains] != 10 || byType[topology.RelComposedOf] != 1 || byType[topology.RelRoutesTo] != 1 {
		t.Fatalf("relationships by type: %v", byType)
	}
	if f.nodeCalls.Load() < 3 { // check + two pages
		t.Fatalf("pagination not followed: %d node calls", f.nodeCalls.Load())
	}
	if f.podCalls.Load() != 2 {
		t.Fatalf("429 not retried exactly once: %d pod calls", f.podCalls.Load())
	}
	for _, r := range s.Resources() {
		if r.Scope != "uid:ks-uid" {
			t.Fatalf("cluster identity must come from kube-system UID, got %q", r.Scope)
		}
		if r.ExternalID == "n1" && r.Region != "eu-1" {
			t.Fatalf("node region label not applied: %q", r.Region)
		}
	}
}

func TestAuthAndRBACErrors(t *testing.T) {
	f := newFakeAPI(t)
	p := provider()
	noop := func(discovery.Page) error { return nil }
	if err := p.Discover(context.Background(), request(f.srv.URL, "wrong"), noop); !errors.Is(err, kubeclient.ErrUnauthorized) {
		t.Fatalf("want ErrUnauthorized, got %v", err)
	}
	f.forbidNode = true
	err := p.Discover(context.Background(), request(f.srv.URL, "tok"), noop)
	if !errors.Is(err, kubeclient.ErrForbidden) || !strings.Contains(err.Error(), "listing nodes") {
		t.Fatalf("want ErrForbidden while listing nodes, got %v", err)
	}
	if strings.Contains(err.Error(), "tok") {
		t.Fatal("error must not contain the token")
	}
}

func TestEgressPolicy(t *testing.T) {
	p := &Provider{Policy: netguard.Policy{Allowlist: []string{"k8s.example.internal"}}}
	cases := map[string]string{
		"https://k8s.example.internal:6443":    "",
		"http://k8s.example.internal":          "https is required",
		"https://evil.example.com":             "not in OBS_EGRESS_ALLOWLIST",
		"https://user:pw@k8s.example.internal": "credentials must not be embedded",
	}
	for server, want := range cases {
		errs := p.ValidateConfig(map[string]any{"server": server, "clusterName": "c"}, "env:OBS_SECRET_T")
		got := ""
		for _, e := range errs {
			if e.Field == "server" {
				got = e.Message
			}
		}
		if (want == "") != (got == "") || (want != "" && !strings.Contains(got, want)) {
			t.Errorf("%s: got %q, want %q", server, got, want)
		}
	}
	if errs := p.ValidateConfig(map[string]any{"server": "https://k8s.example.internal", "clusterName": "c"}, ""); len(errs) == 0 {
		t.Error("missing credential must be rejected")
	}
	// Metadata endpoints are refused even when allow-listed.
	meta := &Provider{Policy: netguard.Policy{Allowlist: []string{"169.254.169.254"}}}
	if errs := meta.ValidateConfig(map[string]any{"server": "https://169.254.169.254", "clusterName": "c"}, "env:OBS_SECRET_T"); len(errs) == 0 {
		t.Error("cloud metadata address must be refused")
	}
}
