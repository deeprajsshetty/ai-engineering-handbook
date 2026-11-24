// Package fixture is a deterministic, credential-free discovery provider that
// simulates a small multi-region cloud estate. It exists so the platform can
// be demonstrated and tested end to end offline. Everything it reports is
// simulated and is labelled as such (provider "fixture").
//
// Determinism: the output depends only on the connection config and the run
// revision. Between revisions it drifts in a fixed way so reconciliation is
// visible: on even revisions one web instance disappears (missing) and comes
// back on odd revisions (reactivated); from revision 2 a batch instance
// appears (added); the east database's maintenance-window label alternates
// every run (changed).
package fixture

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/deeprajsshetty/observability-platform/platform/internal/discovery"
	"github.com/deeprajsshetty/observability-platform/platform/internal/domain/topology"
	"github.com/deeprajsshetty/observability-platform/platform/internal/retry"
)

// Name is the provider name.
const Name = "fixture"

var fields = []discovery.ConfigField{
	{Name: "scenario", Type: "string", Default: "retail-demo", Description: "Simulated estate. Only \"retail-demo\" is defined."},
	{Name: "account", Type: "string", Default: "sim-acct-0001", Description: "Simulated account ID used as the topology scope."},
	{Name: "pageSize", Type: "integer", Default: 10, Description: "Resources per simulated API page (1-500)."},
	{Name: "pageDelayMs", Type: "integer", Default: 150, Description: "Simulated latency per page, so progress is visible (0-5000)."},
	{Name: "failAtPage", Type: "integer", Default: 0, Description: "Fail permanently at this page (0 = never). For testing failure handling."},
	{Name: "transientFailures", Type: "integer", Default: 0, Description: "Retryable errors injected before each page succeeds (0-5). For testing retries."},
}

var accountPattern = regexp.MustCompile(`^[a-z0-9-]{3,64}$`)

// Provider implements discovery.Provider.
type Provider struct {
	Retry retry.Policy
}

// New returns the fixture provider.
func New() *Provider {
	return &Provider{Retry: retry.Policy{MaxAttempts: 6, BaseDelay: 20 * time.Millisecond, MaxDelay: 200 * time.Millisecond}}
}

// Info describes the provider.
func (p *Provider) Info() discovery.Info {
	return discovery.Info{
		Name: Name, DisplayName: "Simulated cloud (fixture)",
		Description:   "Deterministic simulated estate for local demos and tests. Requires no credentials. All data is simulated.",
		Simulated:     true,
		MetricsSource: "simulated",
		ConfigFields:  fields,
		ResourceTypes: []string{"network.vpc", "network.subnet", "network.load_balancer", "compute.instance", "database.instance",
			"database.database", "database.tablespace", "cache.cluster", "storage.bucket", "serverless.function", "k8s.cluster", "k8s.node", "k8s.pod"},
	}
}

// ValidateConfig checks the configuration.
func (p *Provider) ValidateConfig(cfg map[string]any, credentialRef string) []discovery.FieldError {
	errs := discovery.ValidateKnownFields(cfg, fields)
	if s := discovery.Str(cfg, "scenario", "retail-demo"); s != "retail-demo" {
		errs = append(errs, discovery.FieldError{Field: "scenario", Message: "unknown scenario"})
	}
	if a := discovery.Str(cfg, "account", "sim-acct-0001"); !accountPattern.MatchString(a) {
		errs = append(errs, discovery.FieldError{Field: "account", Message: "must be 3-64 lower-case letters, digits or dashes"})
	}
	errs = append(errs, discovery.RangeCheck(cfg, "pageSize", 1, 500)...)
	errs = append(errs, discovery.RangeCheck(cfg, "pageDelayMs", 0, 5000)...)
	errs = append(errs, discovery.RangeCheck(cfg, "failAtPage", 0, 1000)...)
	errs = append(errs, discovery.RangeCheck(cfg, "transientFailures", 0, 5)...)
	if credentialRef != "" {
		errs = append(errs, discovery.FieldError{Field: "credentialRef", Message: "the fixture provider takes no credential"})
	}
	return errs
}

// Check always succeeds: there is nothing to reach.
func (p *Provider) Check(context.Context, discovery.Request) error { return nil }

// ErrInjected is the permanent failure produced by failAtPage.
var ErrInjected = errors.New("fixture: injected permanent failure")

// Discover emits the simulated estate page by page.
func (p *Provider) Discover(ctx context.Context, req discovery.Request, emit discovery.Emit) error {
	cfg := req.Connection.Config
	pageSize := discovery.Int(cfg, "pageSize", 10)
	delay := time.Duration(discovery.Int(cfg, "pageDelayMs", 150)) * time.Millisecond
	failAt := discovery.Int(cfg, "failAtPage", 0)
	transient := discovery.Int(cfg, "transientFailures", 0)
	est := Estate(discovery.Str(cfg, "account", "sim-acct-0001"), max(req.Revision, 1))

	// Group relationships by source so each page carries the edges of its resources.
	bySource := map[topology.Key][]topology.RelationshipObservation{}
	for _, r := range est.Relationships {
		bySource[r.Source] = append(bySource[r.Source], r)
	}
	for page, start := 1, 0; start < len(est.Resources); page, start = page+1, start+pageSize {
		end := min(start+pageSize, len(est.Resources))
		attempts := 0
		// Simulates a paginated, rate-limited list call: transient errors are
		// retried with backoff because listing is read-only and idempotent.
		err := retry.Do(ctx, p.Retry, func(ctx context.Context) error {
			attempts++
			t := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				t.Stop()
				return ctx.Err()
			case <-t.C:
			}
			if failAt == page {
				return fmt.Errorf("%w at page %d", ErrInjected, page)
			}
			if attempts <= transient {
				return retry.Retryable(fmt.Errorf("fixture: simulated throttling on page %d (attempt %d)", page, attempts), 0)
			}
			return nil
		}, nil)
		if err != nil {
			return err
		}
		pg := discovery.Page{Resources: est.Resources[start:end]}
		for _, r := range pg.Resources {
			pg.Relationships = append(pg.Relationships, bySource[r.Key]...)
		}
		if err := emit(pg); err != nil {
			return err
		}
	}
	return nil
}

// EstateData is the full simulated estate for one revision.
type EstateData struct {
	Resources     []topology.Observation
	Relationships []topology.RelationshipObservation
}

// Estate builds the deterministic retail-demo estate.
func Estate(account string, revision int) EstateData {
	var e EstateData
	key := func(id string) topology.Key { return topology.Key{Provider: Name, Scope: account, ExternalID: id} }
	res := func(id, typ, native, name, region string, labels map[string]string, props map[string]any) topology.Key {
		k := key(id)
		if labels == nil {
			labels = map[string]string{}
		}
		labels["simulated"] = "true"
		e.Resources = append(e.Resources, topology.Observation{Key: k, Type: typ, NativeType: native, Name: name, Region: region, Labels: labels, Properties: props})
		return k
	}
	rel := func(src topology.Key, t topology.RelationType, dst topology.Key) {
		e.Relationships = append(e.Relationships, topology.RelationshipObservation{Source: src, Target: dst, Type: t})
	}

	assets := res("bucket-storefront-assets", "storage.bucket", "sim:storage:bucket", "storefront-assets", "global",
		map[string]string{"app": "storefront", "env": "prod"}, map[string]any{"versioning": true, "encryption": "sim-kms"})
	backups := res("bucket-db-backups", "storage.bucket", "sim:storage:bucket", "db-backups", "global",
		map[string]string{"app": "orders", "env": "prod", "retention": "35d"}, map[string]any{"versioning": true, "encryption": "sim-kms"})

	for ri, region := range []string{"sim-east-1", "sim-west-1"} {
		short := map[int]string{0: "east", 1: "west"}[ri]
		cidr := fmt.Sprintf("10.%d.0.0/16", ri+1)
		vpc := res("vpc-"+short, "network.vpc", "sim:network:vpc", "vpc-"+short, region, map[string]string{"env": "prod"}, map[string]any{"cidr": cidr})
		subA := res("subnet-"+short+"-a", "network.subnet", "sim:network:subnet", "subnet-"+short+"-a", region, nil, map[string]any{"cidr": fmt.Sprintf("10.%d.1.0/24", ri+1), "zone": region + "a"})
		subB := res("subnet-"+short+"-b", "network.subnet", "sim:network:subnet", "subnet-"+short+"-b", region, nil, map[string]any{"cidr": fmt.Sprintf("10.%d.2.0/24", ri+1), "zone": region + "b"})
		rel(vpc, topology.RelContains, subA)
		rel(vpc, topology.RelContains, subB)
		lb := res("lb-"+short+"-web", "network.load_balancer", "sim:network:lb", "lb-"+short+"-web", region,
			map[string]string{"app": "storefront", "env": "prod"}, map[string]any{"scheme": "internet-facing", "listeners": []any{"https:443"}})
		rel(vpc, topology.RelContains, lb)

		db := res("db-"+short+"-orders", "database.instance", "sim:db:instance", "orders-db-"+short, region,
			map[string]string{"app": "orders", "env": "prod", "engine": "postgresql", "maintenance-window": map[bool]string{true: "sun-02:00", false: "sat-03:00"}[short == "east" && revision%2 == 0]},
			map[string]any{"engineVersion": "17", "storageGiB": 200, "multiAz": ri == 0})
		rel(subB, topology.RelContains, db)
		rel(db, topology.RelUses, backups)
		ordersDB := res("db-"+short+"-orders/db/orders", "database.database", "sim:postgresql:database", "orders", region,
			map[string]string{"app": "orders", "env": "prod", "engine": "postgresql"}, map[string]any{"encoding": "UTF8"})
		rel(db, topology.RelContains, ordersDB)
		cache := res("cache-"+short, "cache.cluster", "sim:cache:cluster", "session-cache-"+short, region,
			map[string]string{"app": "orders", "env": "prod"}, map[string]any{"nodes": 2})
		rel(subB, topology.RelContains, cache)

		var apis []topology.Key
		for i := 1; i <= 2; i++ {
			id := fmt.Sprintf("i-%s-api-%02d", short, i)
			k := res(id, "compute.instance", "sim:compute:instance", id, region,
				map[string]string{"app": "orders-api", "tier": "app", "env": "prod"},
				map[string]any{"instanceType": "sim.large", "vcpu": 4, "memoryGiB": 16, "state": "running", "privateIp": fmt.Sprintf("10.%d.2.%d", ri+1, 10+i)})
			rel(subB, topology.RelContains, k)
			rel(k, topology.RelDependsOn, db)
			rel(k, topology.RelUses, cache)
			apis = append(apis, k)
		}
		for i := 1; i <= 3; i++ {
			id := fmt.Sprintf("i-%s-web-%02d", short, i)
			if short == "east" && i == 3 && revision%2 == 0 {
				continue // drift: disappears on even revisions
			}
			sub := subA
			if i == 3 {
				sub = subB
			}
			k := res(id, "compute.instance", "sim:compute:instance", id, region,
				map[string]string{"app": "storefront", "tier": "web", "env": "prod"},
				map[string]any{"instanceType": "sim.medium", "vcpu": 2, "memoryGiB": 4, "state": "running", "privateIp": fmt.Sprintf("10.%d.1.%d", ri+1, 10+i)})
			rel(sub, topology.RelContains, k)
			rel(lb, topology.RelRoutesTo, k)
			rel(k, topology.RelUses, assets)
			for _, a := range apis {
				rel(k, topology.RelDependsOn, a)
			}
		}
		if short == "east" && revision >= 2 {
			k := res("i-east-batch-01", "compute.instance", "sim:compute:instance", "i-east-batch-01", region,
				map[string]string{"app": "batch", "tier": "worker", "env": "prod"},
				map[string]any{"instanceType": "sim.xlarge", "vcpu": 8, "memoryGiB": 32, "state": "running", "privateIp": "10.1.2.40"})
			rel(subB, topology.RelContains, k)
			rel(k, topology.RelDependsOn, db)
			rel(k, topology.RelUses, backups)
		}
		if short == "west" {
			cluster := res("k8s-analytics", "k8s.cluster", "sim:k8s:cluster", "analytics", region,
				map[string]string{"app": "analytics", "env": "prod"}, map[string]any{"version": "1.34"})
			rel(vpc, topology.RelContains, cluster)
			for n := 1; n <= 2; n++ {
				node := res(fmt.Sprintf("k8s-analytics-node-%d", n), "k8s.node", "sim:k8s:node", fmt.Sprintf("analytics-node-%d", n), region,
					nil, map[string]any{"cpuCores": 8, "memoryGiB": 32})
				rel(cluster, topology.RelContains, node)
				for _, w := range []string{"ingest", "report"} {
					pod := res(fmt.Sprintf("pod-%s-%d", w, n), "k8s.pod", "sim:k8s:pod", fmt.Sprintf("%s-%d", w, n), region,
						map[string]string{"app": w, "namespace": "analytics"}, map[string]any{"namespace": "analytics", "phase": "Running"})
					rel(node, topology.RelContains, pod)
					rel(pod, topology.RelDependsOn, db)
				}
			}
		}
	}
	// Oracle ERP estate (simulated): a container database with two pluggable
	// databases and their tablespaces, used by an ERP application server.
	{
		region := "sim-east-1"
		oraLabels := func() map[string]string { return map[string]string{"app": "erp", "env": "prod", "engine": "oracle"} }
		subB := key("subnet-east-b")
		ora := res("ora-erp-east", "database.instance", "sim:oracle:instance", "Oracle Database ERPCDB1", region, oraLabels(),
			map[string]any{"engine": "oracle", "engineVersion": "19.0.0.0.0", "cdb": true, "databaseRole": "PRIMARY", "logMode": "ARCHIVELOG"})
		rel(subB, topology.RelContains, ora)
		rel(ora, topology.RelUses, backups)
		for _, pdb := range []struct {
			name string
			ts   []string
		}{{"ERPPDB", []string{"ERP_DATA", "ERP_IDX"}}, {"HRPDB", []string{"HR_DATA"}}} {
			p := res("ora-erp-east/db/"+pdb.name, "database.database", "sim:oracle:pdb", pdb.name, region, oraLabels(),
				map[string]any{"kind": "pdb", "openMode": "READ WRITE"})
			rel(ora, topology.RelContains, p)
			for _, t := range pdb.ts {
				tk := res("ora-erp-east/ts/"+pdb.name+"/"+t, "database.tablespace", "sim:oracle:tablespace", t, region, oraLabels(),
					map[string]any{"contents": "PERMANENT", "sizeBytes": 3.2e10})
				rel(p, topology.RelContains, tk)
			}
		}
		erp := res("i-east-erp-01", "compute.instance", "sim:compute:instance", "i-east-erp-01", region,
			map[string]string{"app": "erp", "tier": "app", "env": "prod"},
			map[string]any{"instanceType": "sim.xlarge", "vcpu": 8, "memoryGiB": 32, "state": "running", "privateIp": "10.1.2.60"})
		rel(subB, topology.RelContains, erp)
		rel(erp, topology.RelDependsOn, ora)
	}
	fn := res("fn-image-resize", "serverless.function", "sim:serverless:function", "image-resize", "sim-east-1",
		map[string]string{"app": "storefront", "env": "prod"}, map[string]any{"runtime": "go1.x", "memoryMiB": 512})
	rel(fn, topology.RelUses, assets)
	return e
}
