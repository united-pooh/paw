package streamma

import (
	"codex-agent-go/internal/message"
	"codex-agent-go/internal/model"
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
)

func TestRuntimeChainPropagatesToDirectSuccessor(t *testing.T) {
	spec := GraphSpec{
		RunID:  "run-chain",
		Agents: []AgentSpec{{ID: "a"}, {ID: "b"}},
		Edges:  []EdgeSpec{{From: "a", To: "b"}},
	}
	model := newFakeModel(
		fakeTextResponse("from a\nEND_STEP\n"),
		fakeTextResponse("from b final\nEND_STEP\n"),
	)

	result, err := RunGraph(context.Background(), spec, model, "problem")
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != RunCompleted {
		t.Fatalf("status = %s", result.Status)
	}
	if result.Final == nil || result.Final.Answer.Text != "from b final" {
		t.Fatalf("unexpected final: %#v", result.Final)
	}
	calls := model.Calls()
	if len(calls) != 2 {
		t.Fatalf("calls = %d, want 2", len(calls))
	}
	if !strings.Contains(promptText(calls[1].Messages), "from a") {
		t.Fatalf("successor prompt missing upstream step: %q", promptText(calls[1].Messages))
	}
}

func TestRuntimeTreeBroadcastsToMultipleDirectSuccessors(t *testing.T) {
	spec := GraphSpec{
		RunID: "run-tree",
		Agents: []AgentSpec{
			{ID: "a", SystemPrompt: "agent A"},
			{ID: "b", SystemPrompt: "agent B"},
			{ID: "c", SystemPrompt: "agent C"},
			{ID: "d", SystemPrompt: "agent D"},
		},
		Edges: []EdgeSpec{
			{From: "a", To: "b"},
			{From: "a", To: "c"},
			{From: "b", To: "d"},
			{From: "c", To: "d"},
		},
	}
	model := newFakeModel(
		fakeTextResponse("A.step1\nEND_STEP\n"),
		fakeTextResponse("B.fromA\nEND_STEP\n"),
		fakeTextResponse("C.fromA\nEND_STEP\n"),
		fakeTextResponse("D.afterB\nEND_STEP\n"),
		fakeTextResponse("D.afterBC\nEND_STEP\n"),
	)

	result, err := RunGraph(context.Background(), spec, model, "problem")
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != RunCompleted {
		t.Fatalf("status = %s", result.Status)
	}
	if result.Final == nil || result.Final.Answer.Text != "D.afterBC" {
		t.Fatalf("D did not finalize after consuming both B and C outputs: %#v", result.Final)
	}
	calls := model.Calls()
	if len(calls) != 5 {
		t.Fatalf("calls = %d, want 5", len(calls))
	}
	promptsBySystem := map[string][]string{}
	for _, call := range calls {
		promptsBySystem[call.Messages[0].Content] = append(promptsBySystem[call.Messages[0].Content], promptText(call.Messages))
	}
	if len(promptsBySystem["agent B"]) != 1 {
		t.Fatalf("B calls = %d, want 1: %#v", len(promptsBySystem["agent B"]), calls)
	}
	if len(promptsBySystem["agent C"]) != 1 {
		t.Fatalf("C calls = %d, want 1: %#v", len(promptsBySystem["agent C"]), calls)
	}
	if !strings.Contains(promptsBySystem["agent B"][0], "A.step1") {
		t.Fatalf("b prompt missing source step: %q", promptsBySystem["agent B"][0])
	}
	if !strings.Contains(promptsBySystem["agent C"][0], "A.step1") {
		t.Fatalf("c prompt missing source step: %q", promptsBySystem["agent C"][0])
	}
	dFinalPrompt := promptText(calls[4].Messages)
	if !strings.Contains(dFinalPrompt, "B.fromA") || !strings.Contains(dFinalPrompt, "C.fromA") {
		t.Fatalf("D final prompt did not include both B and C outputs: %q", dFinalPrompt)
	}
}

func TestRuntimeEOFTriggeredSinkRunsOnceAfterAllPredecessorsComplete(t *testing.T) {
	spec := GraphSpec{
		RunID: "run-tree-eof-sink",
		Agents: []AgentSpec{
			{ID: "a", SystemPrompt: "agent A"},
			{ID: "b", SystemPrompt: "agent B"},
			{ID: "c", SystemPrompt: "agent C"},
			{ID: "d", SystemPrompt: "agent D", InvokePolicy: string(InvokeOnEOF)},
		},
		Edges: []EdgeSpec{
			{From: "a", To: "b"},
			{From: "a", To: "c"},
			{From: "b", To: "d"},
			{From: "c", To: "d"},
		},
	}
	model := newFakeModel(
		fakeTextResponse("A.step1\nEND_STEP\n"),
		fakeTextResponse("B.fromA\nEND_STEP\n"),
		fakeTextResponse("C.fromA\nEND_STEP\n"),
		fakeTextResponse("D.afterBC\nEND_STEP\n"),
	)

	result, err := RunGraph(context.Background(), spec, model, "problem")
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != RunCompleted {
		t.Fatalf("status = %s", result.Status)
	}
	if result.Final == nil || result.Final.Answer.Text != "D.afterBC" {
		t.Fatalf("unexpected final: %#v", result.Final)
	}
	calls := model.Calls()
	if len(calls) != 4 {
		t.Fatalf("calls = %d, want 4", len(calls))
	}
	dPrompts := promptsForSystem(calls, "agent D")
	if len(dPrompts) != 1 {
		t.Fatalf("D calls = %d, want 1: %#v", len(dPrompts), calls)
	}
	if !strings.Contains(dPrompts[0], "B.fromA") || !strings.Contains(dPrompts[0], "C.fromA") {
		t.Fatalf("D prompt did not include both upstream outputs: %q", dPrompts[0])
	}
}

func TestRuntimeGraphMultiPredecessorIsArrivalTriggered(t *testing.T) {
	spec := GraphSpec{
		RunID: "run-graph",
		Agents: []AgentSpec{
			{ID: "a", SystemPrompt: "agent A"},
			{ID: "c", SystemPrompt: "agent C"},
			{ID: "b", SystemPrompt: "agent B"},
			{ID: "d", SystemPrompt: "agent D"},
		},
		Edges: []EdgeSpec{
			{From: "a", To: "b"},
			{From: "a", To: "c"},
			{From: "b", To: "c"},
			{From: "c", To: "d"},
		},
	}
	model := newAgentResponseModel(map[string][]string{
		"agent A": {"A.step1\nEND_STEP\n"},
		"agent B": {"B.fromA\nEND_STEP\n"},
		"agent C": {"C.afterAShortcut\nEND_STEP\n", "C.afterB\nEND_STEP\n"},
		"agent D": {"D.afterCShortcut\nEND_STEP\n", "D.afterCAndB\nEND_STEP\n"},
	})

	result, err := RunGraph(context.Background(), spec, model, "problem")
	if err != nil {
		t.Fatal(err)
	}
	if result.Final == nil || result.Final.Answer.Text != "D.afterCAndB" {
		t.Fatalf("unexpected graph final: %#v", result.Final)
	}
	calls := model.Calls()
	if len(calls) != 6 {
		t.Fatalf("calls = %d, want 6", len(calls))
	}
	cPrompts := promptsForSystem(calls, "agent C")
	if len(cPrompts) != 2 {
		t.Fatalf("C calls = %d, want 2: %#v", len(cPrompts), calls)
	}
	cFirstPrompt := cPrompts[0]
	if !strings.Contains(cFirstPrompt, "A.step1") {
		t.Fatalf("first c prompt missing a step: %q", cFirstPrompt)
	}
	if strings.Contains(cFirstPrompt, "B.fromA") {
		t.Fatalf("c waited for b before first invocation: %q", cFirstPrompt)
	}
	cSecondPrompt := cPrompts[1]
	if !strings.Contains(cSecondPrompt, "A.step1") || !strings.Contains(cSecondPrompt, "B.fromA") {
		t.Fatalf("second c prompt did not consume both shortcut and B steps: %q", cSecondPrompt)
	}
}

func promptsForSystem(calls []fakeCall, system string) []string {
	var prompts []string
	for _, call := range calls {
		if len(call.Messages) == 0 || call.Messages[0].Content != system {
			continue
		}
		prompts = append(prompts, promptText(call.Messages))
	}
	return prompts
}

type agentResponseModel struct {
	mu        sync.Mutex
	responses map[string][]fakeResponse
	calls     []fakeCall
	counts    map[string]int
}

func newAgentResponseModel(responses map[string][]string) *agentResponseModel {
	modelResponses := make(map[string][]fakeResponse, len(responses))
	for agent, texts := range responses {
		for _, text := range texts {
			modelResponses[agent] = append(modelResponses[agent], fakeTextResponse(text))
		}
	}
	return &agentResponseModel{
		responses: modelResponses,
		counts:    make(map[string]int),
	}
}

func (m *agentResponseModel) StreamMessage(ctx context.Context, messages []message.Message, tools []model.ToolDefinition) (<-chan model.StreamEvent, error) {
	agent := ""
	if len(messages) > 0 {
		agent = messages[0].Content
	}
	m.mu.Lock()
	m.calls = append(m.calls, fakeCall{
		Messages: append([]message.Message(nil), messages...),
		Tools:    append([]model.ToolDefinition(nil), tools...),
	})
	index := m.counts[agent]
	m.counts[agent]++
	responses := m.responses[agent]
	m.mu.Unlock()

	if index >= len(responses) {
		return nil, fmt.Errorf("unexpected call %d for %s", index+1, agent)
	}
	response := responses[index]
	if response.err != nil {
		return nil, response.err
	}
	ch := make(chan model.StreamEvent, len(response.events))
	for _, event := range response.events {
		select {
		case <-ctx.Done():
			close(ch)
			return ch, nil
		case ch <- event:
		}
	}
	close(ch)
	return ch, nil
}

func (m *agentResponseModel) Calls() []fakeCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]fakeCall, len(m.calls))
	for i, call := range m.calls {
		out[i] = fakeCall{
			Messages: append([]message.Message(nil), call.Messages...),
			Tools:    append([]model.ToolDefinition(nil), call.Tools...),
		}
	}
	return out
}
