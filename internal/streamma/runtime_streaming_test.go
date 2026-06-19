package streamma

import (
	"context"
	"fmt"
	"codex-agent-go/internal/message"
	"codex-agent-go/internal/model"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRuntimeFanoutStartsDownstreamBeforeUpstreamDone(t *testing.T) {
	spec := GraphSpec{
		RunID: "run-strict-streaming",
		Agents: []AgentSpec{
			{ID: "a", SystemPrompt: "agent A"},
			{ID: "b", SystemPrompt: "agent B"},
			{ID: "d", SystemPrompt: "agent D"},
		},
		Edges: []EdgeSpec{
			{From: "a", To: "b"},
			{From: "b", To: "d"},
		},
	}
	model := newStreamingProbeModel()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	type runOutcome struct {
		result RunResult
		err    error
	}
	outcomes := make(chan runOutcome, 1)
	go func() {
		result, err := RunGraph(ctx, spec, model, "problem")
		outcomes <- runOutcome{result: result, err: err}
	}()

	select {
	case <-model.aFirstStepSent:
	case <-time.After(time.Second):
		t.Fatal("A did not emit its first step")
	}
	select {
	case <-model.bStarted:
	case <-time.After(time.Second):
		t.Fatal("B did not start from A's first completed step before A finished")
	}
	select {
	case <-model.dStarted:
	case <-time.After(time.Second):
		t.Fatal("D did not start from B's completed step before A finished")
	}

	if model.ADoneReleased() {
		t.Fatal("test released A before observing downstream startup")
	}
	close(model.releaseA)

	select {
	case outcome := <-outcomes:
		if outcome.err != nil {
			t.Fatal(outcome.err)
		}
		if outcome.result.Status != RunCompleted {
			t.Fatalf("status = %s, want completed", outcome.result.Status)
		}
		if outcome.result.Final == nil || outcome.result.Final.Answer.Text != "D.fromB2" {
			t.Fatalf("unexpected final: %#v", outcome.result.Final)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("runtime did not finish after releasing A")
	}

	calls := model.Calls()
	if len(calls) != 5 {
		t.Fatalf("calls = %d, want 5: %#v", len(calls), calls)
	}
	if calls[0].AgentID != "a" || calls[1].AgentID != "b" || calls[2].AgentID != "d" {
		t.Fatalf("first calls = %#v, want a -> b -> d before A done", calls[:3])
	}
	if !strings.Contains(promptText(calls[1].Messages), "A.step1") {
		t.Fatalf("B prompt missing A.step1: %q", promptText(calls[1].Messages))
	}
	if strings.Contains(promptText(calls[1].Messages), "A.step2") {
		t.Fatalf("B first prompt waited for A.step2: %q", promptText(calls[1].Messages))
	}
}

type streamingProbeCall struct {
	AgentID  string
	Messages []message.Message
	Tools    []model.ToolDefinition
}

type streamingProbeModel struct {
	mu             sync.Mutex
	calls          []streamingProbeCall
	aFirstStepSent chan struct{}
	releaseA       chan struct{}
	bStarted       chan struct{}
	dStarted       chan struct{}
	bCalls         int
	dCalls         int
	aReleased      bool
}

func newStreamingProbeModel() *streamingProbeModel {
	return &streamingProbeModel{
		aFirstStepSent: make(chan struct{}),
		releaseA:       make(chan struct{}),
		bStarted:       make(chan struct{}),
		dStarted:       make(chan struct{}),
	}
}

func (m *streamingProbeModel) StreamMessage(ctx context.Context, messages []message.Message, tools []model.ToolDefinition) (<-chan model.StreamEvent, error) {
	agentID := probeAgentID(messages)
	m.mu.Lock()
	m.calls = append(m.calls, streamingProbeCall{
		AgentID:  agentID,
		Messages: append([]message.Message(nil), messages...),
		Tools:    append([]model.ToolDefinition(nil), tools...),
	})
	var responseText string
	switch agentID {
	case "a":
	case "b":
		m.bCalls++
		closeOnce(m.bStarted)
		responseText = fmt.Sprintf("B.fromA%d\nEND_STEP\n", m.bCalls)
	case "d":
		m.dCalls++
		closeOnce(m.dStarted)
		responseText = fmt.Sprintf("D.fromB%d\nEND_STEP\n", m.dCalls)
	default:
		m.mu.Unlock()
		return nil, fmt.Errorf("unexpected probe agent %q", agentID)
	}
	m.mu.Unlock()

	if agentID != "a" {
		return streamText(ctx, responseText), nil
	}
	ch := make(chan model.StreamEvent)
	go func() {
		defer close(ch)
		if !sendProbeEvent(ctx, ch, model.StreamEvent{Delta: "A.step1\nEND_STEP\n"}) {
			return
		}
		closeOnce(m.aFirstStepSent)
		select {
		case <-ctx.Done():
			return
		case <-m.releaseA:
			m.mu.Lock()
			m.aReleased = true
			m.mu.Unlock()
		}
		if !sendProbeEvent(ctx, ch, model.StreamEvent{Delta: "A.step2\nEND_STEP\n"}) {
			return
		}
		_ = sendProbeEvent(ctx, ch, model.StreamEvent{Done: true})
	}()
	return ch, nil
}

func (m *streamingProbeModel) Calls() []streamingProbeCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]streamingProbeCall, len(m.calls))
	for i, call := range m.calls {
		out[i] = streamingProbeCall{
			AgentID:  call.AgentID,
			Messages: append([]message.Message(nil), call.Messages...),
			Tools:    append([]model.ToolDefinition(nil), call.Tools...),
		}
	}
	return out
}

func (m *streamingProbeModel) ADoneReleased() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.aReleased
}

func probeAgentID(messages []message.Message) string {
	if len(messages) == 0 {
		return ""
	}
	switch messages[0].Content {
	case "agent A":
		return "a"
	case "agent B":
		return "b"
	case "agent D":
		return "d"
	default:
		return ""
	}
}

func streamText(ctx context.Context, text string) <-chan model.StreamEvent {
	ch := make(chan model.StreamEvent)
	go func() {
		defer close(ch)
		if !sendProbeEvent(ctx, ch, model.StreamEvent{Delta: text}) {
			return
		}
		_ = sendProbeEvent(ctx, ch, model.StreamEvent{Done: true})
	}()
	return ch
}

func sendProbeEvent(ctx context.Context, ch chan<- model.StreamEvent, event model.StreamEvent) bool {
	select {
	case ch <- event:
		return true
	case <-ctx.Done():
		return false
	}
}

func closeOnce(ch chan struct{}) {
	defer func() {
		_ = recover()
	}()
	close(ch)
}
