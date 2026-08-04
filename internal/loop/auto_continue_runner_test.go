package loop

import (
	"context"
	"encoding/json"
	"paw/internal/message"
	"paw/internal/model"
	"paw/internal/todo"
	"paw/internal/tool"
	"strings"
	"testing"
)

type autoContinueModel struct {
	rounds []fakeRound
	calls  [][]message.Message
}

func (m *autoContinueModel) StreamMessage(_ context.Context, messages []message.Message, _ []model.ToolDefinition) (<-chan model.StreamEvent, error) {
	m.calls = append(m.calls, append([]message.Message(nil), messages...))
	round := m.rounds[len(m.calls)-1]
	ch := make(chan model.StreamEvent, len(round.events))
	for _, event := range round.events {
		ch <- event
	}
	close(ch)
	return ch, round.err
}

func TestRunTurnAutoContinuesUntilTodoCompletes(t *testing.T) {
	broker := todo.NewBroker()
	defer broker.Close()
	registry := tool.NewRegistry()
	registry.Register(todo.NewTool(broker))
	model := &autoContinueModel{rounds: []fakeRound{
		{events: []model.StreamEvent{{Delta: `{"type":"tool_use","id":"todo-1","name":"update_todo","input":{"items":[{"id":"work","content":"finish work","status":"pending"}]}}`}, {Done: true}}},
		{events: []model.StreamEvent{{Delta: "partial work is done"}, {Done: true}}},
		{events: []model.StreamEvent{{Delta: `{"type":"tool_use","id":"todo-2","name":"update_todo","input":{"items":[{"id":"work","content":"finish work","status":"completed"}]}}`}, {Done: true}}},
		{events: []model.StreamEvent{{Delta: "final answer"}, {Done: true}}},
	}}
	runner := NewRunner(model, &fakeUI{}, registry, nil, "")
	runner.SetTodoBroker(broker)

	result, err := runner.RunTurn(context.Background(), "finish the work")
	if err != nil {
		t.Fatalf("RunTurn() error = %v", err)
	}
	if result.Content != "final answer" {
		t.Fatalf("result.Content = %q, want final answer", result.Content)
	}
	if len(model.calls) != 4 {
		t.Fatalf("model calls = %d, want 4", len(model.calls))
	}
	if !messagesContainContent(model.calls[2], "任务尚未完成") || !messagesContainContent(model.calls[2], "finish work") {
		t.Fatalf("continuation prompt missing from third model call: %#v", model.calls[2])
	}
	if !messagesContainContent(model.calls[3], "任务尚未完成") {
		t.Fatalf("second continuation prompt missing from fourth model call: %#v", model.calls[3])
	}
	latest, ok := broker.Latest()
	if !ok || !latest.AllCompleted() {
		t.Fatalf("latest todo = %#v, ok=%v; want all completed", latest, ok)
	}
}

func TestAutoContinueModelRoundInputIsValid(t *testing.T) {
	// Keep the test fixture's tool-call payload aligned with the message schema.
	var call message.ToolCall
	if err := json.Unmarshal([]byte(`{"id":"id","name":"update_todo","input":{}}`), &call); err != nil {
		t.Fatal(err)
	}
	if call.Name == "" || !strings.Contains(string(call.Input), "{") {
		t.Fatalf("decoded tool call = %#v", call)
	}
}

var _ tool.Tool = (*todo.Tool)(nil)
