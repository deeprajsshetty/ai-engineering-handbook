package topology

import "sort"

// Edge is the graph view of a relationship.
type Edge struct {
	ID   string       `json:"id"`
	From string       `json:"sourceId"`
	To   string       `json:"targetId"`
	Type RelationType `json:"type"`
}

// Direction selects which adjacency lists a traversal follows.
type Direction string

const (
	DirOut  Direction = "out"
	DirIn   Direction = "in"
	DirBoth Direction = "both"
)

// ValidDirection reports whether d is known.
func ValidDirection(d string) bool {
	return d == string(DirOut) || d == string(DirIn) || d == string(DirBoth)
}

// Graph is a directed multigraph stored as adjacency lists.
//
// Space O(V+E). AddEdge is O(1) amortized (duplicate edge IDs are ignored via
// a set). Out/In lookups are O(1) plus the size of the list.
type Graph struct {
	nodes map[string]struct{}
	out   map[string][]Edge
	in    map[string][]Edge
	seen  map[string]struct{}
}

// NewGraph returns an empty graph.
func NewGraph() *Graph {
	return &Graph{
		nodes: map[string]struct{}{},
		out:   map[string][]Edge{},
		in:    map[string][]Edge{},
		seen:  map[string]struct{}{},
	}
}

// AddNode adds a node if absent.
func (g *Graph) AddNode(id string) { g.nodes[id] = struct{}{} }

// HasNode reports whether id is a node.
func (g *Graph) HasNode(id string) bool { _, ok := g.nodes[id]; return ok }

// AddEdge adds an edge and its endpoints. It returns false for a duplicate ID.
func (g *Graph) AddEdge(e Edge) bool {
	if _, dup := g.seen[e.ID]; dup {
		return false
	}
	g.seen[e.ID] = struct{}{}
	g.AddNode(e.From)
	g.AddNode(e.To)
	g.out[e.From] = append(g.out[e.From], e)
	g.in[e.To] = append(g.in[e.To], e)
	return true
}

// NodeCount returns |V|.
func (g *Graph) NodeCount() int { return len(g.nodes) }

// EdgeCount returns |E|.
func (g *Graph) EdgeCount() int { return len(g.seen) }

// Out returns outgoing edges of id.
func (g *Graph) Out(id string) []Edge { return g.out[id] }

// In returns incoming edges of id.
func (g *Graph) In(id string) []Edge { return g.in[id] }

func (g *Graph) sortedNodes() []string {
	ns := make([]string, 0, len(g.nodes))
	for n := range g.nodes {
		ns = append(ns, n)
	}
	sort.Strings(ns)
	return ns
}

// BackEdges runs an iterative three-colour depth-first search over the edges
// accepted by include and returns the back edges found. Removing every
// returned edge leaves the included subgraph acyclic (a DFS without back edges
// is a DAG). Traversal order is sorted, so the result is deterministic for a
// given graph. Time O(V+E), space O(V).
func (g *Graph) BackEdges(include func(RelationType) bool) []Edge {
	const (
		white = 0
		grey  = 1
		black = 2
	)
	colour := make(map[string]int, len(g.nodes))
	adj := func(n string) []Edge {
		var es []Edge
		for _, e := range g.out[n] {
			if include(e.Type) {
				es = append(es, e)
			}
		}
		sort.Slice(es, func(i, j int) bool { return es[i].ID < es[j].ID })
		return es
	}
	type frame struct {
		node  string
		edges []Edge
		next  int
	}
	var back []Edge
	for _, root := range g.sortedNodes() {
		if colour[root] != white {
			continue
		}
		stack := []frame{{node: root, edges: adj(root)}}
		colour[root] = grey
		for len(stack) > 0 {
			top := &stack[len(stack)-1]
			if top.next >= len(top.edges) {
				colour[top.node] = black
				stack = stack[:len(stack)-1]
				continue
			}
			e := top.edges[top.next]
			top.next++
			switch colour[e.To] {
			case white:
				colour[e.To] = grey
				stack = append(stack, frame{node: e.To, edges: adj(e.To)})
			case grey:
				back = append(back, e) // e closes a cycle
			}
		}
	}
	return back
}

// HasCycle reports whether the included subgraph contains a directed cycle.
func (g *Graph) HasCycle(include func(RelationType) bool) bool {
	return len(g.BackEdges(include)) > 0
}

// NeighborLoader returns all edges touching any of the given node IDs. It lets
// BFS run against a store one frontier at a time instead of loading the whole
// graph.
type NeighborLoader func(nodeIDs []string) ([]Edge, error)

// Subgraph is the result of a bounded traversal.
type Subgraph struct {
	Nodes     []string `json:"nodeIds"`
	Edges     []Edge   `json:"edges"`
	Truncated bool     `json:"truncated"`
}

// BFS expands from start breadth-first up to depth hops, following edges in
// the given direction, and stops adding nodes once maxNodes is reached
// (Truncated is then true). Each level issues one loader call, so a store
// performs at most depth queries. Time O(V'+E') for the visited subgraph.
func BFS(start string, depth, maxNodes int, dir Direction, load NeighborLoader) (Subgraph, error) {
	visited := map[string]struct{}{start: {}}
	order := []string{start}
	edgeSeen := map[string]struct{}{}
	var edges []Edge
	frontier := []string{start}
	truncated := false
	for level := 0; level < depth && len(frontier) > 0; level++ {
		es, err := load(frontier)
		if err != nil {
			return Subgraph{}, err
		}
		sort.Slice(es, func(i, j int) bool { return es[i].ID < es[j].ID })
		inFrontier := make(map[string]struct{}, len(frontier))
		for _, f := range frontier {
			inFrontier[f] = struct{}{}
		}
		var next []string
		for _, e := range es {
			var nb string
			_, fromF := inFrontier[e.From]
			_, toF := inFrontier[e.To]
			switch {
			case fromF && (dir == DirOut || dir == DirBoth):
				nb = e.To
			case toF && (dir == DirIn || dir == DirBoth):
				nb = e.From
			default:
				continue
			}
			if _, ok := visited[nb]; !ok {
				if len(visited) >= maxNodes {
					truncated = true
					continue
				}
				visited[nb] = struct{}{}
				order = append(order, nb)
				next = append(next, nb)
			}
			if _, ok := edgeSeen[e.ID]; !ok {
				edgeSeen[e.ID] = struct{}{}
				edges = append(edges, e)
			}
		}
		frontier = next
	}
	// Keep only edges whose both endpoints are in the result.
	kept := edges[:0]
	for _, e := range edges {
		_, a := visited[e.From]
		_, b := visited[e.To]
		if a && b {
			kept = append(kept, e)
		}
	}
	return Subgraph{Nodes: order, Edges: kept, Truncated: truncated}, nil
}
