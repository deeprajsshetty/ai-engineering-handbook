package topology

import "sort"

// ExistingState is what the store already knows about a resource or edge.
type ExistingState struct {
	ID           string
	ContentHash  string // empty for edges
	Status       Status
	ConnectionID string
}

// ChangeKind classifies one item in a reconciliation plan.
type ChangeKind string

const (
	ChangeAdded       ChangeKind = "added"
	ChangeChanged     ChangeKind = "changed"
	ChangeUnchanged   ChangeKind = "unchanged"
	ChangeReactivated ChangeKind = "reactivated" // was missing, seen again
	ChangeMissing     ChangeKind = "missing"
)

// Plan is the outcome of comparing a snapshot to stored state.
type Plan struct {
	Added       []string
	Changed     []string
	Unchanged   []string
	Reactivated []string
	Missing     []string // resources of this connection not observed in a complete run

	EdgesAdded       int
	EdgesUnchanged   int
	EdgesReactivated int
	EdgesMissing     []string
}

// Counts is a compact summary of a plan, stored with the job.
type Counts struct {
	Added            int `json:"added"`
	Changed          int `json:"changed"`
	Unchanged        int `json:"unchanged"`
	Reactivated      int `json:"reactivated"`
	Missing          int `json:"missing"`
	EdgesAdded       int `json:"edgesAdded"`
	EdgesUnchanged   int `json:"edgesUnchanged"`
	EdgesReactivated int `json:"edgesReactivated"`
	EdgesMissing     int `json:"edgesMissing"`
}

// Counts summarizes the plan.
func (p Plan) Counts() Counts {
	return Counts{
		Added: len(p.Added), Changed: len(p.Changed), Unchanged: len(p.Unchanged),
		Reactivated: len(p.Reactivated), Missing: len(p.Missing),
		EdgesAdded: p.EdgesAdded, EdgesUnchanged: p.EdgesUnchanged,
		EdgesReactivated: p.EdgesReactivated, EdgesMissing: len(p.EdgesMissing),
	}
}

// Reconcile compares a finalized snapshot with existing state.
//
//   - existingResources must contain every stored resource whose ID is in the
//     snapshot, plus every active resource owned by the snapshot's connection.
//   - existingEdges likewise for relationships.
//   - complete is false for partial runs (failed, cancelled, truncated). A
//     partial run never marks anything missing: absence from an incomplete
//     snapshot is not evidence of deletion.
//
// The function is pure and O(n) in the number of items; it allocates the
// result lists only. Lists are sorted for deterministic output.
func Reconcile(snap *Snapshot, existingResources, existingEdges map[string]ExistingState, complete bool) Plan {
	var p Plan
	for _, r := range snap.Resources() {
		ex, ok := existingResources[r.ID]
		switch {
		case !ok:
			p.Added = append(p.Added, r.ID)
		case ex.Status == StatusMissing:
			p.Reactivated = append(p.Reactivated, r.ID)
		case ex.ContentHash != r.ContentHash:
			p.Changed = append(p.Changed, r.ID)
		default:
			p.Unchanged = append(p.Unchanged, r.ID)
		}
	}
	if complete {
		for id, ex := range existingResources {
			if ex.ConnectionID == snap.ConnectionID && ex.Status == StatusActive && !snap.HasResource(id) {
				p.Missing = append(p.Missing, id)
			}
		}
	}
	inSnap := map[string]struct{}{}
	for _, e := range snap.Relationships() {
		inSnap[e.ID] = struct{}{}
		ex, ok := existingEdges[e.ID]
		switch {
		case !ok:
			p.EdgesAdded++
		case ex.Status == StatusMissing:
			p.EdgesReactivated++
		default:
			p.EdgesUnchanged++
		}
	}
	if complete {
		for id, ex := range existingEdges {
			if _, seen := inSnap[id]; !seen && ex.ConnectionID == snap.ConnectionID && ex.Status == StatusActive {
				p.EdgesMissing = append(p.EdgesMissing, id)
			}
		}
	}
	for _, l := range [][]string{p.Added, p.Changed, p.Unchanged, p.Reactivated, p.Missing, p.EdgesMissing} {
		sort.Strings(l)
	}
	return p
}
