package streamma

import (
	"strings"
	"testing"
)

func TestBuildRoutingRationaleSummarizesGraphPoliciesAndEvents(t *testing.T) {
	decision := PlanStrategy("实现一个临时工具", planningTestGraph())
	events := []Event{
		{Type: EventStepCommitted, ProducerAgentID: "planner"},
		{Type: EventAgentRetry, ProducerAgentID: "builder"},
		{Type: EventFinalAnswer, ProducerAgentID: "finalizer", Final: &FinalAnswerPacket{AgentID: "finalizer"}},
	}

	rationale := BuildRoutingRationale(decision, events)

	if !strings.Contains(rationale.Artifact, "streamma-routing") {
		t.Fatalf("artifact = %q, want streamma-routing path", rationale.Artifact)
	}
	if strings.Contains(rationale.Artifact, "tree-grading") || strings.Contains(rationale.Artifact, "review") {
		t.Fatalf("artifact = %q, should be distinct from review grading artifacts", rationale.Artifact)
	}
	if rationale.SelectedStrategy != StrategyImplementationBaseline {
		t.Fatalf("SelectedStrategy = %q", rationale.SelectedStrategy)
	}
	if len(rationale.GraphSummary.Agents) != 5 || len(rationale.GraphSummary.Sources) != 2 {
		t.Fatalf("GraphSummary = %#v", rationale.GraphSummary)
	}
	if rationale.EventSummary.CommittedSteps != 1 || rationale.EventSummary.RetryEvents != 1 || rationale.EventSummary.FinalAgent != "finalizer" {
		t.Fatalf("EventSummary = %#v", rationale.EventSummary)
	}
	if !rationale.ToolPolicy["builder"].Enabled {
		t.Fatalf("builder tool policy = %#v, want enabled", rationale.ToolPolicy["builder"])
	}
	if len(rationale.PaperAssumptions) == 0 || len(rationale.EngineeringExtensions) == 0 {
		t.Fatalf("rationale missing source assumptions/extensions: %#v", rationale)
	}
}
