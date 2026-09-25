package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"paw/internal/capability/model"
	"paw/internal/capability/tool"
	toolExec "paw/internal/capability/tool/exec"
	selecttool "paw/internal/capability/tool/select"
	"paw/internal/message"
	"paw/internal/runtime/goal"
	"paw/internal/runtime/loop"
	"paw/internal/runtime/sessionactor"
	"paw/internal/storage/session"
	"paw/internal/todo"
	"paw/internal/ui/headless"
)

type goalWorkflowModel struct {
	mu        sync.Mutex
	native    bool
	step      int
	calls     [][]message.Message
	responses []string
}

func (m *goalWorkflowModel) StreamMessage(_ context.Context, messages []message.Message, _ []model.ToolDefinition) (<-chan model.StreamEvent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.step >= len(m.responses) {
		return nil, fmt.Errorf("unexpected model request %d", m.step)
	}
	m.calls = append(m.calls, append([]message.Message(nil), messages...))
	ch := make(chan model.StreamEvent, 4)
	var call struct {
		Type, ID, Name string
		Input          json.RawMessage
	}
	if m.native && json.Unmarshal([]byte(m.responses[m.step]), &call) == nil && call.Type == "tool_use" {
		ch <- model.StreamEvent{AssistantPart: &model.AssistantPartEvent{Lifecycle: model.AssistantPartLifecycleStart, Type: "tool_call", ToolCallID: call.ID, ToolName: call.Name}}
		ch <- model.StreamEvent{AssistantPart: &model.AssistantPartEvent{Lifecycle: model.AssistantPartLifecycleDelta, Type: "tool_call", ToolArgs: call.Input}}
		ch <- model.StreamEvent{AssistantPart: &model.AssistantPartEvent{Lifecycle: model.AssistantPartLifecycleEnd, Type: "tool_call"}}
	} else {
		ch <- model.StreamEvent{Delta: m.responses[m.step]}
	}
	ch <- model.StreamEvent{Done: true}
	close(ch)
	m.step++
	return ch, nil
}

func goalWorkflowTool(id, name string, input any) string {
	raw, _ := json.Marshal(map[string]any{"type": "tool_use", "id": id, "name": name, "input": input})
	return string(raw)
}

func TestGoalWorkflowUsesJournalMeasurementsAndExploresUntilTarget(t *testing.T) {
	for _, native := range []bool{false, true} {
		t.Run(fmt.Sprintf("native=%v", native), func(t *testing.T) { runGoalWorkflow(t, native) })
	}
}

func runGoalWorkflow(t *testing.T, native bool) {
	ctx := context.Background()
	root := t.TempDir()
	t.Setenv("PAW_CONFIG_HOME", t.TempDir())
	if err := os.WriteFile(filepath.Join(root, "score.json"), []byte(`{"score":40}`), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := session.NewJSONLStore(filepath.Join(root, "journal"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateRoot(ctx, session.CreateRootRequest{SessionID: "metric-session"}); err != nil {
		t.Fatal(err)
	}
	metric := goal.MetricSpec{Name: "score", Comparison: "gte", Target: 80, Unit: "points", Measurement: goal.MetricMeasurement{Tool: "Bash", Command: "cat score.json", CWD: "", JSONPointer: "/score"}}
	model := &goalWorkflowModel{native: native, responses: []string{
		goalWorkflowTool("set", "goal_set_metric", metric),
		goalWorkflowTool("measure-low", "Bash", map[string]string{"command": "cat score.json"}),
		goalWorkflowTool("record-low", "goal_record_metric", map[string]string{"tool_call_id": "measure-low"}),
		goalWorkflowTool("todos-done", "update_todo", map[string]any{"items": []map[string]string{{"id": "attempt", "content": "第一次尝试", "status": "completed"}}}),
		"工作清单已完成，当前实测 40，需要换方法。",
		goalWorkflowTool("improve", "Bash", map[string]string{"command": "printf '%s' '{\"score\":90}' > score.json"}),
		goalWorkflowTool("measure-high", "Bash", map[string]string{"command": "cat score.json"}),
		goalWorkflowTool("record-high", "goal_record_metric", map[string]string{"tool_call_id": "measure-high"}),
		"实测 90，达到目标 80。",
	}}
	registry := tool.NewRegistry()
	registry.Register(&toolExec.BashTool{Root: root})
	todos := todo.NewBroker()
	defer todos.Close()
	selections := selecttool.NewBroker()
	defer selections.Close()
	toolset := NewToolset(todos)
	if err := toolset.RegisterMain(registry); err != nil {
		t.Fatal(err)
	}
	if err := toolset.RegisterInteractive(registry, selections); err != nil {
		t.Fatal(err)
	}
	engine := loop.NewEngineWithInstructionRoot(model, headless.New(io.Discard), registry, store, "metric-session", root)
	host, err := sessionactor.NewHost(engine, store, "metric-session")
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close()
	app := &WorkspaceRuntime{SessionHost: host, Store: store, Toolset: toolset}
	BindSessionTools(app, todos, "metric-session")
	stateProvider := stateBlockProviderFor("metric-session", store, todos, filepath.Join(root, "plans"))
	controller := goal.NewSessionController(host, todos, store.Dir())
	if err := app.BindGoalController(controller); err != nil {
		t.Fatal(err)
	}
	stopped := make(chan string, 1)
	controller.SetStopped(func(reason string) { stopped <- reason })
	id, err := controller.Start("将 score 提升到至少 80 points，实现方法由你探索。")
	if err != nil {
		t.Fatal(err)
	}
	select {
	case reason := <-stopped:
		if reason != string(goal.EventCompleted) {
			model.mu.Lock()
			if len(model.calls) > 0 {
				for _, msg := range model.calls[len(model.calls)-1] {
					for _, result := range append(msg.ToolResults, dereferenceToolResult(msg.ToolResult)...) {
						if result.IsError {
							t.Logf("tool error %s: %s", result.ToolUseID, result.Content)
						}
					}
				}
			}
			model.mu.Unlock()
			t.Fatalf("goal stopped without measured success: %s; status %s", reason, controller.Status())
		}
	case <-time.After(10 * time.Second):
		controller.Pause()
		t.Fatal("goal failed to finish")
	}
	goals, err := goal.NewEventStore(store.Dir())
	if err != nil {
		t.Fatal(err)
	}
	saved, ok, err := goals.Get(ctx, goal.GoalID(id))
	if err != nil || !ok || saved.Status != goal.GoalCompleted || len(saved.Measurements) != 2 {
		t.Fatalf("persisted goal = %+v, found %v, error %v", saved, ok, err)
	}
	if saved.Measurements[0].Value != 40 || saved.Measurements[1].Value != 90 {
		t.Fatalf("measurements = %+v", saved.Measurements)
	}
	model.mu.Lock()
	defer model.mu.Unlock()
	if len(model.calls) != 9 {
		t.Fatalf("model calls %d; TODO completion must not stop exploration", len(model.calls))
	}
	for _, messages := range model.calls {
		for _, msg := range messages {
			for _, result := range append(msg.ToolResults, dereferenceToolResult(msg.ToolResult)...) {
				if result.IsError {
					t.Fatalf("workflow tool failed: %s", result.Content)
				}
			}
		}
	}
	if out, err := stateProvider.BuildStateContext(ctx); err != nil || !strings.Contains(out, "measure-high") || !strings.Contains(out, `"value":90`) {
		t.Fatalf("compacted goal state = %q, error %v", out, err)
	}
}

func dereferenceToolResult(result *message.ToolResult) []message.ToolResult {
	if result == nil {
		return nil
	}
	return []message.ToolResult{*result}
}
