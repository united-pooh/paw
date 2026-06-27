package streamma

import (
	"codex-agent-go/internal/message"
	"codex-agent-go/internal/model"
	"context"
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
			{ID: "a", Role: "source", SystemPrompt: "agent A system", StepCountHint: 2},
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
	if calls[0].Attempt != 1 {
		t.Fatalf("source attempt = %d, want 1", calls[0].Attempt)
	}
	if calls[0].StepCountHint != 2 {
		t.Fatalf("source StepCountHint = %d, want 2", calls[0].StepCountHint)
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

type scriptedAgentResponse struct {
	events []model.StreamEvent
	err    error
}

type scriptedAgentStreamer struct {
	mu        sync.Mutex
	calls     []AgentInvocation
	responses []scriptedAgentResponse
}

func (s *scriptedAgentStreamer) StreamAgent(ctx context.Context, invocation AgentInvocation) (<-chan model.StreamEvent, error) {
	s.mu.Lock()
	s.calls = append(s.calls, cloneAgentInvocationForTest(invocation))
	index := len(s.calls) - 1
	var response scriptedAgentResponse
	if index < len(s.responses) {
		response = s.responses[index]
	} else if len(s.responses) > 0 {
		response = s.responses[len(s.responses)-1]
	}
	s.mu.Unlock()
	if response.err != nil {
		return nil, response.err
	}
	ch := make(chan model.StreamEvent, len(response.events))
	go func() {
		defer close(ch)
		for _, event := range response.events {
			select {
			case ch <- event:
			case <-ctx.Done():
				return
			}
		}
	}()
	return ch, nil
}

func (s *scriptedAgentStreamer) Calls() []AgentInvocation {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]AgentInvocation, len(s.calls))
	for i := range s.calls {
		out[i] = cloneAgentInvocationForTest(s.calls[i])
	}
	return out
}

func TestRuntimeRetriesParserFatalBeforeFirstStep(t *testing.T) {
	spec := GraphSpec{
		RunID:      "run-retry-parser",
		StepPolicy: StepPolicy{RequireBoundary: true, MaxAttempts: 2},
		Agents:     []AgentSpec{{ID: "a"}},
	}
	agent := &scriptedAgentStreamer{
		responses: []scriptedAgentResponse{
			{events: []model.StreamEvent{{Delta: "missing boundary"}, {Done: true}}},
			{events: []model.StreamEvent{{Delta: "recovered\nEND_STEP\n"}, {Done: true}}},
		},
	}

	result, err := RunGraphWithAgent(context.Background(), spec, agent, "problem")
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != RunCompleted {
		t.Fatalf("status = %s, want completed", result.Status)
	}
	if result.Final == nil || result.Final.Answer.Text != "recovered" {
		t.Fatalf("unexpected final: %#v", result.Final)
	}

	calls := agent.Calls()
	if len(calls) != 2 {
		t.Fatalf("calls = %d, want 2", len(calls))
	}
	if calls[0].Attempt != 1 || calls[1].Attempt != 2 {
		t.Fatalf("attempts = %#v, want [1 2]", []int{calls[0].Attempt, calls[1].Attempt})
	}

	retryEvents := 0
	var committedStep *StepPacket
	for _, event := range result.Events {
		if event.Type == EventAgentRetry {
			retryEvents++
		}
		if event.Type == EventStepCommitted {
			committedStep = event.Step
		}
	}
	if retryEvents != 1 {
		t.Fatalf("retry events = %d, want 1", retryEvents)
	}
	if committedStep == nil || committedStep.Attempt != 2 {
		t.Fatalf("committed step = %#v, want attempt 2", committedStep)
	}
}
