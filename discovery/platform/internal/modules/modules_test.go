package modules

import (
	"strings"
	"testing"

	"github.com/deeprajsshetty/observability-platform/platform/internal/discovery"
	"github.com/deeprajsshetty/observability-platform/platform/internal/discovery/fixture"
	"github.com/deeprajsshetty/observability-platform/platform/internal/domain/catalog"
	dm "github.com/deeprajsshetty/observability-platform/platform/internal/domain/metrics"
	"github.com/deeprajsshetty/observability-platform/platform/internal/metrics"
	"github.com/deeprajsshetty/observability-platform/platform/internal/metrics/simulated"
)

func TestDefaultInstallsAndIsConsistent(t *testing.T) {
	in, err := Install(Default(), Deps{})
	if err != nil {
		t.Fatal(err)
	}
	if len(in.Modules) != 3 {
		t.Fatalf("modules: %+v", in.Modules)
	}
	// Every type a provider declares, and every type a metric applies to, is
	// described by the catalog, so the UI can render it.
	for _, p := range in.Providers.List() {
		for _, ty := range p.ResourceTypes {
			if catalog.Lookup(ty).Domain == "other" {
				t.Errorf("provider %s declares %s, which the catalog does not describe", p.Name, ty)
			}
		}
		if _, ok := in.Collectors[p.Name]; !ok && p.MetricsSource != "none" {
			t.Errorf("provider %s advertises metrics %q but has no collector", p.Name, p.MetricsSource)
		}
	}
	for _, d := range dm.All() {
		for _, ty := range d.ResourceTypes {
			if catalog.Lookup(ty).Domain == "other" {
				t.Errorf("metric %s applies to undescribed type %s", d.Name, ty)
			}
		}
	}
	for _, rt := range catalog.Types() {
		if rt.KeyMetric != "" {
			ok := false
			for _, d := range dm.ForType(rt.Type) {
				ok = ok || d.Name == rt.KeyMetric
			}
			if !ok {
				t.Errorf("type %s key metric %s does not apply to it", rt.Type, rt.KeyMetric)
			}
		}
	}
}

func TestInstallRejectsMiswiring(t *testing.T) {
	fx := Func{"a", "", func(Deps) Contribution { return Contribution{Providers: []discovery.Provider{fixture.New()}} }}
	dup := Func{"b", "", func(Deps) Contribution { return Contribution{Providers: []discovery.Provider{fixture.New()}} }}
	if _, err := Install([]Module{fx, dup}, Deps{}); err == nil || !strings.Contains(err.Error(), "both provide") {
		t.Fatalf("duplicate provider: %v", err)
	}
	orphan := Func{"c", "", func(Deps) Contribution {
		return Contribution{Collectors: map[string]metrics.Collector{"fixture": simulated.Collector{}}}
	}}
	if _, err := Install([]Module{orphan}, Deps{}); err == nil {
		t.Fatal("collector without its provider must be rejected")
	}
}
