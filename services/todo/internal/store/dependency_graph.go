package store

// Edge represents a single dependency: From → To means From depends on To
// (From is blocked until To is done).
type Edge struct {
	From string
	To   string
}

// HasCycle reports whether adding `proposed` to `existing` would create a
// directed cycle. Three-color DFS: white (unvisited), gray (on current stack),
// black (fully explored). A back-edge to a gray node means a cycle.
func HasCycle(existing []Edge, proposed Edge) bool {
	// Build adjacency list including the proposed edge.
	adj := make(map[string][]string)
	for _, e := range existing {
		adj[e.From] = append(adj[e.From], e.To)
	}
	adj[proposed.From] = append(adj[proposed.From], proposed.To)

	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := make(map[string]int)

	var visit func(node string) bool
	visit = func(node string) bool {
		color[node] = gray
		for _, next := range adj[node] {
			switch color[next] {
			case gray:
				return true // back-edge → cycle
			case white:
				if visit(next) {
					return true
				}
			}
		}
		color[node] = black
		return false
	}

	// Start DFS only from the proposed.From — the only node whose reachable
	// set just changed. A cycle introduced by adding this edge must include it.
	return visit(proposed.From)
}
