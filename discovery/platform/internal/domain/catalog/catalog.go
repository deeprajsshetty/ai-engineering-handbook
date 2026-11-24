// Package catalog describes the canonical resource types the platform knows,
// grouped into domains (compute, network, database, ...). The API serves it
// so the UI can render any type — including types added by future modules —
// without code changes: icons, grouping, display names and the key metric
// shown in lists all come from here.
//
// Unknown types are still accepted by discovery (the type grammar is open);
// they appear under the "other" domain until a module registers them.
package catalog

import (
	"fmt"
	"sort"
	"sync"
)

// Domain is a top-level grouping in navigation and the resource explorer.
type Domain struct {
	Name        string `json:"name"`
	DisplayName string `json:"displayName"`
	Description string `json:"description"`
	Order       int    `json:"order"`
}

// ResourceType describes one canonical type.
type ResourceType struct {
	Type          string   `json:"type"`
	Domain        string   `json:"domain"`
	DisplayName   string   `json:"displayName"`
	Description   string   `json:"description"`
	Icon          string   `json:"icon"`                    // UI icon key; unknown keys fall back to the domain icon
	KeyMetric     string   `json:"keyMetric,omitempty"`     // metric shown in lists and fleet views
	KeyProperties []string `json:"keyProperties,omitempty"` // properties shown first on detail pages
}

var (
	mu      sync.RWMutex
	domains = map[string]Domain{}
	types   = map[string]ResourceType{}
)

// RegisterDomain adds a domain. It panics on duplicates because that is a
// programming error detected at start-up.
func RegisterDomain(d Domain) {
	mu.Lock()
	defer mu.Unlock()
	if _, dup := domains[d.Name]; dup {
		panic(fmt.Sprintf("catalog: duplicate domain %q", d.Name))
	}
	domains[d.Name] = d
}

// RegisterType adds a resource type to a registered domain.
func RegisterType(t ResourceType) {
	mu.Lock()
	defer mu.Unlock()
	if _, ok := domains[t.Domain]; !ok {
		panic(fmt.Sprintf("catalog: type %q references unknown domain %q", t.Type, t.Domain))
	}
	if _, dup := types[t.Type]; dup {
		panic(fmt.Sprintf("catalog: duplicate type %q", t.Type))
	}
	types[t.Type] = t
}

// Domains returns registered domains in display order.
func Domains() []Domain {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]Domain, 0, len(domains))
	for _, d := range domains {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Order < out[j].Order || out[i].Order == out[j].Order && out[i].Name < out[j].Name
	})
	return out
}

// Types returns registered types sorted by domain order then type.
func Types() []ResourceType {
	mu.RLock()
	order := map[string]int{}
	for _, d := range domains {
		order[d.Name] = d.Order
	}
	out := make([]ResourceType, 0, len(types))
	for _, t := range types {
		out = append(out, t)
	}
	mu.RUnlock()
	sort.Slice(out, func(i, j int) bool {
		if order[out[i].Domain] != order[out[j].Domain] {
			return order[out[i].Domain] < order[out[j].Domain]
		}
		return out[i].Type < out[j].Type
	})
	return out
}

// Lookup returns a type; unknown types are synthesised under "other".
func Lookup(t string) ResourceType {
	mu.RLock()
	rt, ok := types[t]
	mu.RUnlock()
	if ok {
		return rt
	}
	return ResourceType{Type: t, Domain: "other", DisplayName: t, Icon: "other"}
}

// TypesInDomain returns the registered types of a domain.
func TypesInDomain(domain string) []string {
	var out []string
	for _, t := range Types() {
		if t.Domain == domain {
			out = append(out, t.Type)
		}
	}
	return out
}

// HasDomain reports whether a domain is registered.
func HasDomain(name string) bool {
	mu.RLock()
	defer mu.RUnlock()
	_, ok := domains[name]
	return ok
}

func init() {
	for i, d := range []Domain{
		{Name: "compute", DisplayName: "Compute", Description: "Virtual machines and instances"},
		{Name: "containers", DisplayName: "Kubernetes", Description: "Clusters, nodes and workloads"},
		{Name: "database", DisplayName: "Databases", Description: "Database servers, databases and tablespaces of any engine"},
		{Name: "network", DisplayName: "Network", Description: "Networks, subnets and load balancers"},
		{Name: "storage", DisplayName: "Storage", Description: "Object storage and caches"},
		{Name: "serverless", DisplayName: "Serverless", Description: "Functions"},
		{Name: "other", DisplayName: "Other", Description: "Types not yet described by a module"},
	} {
		d.Order = (i + 1) * 10
		RegisterDomain(d)
	}
	for _, t := range []ResourceType{
		{"compute.instance", "compute", "Instance", "Virtual machine or bare-metal instance", "server", "cpu.utilization", []string{"instanceType", "state", "privateIp"}},
		{"k8s.cluster", "containers", "Kubernetes cluster", "Kubernetes control plane", "cluster", "", []string{"version"}},
		{"k8s.node", "containers", "Kubernetes node", "Worker node", "node", "node.cpu.usage", []string{"kubeletVersion", "cpuCores", "memoryGiB"}},
		{"k8s.namespace", "containers", "Namespace", "Kubernetes namespace", "namespace", "", nil},
		{"k8s.deployment", "containers", "Deployment", "Kubernetes Deployment", "workload", "", []string{"replicas"}},
		{"k8s.pod", "containers", "Pod", "Kubernetes pod", "pod", "container.cpu.usage", []string{"namespace", "phase"}},
		{"k8s.service", "containers", "Service", "Kubernetes service", "service", "", nil},
		{"database.instance", "database", "Database server", "Engine instance (PostgreSQL cluster, Oracle instance, ...)", "database", "db.connections", []string{"engine", "engineVersion", "databaseRole"}},
		{"database.database", "database", "Database", "Logical database (PostgreSQL database, Oracle PDB, ...)", "database-logical", "storage.used", []string{"kind", "openMode", "encoding"}},
		{"database.tablespace", "database", "Tablespace", "Storage container inside a database server", "tablespace", "db.storage.utilization", []string{"contents", "sizeBytes"}},
		{"network.vpc", "network", "Virtual network", "VPC or VNet", "network", "", []string{"cidr"}},
		{"network.subnet", "network", "Subnet", "Network segment", "subnet", "", []string{"cidr", "zone"}},
		{"network.load_balancer", "network", "Load balancer", "Layer 4/7 load balancer", "load-balancer", "request.rate", []string{"scheme"}},
		{"storage.bucket", "storage", "Bucket", "Object storage bucket", "bucket", "storage.used", []string{"versioning", "encryption"}},
		{"cache.cluster", "storage", "Cache cluster", "In-memory cache", "cache", "", []string{"nodes"}},
		{"serverless.function", "serverless", "Function", "Serverless function", "function", "request.rate", []string{"runtime", "memoryMiB"}},
	} {
		RegisterType(t)
	}
}

// DomainOf returns the domain of a type ("other" when unknown).
func DomainOf(t string) string { return Lookup(t).Domain }
