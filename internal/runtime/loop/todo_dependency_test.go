package loop

import (
	"context"
	"encoding/json"
	"paw/internal/capability/model"
	"paw/internal/capability/tool"
	"paw/internal/message"
	"paw/internal/todo"
	"strings"
	"testing"
)

const dependencyTodoJSON = `{"items":[{"id":"work","content":"实现主任务","status":"pending","blocked_by":["repair"]},{"id":"repair","content":"修复前置问题","status":"in_progress"}]}`

func dependencyTodoSnapshot(t *testing.T, raw string) todo.Snapshot {
	t.Helper()
	var snapshot todo.Snapshot
	if err := json.Unmarshal([]byte(raw), &snapshot); err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func TestTodoDependencyContinuationWaitsThenResumesOriginalItem(t *testing.T) {
	snapshot := dependencyTodoSnapshot(t, dependencyTodoJSON)
	gate := CompletionGate{Config: DefaultAutoContinueConfig()}
	decision := gate.Evaluate(CompletionObservation{HasTodo: true, Todo: snapshot})
	if decision.Action != CompletionContinue || pendingTodoCount(snapshot) != 2 {
		t.Fatalf("blocked todo stopped continuation or disappeared: decision=%+v count=%d", decision, pendingTodoCount(snapshot))
	}
	prompt := buildContinuationPrompt(decision, snapshot, 0)
	ready, waiting, ok := strings.Cut(prompt, "等待前置事项：")
	if !ok || strings.Contains(ready, "实现主任务") || !strings.Contains(ready, "#repair 修复前置问题") || !strings.Contains(waiting, "#work 实现主任务（等待：#repair）") {
		t.Fatalf("continuation does not distinguish ready and blocked items: %q", prompt)
	}

	snapshot.Items[1].Status = todo.StatusCompleted
	decision = gate.Evaluate(CompletionObservation{HasTodo: true, Todo: snapshot, ContinuationUsed: 1})
	prompt = buildContinuationPrompt(decision, snapshot, 0)
	if decision.Action != CompletionContinue || pendingTodoCount(snapshot) != 1 || !strings.Contains(prompt, "[pending] #work 实现主任务") || strings.Contains(prompt, "等待前置事项：") || strings.Contains(prompt, "修复前置问题") {
		t.Fatalf("original todo did not resume after dependency completed: decision=%+v prompt=%q", decision, prompt)
	}

	snapshot.Items[0].Status = todo.StatusCompleted
	if decision := gate.Evaluate(CompletionObservation{HasTodo: true, Todo: snapshot, ContinuationUsed: 2}); decision.Action != CompletionComplete {
		t.Fatalf("all completed decision = %+v", decision)
	}
}

func TestTodoDependencyFingerprintsTrackEdgesWithoutOrderNoise(t *testing.T) {
	first := dependencyTodoSnapshot(t, dependencyTodoJSON)
	removed := dependencyTodoSnapshot(t, strings.ReplaceAll(dependencyTodoJSON, `,"blocked_by":["repair"]`, ""))
	ordered := dependencyTodoSnapshot(t, strings.ReplaceAll(dependencyTodoJSON, `["repair"]`, `["repair","review"]`))
	reversed := dependencyTodoSnapshot(t, strings.ReplaceAll(dependencyTodoJSON, `["repair"]`, `["review","repair"]`))
	for name, fingerprint := range map[string]func(todo.Snapshot) string{"progress": progressFingerprint, "stale": todoFingerprint} {
		t.Run(name, func(t *testing.T) {
			if fingerprint(first) == fingerprint(removed) {
				t.Fatal("dependency change did not affect fingerprint")
			}
			if fingerprint(ordered) != fingerprint(reversed) {
				t.Fatal("dependency order alone changed fingerprint")
			}
		})
	}
}

func TestTodoDependencySystemPromptExplainsBlockingAndResumption(t *testing.T) {
	prompt := NewPromptBuilder(nil).Build([]string{"update_todo: Maintain todos."})
	for _, want := range []string{"blocked_by", "pending", "prerequisite", "resume", "in_progress or completed"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("progress policy missing %q", want)
		}
	}
}

func TestTodoDependencyAutoContinueRunsPrerequisiteThenOriginalItem(t *testing.T) {
	broker := todo.NewBroker()
	defer broker.Close()
	registry := tool.NewRegistry()
	registry.Register(todo.NewTool(broker))
	second := strings.ReplaceAll(dependencyTodoJSON, `"status":"in_progress"`, `"status":"completed"`)
	third := strings.ReplaceAll(second, `"status":"pending"`, `"status":"completed"`)
	toolRound := func(id, raw string) fakeRound {
		return fakeRound{events: []model.StreamEvent{{Delta: `{"type":"tool_use","id":"` + id + `","name":"update_todo","input":` + raw + `}`}, {Done: true}}}
	}
	model := &autoContinueModel{rounds: []fakeRound{
		toolRound("todo-1", dependencyTodoJSON),
		{events: []model.StreamEvent{{Delta: "需要先修复前置问题"}, {Done: true}}},
		toolRound("todo-2", second),
		{events: []model.StreamEvent{{Delta: "前置已完成"}, {Done: true}}},
		toolRound("todo-3", third),
		{events: []model.StreamEvent{{Delta: "全部完成"}, {Done: true}}},
	}}
	runner := NewEngine(model, &fakeUI{}, registry, nil, "")
	runner.SetTodoBroker(broker)
	result, err := runner.RunTurn(context.Background(), "完成主任务")
	if err != nil {
		t.Fatal(err)
	}
	if result.Content != "全部完成" || len(model.calls) != 6 {
		t.Fatalf("result=%q calls=%d, want final result after exactly two continuations", result.Content, len(model.calls))
	}
	latestContinuation := func(messages []message.Message) string {
		for i := len(messages) - 1; i >= 0; i-- {
			if messages[i].Role == message.RoleUser && strings.HasPrefix(messages[i].Content, "任务尚未完成") {
				return messages[i].Content
			}
		}
		return ""
	}
	if prompt := latestContinuation(model.calls[2]); !strings.Contains(prompt, "#work 实现主任务（等待：#repair）") {
		t.Fatalf("first continuation missing dependency: %q", prompt)
	}
	if prompt := latestContinuation(model.calls[4]); !strings.Contains(prompt, "[pending] #work 实现主任务") || strings.Contains(prompt, "等待前置事项：") {
		t.Fatalf("second continuation did not resume original todo: %q", prompt)
	}
	if snapshot, ok := broker.Latest(); !ok || !snapshot.AllCompleted() {
		t.Fatalf("latest todo not complete: %+v", snapshot)
	}
}
