package streamma

import (
	"strings"
	"testing"
	"time"
)

func TestBuildRunLoggerMatchesOfficialSummaryShape(t *testing.T) {
	base := time.Date(2026, 6, 21, 10, 0, 0, 0, time.UTC)
	graph, err := GraphFromTopologyDict("runlogger-test", StepPolicy{RequireBoundary: true, MaxAttempts: 2}, TopologyDict{
		"Agent_A": {SystemPrompt: "A", Next: []string{"Agent_B"}},
		"Agent_B": {SystemPrompt: "B", Next: []string{"Agent_C"}},
		"Agent_C": {SystemPrompt: "C"},
	})
	if err != nil {
		t.Fatal(err)
	}
	events := []Event{
		{
			Type:            EventStepCommitted,
			Timestamp:       base.Add(10 * time.Second),
			ProducerAgentID: "Agent_A",
			Step:            &StepPacket{AgentID: "Agent_A", StepID: "Agent_A:1"},
		},
		{
			Type:            EventStepCommitted,
			Timestamp:       base.Add(50 * time.Second),
			ProducerAgentID: "Agent_B",
			Step:            &StepPacket{AgentID: "Agent_B", StepID: "Agent_B:1"},
		},
		{
			Type:            EventStepCommitted,
			Timestamp:       base.Add(80 * time.Second),
			ProducerAgentID: "Agent_B",
			Step:            &StepPacket{AgentID: "Agent_B", StepID: "Agent_B:2"},
		},
		{
			Type:            EventStepCommitted,
			Timestamp:       base.Add(100 * time.Second),
			ProducerAgentID: "Agent_C",
			Step:            &StepPacket{AgentID: "Agent_C", StepID: "Agent_C:1"},
		},
	}
	logger := BuildRunLogger(graph, events, []AgentRunSpan{
		{AgentID: "Agent_A", StartedAt: base, FinishedAt: base.Add(20 * time.Second), Usage: StepUsage{InputTokens: 100, CachedTokens: 20, OutputTokens: 50}},
		{AgentID: "Agent_B", StartedAt: base.Add(10 * time.Second), FinishedAt: base.Add(80 * time.Second), Usage: StepUsage{InputTokens: 200, CachedTokens: 100, OutputTokens: 80}},
		{AgentID: "Agent_C", StartedAt: base.Add(60 * time.Second), FinishedAt: base.Add(120 * time.Second), Usage: StepUsage{InputTokens: 300, CachedTokens: 180, OutputTokens: 70}},
	})

	if logger.Summary.AgentCount != 3 {
		t.Fatalf("AgentCount = %d, want 3", logger.Summary.AgentCount)
	}
	if logger.Summary.Agents["Agent_B"].Segments != 2 {
		t.Fatalf("Agent_B segments = %d, want 2", logger.Summary.Agents["Agent_B"].Segments)
	}
	if logger.Summary.TotalPrefillTokens != 600 ||
		logger.Summary.TotalCachedTokens != 300 ||
		logger.Summary.TotalDecodeTokens != 200 ||
		logger.Summary.TotalTokens != 800 {
		t.Fatalf("summary totals = %#v", logger.Summary)
	}
	if logger.Summary.Agents["Agent_C"].KVCacheHitRatio != 0.6 {
		t.Fatalf("Agent_C KVCacheHitRatio = %v, want 0.6", logger.Summary.Agents["Agent_C"].KVCacheHitRatio)
	}
	if logger.Summary.SpeedupAnalysis.APITime != 150 ||
		logger.Summary.SpeedupAnalysis.WallTime != 120 ||
		logger.Summary.SpeedupAnalysis.Speedup != 1.25 ||
		logger.Summary.SpeedupAnalysis.TotalKVCacheHitRatio != 0.5 {
		t.Fatalf("speedup analysis = %#v", logger.Summary.SpeedupAnalysis)
	}
	timeline := strings.Join(logger.Summary.SpeedupAnalysis.Timeline, "\n")
	for _, want := range []string{"[Timeline]", "Agent Agent_A:", "Agent Agent_B:", "Agent Agent_C:", "█", "░", "Legend: █ = processing, ░ = idle"} {
		if !strings.Contains(timeline, want) {
			t.Fatalf("timeline = %q, want %q", timeline, want)
		}
	}
}

func TestSafeArtifactIDSanitizesRunIDForPersistentPath(t *testing.T) {
	if got := SafeArtifactID("../streamma run:1"); got != "streamma_run_1" {
		t.Fatalf("SafeArtifactID() = %q, want streamma_run_1", got)
	}
	if got := RunLoggerRunArtifactPath("../streamma run:1"); got != ".pipeline-workspace/streamma-routing/runs/streamma_run_1/streamma-runlogger.json" {
		t.Fatalf("RunLoggerRunArtifactPath() = %q", got)
	}
}

func TestBuildRunLoggerComputesDAGCriticalPathInsteadOfTotalAPI(t *testing.T) {
	base := time.Date(2026, 6, 21, 10, 0, 0, 0, time.UTC)
	graph, err := GraphFromTopologyDict("critical-path-test", StepPolicy{RequireBoundary: true, MaxAttempts: 2}, TopologyDict{
		"A": {Next: []string{"B", "C"}},
		"B": {},
		"C": {},
	})
	if err != nil {
		t.Fatal(err)
	}
	logger := BuildRunLogger(graph, nil, []AgentRunSpan{
		{AgentID: "A", StartedAt: base, FinishedAt: base.Add(10 * time.Second)},
		{AgentID: "B", StartedAt: base.Add(10 * time.Second), FinishedAt: base.Add(40 * time.Second)},
		{AgentID: "C", StartedAt: base.Add(10 * time.Second), FinishedAt: base.Add(20 * time.Second)},
	})
	if logger.Summary.SpeedupAnalysis.APITime != 50 {
		t.Fatalf("APITime = %v, want 50", logger.Summary.SpeedupAnalysis.APITime)
	}
	if logger.Summary.SpeedupAnalysis.CriticalPathTime != 40 {
		t.Fatalf("CriticalPathTime = %v, want 40", logger.Summary.SpeedupAnalysis.CriticalPathTime)
	}
}
