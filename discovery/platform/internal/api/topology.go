package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/deeprajsshetty/observability-platform/platform/internal/domain/catalog"
	dm "github.com/deeprajsshetty/observability-platform/platform/internal/domain/metrics"
	"github.com/deeprajsshetty/observability-platform/platform/internal/domain/topology"
	"github.com/deeprajsshetty/observability-platform/platform/internal/httpx"
	"github.com/deeprajsshetty/observability-platform/platform/internal/store"
)

// Freshness classifies how recently a resource was observed.
type Freshness string

const (
	Fresh Freshness = "fresh" // seen within OBS_FRESHNESS_WINDOW
	Aging Freshness = "aging" // seen within OBS_STALE_AFTER
	Stale Freshness = "stale" // older, or missing from the latest complete run
)

type resourceView struct {
	topology.Resource
	Domain             string    `json:"domain"`
	Freshness          Freshness `json:"freshness"`
	AgeSeconds         int64     `json:"ageSeconds"`
	MetricCapabilities []string  `json:"metricCapabilities"`
}

func (a *API) resView(r topology.Resource) resourceView {
	age := a.Now().Sub(r.LastSeenAt)
	f := Fresh
	switch {
	case r.Status == topology.StatusMissing || age > a.Config.StaleAfter:
		f = Stale
	case age > a.Config.FreshnessWindow:
		f = Aging
	}
	caps := []string{}
	for _, d := range dm.ForType(r.Type) {
		caps = append(caps, d.Name)
	}
	return resourceView{Resource: r, Domain: catalog.DomainOf(r.Type), Freshness: f, AgeSeconds: int64(age.Seconds()), MetricCapabilities: caps}
}

func (a *API) resourceFilter(r *http.Request, v *validator) store.ResourceFilter {
	q := r.URL.Query()
	f := store.ResourceFilter{
		Query: q.Get("q"), Type: q.Get("type"), Provider: q.Get("provider"), Region: q.Get("region"),
		Status: q.Get("status"), ConnectionID: optID(r, v, "connectionId", "con"),
	}
	if d := q.Get("domain"); d != "" {
		if !catalog.HasDomain(d) || d == "other" {
			v.add("domain", "is not a filterable domain")
		} else {
			f.Types = catalog.TypesInDomain(d)
		}
	}
	if len(f.Query) > 128 {
		v.add("q", "must be at most 128 characters")
	}
	if f.Type != "" && !topology.ValidType(f.Type) {
		v.add("type", "is not a valid resource type")
	}
	if f.Provider != "" && !topology.ValidProvider(f.Provider) {
		v.add("provider", "is not a valid provider name")
	}
	if len(f.Region) > 64 {
		v.add("region", "must be at most 64 characters")
	}
	if f.Status != "" && !topology.ValidStatus(f.Status) {
		v.add("status", "must be active or missing")
	}
	if l := q.Get("label"); l != "" {
		k, val, ok := cut(l, '=')
		if !ok || k == "" || len(k) > topology.MaxLabelKeyLen || len(val) > topology.MaxLabelValueLen {
			v.add("label", "must be key=value")
		}
		f.LabelKey, f.LabelValue = k, val
	}
	return f
}

func cut(s string, sep byte) (string, string, bool) {
	for i := 0; i < len(s); i++ {
		if s[i] == sep {
			return s[:i], s[i+1:], true
		}
	}
	return s, "", false
}

func (a *API) listResources(w http.ResponseWriter, r *http.Request) {
	v := &validator{}
	pg := a.page(r, v)
	f := a.resourceFilter(r, v)
	if !v.ok() {
		v.fail(w, r)
		return
	}
	rs, next, err := a.Store.ListResources(r.Context(), principal(r).TenantID, f, pg)
	if err != nil {
		a.storeError(w, r, err, "resource")
		return
	}
	out := make([]resourceView, len(rs))
	for i, x := range rs {
		out[i] = a.resView(x)
	}
	httpx.JSON(w, http.StatusOK, list(out, next))
}

func (a *API) loadResource(w http.ResponseWriter, r *http.Request) (topology.Resource, bool) {
	v := &validator{}
	id := pathID(r, v, "res")
	if !v.ok() {
		v.fail(w, r)
		return topology.Resource{}, false
	}
	res, err := a.Store.GetResource(r.Context(), principal(r).TenantID, id)
	if err != nil {
		a.storeError(w, r, err, "resource")
		return res, false
	}
	return res, true
}

func (a *API) getResource(w http.ResponseWriter, r *http.Request) {
	res, ok := a.loadResource(w, r)
	if !ok {
		return
	}
	httpx.JSON(w, http.StatusOK, a.resView(res))
}

type subgraphView struct {
	Root      string          `json:"rootId"`
	Depth     int             `json:"depth"`
	Direction string          `json:"direction"`
	Nodes     []resourceView  `json:"nodes"`
	Edges     []topology.Edge `json:"edges"`
	Truncated bool            `json:"truncated"`
}

func intParam(r *http.Request, v *validator, name string, def, min, max int) int {
	s := r.URL.Query().Get(name)
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < min || n > max {
		v.add(name, "must be an integer between "+strconv.Itoa(min)+" and "+strconv.Itoa(max))
		return def
	}
	return n
}

// neighborhood runs a bounded BFS from a resource, one store query per hop.
func (a *API) neighborhood(w http.ResponseWriter, r *http.Request) {
	res, ok := a.loadResource(w, r)
	if !ok {
		return
	}
	v := &validator{}
	depth := intParam(r, v, "depth", 1, 1, 4)
	maxNodes := intParam(r, v, "maxNodes", 100, 1, 500)
	dir := r.URL.Query().Get("direction")
	if dir == "" {
		dir = string(topology.DirBoth)
	}
	if !topology.ValidDirection(dir) {
		v.add("direction", "must be in, out or both")
	}
	includeMissing := r.URL.Query().Get("includeMissing") == "true"
	if !v.ok() {
		v.fail(w, r)
		return
	}
	tenant := principal(r).TenantID
	sub, err := topology.BFS(res.ID, depth, maxNodes, topology.Direction(dir), func(ids []string) ([]topology.Edge, error) {
		return a.Store.EdgesTouching(r.Context(), tenant, ids, includeMissing)
	})
	if err != nil {
		a.storeError(w, r, err, "relationship")
		return
	}
	nodes, err := a.Store.GetResources(r.Context(), tenant, sub.Nodes)
	if err != nil {
		a.storeError(w, r, err, "resource")
		return
	}
	out := subgraphView{Root: res.ID, Depth: depth, Direction: dir, Edges: sub.Edges, Truncated: sub.Truncated, Nodes: make([]resourceView, len(nodes))}
	if out.Edges == nil {
		out.Edges = []topology.Edge{}
	}
	for i, n := range nodes {
		out.Nodes[i] = a.resView(n)
	}
	httpx.JSON(w, http.StatusOK, out)
}

func (a *API) listRelationships(w http.ResponseWriter, r *http.Request) {
	v := &validator{}
	pg := a.page(r, v)
	q := r.URL.Query()
	f := store.RelationshipFilter{Type: q.Get("type"), Status: q.Get("status"), ResourceID: optID(r, v, "resourceId", "res"), ConnectionID: optID(r, v, "connectionId", "con")}
	if _, ok := topology.Spec(topology.RelationType(f.Type)); f.Type != "" && !ok {
		v.add("type", "unknown relationship type")
	}
	if f.Status != "" && !topology.ValidStatus(f.Status) {
		v.add("status", "must be active or missing")
	}
	if !v.ok() {
		v.fail(w, r)
		return
	}
	rels, next, err := a.Store.ListRelationships(r.Context(), principal(r).TenantID, f, pg)
	if err != nil {
		a.storeError(w, r, err, "relationship")
		return
	}
	httpx.JSON(w, http.StatusOK, list(rels, next))
}

func (a *API) relationshipTypes(w http.ResponseWriter, _ *http.Request) {
	httpx.JSON(w, http.StatusOK, list(topology.RelationSpecs(), ""))
}

func (a *API) summary(w http.ResponseWriter, r *http.Request) {
	v := &validator{}
	conn := optID(r, v, "connectionId", "con")
	if !v.ok() {
		v.fail(w, r)
		return
	}
	s, err := a.Store.Summary(r.Context(), principal(r).TenantID, conn)
	if err != nil {
		a.storeError(w, r, err, "topology")
		return
	}
	httpx.JSON(w, http.StatusOK, s)
}

type graphView struct {
	Nodes     []resourceView  `json:"nodes"`
	Edges     []topology.Edge `json:"edges"`
	Truncated bool            `json:"truncated"`
	Cycles    struct {
		Acyclic   int `json:"acyclicTypes"`    // must be 0; containment/composition are DAGs
		Dependent int `json:"dependencyTypes"` // permitted; reported for operators
	} `json:"cycleBackEdges"`
}

// graph returns a bounded node/edge set for visualization plus cycle
// diagnostics computed with DFS back-edge detection.
func (a *API) graph(w http.ResponseWriter, r *http.Request) {
	v := &validator{}
	f := a.resourceFilter(r, v)
	maxNodes := intParam(r, v, "maxNodes", 300, 1, 1000)
	if f.Status == "" && r.URL.Query().Get("includeMissing") != "true" {
		f.Status = string(topology.StatusActive)
	}
	if !v.ok() {
		v.fail(w, r)
		return
	}
	nodes, edges, truncated, err := a.Store.Graph(r.Context(), principal(r).TenantID, f, maxNodes)
	if err != nil {
		a.storeError(w, r, err, "topology")
		return
	}
	g := topology.NewGraph()
	for _, n := range nodes {
		g.AddNode(n.ID)
	}
	for _, e := range edges {
		g.AddEdge(e)
	}
	out := graphView{Nodes: make([]resourceView, len(nodes)), Edges: edges, Truncated: truncated}
	if out.Edges == nil {
		out.Edges = []topology.Edge{}
	}
	for i, n := range nodes {
		out.Nodes[i] = a.resView(n)
	}
	out.Cycles.Acyclic = len(g.BackEdges(topology.IsAcyclic))
	out.Cycles.Dependent = len(g.BackEdges(func(t topology.RelationType) bool { return !topology.IsAcyclic(t) }))
	httpx.JSON(w, http.StatusOK, out)
}

func (a *API) changes(w http.ResponseWriter, r *http.Request) {
	v := &validator{}
	pg := a.page(r, v)
	f := store.ChangeFilter{JobID: optID(r, v, "jobId", "job"), ResourceID: optID(r, v, "resourceId", "res")}
	if !v.ok() {
		v.fail(w, r)
		return
	}
	cs, next, err := a.Store.ListChanges(r.Context(), principal(r).TenantID, f, pg)
	if err != nil {
		a.storeError(w, r, err, "change")
		return
	}
	httpx.JSON(w, http.StatusOK, list(cs, next))
}

// ----------------------------------------------------------------- metrics

func (a *API) metricDefinitions(w http.ResponseWriter, _ *http.Request) {
	httpx.JSON(w, http.StatusOK, list(dm.All(), ""))
}

type metricView struct {
	dm.Definition
	Latest *dm.Sample `json:"latest,omitempty"`
}

func (a *API) resourceMetrics(w http.ResponseWriter, r *http.Request) {
	res, ok := a.loadResource(w, r)
	if !ok {
		return
	}
	latest, err := a.Store.LatestSamples(r.Context(), res.TenantID, res.ID)
	if err != nil {
		a.storeError(w, r, err, "metrics")
		return
	}
	out := []metricView{}
	for _, d := range dm.ForType(res.Type) {
		mv := metricView{Definition: d}
		if s, ok := latest[d.Name]; ok {
			s := s
			mv.Latest = &s
		}
		out = append(out, mv)
	}
	httpx.JSON(w, http.StatusOK, list(out, ""))
}

const maxPoints = 1500

type seriesView struct {
	ResourceID  string        `json:"resourceId"`
	Metric      string        `json:"metric"`
	Unit        string        `json:"unit"`
	Description string        `json:"description"`
	From        time.Time     `json:"from"`
	To          time.Time     `json:"to"`
	StepSeconds int64         `json:"stepSeconds"`
	Sources     []dm.Source   `json:"sources"`
	Simulated   bool          `json:"simulated"`
	Points      []store.Point `json:"points"`
}

func (a *API) metricSeries(w http.ResponseWriter, r *http.Request) {
	res, ok := a.loadResource(w, r)
	if !ok {
		return
	}
	v := &validator{}
	name := r.PathValue("metric")
	def, known := dm.Lookup(name)
	if !known {
		v.add("metric", "unknown metric")
	} else if !contains(dm.ForType(res.Type), name) {
		v.add("metric", "metric does not apply to resource type "+res.Type)
	}
	q := r.URL.Query()
	to := a.Now()
	from := to.Add(-time.Hour)
	parseT := func(k string, dst *time.Time) {
		if s := q.Get(k); s != "" {
			t, err := time.Parse(time.RFC3339, s)
			if err != nil {
				v.add(k, "must be an RFC 3339 timestamp, for example 2026-11-17T10:00:00Z")
				return
			}
			*dst = t.UTC()
		}
	}
	parseT("from", &from)
	parseT("to", &to)
	if !from.Before(to) {
		v.add("from", "must be before to")
	} else if to.Sub(from) > 7*24*time.Hour {
		v.add("from", "range must not exceed 7 days")
	}
	step := a.Config.MetricsInterval
	if rng := to.Sub(from); rng/300 > step {
		step = (rng / 300).Round(time.Minute)
	}
	if s := q.Get("step"); s != "" {
		d, err := time.ParseDuration(s)
		if err != nil || d < time.Second {
			v.add("step", "must be a duration of at least 1s, for example 60s or 5m")
		} else {
			step = d
		}
	}
	if step > 0 && to.Sub(from)/step > maxPoints {
		v.add("step", "too many points; increase step or narrow the range")
	}
	if !v.ok() {
		v.fail(w, r)
		return
	}
	s, err := a.Store.QuerySeries(r.Context(), res.TenantID, res.ID, name, from, to, step)
	if err != nil {
		a.storeError(w, r, err, "metrics")
		return
	}
	sim := false
	for _, src := range s.Sources {
		if src == dm.SourceSimulated {
			sim = true
		}
	}
	httpx.JSON(w, http.StatusOK, seriesView{ResourceID: res.ID, Metric: name, Unit: def.Unit, Description: def.Description,
		From: from, To: to, StepSeconds: int64(step.Seconds()), Sources: s.Sources, Simulated: sim, Points: s.Points})
}

func contains(ds []dm.Definition, name string) bool {
	for _, d := range ds {
		if d.Name == name {
			return true
		}
	}
	return false
}
