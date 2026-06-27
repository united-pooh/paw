package streamma

import "testing"

func TestStepCorrectnessFromValuesMatchesPaperHeadTailCase(t *testing.T) {
	ev := StepCorrectnessFromValues([]float64{1, 1, 0, 0, 0, 0, 0, 0}, 0.3)
	if ev.ProfileCase != StepProfileCaseStreamIb || ev.Advantage != PlanProtocolStream {
		t.Fatalf("profile = %s advantage=%s, want stream I.b", ev.ProfileCase, ev.Advantage)
	}
	if ev.PHead != 0.4167 || ev.PTail != 0.0357 || ev.PAvg != 0.25 {
		t.Fatalf("p metrics = head %.4f tail %.4f avg %.4f", ev.PHead, ev.PTail, ev.PAvg)
	}
}

func TestEvidenceRouterSelectsChainForHeadStrongTailWeak(t *testing.T) {
	understanding := UnderstandTask("solve a linear math proof")
	step := StepCorrectnessFromValues([]float64{1, 1, 0, 0}, 0.5)
	decision := SelectGraphTopology(GraphRouterInput{
		Understanding:   understanding,
		AgentCount:      32,
		StepCount:       64,
		StepCorrectness: &step,
	})
	if decision.SelectedTopology != TopologyShapeChain {
		t.Fatalf("SelectedTopology = %q, want chain", decision.SelectedTopology)
	}
	if decision.EvidenceLevel == EvidenceLevelHeuristic {
		t.Fatalf("EvidenceLevel = heuristic, want measured/prior with step evidence")
	}
}

func TestEvidenceRouterSelectsTreeForIndependentSubproblems(t *testing.T) {
	decision := SelectGraphTopology(GraphRouterInput{
		Understanding: UnderstandTask("compare multiple independent subproblems in parallel"),
		AgentCount:    8,
		StepCount:     16,
	})
	if decision.SelectedTopology != TopologyShapeTree {
		t.Fatalf("SelectedTopology = %q, want tree", decision.SelectedTopology)
	}
	if decision.CriticalPath.CriticalPathAgentCount != 2 {
		t.Fatalf("CriticalPathAgentCount = %d, want 2", decision.CriticalPath.CriticalPathAgentCount)
	}
}

func TestEvidenceRouterSelectsGraphForCrossCheckTasks(t *testing.T) {
	decision := SelectGraphTopology(GraphRouterInput{
		Understanding: UnderstandTask("solve and cross-check with shortcut reasoning"),
		AgentCount:    8,
		StepCount:     16,
	})
	if decision.SelectedTopology != TopologyShapeGraph {
		t.Fatalf("SelectedTopology = %q, want graph", decision.SelectedTopology)
	}
}

func TestEvidenceRouterKeepsNonDecomposableOutOfPaperTopology(t *testing.T) {
	decision := SelectGraphTopology(GraphRouterInput{
		Understanding: UnderstandTask("write a poem"),
		AgentCount:    32,
		StepCount:     64,
	})
	if decision.SelectedTopology != TopologyShapeAdaptive {
		t.Fatalf("SelectedTopology = %q, want adaptive fallback", decision.SelectedTopology)
	}
}

func TestEvidenceRouterMissingCacheSignalIsUnknownNotLowCache(t *testing.T) {
	decision := SelectGraphTopology(GraphRouterInput{
		Understanding: UnderstandTask("solve a linear math proof"),
		AgentCount:    32,
		StepCount:     64,
		Cache:         CacheEvidence{PrefillTokens: 1000, DecodeTokens: 10},
	})
	if decision.Cache.CacheSignal != "unknown" {
		t.Fatalf("CacheSignal = %q, want unknown", decision.Cache.CacheSignal)
	}
	if decision.SelectedTopology != TopologyShapeChain {
		t.Fatalf("SelectedTopology = %q, want missing cache signal not to force tree", decision.SelectedTopology)
	}
}

func TestSelectBestScalingPrefersSpeedThenStepDeviation(t *testing.T) {
	best, ok := SelectBestScaling([]ScalingEvidence{
		{TopologyShape: TopologyShapeChain, ObservedOverlapSpeedup: 2, StepDeviation: 10},
		{TopologyShape: TopologyShapeTree, ObservedOverlapSpeedup: 3, StepDeviation: 20},
		{TopologyShape: TopologyShapeGraph, ObservedOverlapSpeedup: 3, StepDeviation: 2},
	})
	if !ok || best.TopologyShape != TopologyShapeGraph {
		t.Fatalf("best = %#v ok=%t, want graph by tie-break", best, ok)
	}
}
