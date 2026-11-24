package topology

import (
	"errors"
	"fmt"
	"sort"
)

// ErrSnapshotLimit is returned when a snapshot would exceed its resource cap.
var ErrSnapshotLimit = errors.New("snapshot resource limit exceeded")

// Snapshot accumulates the observations of one discovery run. Duplicate
// observations of the same key are merged (later attributes win, labels are
// unioned), so a provider that returns the same resource on two pages, or a
// retried page, does not create duplicates.
type Snapshot struct {
	TenantID     string
	ConnectionID string
	maxResources int

	resources map[string]Resource
	order     []string
	edges     map[string]Relationship

	DuplicateResources int
	DuplicateEdges     int
	InvalidResources   int
	InvalidEdges       int
}

// NewSnapshot creates an empty snapshot capped at maxResources.
func NewSnapshot(tenantID, connectionID string, maxResources int) *Snapshot {
	return &Snapshot{
		TenantID:     tenantID,
		ConnectionID: connectionID,
		maxResources: maxResources,
		resources:    map[string]Resource{},
		edges:        map[string]Relationship{},
	}
}

// AddResource normalizes and adds o. Invalid observations are counted and
// returned as an error; callers record them as job warnings and continue.
func (s *Snapshot) AddResource(o Observation) error {
	r, err := Normalize(s.TenantID, s.ConnectionID, o)
	if err != nil {
		s.InvalidResources++
		return err
	}
	if prev, ok := s.resources[r.ID]; ok {
		s.DuplicateResources++
		merged := map[string]string{}
		for k, v := range prev.Labels {
			merged[k] = v
		}
		for k, v := range r.Labels {
			merged[k] = v
		}
		r.Labels = merged
		r.ContentHash = ContentHash(r)
		s.resources[r.ID] = r
		return nil
	}
	if len(s.resources) >= s.maxResources {
		return fmt.Errorf("%w (%d)", ErrSnapshotLimit, s.maxResources)
	}
	s.resources[r.ID] = r
	s.order = append(s.order, r.ID)
	return nil
}

// AddRelationship validates and adds an edge. Endpoints are resolved by key;
// whether they exist in the snapshot is checked in Finalize because the
// target may arrive on a later page.
func (s *Snapshot) AddRelationship(o RelationshipObservation) error {
	if _, ok := Spec(o.Type); !ok {
		s.InvalidEdges++
		return invalid("unknown relationship type %q", o.Type)
	}
	if err := ValidateKey(o.Source); err != nil {
		s.InvalidEdges++
		return err
	}
	if err := ValidateKey(o.Target); err != nil {
		s.InvalidEdges++
		return err
	}
	src := ResourceID(s.TenantID, o.Source)
	dst := ResourceID(s.TenantID, o.Target)
	if src == dst {
		s.InvalidEdges++
		return invalid("self-referencing %s relationship on %s", o.Type, o.Source)
	}
	id := RelationshipID(s.TenantID, src, o.Type, dst)
	if _, ok := s.edges[id]; ok {
		s.DuplicateEdges++
		return nil
	}
	s.edges[id] = Relationship{
		ID: id, TenantID: s.TenantID, ConnectionID: s.ConnectionID,
		SourceID: src, TargetID: dst, Type: o.Type, Status: StatusActive,
	}
	return nil
}

// FinalizeReport describes edges dropped by Finalize.
type FinalizeReport struct {
	DanglingEdges []Relationship // an endpoint was not observed in this run
	CycleEdges    []Relationship // edge would close a cycle in an acyclic type
}

// Finalize drops edges whose endpoints were not observed in this snapshot,
// then removes edges that close a cycle among acyclic relationship types
// (contains, composed_of). It must be called once, after the last page.
func (s *Snapshot) Finalize() FinalizeReport {
	var rep FinalizeReport
	g := NewGraph()
	for _, id := range s.sortedEdgeIDs() {
		e := s.edges[id]
		_, a := s.resources[e.SourceID]
		_, b := s.resources[e.TargetID]
		if !a || !b {
			rep.DanglingEdges = append(rep.DanglingEdges, e)
			delete(s.edges, id)
			continue
		}
		g.AddEdge(Edge{ID: e.ID, From: e.SourceID, To: e.TargetID, Type: e.Type})
	}
	for _, be := range g.BackEdges(IsAcyclic) {
		rep.CycleEdges = append(rep.CycleEdges, s.edges[be.ID])
		delete(s.edges, be.ID)
	}
	return rep
}

func (s *Snapshot) sortedEdgeIDs() []string {
	out := make([]string, 0, len(s.edges))
	for id := range s.edges {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// Resources returns resources in first-observed order.
func (s *Snapshot) Resources() []Resource {
	out := make([]Resource, 0, len(s.order))
	for _, id := range s.order {
		out = append(out, s.resources[id])
	}
	return out
}

// Relationships returns edges in ID order.
func (s *Snapshot) Relationships() []Relationship {
	out := make([]Relationship, 0, len(s.edges))
	for _, id := range s.sortedEdgeIDs() {
		out = append(out, s.edges[id])
	}
	return out
}

// ResourceCount returns the number of distinct resources.
func (s *Snapshot) ResourceCount() int { return len(s.resources) }

// RelationshipCount returns the number of distinct edges.
func (s *Snapshot) RelationshipCount() int { return len(s.edges) }

// HasResource reports whether id is in the snapshot.
func (s *Snapshot) HasResource(id string) bool { _, ok := s.resources[id]; return ok }
