// Package topology holds the provider-neutral topology model: resources
// (nodes), relationships (directed, typed edges), normalization of provider
// observations, the in-memory graph used for traversal and cycle checks, and
// reconciliation between discovery snapshots.
//
// The package has no I/O dependencies. Provider SDK types never enter it:
// providers translate their responses into Observation values first.
package topology

import (
	"time"

	"github.com/deeprajsshetty/observability-platform/platform/internal/ids"
)

// Status is the reconciliation state of a resource or relationship.
type Status string

const (
	// StatusActive means the item was seen in the latest complete discovery
	// run of the connection that last observed it.
	StatusActive Status = "active"
	// StatusMissing means a later complete run of the owning connection did
	// not observe the item. It is retained (not deleted) until the purge
	// window elapses, so a transient gap does not lose history.
	StatusMissing Status = "missing"
)

// ValidStatus reports whether s is a known status.
func ValidStatus(s string) bool { return s == string(StatusActive) || s == string(StatusMissing) }

// RelationType is the type of a directed relationship Source -> Target.
type RelationType string

// Relationship types. They are derived from the relationship kinds the
// existing collectors emit (containment, composition, membership, usage,
// dependency) plus two explicit network kinds.
const (
	RelContains   RelationType = "contains"    // parent contains child (VPC contains subnet)
	RelComposedOf RelationType = "composed_of" // whole is composed of part (cluster composed_of node pool)
	RelMemberOf   RelationType = "member_of"   // member belongs to group (instance member_of auto-scaling group)
	RelUses       RelationType = "uses"        // consumer uses resource (instance uses volume)
	RelDependsOn  RelationType = "depends_on"  // runtime dependency (service depends_on database)
	RelRoutesTo   RelationType = "routes_to"   // traffic is routed to target (load balancer routes_to instance)
	RelConnectsTo RelationType = "connects_to" // network connectivity (peering, VPN)
)

// RelationSpec describes the rules for one relationship type.
type RelationSpec struct {
	Type        RelationType `json:"type"`
	Acyclic     bool         `json:"acyclic"`
	Description string       `json:"description"`
}

// relationSpecs: hierarchical types must form a DAG (a resource cannot
// transitively contain itself). Dependency-like types may legitimately form
// cycles (two services calling each other), so cycles are permitted there and
// only reported by analysis endpoints.
var relationSpecs = map[RelationType]RelationSpec{
	RelContains:   {RelContains, true, "Source contains target (hierarchical placement)."},
	RelComposedOf: {RelComposedOf, true, "Source is composed of target (whole-part)."},
	RelMemberOf:   {RelMemberOf, false, "Source is a member of target group."},
	RelUses:       {RelUses, false, "Source uses target (storage, identity, cache)."},
	RelDependsOn:  {RelDependsOn, false, "Source depends on target at runtime."},
	RelRoutesTo:   {RelRoutesTo, false, "Source routes traffic to target."},
	RelConnectsTo: {RelConnectsTo, false, "Source has network connectivity to target."},
}

// Spec returns the spec for t and whether t is known.
func Spec(t RelationType) (RelationSpec, bool) {
	s, ok := relationSpecs[t]
	return s, ok
}

// RelationSpecs returns all specs in a stable order.
func RelationSpecs() []RelationSpec {
	order := []RelationType{RelContains, RelComposedOf, RelMemberOf, RelUses, RelDependsOn, RelRoutesTo, RelConnectsTo}
	out := make([]RelationSpec, 0, len(order))
	for _, t := range order {
		out = append(out, relationSpecs[t])
	}
	return out
}

// IsAcyclic reports whether cycles are forbidden for t.
func IsAcyclic(t RelationType) bool { return relationSpecs[t].Acyclic }

// Key is the stable natural identity of a resource: the provider, the scope
// (account, project, subscription or cluster) and the provider's own stable
// identifier (for example an ARN or a Kubernetes UID). Display names are never
// part of the key because they change.
type Key struct {
	Provider   string `json:"provider"`
	Scope      string `json:"scope"`
	ExternalID string `json:"externalId"`
}

func (k Key) String() string { return k.Provider + "/" + k.Scope + "/" + k.ExternalID }

// ResourceID returns the deterministic platform ID for a key within a tenant.
func ResourceID(tenantID string, k Key) string {
	return ids.Deterministic("res", tenantID, k.Provider, k.Scope, k.ExternalID)
}

// RelationshipID returns the deterministic ID of an edge. Direction and type
// are part of the identity: A contains B and B contains A are different edges.
func RelationshipID(tenantID, sourceID string, t RelationType, targetID string) string {
	return ids.Deterministic("rel", tenantID, sourceID, string(t), targetID)
}

// Observation is what a provider reports for one resource in one run.
type Observation struct {
	Key        Key
	Type       string            // canonical type, e.g. "compute.instance"
	NativeType string            // provider type, e.g. "aws:ec2:instance"
	Name       string            // display name; defaults to ExternalID
	Region     string            // provider region or zone; "global" when not regional
	Labels     map[string]string // provider tags or labels
	Properties map[string]any    // provider-specific metadata, preserved verbatim (size-capped)
}

// RelationshipObservation is what a provider reports for one edge.
type RelationshipObservation struct {
	Source Key
	Target Key
	Type   RelationType
}

// Resource is a normalized resource ready to be stored.
type Resource struct {
	ID           string            `json:"id"`
	TenantID     string            `json:"tenantId"`
	ConnectionID string            `json:"connectionId"`
	Provider     string            `json:"provider"`
	Scope        string            `json:"scope"`
	ExternalID   string            `json:"externalId"`
	Type         string            `json:"type"`
	NativeType   string            `json:"nativeType"`
	Name         string            `json:"name"`
	Region       string            `json:"region"`
	Labels       map[string]string `json:"labels"`
	Properties   map[string]any    `json:"properties"`
	ContentHash  string            `json:"-"`
	Status       Status            `json:"status"`
	FirstSeenAt  time.Time         `json:"firstSeenAt"`
	LastSeenAt   time.Time         `json:"lastSeenAt"`
	MissingSince *time.Time        `json:"missingSince,omitempty"`
	LastJobID    string            `json:"lastJobId"`
}

// Key returns the natural key.
func (r Resource) Key() Key { return Key{r.Provider, r.Scope, r.ExternalID} }

// Relationship is a normalized directed edge.
type Relationship struct {
	ID           string       `json:"id"`
	TenantID     string       `json:"tenantId"`
	ConnectionID string       `json:"connectionId"`
	SourceID     string       `json:"sourceId"`
	TargetID     string       `json:"targetId"`
	Type         RelationType `json:"type"`
	Status       Status       `json:"status"`
	FirstSeenAt  time.Time    `json:"firstSeenAt"`
	LastSeenAt   time.Time    `json:"lastSeenAt"`
	MissingSince *time.Time   `json:"missingSince,omitempty"`
	LastJobID    string       `json:"lastJobId"`
}
