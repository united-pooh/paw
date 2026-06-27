package streamma

import "testing"

func TestGraphFromTopologyShapeBuildsChainTreeAndGraph(t *testing.T) {
	agents := []TopologyAgent{
		{ID: "A"},
		{ID: "B"},
		{ID: "C"},
		{ID: "D"},
	}
	tests := []struct {
		name      string
		shape     string
		wantEdges []string
		wantPath  int
	}{
		{name: "chain", shape: TopologyShapeChain, wantEdges: []string{"A->B", "B->C", "C->D"}, wantPath: 4},
		{name: "tree", shape: TopologyShapeTree, wantEdges: []string{"A->B", "A->C", "A->D"}, wantPath: 2},
		{name: "graph", shape: TopologyShapeGraph, wantEdges: []string{"A->B", "A->C", "A->D", "B->C", "C->D"}, wantPath: 4},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			graph, err := GraphFromTopologyShape("shape-test", StepPolicy{RequireBoundary: true, MaxAttempts: 2}, agents, tt.shape)
			if err != nil {
				t.Fatal(err)
			}
			edges := map[string]bool{}
			for _, edge := range graph.Edges {
				edges[edge.From+"->"+edge.To] = true
			}
			for _, want := range tt.wantEdges {
				if !edges[want] {
					t.Fatalf("edges = %#v, missing %s", edges, want)
				}
			}
			if got := CriticalPathAgentCount(graph); got != tt.wantPath {
				t.Fatalf("CriticalPathAgentCount = %d, want %d", got, tt.wantPath)
			}
		})
	}
}

func TestGraphFromTopologyShapeRejectsTooFewAgents(t *testing.T) {
	_, err := GraphFromTopologyShape("shape-test", StepPolicy{}, []TopologyAgent{{ID: "A"}}, TopologyShapeChain)
	if err == nil {
		t.Fatal("GraphFromTopologyShape() error = nil, want too few agents error")
	}
}
