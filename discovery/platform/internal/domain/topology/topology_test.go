package topology

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func key(id string) Key { return Key{Provider: "fixture", Scope: "acct-1", ExternalID: id} }

func obs(id, typ string, labels map[string]string) Observation {
	return Observation{Key: key(id), Type: typ, Name: id, Region: "eu-west-1", Labels: labels}
}

func TestResourceIDIsDeterministicAndTenantScoped(t *testing.T) {
	a := ResourceID("t1", key("i-1"))
	if a != ResourceID("t1", key("i-1")) {
		t.Fatal("same key must give same ID")
	}
	if a == ResourceID("t2", key("i-1")) {
		t.Fatal("different tenants must give different IDs")
	}
	if ResourceID("t1", Key{"fixture", "ab", "c"}) == ResourceID("t1", Key{"fixture", "a", "bc"}) {
		t.Fatal("key parts must be length-delimited")
	}
}

func TestRelationshipIDPreservesDirectionAndType(t *testing.T) {
	if RelationshipID("t", "a", RelContains, "b") == RelationshipID("t", "b", RelContains, "a") {
		t.Fatal("direction must be part of identity")
	}
	if RelationshipID("t", "a", RelContains, "b") == RelationshipID("t", "a", RelUses, "b") {
		t.Fatal("type must be part of identity")
	}
}

func TestNormalizeDefaultsAndValidation(t *testing.T) {
	r, err := Normalize("t", "c", Observation{Key: key("i-1"), Type: "compute.instance", Labels: map[string]string{" env ": " prod\n", "": "x"}})
	if err != nil {
		t.Fatal(err)
	}
	if r.Name != "i-1" || r.Region != DefaultRegion {
		t.Fatalf("defaults not applied: %+v", r)
	}
	if !reflect.DeepEqual(r.Labels, map[string]string{"env": "prod"}) {
		t.Fatalf("labels not normalized: %v", r.Labels)
	}

	cases := map[string]Observation{
		"bad type":        {Key: key("x"), Type: "Instance"},
		"empty id":        {Key: Key{"fixture", "s", ""}, Type: "compute.instance"},
		"bad provider":    {Key: Key{"AWS!", "s", "x"}, Type: "compute.instance"},
		"control char id": {Key: Key{"fixture", "s", "a\x00b"}, Type: "compute.instance"},
		"bad region":      {Key: key("x"), Type: "compute.instance", Region: "eu west"},
		"huge props":      {Key: key("x"), Type: "compute.instance", Properties: map[string]any{"blob": strings.Repeat("a", MaxPropertiesBytes)}},
	}
	for name, o := range cases {
		if _, err := Normalize("t", "c", o); !errors.Is(err, ErrInvalidObservation) {
			t.Errorf("%s: want ErrInvalidObservation, got %v", name, err)
		}
	}
	tooMany := map[string]string{}
	for i := 0; i <= MaxLabels; i++ {
		tooMany[fmt.Sprintf("k%d", i)] = "v"
	}
	if _, err := Normalize("t", "c", Observation{Key: key("x"), Type: "compute.instance", Labels: tooMany}); err == nil {
		t.Error("label count limit not enforced")
	}
}

func TestContentHashIgnoresMapOrderAndDetectsChanges(t *testing.T) {
	a, _ := Normalize("t", "c", obs("i-1", "compute.instance", map[string]string{"a": "1", "b": "2"}))
	b, _ := Normalize("t", "c", obs("i-1", "compute.instance", map[string]string{"b": "2", "a": "1"}))
	if a.ContentHash != b.ContentHash {
		t.Fatal("hash must not depend on map order")
	}
	c, _ := Normalize("t", "c", obs("i-1", "compute.instance", map[string]string{"a": "1", "b": "3"}))
	if a.ContentHash == c.ContentHash {
		t.Fatal("label change must change hash")
	}
}

func TestSnapshotDeduplicatesAndMergesLabels(t *testing.T) {
	s := NewSnapshot("t", "c", 10)
	must(t, s.AddResource(obs("i-1", "compute.instance", map[string]string{"a": "1"})))
	must(t, s.AddResource(obs("i-1", "compute.instance", map[string]string{"b": "2"})))
	if s.ResourceCount() != 1 || s.DuplicateResources != 1 {
		t.Fatalf("dedupe failed: count=%d dup=%d", s.ResourceCount(), s.DuplicateResources)
	}
	if got := s.Resources()[0].Labels; !reflect.DeepEqual(got, map[string]string{"a": "1", "b": "2"}) {
		t.Fatalf("labels not merged: %v", got)
	}
	must(t, s.AddResource(obs("i-2", "compute.instance", nil)))
	must(t, s.AddRelationship(RelationshipObservation{key("i-1"), key("i-2"), RelDependsOn}))
	must(t, s.AddRelationship(RelationshipObservation{key("i-1"), key("i-2"), RelDependsOn}))
	if s.RelationshipCount() != 1 || s.DuplicateEdges != 1 {
		t.Fatal("edge dedupe failed")
	}
	if err := s.AddRelationship(RelationshipObservation{key("i-1"), key("i-1"), RelUses}); err == nil {
		t.Fatal("self edge must be rejected")
	}
	if err := s.AddRelationship(RelationshipObservation{key("i-1"), key("i-2"), "likes"}); err == nil {
		t.Fatal("unknown relationship type must be rejected")
	}
}

func TestSnapshotLimit(t *testing.T) {
	s := NewSnapshot("t", "c", 2)
	must(t, s.AddResource(obs("a", "compute.instance", nil)))
	must(t, s.AddResource(obs("b", "compute.instance", nil)))
	if err := s.AddResource(obs("c", "compute.instance", nil)); !errors.Is(err, ErrSnapshotLimit) {
		t.Fatalf("want ErrSnapshotLimit, got %v", err)
	}
	// A duplicate of an existing key is still accepted at the limit.
	must(t, s.AddResource(obs("a", "compute.instance", nil)))
}

func TestFinalizeDropsDanglingAndContainmentCycles(t *testing.T) {
	s := NewSnapshot("t", "c", 100)
	for _, id := range []string{"vpc", "subnet", "vm", "db"} {
		must(t, s.AddResource(obs(id, "compute.instance", nil)))
	}
	must(t, s.AddRelationship(RelationshipObservation{key("vpc"), key("subnet"), RelContains}))
	must(t, s.AddRelationship(RelationshipObservation{key("subnet"), key("vm"), RelContains}))
	must(t, s.AddRelationship(RelationshipObservation{key("vm"), key("vpc"), RelContains})) // closes a cycle
	must(t, s.AddRelationship(RelationshipObservation{key("vm"), key("db"), RelDependsOn}))
	must(t, s.AddRelationship(RelationshipObservation{key("db"), key("vm"), RelDependsOn})) // cycle allowed
	must(t, s.AddRelationship(RelationshipObservation{key("vm"), key("ghost"), RelUses}))   // dangling

	rep := s.Finalize()
	if len(rep.DanglingEdges) != 1 {
		t.Fatalf("want 1 dangling edge, got %d", len(rep.DanglingEdges))
	}
	if len(rep.CycleEdges) != 1 || rep.CycleEdges[0].Type != RelContains {
		t.Fatalf("want exactly one containment edge rejected, got %+v", rep.CycleEdges)
	}
	if s.RelationshipCount() != 4 {
		t.Fatalf("want 4 edges kept, got %d", s.RelationshipCount())
	}
	g := NewGraph()
	for _, e := range s.Relationships() {
		g.AddEdge(Edge{e.ID, e.SourceID, e.TargetID, e.Type})
	}
	if g.HasCycle(IsAcyclic) {
		t.Fatal("containment subgraph must be acyclic after Finalize")
	}
	if !g.HasCycle(func(t RelationType) bool { return t == RelDependsOn }) {
		t.Fatal("depends_on cycle must be preserved")
	}
}

func TestBackEdgesDeterministic(t *testing.T) {
	build := func() *Graph {
		g := NewGraph()
		g.AddEdge(Edge{"e1", "a", "b", RelContains})
		g.AddEdge(Edge{"e2", "b", "c", RelContains})
		g.AddEdge(Edge{"e3", "c", "a", RelContains})
		return g
	}
	first := build().BackEdges(IsAcyclic)
	for i := 0; i < 20; i++ {
		if !reflect.DeepEqual(first, build().BackEdges(IsAcyclic)) {
			t.Fatal("BackEdges must be deterministic")
		}
	}
	if len(first) != 1 || first[0].ID != "e3" {
		t.Fatalf("unexpected back edges %+v", first)
	}
}

func TestBFSDepthDirectionAndCap(t *testing.T) {
	g := NewGraph()
	// a -> b -> c -> d, and x -> a
	g.AddEdge(Edge{"1", "a", "b", RelContains})
	g.AddEdge(Edge{"2", "b", "c", RelContains})
	g.AddEdge(Edge{"3", "c", "d", RelContains})
	g.AddEdge(Edge{"4", "x", "a", RelDependsOn})
	calls := 0
	load := func(ids []string) ([]Edge, error) {
		calls++
		var es []Edge
		for _, id := range ids {
			es = append(es, g.Out(id)...)
			es = append(es, g.In(id)...)
		}
		return es, nil
	}
	sub, err := BFS("a", 2, 100, DirOut, load)
	must(t, err)
	if !reflect.DeepEqual(sub.Nodes, []string{"a", "b", "c"}) || len(sub.Edges) != 2 || calls != 2 {
		t.Fatalf("out depth 2: %+v calls=%d", sub, calls)
	}
	sub, _ = BFS("a", 1, 100, DirIn, load)
	if !reflect.DeepEqual(sub.Nodes, []string{"a", "x"}) {
		t.Fatalf("in depth 1: %+v", sub.Nodes)
	}
	sub, _ = BFS("a", 3, 2, DirBoth, load)
	if len(sub.Nodes) != 2 || !sub.Truncated {
		t.Fatalf("cap not applied: %+v", sub)
	}
	for _, e := range sub.Edges {
		if !(contains(sub.Nodes, e.From) && contains(sub.Nodes, e.To)) {
			t.Fatalf("edge %v references node outside result", e)
		}
	}
}

func TestReconcileClassification(t *testing.T) {
	s := NewSnapshot("t", "conn", 100)
	must(t, s.AddResource(obs("same", "compute.instance", nil)))
	must(t, s.AddResource(obs("changed", "compute.instance", map[string]string{"v": "2"})))
	must(t, s.AddResource(obs("new", "compute.instance", nil)))
	must(t, s.AddResource(obs("back", "compute.instance", nil)))
	s.Finalize()
	byID := map[string]Resource{}
	for _, r := range s.Resources() {
		byID[r.ExternalID] = r
	}
	old, _ := Normalize("t", "conn", obs("changed", "compute.instance", map[string]string{"v": "1"}))
	existing := map[string]ExistingState{
		byID["same"].ID:    {ID: byID["same"].ID, ContentHash: byID["same"].ContentHash, Status: StatusActive, ConnectionID: "conn"},
		byID["changed"].ID: {ID: byID["changed"].ID, ContentHash: old.ContentHash, Status: StatusActive, ConnectionID: "conn"},
		byID["back"].ID:    {ID: byID["back"].ID, ContentHash: byID["back"].ContentHash, Status: StatusMissing, ConnectionID: "conn"},
		"res_gone":         {ID: "res_gone", Status: StatusActive, ConnectionID: "conn"},
		"res_other_conn":   {ID: "res_other_conn", Status: StatusActive, ConnectionID: "other"},
		"res_already_gone": {ID: "res_already_gone", Status: StatusMissing, ConnectionID: "conn"},
	}
	p := Reconcile(s, existing, nil, true)
	c := p.Counts()
	want := Counts{Added: 1, Changed: 1, Unchanged: 1, Reactivated: 1, Missing: 1}
	if c != want {
		t.Fatalf("counts = %+v, want %+v", c, want)
	}
	if p.Missing[0] != "res_gone" {
		t.Fatalf("wrong missing: %v", p.Missing)
	}
	// Partial runs must never mark anything missing.
	if p := Reconcile(s, existing, nil, false); len(p.Missing) != 0 {
		t.Fatalf("partial run marked missing: %v", p.Missing)
	}
	// Idempotency: replaying the same snapshot against the state it produced is all-unchanged.
	after := map[string]ExistingState{}
	for _, r := range s.Resources() {
		after[r.ID] = ExistingState{ID: r.ID, ContentHash: r.ContentHash, Status: StatusActive, ConnectionID: "conn"}
	}
	if c := Reconcile(s, after, nil, true).Counts(); c.Unchanged != 4 || c.Added+c.Changed+c.Missing+c.Reactivated != 0 {
		t.Fatalf("replay not idempotent: %+v", c)
	}
}

func TestReconcileEdges(t *testing.T) {
	s := NewSnapshot("t", "conn", 100)
	must(t, s.AddResource(obs("a", "compute.instance", nil)))
	must(t, s.AddResource(obs("b", "compute.instance", nil)))
	must(t, s.AddRelationship(RelationshipObservation{key("a"), key("b"), RelUses}))
	s.Finalize()
	eid := s.Relationships()[0].ID
	p := Reconcile(s, nil, map[string]ExistingState{
		eid:        {ID: eid, Status: StatusMissing, ConnectionID: "conn"},
		"rel_gone": {ID: "rel_gone", Status: StatusActive, ConnectionID: "conn"},
	}, true)
	if p.EdgesReactivated != 1 || len(p.EdgesMissing) != 1 || p.EdgesAdded != 0 {
		t.Fatalf("edge plan wrong: %+v", p.Counts())
	}
}

func TestRelationSpecs(t *testing.T) {
	if !IsAcyclic(RelContains) || !IsAcyclic(RelComposedOf) || IsAcyclic(RelDependsOn) {
		t.Fatal("acyclic flags wrong")
	}
	if len(RelationSpecs()) != 7 {
		t.Fatal("expected 7 relation types")
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}
