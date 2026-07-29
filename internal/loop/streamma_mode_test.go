package loop

import (
	"paw/internal/streamma"
	"testing"
)

func TestStreamMATopologyFlagSelectsTopologyWithoutPaperProfile(t *testing.T) {
	invocation, ok := parseStreamMAInvocation("/streamma-trace --topology chain --agents 3 --steps 1 explain a cache trace")
	if !ok {
		t.Fatal("parseStreamMAInvocation() did not detect command")
	}
	if invocation.ParseError != "" {
		t.Fatalf("ParseError = %q", invocation.ParseError)
	}

	kind, spec, evidence := streamMAGraphForInvocation(invocation.Task, invocation)
	if kind != streamMAGraphReasoning {
		t.Fatalf("kind = %s, want %s", kind, streamMAGraphReasoning)
	}
	if evidence == nil || evidence.SelectedTopology != streamma.TopologyShapeChain {
		t.Fatalf("evidence.SelectedTopology = %#v, want chain", evidence)
	}
	if len(spec.Agents) != 3 {
		t.Fatalf("len(spec.Agents) = %d, want 3", len(spec.Agents))
	}
	wantEdges := []streamma.EdgeSpec{
		{From: "Agent_01", To: "Agent_02"},
		{From: "Agent_02", To: "Agent_03"},
	}
	if len(spec.Edges) != len(wantEdges) {
		t.Fatalf("edges = %#v, want %#v", spec.Edges, wantEdges)
	}
	for i := range wantEdges {
		if spec.Edges[i] != wantEdges[i] {
			t.Fatalf("edges[%d] = %#v, want %#v", i, spec.Edges[i], wantEdges[i])
		}
	}
}
