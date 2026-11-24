package database_test

import (
	"context"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/deeprajsshetty/observability-platform/platform/internal/discovery"
	"github.com/deeprajsshetty/observability-platform/platform/internal/discovery/database"
	dm "github.com/deeprajsshetty/observability-platform/platform/internal/domain/metrics"
	"github.com/deeprajsshetty/observability-platform/platform/internal/domain/topology"
	"github.com/deeprajsshetty/observability-platform/platform/internal/metrics"
	"github.com/deeprajsshetty/observability-platform/platform/internal/metrics/dbmetrics"
	"github.com/deeprajsshetty/observability-platform/platform/internal/modules"
	"github.com/deeprajsshetty/observability-platform/platform/internal/netguard"
	"github.com/deeprajsshetty/observability-platform/platform/internal/secrets"
	"github.com/deeprajsshetty/observability-platform/platform/internal/store"
	"github.com/deeprajsshetty/observability-platform/platform/internal/testdb"
)

func TestMain(m *testing.M) { testdb.Main(m) }

func provider(p netguard.Policy) *database.Provider {
	return &database.Provider{Engines: modules.DatabaseEngines(), Policy: p, DialTimeout: 5 * time.Second}
}

func TestValidateConfig(t *testing.T) {
	p := provider(netguard.Policy{Allowlist: []string{"db.internal.example"}})
	fieldErrs := func(cfg map[string]any, ref string) map[string]string {
		out := map[string]string{}
		for _, e := range p.ValidateConfig(cfg, ref) {
			out[e.Field] = e.Message
		}
		return out
	}
	ok := map[string]any{"engine": "oracle", "host": "db.internal.example", "database": "ORCLPDB1", "username": "c##monitor"}
	if errs := fieldErrs(ok, "env:OBS_SECRET_ORA"); len(errs) != 0 {
		t.Fatalf("valid oracle config rejected: %v", errs)
	}
	cases := []struct {
		name  string
		cfg   map[string]any
		ref   string
		field string
	}{
		{"unknown engine", map[string]any{"engine": "db2", "host": "db.internal.example", "database": "x", "username": "u"}, "env:OBS_SECRET_X", "engine"},
		{"host outside allowlist", map[string]any{"engine": "postgresql", "host": "evil.example", "database": "x", "username": "u"}, "env:OBS_SECRET_X", "host"},
		{"metadata ip", map[string]any{"engine": "postgresql", "host": "169.254.169.254", "database": "x", "username": "u"}, "env:OBS_SECRET_X", "host"},
		{"plaintext without dev flag", map[string]any{"engine": "postgresql", "host": "db.internal.example", "database": "x", "username": "u", "tls": "disable"}, "env:OBS_SECRET_X", "tls"},
		{"password in config", map[string]any{"engine": "postgresql", "host": "db.internal.example", "database": "x", "username": "u", "password": "p"}, "env:OBS_SECRET_X", "password"},
		{"no credential", ok, "", "credentialRef"},
		{"bad port", map[string]any{"engine": "postgresql", "host": "db.internal.example", "database": "x", "username": "u", "port": float64(70000)}, "env:OBS_SECRET_X", "port"},
		{"injection in database", map[string]any{"engine": "postgresql", "host": "db.internal.example", "database": "x?sslmode=disable", "username": "u"}, "env:OBS_SECRET_X", "database"},
	}
	for _, c := range cases {
		if _, ok := fieldErrs(c.cfg, c.ref)[c.field]; !ok {
			t.Errorf("%s: want error on %q, got %v", c.name, c.field, fieldErrs(c.cfg, c.ref))
		}
	}
	info := p.Info()
	if len(info.ConfigFields[0].Options) != 2 || info.MetricsSource != "measured" {
		t.Fatalf("engine field must enumerate engines: %+v", info.ConfigFields[0])
	}
}

func localConfig(t *testing.T) map[string]any {
	u, err := url.Parse(testdb.URL(t))
	if err != nil {
		t.Fatal(err)
	}
	port, _ := strconv.Atoi(u.Port())
	return map[string]any{"engine": "postgresql", "host": u.Hostname(), "port": float64(port), "database": strings.TrimPrefix(u.Path, "/"),
		"username": u.User.Username(), "tls": "disable", "location": "local-dev", "environment": "test"}
}

// TestPostgreSQLEndToEnd discovers a real PostgreSQL server and collects
// measured metrics from it twice, so counter-derived rates appear.
func TestPostgreSQLEndToEnd(t *testing.T) {
	cfg := localConfig(t)
	p := provider(netguard.Policy{Allowlist: []string{cfg["host"].(string)}, AllowInsecure: true})
	if errs := p.ValidateConfig(cfg, "env:OBS_SECRET_TESTPG"); len(errs) != 0 {
		t.Fatal(errs)
	}
	ctx := context.Background()
	conn := store.Connection{ID: "con_test", TenantID: "t1", Provider: database.Name, Config: cfg}
	req := discovery.Request{TenantID: "t1", Connection: conn, Credential: secrets.Value{}}
	if err := p.Check(ctx, req); err != nil {
		t.Fatal(err)
	}
	var pages []discovery.Page
	if err := p.Discover(ctx, req, func(pg discovery.Page) error { pages = append(pages, pg); return nil }); err != nil {
		t.Fatal(err)
	}
	if len(pages) != 1 {
		t.Fatalf("pages: %d", len(pages))
	}
	byType := map[string][]topology.Observation{}
	for _, o := range pages[0].Resources {
		byType[o.Type] = append(byType[o.Type], o)
		if _, err := topology.Normalize("t1", "con_test", o); err != nil {
			t.Fatalf("observation does not normalize: %v", err)
		}
		if o.Labels["engine"] != "postgresql" || o.Labels["env"] != "test" || o.Region != "local-dev" {
			t.Fatalf("labels/region: %+v", o)
		}
	}
	inst := byType["database.instance"]
	if len(inst) != 1 || !strings.HasPrefix(inst[0].Key.Scope, "postgresql:") || inst[0].Properties["engineVersion"] == "" {
		t.Fatalf("instance: %+v", inst)
	}
	names := map[string]bool{}
	for _, d := range byType["database.database"] {
		names[d.Name] = true
	}
	if !names["postgres"] || len(byType["database.tablespace"]) < 2 {
		t.Fatalf("databases %v tablespaces %d", names, len(byType["database.tablespace"]))
	}
	if len(pages[0].Relationships) != len(byType["database.database"])+len(byType["database.tablespace"]) {
		t.Fatal("every database and tablespace must be contained by the instance")
	}

	// Metrics: resources as the store would hand them to the collector.
	var resources []topology.Resource
	for _, o := range pages[0].Resources {
		resources = append(resources, topology.Resource{ID: topology.ResourceID("t1", o.Key), ExternalID: o.Key.ExternalID, Type: o.Type})
	}
	col := &dbmetrics.Collector{Provider: p}
	now := time.Now().UTC()
	mreq := metrics.Request{TenantID: "t1", Connection: conn, Resources: resources, Now: now, Interval: time.Minute}
	first, err := col.Collect(ctx, mreq)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, s := range first {
		got[s.Metric] = true
		if s.Source != dm.SourceMeasured {
			t.Fatal("database samples must be measured")
		}
		if s.Metric == "db.transactions.rate" {
			t.Fatal("first snapshot cannot produce a rate")
		}
	}
	for _, m := range []string{"db.connections", "db.sessions.active", "db.cache.hit_ratio", "storage.used"} {
		if !got[m] {
			t.Errorf("missing %s in first collection: %v", m, got)
		}
	}
	mreq.Now = now.Add(2 * time.Second)
	second, err := col.Collect(ctx, mreq)
	if err != nil {
		t.Fatal(err)
	}
	rate := false
	for _, s := range second {
		if s.Metric == "db.transactions.rate" && s.Value >= 0 {
			rate = true
		}
	}
	if !rate {
		t.Fatal("second snapshot must yield a transaction rate")
	}
}

// TestDialTimeGuard: a host name that passes the allowlist but resolves to
// loopback is refused when dialing, unless the operator allowed it literally.
func TestDialTimeGuard(t *testing.T) {
	cfg := localConfig(t)
	cfg["host"], cfg["tls"] = "localhost", "require"
	p := provider(netguard.Policy{Allowlist: []string{"localhost"}})
	err := p.Check(context.Background(), discovery.Request{Connection: store.Connection{Config: cfg}})
	if err == nil || !strings.Contains(err.Error(), "egress policy") {
		t.Fatalf("want egress denial at dial time, got %v", err)
	}
}
