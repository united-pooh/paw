package loop

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"paw/internal/message"
	"paw/internal/model"
	"paw/internal/tool"
)

// 流式 tool_call block start 时在外层 TurnState 记录参数生成起点（只取第一次）。
func TestHandleAssistantPartEventMarksToolArgsGenStarted(t *testing.T) {
	runner := &Engine{}
	outer := &TurnState{}
	state := &turnState{outer: outer}

	start := &model.AssistantPartEvent{Lifecycle: model.AssistantPartLifecycleStart, Type: "tool_call", ToolCallID: "call-9", ToolName: "Read", BlockIndex: 0}
	if err := runner.handleAssistantPartEvent(state, start); err != nil {
		t.Fatalf("handleAssistantPartEvent: %v", err)
	}
	first, ok := outer.ToolArgsGenAt["call-9"]
	if !ok || first.IsZero() {
		t.Fatalf("ToolArgsGenAt = %#v, want call-9 marked", outer.ToolArgsGenAt)
	}
	// 重复的 start 事件（续流/重试）不重置起点。
	if err := runner.handleAssistantPartEvent(state, start); err != nil {
		t.Fatalf("repeat start: %v", err)
	}
	if got := outer.ToolArgsGenAt["call-9"]; !got.Equal(first) {
		t.Fatalf("args gen start overwritten: %v → %v", first, got)
	}

	// 裸 turnState（无 outer）不记录也不 panic。
	bare := &turnState{}
	if err := runner.handleAssistantPartEvent(bare, start); err != nil {
		t.Fatalf("bare state: %v", err)
	}
}

// runToolCallsWithPhases 把参数生成起点传到 resolved/ToolCallEvent，并聚合
// turn 级阶段耗时（args_gen 与 exec）；无流式窗口的调用 args_gen 为 0。
func TestRunToolCallsWithPhasesTracksStages(t *testing.T) {
	read := &fakeTool{name: "Read", output: "ok", safe: true}
	write := &fakeTool{name: "Write", output: "ok", safe: true}
	registry := tool.NewRegistry()
	registry.Register(read)
	registry.Register(write)
	ui := &fakeUI{}
	runner := NewEngine(nil, ui, registry, nil, "session-1")

	argsGenStart := time.Now().Add(-2 * time.Second)
	argsGen := map[string]time.Time{"call-1": argsGenStart}
	totals := &ToolPhaseTotals{}
	calls := []message.ToolCall{
		{ID: "call-1", Name: "Read", Input: json.RawMessage(`{"file_path":"x"}`)},
		{ID: "call-2", Name: "Write", Input: json.RawMessage(`{}`)},
	}
	if _, err := runner.runToolCallsWithPhases(context.Background(), calls, nil, argsGen, totals); err != nil {
		t.Fatalf("runToolCallsWithPhases: %v", err)
	}
	if totals.Calls != 2 {
		t.Fatalf("totals.Calls = %d, want 2", totals.Calls)
	}
	// call-1 有约 2s 的参数生成窗口；call-2 无窗口。
	if totals.ArgsGenMS < 1500 {
		t.Fatalf("totals.ArgsGenMS = %d, want >= ~2000 (one 2s window)", totals.ArgsGenMS)
	}
	// UI 事件携带参数生成起点（call-1 有，call-2 为零值）。
	if len(ui.toolCalls) != 2 {
		t.Fatalf("ui tool calls = %#v", ui.toolCalls)
	}
	if !ui.toolCalls[0].ArgsGenStartedAt.Equal(argsGenStart) {
		t.Fatalf("event ArgsGenStartedAt = %v, want %v", ui.toolCalls[0].ArgsGenStartedAt, argsGenStart)
	}
	if !ui.toolCalls[1].ArgsGenStartedAt.IsZero() {
		t.Fatalf("event without window must be zero: %v", ui.toolCalls[1].ArgsGenStartedAt)
	}

	// 无 argsGen 传参：不聚合参数生成段。
	totals = &ToolPhaseTotals{}
	if _, err := runner.runToolCallsWithPhases(context.Background(), calls[:1], nil, nil, totals); err != nil {
		t.Fatalf("runToolCallsWithPhases without argsGen: %v", err)
	}
	if totals.ArgsGenMS != 0 {
		t.Fatalf("args gen without window = %d, want 0", totals.ArgsGenMS)
	}
}
