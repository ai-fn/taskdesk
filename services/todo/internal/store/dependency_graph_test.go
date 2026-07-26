package store

import "testing"

func TestHasCycle(t *testing.T) {
	tests := []struct {
		name     string
		existing []Edge
		proposed Edge
		want     bool
	}{
		{
			name:     "empty graph, simple edge",
			existing: nil,
			proposed: Edge{From: "A", To: "B"},
			want:     false,
		},
		{
			name:     "linear chain extension is fine",
			existing: []Edge{{"A", "B"}, {"B", "C"}},
			proposed: Edge{From: "C", To: "D"},
			want:     false,
		},
		{
			name:     "diamond is fine (DAG, not a tree)",
			existing: []Edge{{"A", "B"}, {"A", "C"}, {"B", "D"}},
			proposed: Edge{From: "C", To: "D"},
			want:     false,
		},
		{
			name:     "direct cycle: A→B then B→A",
			existing: []Edge{{"A", "B"}},
			proposed: Edge{From: "B", To: "A"},
			want:     true,
		},
		{
			name:     "transitive cycle: A→B, B→C, then C→A",
			existing: []Edge{{"A", "B"}, {"B", "C"}},
			proposed: Edge{From: "C", To: "A"},
			want:     true,
		},
		{
			name:     "deep transitive cycle",
			existing: []Edge{{"A", "B"}, {"B", "C"}, {"C", "D"}, {"D", "E"}},
			proposed: Edge{From: "E", To: "A"},
			want:     true,
		},
		{
			name:     "self-loop",
			existing: nil,
			proposed: Edge{From: "A", To: "A"},
			want:     true,
		},
		{
			name:     "unrelated subgraphs, no cycle",
			existing: []Edge{{"A", "B"}, {"C", "D"}},
			proposed: Edge{From: "E", To: "F"},
			want:     false,
		},
		{
			name:     "would-be cycle via separate path",
			existing: []Edge{{"A", "B"}, {"A", "C"}, {"C", "D"}},
			proposed: Edge{From: "D", To: "A"},
			want:     true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := HasCycle(tc.existing, tc.proposed)
			if got != tc.want {
				t.Errorf("HasCycle(%v, %v) = %v, want %v",
					tc.existing, tc.proposed, got, tc.want)
			}
		})
	}
}
