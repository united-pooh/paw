package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"paw/internal/model"
	"paw/internal/session"
	"paw/internal/todo"
	"paw/internal/tool"
	toolfile "paw/internal/tool/file"
	selecttool "paw/internal/tool/select"
)

func TestRegisterInteractiveToolsAddsQuestion(t *testing.T) {
	registry := tool.NewRegistry()
	broker := selecttool.NewBroker()
	defer broker.Close()
	if err := registerInteractiveTools(registry, broker); err != nil {
		t.Fatal(err)
	}
	if _, ok := registry.Get("question"); !ok {
		t.Fatal("interactive registry missing question")
	}
}

func TestRegisterMainAgentToolsAddsUpdateTodo(t *testing.T) {
	registry := tool.NewRegistry()
	broker := todo.NewBroker()
	defer broker.Close()
	if err := registerMainAgentTools(registry, broker); err != nil {
		t.Fatalf("registerMainAgentTools() error = %v", err)
	}
	registered, ok := registry.Get("update_todo")
	if !ok {
		t.Fatal("main registry missing update_todo")
	}
	result, err := registered.Run(context.Background(), json.RawMessage(`{"items":[{"id":"a","content":"A","status":"in_progress"}]}`))
	if err != nil || result == "" {
		t.Fatalf("update_todo Run() = %q, %v", result, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := broker.Next(ctx); err != nil {
		t.Fatalf("interactive update_todo did not publish: %v", err)
	}
}

func TestRegisterMainAgentToolsAddsHeadlessUpdateTodo(t *testing.T) {
	registry := tool.NewRegistry()
	if err := registerMainAgentTools(registry, nil); err != nil {
		t.Fatalf("registerMainAgentTools() error = %v", err)
	}
	registered, ok := registry.Get("update_todo")
	if !ok {
		t.Fatal("main registry missing headless update_todo")
	}
	if _, err := registered.Run(context.Background(), json.RawMessage(`{"items":[]}`)); err != nil {
		t.Fatalf("headless update_todo Run() error = %v", err)
	}
}

// worker 进程不注册任务编排工具（Task/TaskStatus/TaskStop/TaskWait）：任务
// 注册表投影是进程内的，worker 里 TaskWait 等不到其他进程任务的终态更新会
// 死锁；主 agent 保留完整编排能力。
func TestRegisterToolsSkipsTaskOrchestrationForWorker(t *testing.T) {
	worker := tool.NewRegistry()
	if err := registerTools(worker, t.TempDir(), nil, nil, "", nil, false, true); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"Task", "TaskStatus", "TaskStop", "TaskWait"} {
		if _, ok := worker.Get(name); ok {
			t.Fatalf("worker registry must not expose %s", name)
		}
	}
	if _, ok := worker.Get("Read"); !ok {
		t.Fatal("worker registry missing base tool Read")
	}

	main := tool.NewRegistry()
	if err := registerTools(main, t.TempDir(), nil, nil, "", nil, false, false); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"Task", "TaskStatus", "TaskStop", "TaskWait"} {
		if _, ok := main.Get(name); !ok {
			t.Fatalf("main registry missing %s", name)
		}
	}
}

func TestRegisterToolsDoesNotAddQuestion(t *testing.T) {
	registry := tool.NewRegistry()
	if err := registerTools(registry, t.TempDir(), nil, nil, "", nil, false, false); err != nil {
		t.Fatal(err)
	}
	if _, ok := registry.Get("question"); ok {
		t.Fatal("base registry unexpectedly contains question")
	}
}

func TestAllBuiltinToolSchemasPrepareForDeepSeekStrict(t *testing.T) {
	registry := tool.NewRegistry()
	if err := registerTools(registry, t.TempDir(), nil, nil, "", nil, false, false); err != nil {
		t.Fatal(err)
	}
	selectBroker := selecttool.NewBroker()
	defer selectBroker.Close()
	if err := registerInteractiveTools(registry, selectBroker); err != nil {
		t.Fatal(err)
	}
	if err := registerMainAgentTools(registry, nil); err != nil {
		t.Fatal(err)
	}
	definitions := registry.Definitions()
	before := make(map[string]string, len(definitions))
	for _, definition := range definitions {
		before[definition.Name] = string(definition.InputSchema)
	}
	prepared, err := (model.DeepSeekAdapter{}).PrepareTools(definitions)
	if err != nil {
		t.Fatalf("prepare all builtins: %v", err)
	}
	if len(prepared) != len(definitions) || len(prepared) == 0 {
		t.Fatalf("prepared=%d definitions=%d", len(prepared), len(definitions))
	}
	for i, function := range prepared {
		if !function.Strict || !json.Valid(function.Parameters) {
			t.Fatalf("builtin %s strict=%v schema=%s", function.Name, function.Strict, function.Parameters)
		}
		if string(definitions[i].InputSchema) != before[definitions[i].Name] {
			t.Fatalf("builtin %s original schema mutated", definitions[i].Name)
		}
	}
}

func TestRegisterInteractiveToolsRejectsNil(t *testing.T) {
	broker := selecttool.NewBroker()
	defer broker.Close()
	if err := registerInteractiveTools(nil, broker); err == nil || err.Error() != "tool registry is nil" {
		t.Fatalf("nil registry error = %v", err)
	}
	if err := registerInteractiveTools(tool.NewRegistry(), nil); err == nil || err.Error() != "selection broker is nil" {
		t.Fatalf("nil broker error = %v", err)
	}
	if err := registerMainAgentTools(nil, nil); err == nil || err.Error() != "tool registry is nil" {
		t.Fatalf("nil main registry error = %v", err)
	}
}

func TestRegisterToolsIncludesEdit(t *testing.T) {
	registry := tool.NewRegistry()
	if err := registerTools(registry, t.TempDir(), nil, nil, "", nil, false, false); err != nil {
		t.Fatalf("registerTools: %v", err)
	}
	ed, ok := registry.Get("Edit")
	if !ok {
		t.Fatal("Edit tool not registered")
	}
	if ed.Name() != "Edit" {
		t.Fatalf("Edit tool name = %q", ed.Name())
	}
	if _, ok := ed.(*toolfile.EditTool); !ok {
		t.Fatalf("Edit tool concrete type = %T, want *toolfile.EditTool", ed)
	}
}

func TestRegisterToolsEnablesOutsideReadInDangerousMode(t *testing.T) {
	registry := tool.NewRegistry()
	if err := registerTools(registry, t.TempDir(), nil, nil, "", nil, true, false); err != nil {
		t.Fatalf("registerTools: %v", err)
	}
	registered, ok := registry.Get("Read")
	if !ok {
		t.Fatal("Read tool not registered")
	}
	readTool, ok := registered.(*toolfile.ReadTool)
	if !ok {
		t.Fatalf("Read tool concrete type = %T, want *toolfile.ReadTool", registered)
	}
	if !readTool.AllowOutsideRoot {
		t.Fatal("Read tool outside-root access is disabled")
	}
}

func TestWireTodoEventsPersistsSessionEvent(t *testing.T) {
	registry := tool.NewRegistry()
	if err := registerMainAgentTools(registry, nil); err != nil {
		t.Fatalf("register: %v", err)
	}
	store, err := session.NewJSONLStore(t.TempDir())
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	wireTodoEvents(store, "s1", "")

	todoTool, ok := registry.Get("update_todo")
	if !ok {
		t.Fatal("update_todo not registered")
	}
	tt, ok := todoTool.(*todo.Tool)
	if !ok {
		t.Fatalf("unexpected tool type %T", todoTool)
	}
	out, err := tt.Run(context.Background(), json.RawMessage(`{"items":[{"id":"a","content":"do it","status":"in_progress"}]}`))
	if err != nil || !strings.Contains(out, `"accepted":true`) {
		t.Fatalf("run = %q, %v", out, err)
	}
	raw, err := store.LoadResolvedJournalRecords(context.Background(), "s1")
	if err != nil {
		t.Fatalf("load records: %v", err)
	}
	if len(raw) != 1 || raw[0].Kind != session.JournalTodoSnapshot {
		t.Fatalf("expected one todo_snapshot record, got %+v", raw)
	}
	if raw[0].TodoSnapshot == nil || raw[0].TodoSnapshot.Items[0].ID != "a" {
		t.Fatalf("todo snapshot mismatch: %+v", raw[0])
	}
}
