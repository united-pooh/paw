package streamma

import (
	"context"
	"codex-agent-go/internal/message"
	"codex-agent-go/internal/model"
	"strings"
	"sync"
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

func TestRuntimePassesStructuredAgentInvocation(t *testing.T) {
	spec := GraphSpec{
		RunID: "run-agent-invocation",
		Agents: []AgentSpec{
			{ID: "a", Role: "source", SystemPrompt: "agent A system"},
			{ID: "b", Role: "sink", SystemPrompt: "agent B system"},
		},
		Edges: []EdgeSpec{{From: "a", To: "b"}},
	}
	agent := &capturingAgentStreamer{}

	result, err := RunGraphWithAgent(context.Background(), spec, agent, "fixed problem")
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != RunCompleted {
		t.Fatalf("status = %s, want completed", result.Status)
	}

	calls := agent.Calls()
	if len(calls) != 2 {
		t.Fatalf("calls = %d, want 2: %#v", len(calls), calls)
	}
	if calls[0].RunID != spec.RunID || calls[0].AgentID != "a" || calls[0].InvocationIndex != 1 {
		t.Fatalf("source invocation metadata = %#v", calls[0])
	}
	if calls[0].Problem != "fixed problem" || calls[0].InboundStep != nil || len(calls[0].Transcript) != 0 {
		t.Fatalf("source invocation context = %#v", calls[0])
	}
	if calls[1].AgentID != "b" || calls[1].Role != "sink" || calls[1].SystemPrompt != "agent B system" {
		t.Fatalf("sink invocation metadata = %#v", calls[1])
	}
	if calls[1].InboundFrom != "a" || calls[1].InboundStep == nil || !strings.Contains(calls[1].InboundStep.Content.Text, "a step") {
		t.Fatalf("sink inbound step = %#v", calls[1])
	}
	if len(calls[1].Transcript) != 1 || calls[1].Transcript[0].Kind != TranscriptInbound || calls[1].Transcript[0].From != "a" {
		t.Fatalf("sink transcript snapshot = %#v", calls[1].Transcript)
	}
}

type capturingAgentStreamer struct {
	mu    sync.Mutex
	calls []AgentInvocation
}

func (s *capturingAgentStreamer) StreamAgent(ctx context.Context, invocation AgentInvocation) (<-chan model.StreamEvent, error) {
	s.mu.Lock()
	s.calls = append(s.calls, cloneAgentInvocationForTest(invocation))
	s.mu.Unlock()
	text := invocation.AgentID + " step\nEND_STEP\n"
	return streamAgentTextForTest(ctx, text), nil
}

func (s *capturingAgentStreamer) Calls() []AgentInvocation {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]AgentInvocation, len(s.calls))
	for i := range s.calls {
		out[i] = cloneAgentInvocationForTest(s.calls[i])
	}
	return out
}

func cloneAgentInvocationForTest(invocation AgentInvocation) AgentInvocation {
	invocation.InputEvents = append([]string(nil), invocation.InputEvents...)
	invocation.Transcript = append([]TranscriptEntry(nil), invocation.Transcript...)
	if invocation.InboundStep != nil {
		step := cloneStepPacket(*invocation.InboundStep)
		invocation.InboundStep = &step
	}
	return invocation
}

func streamAgentTextForTest(ctx context.Context, text string) <-chan model.StreamEvent {
	ch := make(chan model.StreamEvent, 2)
	select {
	case ch <- model.StreamEvent{Delta: text}:
	case <-ctx.Done():
	}
	select {
	case ch <- model.StreamEvent{Done: true}:
	case <-ctx.Done():
	}
	close(ch)
	return ch
}
