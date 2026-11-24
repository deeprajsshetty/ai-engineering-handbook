package fixture

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/deeprajsshetty/observability-platform/platform/internal/discovery"
	"github.com/deeprajsshetty/observability-platform/platform/internal/domain/topology"
	"github.com/deeprajsshetty/observability-platform/platform/internal/store"
)

func req(cfg map[string]any, rev int) discovery.Request {
	return discovery.Request{TenantID: "t", Connection: store.Connection{ID: "con", Config: cfg}, Revision: rev}
}

func collect(t *testing.T, p *Provider, r discovery.Request) ([]discovery.Page, error) {
	t.Helper()
	var pages []discovery.Page
	err := p.Discover(context.Background(), r, func(pg discovery.Page) error { pages = append(pages, pg); return nil })
	return pages, err
}

func TestEstateIsDeterministicAndValid(t *testing.T) {
	a, b := Estate("acct-1", 1), Estate("acct-1", 1)
	if !reflect.DeepEqual(a, b) {
		t.Fatal("same inputs must yield identical estates")
	}
	s := topology.NewSnapshot("t", "c", 1000)
	for _, o := range a.Resources {
		if err := s.AddResource(o); err != nil {
			t.Fatalf("fixture resource invalid: %v", err)
		}
	}
	for _, r := range a.Relationships {
		if err := s.AddRelationship(r); err != nil {
			t.Fatalf("fixture relationship invalid: %v", err)
		}
	}
	rep := s.Finalize()
	if len(rep.DanglingEdges)+len(rep.CycleEdges) != 0 || s.DuplicateResources != 0 {
		t.Fatalf("fixture must be self-consistent: %+v dup=%d", rep, s.DuplicateResources)
	}
	for _, r := range s.Resources() {
		if r.Labels["simulated"] != "true" {
			t.Fatalf("%s is not labelled simulated", r.ExternalID)
		}
	}
}

func TestDriftBetweenRevisions(t *testing.T) {
	ids := func(rev int) map[string]bool {
		m := map[string]bool{}
		for _, o := range Estate("a", rev).Resources {
			m[o.Key.ExternalID] = true
		}
		return m
	}
	r1, r2, r3 := ids(1), ids(2), ids(3)
	if !r1["i-east-web-03"] || r2["i-east-web-03"] || !r3["i-east-web-03"] {
		t.Fatal("web-03 must disappear on even revisions and return on odd")
	}
	if r1["i-east-batch-01"] || !r2["i-east-batch-01"] || !r3["i-east-batch-01"] {
		t.Fatal("batch-01 must appear from revision 2")
	}
}

func TestPaginationAndPageRelationships(t *testing.T) {
	p := New()
	pages, err := collect(t, p, req(map[string]any{"pageSize": float64(7), "pageDelayMs": float64(0)}, 1))
	if err != nil {
		t.Fatal(err)
	}
	total := len(Estate("sim-acct-0001", 1).Resources)
	want := (total + 6) / 7
	if len(pages) != want {
		t.Fatalf("pages=%d want %d", len(pages), want)
	}
	n := 0
	for _, pg := range pages {
		n += len(pg.Resources)
		inPage := map[topology.Key]bool{}
		for _, r := range pg.Resources {
			inPage[r.Key] = true
		}
		for _, rel := range pg.Relationships {
			if !inPage[rel.Source] {
				t.Fatal("relationships must travel with their source resource")
			}
		}
	}
	if n != total {
		t.Fatalf("resources=%d want %d", n, total)
	}
}

func TestTransientErrorsAreRetriedAndPermanentFailuresStop(t *testing.T) {
	p := New()
	if _, err := collect(t, p, req(map[string]any{"pageDelayMs": float64(0), "transientFailures": float64(3)}, 1)); err != nil {
		t.Fatalf("transient failures must be retried: %v", err)
	}
	pages, err := collect(t, p, req(map[string]any{"pageDelayMs": float64(0), "failAtPage": float64(2)}, 1))
	if !errors.Is(err, ErrInjected) || len(pages) != 1 {
		t.Fatalf("permanent failure: pages=%d err=%v", len(pages), err)
	}
}

func TestCancellationStopsDiscovery(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	err := New().Discover(ctx, req(map[string]any{"pageDelayMs": float64(1000)}, 1), func(discovery.Page) error { return nil })
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want deadline exceeded, got %v", err)
	}
}

func TestValidateConfig(t *testing.T) {
	p := New()
	if errs := p.ValidateConfig(map[string]any{"pageSize": float64(10)}, ""); len(errs) != 0 {
		t.Fatalf("valid config rejected: %v", errs)
	}
	bad := map[string]any{"pageSize": float64(0), "scenario": "other", "account": "UPPER", "password": "x", "extra": true}
	errs := p.ValidateConfig(bad, "env:OBS_SECRET_X")
	fields := map[string]bool{}
	for _, e := range errs {
		fields[e.Field] = true
	}
	for _, f := range []string{"pageSize", "scenario", "account", "password", "extra", "credentialRef"} {
		if !fields[f] {
			t.Errorf("expected error on %s; got %v", f, errs)
		}
	}
}
