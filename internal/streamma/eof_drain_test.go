package streamma

import (
	"context"
	"testing"
)

func TestRuntimeDrainsQueuedStepsBeforeSinkFinal(t *testing.T) {
	spec := GraphSpec{
		RunID: "run-eof-drain",
		Agents: []AgentSpec{
			{ID: "a"},
			{ID: "c"},
			{ID: "b"},
		},
		Edges: []EdgeSpec{
			{From: "a", To: "c"},
			{From: "b", To: "c"},
		},
	}
	model := newFakeModel(
		fakeTextResponse("from a\nEND_STEP\n"),
		fakeTextResponse("c after a\nEND_STEP\n"),
		fakeTextResponse("from b\nEND_STEP\n"),
		fakeTextResponse("c after b\nEND_STEP\n"),
	)

	result, err := RunGraph(context.Background(), spec, model, "problem")
	if err != nil {
		t.Fatal(err)
	}
	if result.Final == nil || result.Final.Answer.Text != "c after b" {
		t.Fatalf("sink finalized before draining later queued step: %#v", result.Final)
	}

	eofBeforeFinal := 0
	for _, event := range result.Events {
		if event.Type == EventFinalAnswer {
			break
		}
		if event.Type == EventUpstreamEOF && event.TargetAgentID == "c" {
			eofBeforeFinal++
		}
	}
	if eofBeforeFinal != 2 {
		t.Fatalf("EOFs before final = %d, want 2", eofBeforeFinal)
	}
}

func TestRuntimeFailedSummaryForSinkFailure(t *testing.T) {
	spec := GraphSpec{
		RunID:      "run-failed-summary",
		StepPolicy: StepPolicy{MaxStepBytes: 3},
		Agents:     []AgentSpec{{ID: "sink"}},
	}
	model := newFakeModel(fakeTextResponse("too long without boundary"))

	result, err := RunGraph(context.Background(), spec, model, "problem")
	if err == nil {
		t.Fatal("expected failure")
	}
	if result.Status != RunFailed {
		t.Fatalf("status = %s, want failed", result.Status)
	}
	if result.Error == "" {
		t.Fatal("failed summary is missing error")
	}
}
