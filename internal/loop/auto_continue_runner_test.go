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
	runner := NewEngine(model, &fakeUI{}, registry, nil, "")
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

func TestEvaluateCompletionTracksStaleTodoTurns(t *testing.T) {
	broker := todo.NewBroker()
	defer broker.Close()
	runner := NewEngine(&autoContinueModel{}, &fakeUI{}, tool.NewRegistry(), nil, "")
	runner.SetTodoBroker(broker)
	runner.SetAutoContinueConfig(AutoContinueConfig{Enabled: true, BaseBudget: 100, AbsoluteMax: 100, MaxNoProgress: 100, StaleTodoThreshold: 3})

	snapshot := todo.Snapshot{Items: []todo.Item{{ID: "work", Content: "finish", Status: todo.StatusInProgress}}}
	broker.Publish(snapshot)

	round := func(content string) int {
		decision, _, _, _ := runner.evaluateCompletion(message.Message{Role: message.RoleAssistant, Content: content}, false, 1, 0)
		return decision.StaleTodoTurns
	}
	if got := round("first"); got != 0 {
		t.Fatalf("baseline round stale = %d, want 0", got)
	}
	if got := round("second"); got != 1 {
		t.Fatalf("round 2 stale = %d, want 1", got)
	}
	if got := round("third"); got != 2 {
		t.Fatalf("round 3 stale = %d, want 2", got)
	}
	if got := round("fourth"); got != 3 {
		t.Fatalf("round 4 stale = %d, want 3", got)
	}

	updated := snapshot.Clone()
	updated.Items[0].Status = todo.StatusCompleted
	broker.Publish(updated)
	if got := round("fifth"); got != 0 {
		t.Fatalf("stale after update = %d, want 0", got)
	}
	decision, _, _, _ := runner.evaluateCompletion(message.Message{Role: message.RoleAssistant, Content: "done"}, false, 1, 0)
	if decision.Action != CompletionComplete {
		t.Fatalf("action = %q, want complete", decision.Action)
	}
}

func TestRunTurnAutoContinueSilentUntilPause(t *testing.T) {
	broker := todo.NewBroker()
	defer broker.Close()
	registry := tool.NewRegistry()
	registry.Register(todo.NewTool(broker))
	ui := &fakeUI{}
	model := &autoContinueModel{rounds: []fakeRound{
		{events: []model.StreamEvent{{Delta: "做了些工作"}, {Done: true, FinishReason: model.FinishReasonStop}}},
		{events: []model.StreamEvent{{Delta: "做了些工作"}, {Done: true, FinishReason: model.FinishReasonStop}}},
	}}
	runner := NewEngine(model, ui, registry, nil, "")
	runner.SetTodoBroker(broker)
	runner.SetAutoContinueConfig(AutoContinueConfig{Enabled: true, BaseBudget: 2, AbsoluteMax: 12, MaxNoProgress: 1})
	broker.Publish(todo.Snapshot{Items: []todo.Item{{ID: "work", Content: "finish work", Status: todo.StatusInProgress}}})

	if _, err := runner.RunTurn(context.Background(), "continue"); err != nil {
		t.Fatalf("RunTurn() error = %v", err)
	}
	if len(model.calls) != 2 {
		t.Fatalf("model calls = %d, want initial + one continuation", len(model.calls))
	}
	// 续行本身无感：唯一的 auto-continue 系统条目是暂停通知。
	events := ui.systemEvents()
	if got := countSystemEventsByTitle(events, "auto-continue"); got != 1 {
		t.Fatalf("auto-continue system events = %d, want only the pause notice: %#v", got, events)
	}
	body := events[len(events)-1].Body
	for _, want := range []string{"已暂停", "连续 1 轮没有可验证的进展", "还有 1 项 todo 未完成", "继续"} {
		if !strings.Contains(body, want) {
			t.Fatalf("pause body = %q, want substring %q", body, want)
		}
	}
	// 续行提示仍以 synthetic 用户消息进入模型上下文与会话历史。
	if !messagesContainContent(model.calls[1], "任务尚未完成") {
		t.Fatalf("continuation prompt missing from second call: %#v", model.calls[1])
	}
	syntheticSeen := false
	for _, msg := range runner.currentHistory() {
		if msg.Synthetic == message.SyntheticAutoContinue && strings.Contains(msg.Content, "任务尚未完成") {
			syntheticSeen = true
		}
	}
	if !syntheticSeen {
		t.Fatalf("committed history missing synthetic continuation prompt: %#v", runner.currentHistory())
	}
}

func TestRunTurnRetriesEmptyAssistantResponse(t *testing.T) {
	ui := &fakeUI{}
	modelClient := &fakeModel{rounds: []fakeRound{
		{events: []model.StreamEvent{{Done: true, FinishReason: model.FinishReasonStop}}},
		{events: []model.StreamEvent{{Delta: "真正的答案"}, {Done: true, FinishReason: model.FinishReasonStop}}},
	}}
	runner := NewEngine(modelClient, ui, tool.NewRegistry(), nil, "")

	msg, err := runner.RunTurn(context.Background(), "start")
	if err != nil {
		t.Fatalf("RunTurn() error = %v", err)
	}
	if msg.Content != "真正的答案" {
		t.Fatalf("content = %q, want retried answer", msg.Content)
	}
	if len(modelClient.calls) != 2 {
		t.Fatalf("model calls = %d, want initial + one silent retry", len(modelClient.calls))
	}
	if got := strings.Join(ui.deltas, ""); got != "真正的答案" {
		t.Fatalf("ui deltas = %q, want retried answer only", got)
	}
	if got := countSystemEventsByTitle(ui.systemEvents(), "model"); got != 0 {
		t.Fatalf("unexpected empty-response notice: %#v", ui.systemEvents())
	}
}

func TestRunTurnEmptyResponseAfterRetryNotifies(t *testing.T) {
	ui := &fakeUI{}
	modelClient := &fakeModel{rounds: []fakeRound{
		{events: []model.StreamEvent{{Done: true, FinishReason: model.FinishReasonStop}}},
		{events: []model.StreamEvent{{Done: true, FinishReason: model.FinishReasonStop}}},
	}}
	runner := NewEngine(modelClient, ui, tool.NewRegistry(), nil, "")

	if _, err := runner.RunTurn(context.Background(), "start"); err != nil {
		t.Fatalf("RunTurn() error = %v", err)
	}
	if len(modelClient.calls) != 2 {
		t.Fatalf("model calls = %d, want initial + one retry", len(modelClient.calls))
	}
	events := ui.systemEvents()
	if got := countSystemEventsByTitle(events, "model"); got != 1 {
		t.Fatalf("model system events = %d, want empty-response notice: %#v", got, events)
	}
	if !strings.Contains(events[len(events)-1].Body, "空响应") {
		t.Fatalf("notice body = %q, want 空响应", events[len(events)-1].Body)
	}
}

var _ tool.Tool = (*todo.Tool)(nil)
