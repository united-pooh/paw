package streamma

import (
	"context"
	"gocode/internal/message"
	"gocode/internal/model"
	"testing"
)

func TestRuntimeCallsModelWithoutToolsAndIgnoresToolEvents(t *testing.T) {
	spec := GraphSpec{
		RunID:  "run-no-tools",
		Agents: []AgentSpec{{ID: "agent"}},
	}
	model := newFakeModel(fakeResponse{events: []model.StreamEvent{
		{ToolCalls: []message.ToolCall{{ID: "tool-1", Name: "shell"}}},
		{Delta: "tool call ignored\nEND_STEP\n"},
		{Done: true},
	}})

	result, err := RunGraph(context.Background(), spec, model, "problem")
	if err != nil {
		t.Fatal(err)
	}
	if result.Final == nil || result.Final.Answer.Text != "tool call ignored" {
		t.Fatalf("unexpected final: %#v", result.Final)
	}
	calls := model.Calls()
	if len(calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(calls))
	}
	if len(calls[0].Tools) != 0 {
		t.Fatalf("tools passed to model: %#v", calls[0].Tools)
	}
}

func TestRuntimeForcedCloseTrailingContent(t *testing.T) {
	spec := GraphSpec{
		RunID:  "run-forced-close",
		Agents: []AgentSpec{{ID: "agent"}},
	}
	model := newFakeModel(fakeTextResponse("final without sentinel"))

	result, err := RunGraph(context.Background(), spec, model, "problem")
	if err != nil {
		t.Fatal(err)
	}
	var step *StepPacket
	for _, event := range result.Events {
		if event.Type == EventStepCommitted {
			step = event.Step
			break
		}
	}
	if step == nil {
		t.Fatal("missing committed step")
	}
	if !step.Boundary.BoundaryRecovered {
		t.Fatalf("BoundaryRecovered = false, want true")
	}
}
