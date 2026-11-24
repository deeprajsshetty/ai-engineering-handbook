package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/deeprajsshetty/observability-platform/platform/contracts"
	"github.com/deeprajsshetty/observability-platform/platform/internal/app"
	"github.com/deeprajsshetty/observability-platform/platform/internal/config"
	"github.com/deeprajsshetty/observability-platform/platform/internal/discovery/fixture"
	"github.com/deeprajsshetty/observability-platform/platform/internal/domain/topology"
	"github.com/deeprajsshetty/observability-platform/platform/internal/store"
	"github.com/deeprajsshetty/observability-platform/platform/internal/testdb"
	"github.com/deeprajsshetty/observability-platform/platform/internal/testdb/oratest"
)

func TestMain(m *testing.M) { testdb.Main(m) }

func TestOpenAPIMatchesRouteTable(t *testing.T) {
	var doc struct {
		Paths map[string]map[string]struct {
			Permission string `yaml:"x-permission"`
		} `yaml:"paths"`
	}
	if err := yaml.Unmarshal(contracts.OpenAPI, &doc); err != nil {
		t.Fatalf("openapi.yaml is not valid YAML: %v", err)
	}
	spec := map[string]string{}
	for p, ops := range doc.Paths {
		for m, op := range ops {
			spec[strings.ToUpper(m)+" "+p] = op.Permission
		}
	}
	a, _ := newApp(t, false)
	impl := map[string]string{"GET /metrics": ""}
	for _, r := range a.API.Routes() {
		perm := string(r.Perm)
		switch perm {
		case "-":
			perm = ""
		case "":
			perm = "authenticated"
		}
		impl[r.Method+" "+r.Path] = perm
	}
	var missing, extra, permDiff []string
	for k, perm := range impl {
		sp, ok := spec[k]
		if !ok {
			missing = append(missing, k)
		} else if sp != perm {
			permDiff = append(permDiff, fmt.Sprintf("%s: spec %q, code %q", k, sp, perm))
		}
	}
	for k := range spec {
		if _, ok := impl[k]; !ok {
			extra = append(extra, k)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	if len(missing)+len(extra)+len(permDiff) > 0 {
		t.Fatalf("OpenAPI drift\nnot in spec: %v\nnot implemented: %v\npermission mismatch: %v", missing, extra, permDiff)
	}
}

func TestStaticUIAndFallbacks(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "index.html"), []byte(`<div id="root"></div>`), 0o644)
	os.WriteFile(filepath.Join(dir, "assets", "app.js"), []byte(`console.log(1)`), 0o644)
	cfg := testConfig(t)
	cfg.WebDir = dir
	a, err := app.New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(a.API.Handler())
	defer srv.Close()
	get := func(method, path string) (int, string, http.Header) {
		req, _ := http.NewRequest(method, srv.URL+path, nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b), resp.Header
	}
	if s, b, h := get("GET", "/"); s != 200 || !strings.Contains(b, "root") || h.Get("Content-Security-Policy") == "" {
		t.Fatalf("index: %d %q", s, b)
	}
	if s, b, _ := get("GET", "/topology/deep/link"); s != 200 || !strings.Contains(b, "root") {
		t.Fatalf("SPA fallback: %d", s)
	}
	if s, _, h := get("GET", "/assets/app.js"); s != 200 || !strings.Contains(h.Get("Cache-Control"), "immutable") {
		t.Fatalf("asset: %d %v", s, h)
	}
	if s, _, _ := get("GET", "/assets/missing.js"); s != 404 {
		t.Fatalf("missing asset must 404, got %d", s)
	}
	if s, b, _ := get("GET", "/api/v2/anything"); s != 404 || !strings.Contains(b, "not_found") {
		t.Fatalf("unknown API path: %d %s", s, b)
	}
	if s, _, _ := get("POST", "/"); s != 405 {
		t.Fatalf("POST to static: %d", s)
	}
	if s, _, _ := get("GET", "/../../etc/passwd"); s == 200 {
		if _, b, _ := get("GET", "/../../etc/passwd"); strings.Contains(b, "root:") {
			t.Fatal("path traversal")
		}
	}
	if s, _, _ := get("GET", "/healthz"); s != 200 {
		t.Fatalf("healthz with web: %d", s)
	}
}

func testConfig(t testing.TB) config.Config {
	env := map[string]string{
		"OBS_DATABASE_URL":            "postgres://unused@localhost/unused",
		"OBS_DISCOVERY_POLL_INTERVAL": "50ms",
		"OBS_JOB_LEASE_TIMEOUT":       "3s",
		"OBS_METRICS_INTERVAL":        "60s",
		"OBS_RATE_LIMIT_RPS":          "1000",
		"OBS_RATE_LIMIT_BURST":        "1000",
		"OBS_LOG_LEVEL":               "error",
	}
	cfg, err := config.LoadFrom(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func newApp(t *testing.T, withDB bool) (*app.App, *httptest.Server) {
	t.Helper()
	cfg := testConfig(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	if !withDB {
		a, err := app.New(cfg, log, nil)
		if err != nil {
			t.Fatal(err)
		}
		return a, nil
	}
	// OBS_TEST_STORE=oracle runs the whole API suite against Oracle (ADR 0011).
	var st store.Backend
	if os.Getenv("OBS_TEST_STORE") == "oracle" {
		st = oratest.New(t)
	} else {
		st = testdb.New(t)
	}
	a, err := app.New(cfg, log, st)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { a.Runner.Run(ctx); close(done) }()
	srv := httptest.NewServer(a.API.Handler())
	t.Cleanup(func() {
		srv.Close()
		cancel()
		<-done
		a.Metrics.Wait()
	})
	return a, srv
}

type client struct {
	t     *testing.T
	base  string
	token string
}

func (c client) do(method, path string, body any, hdr map[string]string) (int, map[string]any, http.Header) {
	c.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, c.base+path, rd)
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out, resp.Header
}

func (c client) get(path string) (int, map[string]any) {
	s, b, _ := c.do("GET", path, nil, nil)
	return s, b
}

func (c client) must(status int, method, path string, body any) map[string]any {
	c.t.Helper()
	s, b, _ := c.do(method, path, body, nil)
	if s != status {
		c.t.Fatalf("%s %s: status %d, want %d: %v", method, path, s, status, b)
	}
	return b
}

func items(b map[string]any) []map[string]any {
	raw, _ := b["items"].([]any)
	out := make([]map[string]any, 0, len(raw))
	for _, x := range raw {
		out = append(out, x.(map[string]any))
	}
	return out
}

func (c client) waitJob(id string, want string) map[string]any {
	c.t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		_, j := c.get("/api/v1/discovery-jobs/" + id)
		if s, _ := j["status"].(string); s == want {
			return j
		} else if s == "succeeded" || s == "failed" || s == "cancelled" {
			c.t.Fatalf("job %s ended %s, want %s: %v", id, s, want, j)
		}
		time.Sleep(50 * time.Millisecond)
	}
	c.t.Fatalf("job %s did not reach %s", id, want)
	return nil
}

func num(m map[string]any, path ...string) float64 {
	var cur any = m
	for _, p := range path {
		cur = cur.(map[string]any)[p]
	}
	f, _ := cur.(float64)
	return f
}

func TestEndToEndDiscoveryTopologyMetrics(t *testing.T) {
	_, srv := newApp(t, true)
	admin := client{t, srv.URL, "dev-admin"}
	viewer := client{t, srv.URL, "dev-viewer"}
	acme := client{t, srv.URL, "dev-acme-admin"}
	anon := client{t, srv.URL, ""}

	// Health, auth and authorization boundaries.
	if s, _ := anon.get("/healthz"); s != 200 {
		t.Fatalf("healthz %d", s)
	}
	if s, b := anon.get("/readyz"); s != 200 {
		t.Fatalf("readyz %d %v", s, b)
	}
	if s, b := anon.get("/api/v1/me"); s != 401 || b["code"] != "unauthenticated" || b["requestId"] == "" {
		t.Fatalf("anonymous: %d %v", s, b)
	}
	if s, _ := (client{t, srv.URL, "wrong-token"}).get("/api/v1/me"); s != 401 {
		t.Fatalf("bad token: %d", s)
	}
	me := admin.must(200, "GET", "/api/v1/me", nil)
	if me["tenantId"] != "demo" || me["authMode"] != "dev" {
		t.Fatalf("me: %v", me)
	}
	if s, b, _ := viewer.do("POST", "/api/v1/connections", map[string]any{"name": "x", "provider": "fixture"}, nil); s != 403 {
		t.Fatalf("viewer create: %d %v", s, b)
	}

	// Validation: unknown provider, bad name, secret in config, unknown field, bad credential ref.
	s, b, _ := admin.do("POST", "/api/v1/connections", map[string]any{"name": "", "provider": "nope"}, nil)
	if s != 400 || b["code"] != "validation_failed" || len(b["errors"].([]any)) < 2 {
		t.Fatalf("validation: %d %v", s, b)
	}
	s, b, _ = admin.do("POST", "/api/v1/connections", map[string]any{"name": "k", "provider": "kubernetes",
		"config": map[string]any{"server": "https://169.254.169.254", "clusterName": "c", "apiToken": "hunter2"}, "credentialRef": "env:DATABASE_URL"}, nil)
	if s != 400 {
		t.Fatalf("unsafe kubernetes connection accepted: %d %v", s, b)
	}
	if bs, _ := json.Marshal(b); strings.Contains(string(bs), "hunter2") {
		t.Fatal("error response echoed a secret value")
	}
	if s, _, _ := admin.do("POST", "/api/v1/connections", map[string]any{"name": "x", "provider": "fixture", "bogus": 1}, nil); s != 400 {
		t.Fatalf("unknown body field accepted: %d", s)
	}

	conn := admin.must(201, "POST", "/api/v1/connections", map[string]any{
		"name": "demo", "provider": "fixture", "config": map[string]any{"pageSize": 7, "pageDelayMs": 0}})
	connID := conn["id"].(string)
	if conn["simulated"] != true || conn["metricsSource"] != "simulated" {
		t.Fatalf("connection view: %v", conn)
	}
	if s, _, _ := admin.do("POST", "/api/v1/connections", map[string]any{"name": "demo", "provider": "fixture"}, nil); s != 409 {
		t.Fatalf("duplicate name: %d", s)
	}
	if v := admin.must(200, "POST", "/api/v1/connections/"+connID+"/validate", nil); v["valid"] != true {
		t.Fatalf("validate: %v", v)
	}

	// Discovery run 1, with idempotent replay.
	hdr := map[string]string{"Idempotency-Key": "demo-run-1"}
	s, job, h := admin.do("POST", "/api/v1/connections/"+connID+"/discovery-jobs", nil, hdr)
	if s != 202 || h.Get("Location") == "" {
		t.Fatalf("start job: %d %v", s, job)
	}
	jobID := job["id"].(string)
	if s, again, _ := admin.do("POST", "/api/v1/connections/"+connID+"/discovery-jobs", nil, hdr); s != 200 || again["id"] != jobID {
		t.Fatalf("idempotent replay: %d %v", s, again)
	}
	j := admin.waitJob(jobID, "succeeded")
	want := len(fixture.Estate("sim-acct-0001", 1).Resources)
	if int(num(j, "counts", "added")) != want || j["complete"] != true || int(num(j, "progress", "pages")) < 4 {
		t.Fatalf("job 1: want %d added: %v", want, j)
	}
	if evs := items(admin.must(200, "GET", "/api/v1/discovery-jobs/"+jobID+"/events", nil)); len(evs) < 3 {
		t.Fatalf("events: %v", evs)
	}

	// Topology queries.
	sum := admin.must(200, "GET", "/api/v1/topology/summary", nil)
	if int(num(sum, "activeResources")) != want || num(sum, "relationships") < 30 {
		t.Fatalf("summary: %v", sum)
	}
	var instances []map[string]any
	cursor := ""
	for {
		page := admin.must(200, "GET", "/api/v1/resources?type=compute.instance&limit=3&cursor="+url.QueryEscape(cursor), nil)
		instances = append(instances, items(page)...)
		next, _ := page["nextCursor"].(string)
		if next == "" {
			break
		}
		cursor = next
	}
	if len(instances) != 11 {
		t.Fatalf("paged instances: %d", len(instances))
	}
	web := items(admin.must(200, "GET", "/api/v1/resources?q=east-web-01", nil))
	if len(web) != 1 || web[0]["freshness"] != "fresh" || len(web[0]["metricCapabilities"].([]any)) == 0 {
		t.Fatalf("search: %v", web)
	}
	webID := web[0]["id"].(string)
	vpc := items(admin.must(200, "GET", "/api/v1/resources?q=vpc-east&type=network.vpc", nil))[0]
	nb := admin.must(200, "GET", "/api/v1/resources/"+vpc["id"].(string)+"/neighborhood?depth=2&direction=out", nil)
	if len(nb["nodes"].([]any)) < 10 || len(nb["edges"].([]any)) < 9 {
		t.Fatalf("neighborhood: nodes=%d edges=%d", len(nb["nodes"].([]any)), len(nb["edges"].([]any)))
	}
	g := admin.must(200, "GET", "/api/v1/topology/graph", nil)
	if num(g, "cycleBackEdges", "acyclicTypes") != 0 || len(g["nodes"].([]any)) != want {
		t.Fatalf("graph: %v", g["cycleBackEdges"])
	}
	rels := items(admin.must(200, "GET", "/api/v1/relationships?type=routes_to&resourceId="+webID, nil))
	if len(rels) != 1 || rels[0]["targetId"] != webID || rels[0]["sourceType"] != "network.load_balancer" {
		t.Fatalf("relationships: %v", rels)
	}

	// Discovery triggers simulated metrics collection with backfill.
	deadline := time.Now().Add(15 * time.Second)
	for {
		runs := items(admin.must(200, "GET", "/api/v1/metric-collections?connectionId="+connID, nil))
		if len(runs) > 0 && runs[0]["status"] == "succeeded" && num(runs[0], "samples") > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no successful collection: %v", runs)
		}
		time.Sleep(100 * time.Millisecond)
	}
	ms := items(admin.must(200, "GET", "/api/v1/resources/"+webID+"/metrics", nil))
	var cpu map[string]any
	for _, m := range ms {
		if m["name"] == "cpu.utilization" {
			cpu = m
		}
	}
	if cpu == nil || cpu["unit"] != "%" || cpu["latest"].(map[string]any)["source"] != "simulated" {
		t.Fatalf("metric capabilities: %v", ms)
	}
	series := admin.must(200, "GET", "/api/v1/resources/"+webID+"/metrics/cpu.utilization", nil)
	if series["simulated"] != true || len(series["points"].([]any)) < 30 {
		t.Fatalf("series: simulated=%v points=%d", series["simulated"], len(series["points"].([]any)))
	}
	if s, b := admin.get("/api/v1/resources/" + webID + "/metrics/db.connections"); s != 400 {
		t.Fatalf("inapplicable metric: %d %v", s, b)
	}
	// Generic catalog, faceted search and fleet tables.
	cat := admin.must(200, "GET", "/api/v1/catalog", nil)
	if len(cat["domains"].([]any)) < 5 || len(cat["resourceTypes"].([]any)) < 10 || len(cat["modules"].([]any)) != 3 {
		t.Fatalf("catalog: domains=%d types=%d modules=%v", len(cat["domains"].([]any)), len(cat["resourceTypes"].([]any)), cat["modules"])
	}
	fac := admin.must(200, "GET", "/api/v1/resources/facets?domain=database&status=active", nil)["facets"].(map[string]any)
	engines := map[string]float64{}
	for _, e := range fac["engine"].([]any) {
		engines[e.(map[string]any)["value"].(string)] = e.(map[string]any)["count"].(float64)
	}
	if engines["oracle"] != 6 || engines["postgresql"] != 4 {
		t.Fatalf("engine facet under domain=database: %v", engines)
	}
	domains := map[string]bool{}
	for _, d := range fac["domain"].([]any) {
		domains[d.(map[string]any)["value"].(string)] = true
	}
	if !domains["compute"] || !domains["database"] {
		t.Fatalf("domain facet must ignore the domain filter itself: %v", fac["domain"])
	}
	if s, _ := admin.get("/api/v1/resources/facets?domain=nope"); s != 400 {
		t.Fatalf("unknown domain: %d", s)
	}
	fl := admin.must(200, "GET", "/api/v1/fleet?type=database.instance&label=engine%3Doracle", nil)
	rows := fl["items"].([]any)
	if len(rows) != 1 || rows[0].(map[string]any)["domain"] != "database" || len(fl["metrics"].([]any)) < 4 {
		t.Fatalf("fleet: %v", fl)
	}
	if lat := rows[0].(map[string]any)["latest"].(map[string]any); lat["db.connections"] == nil {
		t.Fatalf("fleet latest values: %v", lat)
	}
	if s, _ := admin.get("/api/v1/fleet"); s != 400 {
		t.Fatalf("fleet without type: %d", s)
	}

	// Manual collection is idempotent: nothing new for already-collected timestamps.
	run := admin.must(202, "POST", "/api/v1/connections/"+connID+"/metric-collections", nil)
	for i := 0; i < 100; i++ {
		r := admin.must(200, "GET", "/api/v1/metric-collections/"+run["id"].(string), nil)
		if r["status"] != "running" {
			if r["status"] != "succeeded" {
				t.Fatalf("manual collection: %v", r)
			}
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Run 2: drift. web-03 (east) disappears, batch-01 appears, db label changes.
	job2 := admin.must(202, "POST", "/api/v1/connections/"+connID+"/discovery-jobs", nil)
	j2 := admin.waitJob(job2["id"].(string), "succeeded")
	if num(j2, "counts", "missing") != 1 || num(j2, "counts", "added") != 1 || num(j2, "counts", "changed") != 1 {
		t.Fatalf("drift counts: %v", j2["counts"])
	}
	gone := items(admin.must(200, "GET", "/api/v1/resources?q=i-east-web-03", nil))
	if len(gone) != 1 || gone[0]["status"] != "missing" || gone[0]["freshness"] != "stale" || gone[0]["missingSince"] == nil {
		t.Fatalf("missing resource: %v", gone)
	}
	changes := items(admin.must(200, "GET", "/api/v1/topology/changes?jobId="+job2["id"].(string), nil))
	kinds := map[string]int{}
	for _, c := range changes {
		kinds[c["kind"].(string)]++
	}
	if kinds["missing"] != 1 || kinds["added"] != 1 || kinds["changed"] != 1 {
		t.Fatalf("changes: %v", kinds)
	}

	// Tenant isolation.
	if s, _ := acme.get("/api/v1/resources/" + webID); s != 404 {
		t.Fatalf("cross-tenant resource read: %d", s)
	}
	if l := items(acme.must(200, "GET", "/api/v1/resources", nil)); len(l) != 0 {
		t.Fatalf("cross-tenant list: %d", len(l))
	}
	if s, _ := acme.get("/api/v1/discovery-jobs/" + jobID); s != 404 {
		t.Fatalf("cross-tenant job read: %d", s)
	}

	// Audit.
	if s, _ := viewer.get("/api/v1/audit-events"); s != 403 {
		t.Fatalf("viewer audit: %d", s)
	}
	acts := map[string]bool{}
	for _, e := range items(admin.must(200, "GET", "/api/v1/audit-events", nil)) {
		acts[e["action"].(string)] = true
	}
	for _, a := range []string{"connection.create", "connection.validate", "discovery.start", "metrics.collect"} {
		if !acts[a] {
			t.Errorf("audit event %s missing: %v", a, acts)
		}
	}

	// Error model.
	if s, b := admin.get("/api/v1/resources/not-an-id"); s != 400 || b["errors"] == nil {
		t.Fatalf("bad id: %d %v", s, b)
	}
	if s, b := admin.get("/api/v1/resources?cursor=@@@"); s != 400 || b["code"] != "invalid_cursor" {
		t.Fatalf("bad cursor: %d %v", s, b)
	}
	if s, b := admin.get("/api/v1/nope"); s != 404 || b["code"] != "not_found" {
		t.Fatalf("unknown route: %d %v", s, b)
	}
	if s, _ := admin.get("/api/v1/resources?limit=5000"); s != 400 {
		t.Fatalf("limit not enforced: %d", s)
	}
	_, _, hh := admin.do("GET", "/api/v1/me", nil, map[string]string{"X-Request-ID": "corr-123"})
	if hh.Get("X-Request-ID") != "corr-123" || hh.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("headers: %v", hh)
	}
}

func TestCancelAndFailurePreservePartialState(t *testing.T) {
	_, srv := newApp(t, true)
	admin := client{t, srv.URL, "dev-admin"}

	slow := admin.must(201, "POST", "/api/v1/connections", map[string]any{
		"name": "slow", "provider": "fixture", "config": map[string]any{"pageSize": 2, "pageDelayMs": 400}})
	job := admin.must(202, "POST", "/api/v1/connections/"+slow["id"].(string)+"/discovery-jobs", nil)
	admin.waitJob(job["id"].(string), "running")
	if s, b, _ := admin.do("POST", "/api/v1/connections/"+slow["id"].(string)+"/discovery-jobs", nil, nil); s != 409 || b["extra"].(map[string]any)["activeJobId"] != job["id"] {
		t.Fatalf("second active job: %d %v", s, b)
	}
	admin.must(202, "POST", "/api/v1/discovery-jobs/"+job["id"].(string)+"/cancel", nil)
	j := admin.waitJob(job["id"].(string), "cancelled")
	if j["complete"] != false {
		t.Fatalf("cancelled job must be incomplete: %v", j)
	}

	failing := admin.must(201, "POST", "/api/v1/connections", map[string]any{
		"name": "failing", "provider": "fixture", "config": map[string]any{"account": "sim-acct-fail", "pageSize": 5, "pageDelayMs": 0, "failAtPage": 3, "transientFailures": 2}})
	fj := admin.must(202, "POST", "/api/v1/connections/"+failing["id"].(string)+"/discovery-jobs", nil)
	f := admin.waitJob(fj["id"].(string), "failed")
	if !strings.Contains(f["error"].(string), "injected") || f["complete"] != false || num(f, "counts", "added") != 10 || num(f, "counts", "missing") != 0 {
		t.Fatalf("failed job (transient errors retried, partial state kept): %v", f)
	}
	stored := items(admin.must(200, "GET", "/api/v1/resources?connectionId="+failing["id"].(string)+"&limit=500", nil))
	if len(stored) != 10 {
		t.Fatalf("partial resources stored: %d", len(stored))
	}
	for _, r := range stored {
		if r["status"] != string(topology.StatusActive) {
			t.Fatal("a failed run marked resources missing")
		}
	}
}
