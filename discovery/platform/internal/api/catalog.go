package api

import (
	"net/http"
	"strings"

	"github.com/deeprajsshetty/observability-platform/platform/internal/domain/catalog"
	dm "github.com/deeprajsshetty/observability-platform/platform/internal/domain/metrics"
	"github.com/deeprajsshetty/observability-platform/platform/internal/domain/topology"
	"github.com/deeprajsshetty/observability-platform/platform/internal/httpx"
	"github.com/deeprajsshetty/observability-platform/platform/internal/modules"
)

type catalogView struct {
	Domains           []catalog.Domain        `json:"domains"`
	ResourceTypes     []catalog.ResourceType  `json:"resourceTypes"`
	MetricDefinitions []dm.Definition         `json:"metricDefinitions"`
	RelationshipTypes []topology.RelationSpec `json:"relationshipTypes"`
	Modules           []modules.Info          `json:"modules"`
}

// catalogHandler serves everything the UI needs to render any resource type
// generically. Adding a module changes this response, not the UI code.
func (a *API) catalogHandler(w http.ResponseWriter, _ *http.Request) {
	mods := a.Modules
	if mods == nil {
		mods = []modules.Info{}
	}
	httpx.JSON(w, http.StatusOK, catalogView{Domains: catalog.Domains(), ResourceTypes: catalog.Types(), MetricDefinitions: dm.All(),
		RelationshipTypes: topology.RelationSpecs(), Modules: mods})
}

type facetsView struct {
	Facets map[string][]facetCount `json:"facets"`
}

type facetCount struct {
	Value string `json:"value"`
	Count int    `json:"count"`
}

// facets returns counts per dimension for the resource explorer. The domain
// facet is derived from the type facet through the catalog.
func (a *API) facets(w http.ResponseWriter, r *http.Request) {
	v := &validator{}
	f := a.resourceFilter(r, v)
	if !v.ok() {
		v.fail(w, r)
		return
	}
	tenant := principal(r).TenantID
	raw, err := a.Store.Facets(r.Context(), tenant, f, 100)
	if err != nil {
		a.storeError(w, r, err, "resource")
		return
	}
	out := facetsView{Facets: map[string][]facetCount{}}
	for k, cs := range raw {
		l := make([]facetCount, len(cs))
		for i, c := range cs {
			l[i] = facetCount{c.Key, c.Count}
		}
		out.Facets[k] = l
	}
	// Domain counts: types counted without any type/domain filter.
	g := f
	g.Type, g.Types = "", nil
	byType, err := a.Store.Facets(r.Context(), tenant, g, 1000)
	if err != nil {
		a.storeError(w, r, err, "resource")
		return
	}
	counts := map[string]int{}
	for _, c := range byType["type"] {
		counts[catalog.DomainOf(c.Key)] += c.Count
	}
	dom := []facetCount{}
	for _, d := range catalog.Domains() {
		if n := counts[d.Name]; n > 0 {
			dom = append(dom, facetCount{d.Name, n})
		}
	}
	out.Facets["domain"] = dom
	httpx.JSON(w, http.StatusOK, out)
}

type fleetRow struct {
	resourceView
	Latest map[string]dm.Sample `json:"latest"`
}

type fleetView struct {
	Metrics    []dm.Definition `json:"metrics"`
	Items      []fleetRow      `json:"items"`
	NextCursor string          `json:"nextCursor,omitempty"`
}

// fleet lists resources with the latest value of selected metrics, the
// generic "entity table" behind domain views such as Databases.
func (a *API) fleet(w http.ResponseWriter, r *http.Request) {
	v := &validator{}
	pg := a.page(r, v)
	f := a.resourceFilter(r, v)
	if f.Type == "" && len(f.Types) == 0 {
		v.add("type", "type or domain is required")
	}
	var defs []dm.Definition
	if s := r.URL.Query().Get("metrics"); s != "" {
		for _, n := range strings.Split(s, ",") {
			d, ok := dm.Lookup(strings.TrimSpace(n))
			if !ok {
				v.add("metrics", "unknown metric "+n)
				continue
			}
			defs = append(defs, d)
		}
		if len(defs) > 12 {
			v.add("metrics", "at most 12 metrics")
		}
	} else if f.Type != "" {
		defs = dm.ForType(f.Type)
	} else {
		seen := map[string]bool{}
		for _, t := range f.Types {
			if k := catalog.Lookup(t).KeyMetric; k != "" && !seen[k] {
				seen[k] = true
				d, _ := dm.Lookup(k)
				defs = append(defs, d)
			}
		}
	}
	if !v.ok() {
		v.fail(w, r)
		return
	}
	tenant := principal(r).TenantID
	rs, next, err := a.Store.ListResources(r.Context(), tenant, f, pg)
	if err != nil {
		a.storeError(w, r, err, "resource")
		return
	}
	idsList := make([]string, len(rs))
	for i, x := range rs {
		idsList[i] = x.ID
	}
	names := make([]string, len(defs))
	for i, d := range defs {
		names[i] = d.Name
	}
	latest, err := a.Store.LatestSamplesFor(r.Context(), tenant, idsList, names)
	if err != nil {
		a.storeError(w, r, err, "metrics")
		return
	}
	out := fleetView{Metrics: defs, Items: make([]fleetRow, len(rs)), NextCursor: next}
	if out.Metrics == nil {
		out.Metrics = []dm.Definition{}
	}
	for i, x := range rs {
		l := latest[x.ID]
		if l == nil {
			l = map[string]dm.Sample{}
		}
		out.Items[i] = fleetRow{resourceView: a.resView(x), Latest: l}
	}
	httpx.JSON(w, http.StatusOK, out)
}
