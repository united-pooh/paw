package streamma

import "testing"

func TestPlanStrategyUnderstandsImplementationAndKeepsToolPolicyConservative(t *testing.T) {
	baseline := planningTestGraph()

	decision := PlanStrategy("实现一个临时工具并运行测试", baseline)

	if decision.Understanding.TaskType != TaskTypeImplementation {
		t.Fatalf("task type = %q, want implementation", decision.Understanding.TaskType)
	}
	if decision.Protocol != PlanProtocolStream {
		t.Fatalf("protocol = %q, want stream", decision.Protocol)
	}
	if decision.StrategyID != StrategyImplementationBaseline {
		t.Fatalf("strategy = %q, want implementation baseline", decision.StrategyID)
	}
	tools := ToolPolicyByAgent(decision.NodePolicies)
	for _, id := range []string{"scout", "builder", "verifier"} {
		if !tools[id].Enabled {
			t.Fatalf("tools[%s].Enabled = false, want true", id)
		}
	}
	for _, id := range []string{"planner", "finalizer"} {
		if tools[id].Enabled {
			t.Fatalf("tools[%s].Enabled = true, want false", id)
		}
	}
	for _, agent := range decision.Graph.Agents {
		if agent.StepCountHint == 0 {
			t.Fatalf("agent %s StepCountHint = 0, want planner hint", agent.ID)
		}
	}
}

func TestPlanStrategySingleStepUsesLogicalSingleFallback(t *testing.T) {
	decision := PlanStrategy("只回答 yes/no", planningTestGraph())

	if decision.Understanding.Decomposable {
		t.Fatalf("Decomposable = true, want false")
	}
	if decision.Protocol != PlanProtocolSingle {
		t.Fatalf("protocol = %q, want single", decision.Protocol)
	}
	if decision.Fallback == nil || !decision.Fallback.Used {
		t.Fatalf("fallback = %#v, want logical protocol fallback recorded", decision.Fallback)
	}
}

func TestPlanStrategyFallsBackToMinimalGraphWhenBaselineIsInvalid(t *testing.T) {
	decision := PlanStrategy("实现一个临时工具", GraphSpec{
		RunID:    "invalid-baseline",
		Protocol: ProtocolStream,
		Agents:   []AgentSpec{{ID: "planner"}},
		Edges:    []EdgeSpec{{From: "planner", To: "missing"}},
	})

	if decision.Fallback == nil || !decision.Fallback.Used {
		t.Fatalf("fallback = %#v, want used fallback", decision.Fallback)
	}
	if err := ValidateStrategyDecision(decision); err != nil {
		t.Fatalf("fallback decision should validate: %v", err)
	}
	if len(decision.Graph.Agents) != 1 || decision.Graph.Agents[0].ID != "finalizer" {
		t.Fatalf("fallback graph = %#v, want single finalizer", decision.Graph)
	}
}

func TestGraphFromTopologyBuildsConfigStyleDAG(t *testing.T) {
	graph, err := GraphFromTopology("topology-run", StepPolicy{RequireBoundary: true, MaxAttempts: 2}, TopologyConfig{
		Agents: []TopologyAgent{
			{ID: "planner", Role: "source", Next: []string{"solver"}, StepCountHint: 2},
			{ID: "solver", Role: "worker", Next: []string{"finalizer"}},
			{ID: "finalizer", Role: "sink", InvokePolicy: string(InvokeOnEOF)},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(graph.Agents) != 3 || len(graph.Edges) != 2 {
		t.Fatalf("graph = %#v, want 3 agents and 2 edges", graph)
	}
	if graph.Agents[0].StepCountHint != 2 {
		t.Fatalf("planner StepCountHint = %d, want 2", graph.Agents[0].StepCountHint)
	}
	if graph.Agents[2].InvokePolicy != string(InvokeOnEOF) {
		t.Fatalf("finalizer InvokePolicy = %q, want eof", graph.Agents[2].InvokePolicy)
	}
}

func TestGraphFromTopologyDictBuildsOfficialStyleDAG(t *testing.T) {
	graph, err := GraphFromTopologyDict("dict-run", StepPolicy{RequireBoundary: true, MaxAttempts: 2}, TopologyDict{
		"A": {SystemPrompt: "agent A", Next: []string{"B", "C"}},
		"B": {SystemPrompt: "agent B", Next: []string{"C"}},
		"C": {SystemPrompt: "agent C", InvokePolicy: string(InvokeOnEOF)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(graph.Agents) != 3 || len(graph.Edges) != 3 {
		t.Fatalf("graph = %#v, want 3 agents and 3 edges", graph)
	}
	edges := map[string]bool{}
	for _, edge := range graph.Edges {
		edges[edge.From+"->"+edge.To] = true
	}
	for _, want := range []string{"A->B", "A->C", "B->C"} {
		if !edges[want] {
			t.Fatalf("edges = %#v, missing %s", edges, want)
		}
	}
	if graph.Agents[0].ID != "A" || graph.Agents[2].InvokePolicy != string(InvokeOnEOF) {
		t.Fatalf("agents = %#v, want stable dict conversion with C EOF", graph.Agents)
	}
}

func TestValidateStrategyDecisionRejectsInvalidGraph(t *testing.T) {
	err := ValidateStrategyDecision(StrategyDecision{
		StrategyID: "test.invalid",
		Protocol:   PlanProtocolStream,
		Graph: GraphSpec{
			RunID:    "invalid",
			Protocol: ProtocolStream,
			Agents:   []AgentSpec{{ID: "planner"}},
			Edges:    []EdgeSpec{{From: "planner", To: "missing"}},
		},
	})
	if err == nil {
		t.Fatalf("ValidateStrategyDecision() error = nil, want invalid edge error")
	}
}

func TestClassifyReplanPreservesCommittedSteps(t *testing.T) {
	decision := ClassifyReplan("retry exhaustion", true)
	if !decision.Triggered {
		t.Fatalf("Triggered = false, want true")
	}
	if decision.Action != "fail_fast_preserve_committed_steps" {
		t.Fatalf("Action = %q, want preserve committed fail-fast", decision.Action)
	}
}

func planningTestGraph() GraphSpec {
	return GraphSpec{
		RunID:    "planning-test",
		Protocol: ProtocolStream,
		StepPolicy: StepPolicy{
			Boundary:        DefaultBoundary,
			RequireBoundary: true,
			MaxAttempts:     2,
		},
		Agents: []AgentSpec{
			{ID: "planner", Role: "source"},
			{ID: "scout", Role: "source"},
			{ID: "builder", Role: "worker"},
			{ID: "verifier", Role: "checker", InvokePolicy: string(InvokeOnEOF)},
			{ID: "finalizer", Role: "sink", InvokePolicy: string(InvokeOnEOF)},
		},
		Edges: []EdgeSpec{
			{From: "planner", To: "builder"},
			{From: "scout", To: "builder"},
			{From: "builder", To: "verifier"},
			{From: "builder", To: "finalizer"},
			{From: "verifier", To: "finalizer"},
		},
	}
}
