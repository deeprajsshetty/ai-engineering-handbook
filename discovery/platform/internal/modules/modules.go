// Package modules is the single place where integrations are plugged into
// the platform. A Module bundles everything one domain or integration
// contributes — discovery providers and metrics collectors — so adding a new
// domain (a cloud, a database engine, a message broker, an APM source) is:
//
//  1. implement discovery.Provider (and optionally metrics.Collector) in a
//     new package;
//  2. register its resource types in internal/domain/catalog and any new
//     metrics in internal/domain/metrics;
//  3. add a Module here and list it in Default.
//
// No API, store, scheduler or UI change is needed: they are driven by the
// provider Info, the catalog and the metric definitions. See
// docs/guides/extending.md.
package modules

import (
	"fmt"
	"sort"

	"github.com/deeprajsshetty/observability-platform/platform/internal/config"
	"github.com/deeprajsshetty/observability-platform/platform/internal/dbengine"
	"github.com/deeprajsshetty/observability-platform/platform/internal/dbengine/oracle"
	"github.com/deeprajsshetty/observability-platform/platform/internal/dbengine/postgresql"
	"github.com/deeprajsshetty/observability-platform/platform/internal/discovery"
	"github.com/deeprajsshetty/observability-platform/platform/internal/discovery/database"
	"github.com/deeprajsshetty/observability-platform/platform/internal/discovery/fixture"
	"github.com/deeprajsshetty/observability-platform/platform/internal/discovery/kubernetes"
	"github.com/deeprajsshetty/observability-platform/platform/internal/metrics"
	"github.com/deeprajsshetty/observability-platform/platform/internal/metrics/dbmetrics"
	"github.com/deeprajsshetty/observability-platform/platform/internal/metrics/kubemetrics"
	"github.com/deeprajsshetty/observability-platform/platform/internal/metrics/simulated"
	"github.com/deeprajsshetty/observability-platform/platform/internal/netguard"
	"github.com/deeprajsshetty/observability-platform/platform/internal/retry"
)

// Deps are the shared services a module may use.
type Deps struct {
	Config config.Config
	Egress netguard.Policy
}

// Contribution is what a module adds.
type Contribution struct {
	Providers  []discovery.Provider
	Collectors map[string]metrics.Collector // keyed by provider name
}

// Module is one integration bundle.
type Module interface {
	Name() string
	Description() string
	Build(Deps) Contribution
}

// Info describes an installed module for the API.
type Info struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Providers   []string `json:"providers"`
}

// Func adapts a function to Module.
type Func struct {
	ModuleName, Desc string
	BuildFunc        func(Deps) Contribution
}

func (f Func) Name() string              { return f.ModuleName }
func (f Func) Description() string       { return f.Desc }
func (f Func) Build(d Deps) Contribution { return f.BuildFunc(d) }

// DatabaseEngines lists the compiled-in database engines. Add an engine here.
func DatabaseEngines() *dbengine.Registry {
	return dbengine.NewRegistry(postgresql.Engine{}, oracle.Engine{})
}

// Default returns the modules compiled into this build.
func Default() []Module {
	return []Module{
		Func{"fixture", "Deterministic simulated estate for offline demos and tests", func(Deps) Contribution {
			return Contribution{Providers: []discovery.Provider{fixture.New()}, Collectors: map[string]metrics.Collector{fixture.Name: simulated.Collector{}}}
		}},
		Func{"kubernetes", "Kubernetes API discovery and metrics.k8s.io usage", func(d Deps) Contribution {
			return Contribution{
				Providers:  []discovery.Provider{&kubernetes.Provider{Policy: d.Egress, PageTimeout: d.Config.DiscoveryPageTimeout, Retry: retry.Default}},
				Collectors: map[string]metrics.Collector{kubernetes.Name: &kubemetrics.Collector{Policy: d.Egress, Timeout: d.Config.DiscoveryPageTimeout, Retry: retry.Default}},
			}
		}},
		Func{"database", "Generic database server integration (engines: PostgreSQL, Oracle)", func(d Deps) Contribution {
			p := &database.Provider{Engines: DatabaseEngines(), Policy: d.Egress, DialTimeout: d.Config.DiscoveryPageTimeout}
			return Contribution{Providers: []discovery.Provider{p}, Collectors: map[string]metrics.Collector{database.Name: &dbmetrics.Collector{Provider: p}}}
		}},
	}
}

// Installed is the result of building a module set.
type Installed struct {
	Providers  *discovery.Registry
	Collectors map[string]metrics.Collector
	Modules    []Info
}

// Install builds modules and rejects duplicate provider names or collectors
// without a provider, so a mis-wired module fails at start-up.
func Install(mods []Module, d Deps) (Installed, error) {
	var providers []discovery.Provider
	seen := map[string]string{}
	collectors := map[string]metrics.Collector{}
	var infos []Info
	for _, m := range mods {
		c := m.Build(d)
		info := Info{Name: m.Name(), Description: m.Description(), Providers: []string{}}
		for _, p := range c.Providers {
			n := p.Info().Name
			if other, dup := seen[n]; dup {
				return Installed{}, fmt.Errorf("modules %q and %q both provide %q", other, m.Name(), n)
			}
			seen[n] = m.Name()
			providers = append(providers, p)
			info.Providers = append(info.Providers, n)
		}
		for n, col := range c.Collectors {
			if seen[n] != m.Name() {
				return Installed{}, fmt.Errorf("module %q registers a collector for provider %q it does not provide", m.Name(), n)
			}
			collectors[n] = col
		}
		infos = append(infos, info)
	}
	sort.Slice(infos, func(i, j int) bool { return infos[i].Name < infos[j].Name })
	return Installed{Providers: discovery.NewRegistry(providers...), Collectors: collectors, Modules: infos}, nil
}
