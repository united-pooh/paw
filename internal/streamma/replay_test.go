package streamma

import (
	"context"
	"testing"
)

func TestReplayPreservesCommittedEventOrder(t *testing.T) {
	spec := GraphSpec{
		RunID: "run-replay-order",
		Agents: []AgentSpec{
			{ID: "b"},
			{ID: "a"},
		},
		Edges: []EdgeSpec{{From: "b", To: "a"}},
	}
	model := newFakeModel(
		fakeTextResponse("from b\nEND_STEP\n"),
		fakeTextResponse("from a\nEND_STEP\n"),
	)

	result, err := RunGraph(context.Background(), spec, model, "problem")
	if err != nil {
		t.Fatal(err)
	}
	replayed := Replay(result.Events)
	if len(replayed.Events) != len(result.Events) {
		t.Fatalf("replayed events = %d, want %d", len(replayed.Events), len(result.Events))
	}
	for i := range result.Events {
		if replayed.Events[i].EventID != result.Events[i].EventID {
			t.Fatalf("event order changed at %d: got %s want %s", i, replayed.Events[i].EventID, result.Events[i].EventID)
		}
	}
	if len(replayed.Steps) != 2 || replayed.Steps[0].AgentID != "b" || replayed.Steps[1].AgentID != "a" {
		t.Fatalf("step order changed: %#v", replayed.Steps)
	}
	if replayed.Final == nil || replayed.Final.Answer.Text != "from a" {
		t.Fatalf("final replay mismatch: %#v", replayed.Final)
	}
}

func TestReplayReconstructsTranscriptAndLifecycle(t *testing.T) {
	spec := GraphSpec{
		RunID: "run-replay-state",
		Agents: []AgentSpec{
			{ID: "a"},
			{ID: "b"},
			{ID: "c"},
		},
		Edges: []EdgeSpec{
			{From: "a", To: "b"},
			{From: "b", To: "c"},
		},
	}
	model := newFakeModel(
		fakeTextResponse("from a\nEND_STEP\n"),
		fakeTextResponse("from b\nEND_STEP\n"),
		fakeTextResponse("from c\nEND_STEP\n"),
	)

	result, err := RunGraph(context.Background(), spec, model, "problem")
	if err != nil {
		t.Fatal(err)
	}
	replayed := Replay(result.Events)
	stateA := replayAgentState(t, replayed, "a")
	stateB := replayAgentState(t, replayed, "b")
	stateC := replayAgentState(t, replayed, "c")

	if !stateA.Completed || stateA.Final {
		t.Fatalf("unexpected a lifecycle: %#v", stateA)
	}
	if !stateB.Completed || len(stateB.ReceivedEOF) != 1 || stateB.ReceivedEOF[0] != "a" {
		t.Fatalf("unexpected b lifecycle: %#v", stateB)
	}
	if !stateC.Completed || !stateC.Final || len(stateC.ReceivedEOF) != 1 || stateC.ReceivedEOF[0] != "b" {
		t.Fatalf("unexpected c lifecycle: %#v", stateC)
	}
	if len(stateB.Transcript) != 2 ||
		stateB.Transcript[0].Kind != TranscriptInbound ||
		stateB.Transcript[0].From != "a" ||
		stateB.Transcript[0].Text != "from a\n" ||
		stateB.Transcript[1].Kind != TranscriptOwn ||
		stateB.Transcript[1].Text != "from b\n" {
		t.Fatalf("unexpected b transcript: %#v", stateB.Transcript)
	}
	if len(stateC.Transcript) != 2 ||
		stateC.Transcript[0].Kind != TranscriptInbound ||
		stateC.Transcript[0].From != "b" ||
		stateC.Transcript[1].Kind != TranscriptOwn {
		t.Fatalf("unexpected c transcript: %#v", stateC.Transcript)
	}
}

func TestFailureReplayReachesSameFailurePointWithoutModelInvocation(t *testing.T) {
	spec := GraphSpec{
		RunID:      "run-replay-failure",
		StepPolicy: StepPolicy{MaxStepBytes: 3},
		Agents:     []AgentSpec{{ID: "a"}, {ID: "b"}},
		Edges:      []EdgeSpec{{From: "a", To: "b"}},
	}
	model := newFakeModel(fakeTextResponse("too long"))

	result, err := RunGraph(context.Background(), spec, model, "problem")
	if err == nil {
		t.Fatal("expected failure")
	}
	callsBeforeReplay := len(model.Calls())
	replayed := Replay(result.Events)
	callsAfterReplay := len(model.Calls())
	if callsAfterReplay != callsBeforeReplay {
		t.Fatalf("replay invoked model: before=%d after=%d", callsBeforeReplay, callsAfterReplay)
	}
	if replayed.Status != RunFailed {
		t.Fatalf("replay status = %s, want failed", replayed.Status)
	}
	if replayed.FailureSequence == 0 || replayed.FailureEventID == "" {
		t.Fatalf("replay missing failure point: %#v", replayed)
	}
	if replayed.FailureSequence != result.Events[len(result.Events)-1].Seq {
		t.Fatalf("failure sequence mismatch: got %d want %d", replayed.FailureSequence, result.Events[len(result.Events)-1].Seq)
	}
}

func replayAgentState(t *testing.T, summary ReplaySummary, agentID string) ReplayAgentState {
	t.Helper()
	for _, state := range summary.AgentStates {
		if state.AgentID == agentID {
			return state
		}
	}
	t.Fatalf("missing replay state for %s: %#v", agentID, summary.AgentStates)
	return ReplayAgentState{}
}
