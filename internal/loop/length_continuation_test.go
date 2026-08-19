package loop

import (
	"context"
	"errors"
	"strings"
	"testing"

	"paw/internal/message"
	"paw/internal/model"
	"paw/internal/session"
	"paw/internal/tool"
)

func TestRunTurnLengthContinuationMergesDedupesUsageAndPersistsOnce(t *testing.T) {
	store, err := session.NewJSONLStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ui := &fakeUI{}
	modelClient := &fakeModel{rounds: []fakeRound{
		{events: []model.StreamEvent{
			{Usage: &model.Usage{PromptTokens: 10, CompletionTokens: 2, TotalTokens: 12}},
			{Delta: "hello world"},
			{Done: true, FinishReason: model.FinishReasonLength},
		}},
		{events: []model.StreamEvent{
			{Usage: &model.Usage{PromptTokens: 20, CompletionTokens: 3, TotalTokens: 23}},
			{Delta: "world and done"},
			{Done: true, FinishReason: model.FinishReasonStop},
		}},
	}}
	runner := NewEngine(modelClient, ui, tool.NewRegistry(), store, "session-1")

	msg, err := runner.RunTurn(context.Background(), "start")
	if err != nil {
		t.Fatalf("RunTurn() error = %v", err)
	}
	if msg.Content != "hello world and done" {
		t.Fatalf("assistant content = %q, want merged deduped text", msg.Content)
	}
	if got := strings.Join(ui.deltas, ""); got != "hello world and done" {
		t.Fatalf("ui deltas = %q, want merged deduped text", got)
	}
	if ui.doneCount != 1 {
		t.Fatalf("doneCount = %d, want 1", ui.doneCount)
	}
	if len(modelClient.calls) != 2 {
		t.Fatalf("model calls = %d, want 2", len(modelClient.calls))
	}
	second := modelClient.calls[1]
	if len(second) < 3 {
		t.Fatalf("second call messages = %#v", second)
	}
	if got := second[len(second)-2]; got.Role != message.RoleAssistant || got.Content != "hello world" {
		t.Fatalf("continuation assistant context = %#v, want accumulated text", got)
	}
	if got := second[len(second)-1]; got.Role != message.RoleUser || got.Content != lengthContinuationInstruction {
		t.Fatalf("continuation instruction = %#v, want literal instruction", got)
	}
	stats := runner.ContextStats(1024, "")
	if stats.UsedTokens != 35 || stats.SessionUsedTokens != 35 {
		t.Fatalf("ContextStats() = %#v, want accumulated usage 35", stats)
	}
	history, err := store.LoadResolvedHistory(context.Background(), "session-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 2 || history[1].Content != "hello world and done" {
		t.Fatalf("history=%#v, want one complete assistant message", history)
	}
}

func TestRunTurnLengthContinuationDedupesOverlapSplitAcrossDeltas(t *testing.T) {
	ui := &fakeUI{}
	modelClient := &fakeModel{rounds: []fakeRound{
		{events: []model.StreamEvent{
			{Delta: "hello world"},
			{Done: true, FinishReason: model.FinishReasonLength},
		}},
		{events: []model.StreamEvent{
			{Delta: "wo"},
			{Delta: "rld and done"},
			{Done: true, FinishReason: model.FinishReasonStop},
		}},
	}}
	runner := NewEngine(modelClient, ui, tool.NewRegistry(), nil, "")

	msg, err := runner.RunTurn(context.Background(), "start")
	if err != nil {
		t.Fatalf("RunTurn() error = %v", err)
	}
	if msg.Content != "hello world and done" {
		t.Fatalf("assistant content = %q, want split-overlap deduped text", msg.Content)
	}
	if got := strings.Join(ui.deltas, ""); got != "hello world and done" {
		t.Fatalf("ui deltas = %q, want split-overlap deduped text", got)
	}
	if ui.doneCount != 1 {
		t.Fatalf("doneCount = %d, want 1", ui.doneCount)
	}
}

func TestRunTurnLengthContinuationDedupesUnicodeOverlapSplitAcrossDeltas(t *testing.T) {
	ui := &fakeUI{}
	modelClient := &fakeModel{rounds: []fakeRound{
		{events: []model.StreamEvent{
			{Delta: "你好世界"},
			{Done: true, FinishReason: model.FinishReasonLength},
		}},
		{events: []model.StreamEvent{
			{Delta: "世"},
			{Delta: "界继续"},
			{Done: true, FinishReason: model.FinishReasonStop},
		}},
	}}
	runner := NewEngine(modelClient, ui, tool.NewRegistry(), nil, "")

	msg, err := runner.RunTurn(context.Background(), "start")
	if err != nil {
		t.Fatalf("RunTurn() error = %v", err)
	}
	if msg.Content != "你好世界继续" {
		t.Fatalf("assistant content = %q, want rune-aware deduped text", msg.Content)
	}
	if got := strings.Join(ui.deltas, ""); got != "你好世界继续" {
		t.Fatalf("ui deltas = %q, want rune-aware deduped text", got)
	}
	if ui.doneCount != 1 {
		t.Fatalf("doneCount = %d, want 1", ui.doneCount)
	}
}

func TestRunTurnThreeLengthFinishesWithExplicitErrorAndOneDone(t *testing.T) {
	ui := &fakeUI{}
	modelClient := &fakeModel{rounds: []fakeRound{
		{events: []model.StreamEvent{{Delta: "a"}, {Done: true, FinishReason: model.FinishReasonLength}}},
		{events: []model.StreamEvent{{Delta: "b"}, {Done: true, FinishReason: model.FinishReasonLength}}},
		{events: []model.StreamEvent{{Delta: "c"}, {Done: true, FinishReason: model.FinishReasonLength}}},
	}}
	runner := NewEngine(modelClient, ui, tool.NewRegistry(), nil, "")

	_, err := runner.RunTurn(context.Background(), "start")
	if err == nil || !strings.Contains(err.Error(), "连续 3 次以 length 截断") {
		t.Fatalf("RunTurn() error = %v, want repeated length error", err)
	}
	if ui.doneCount != 1 {
		t.Fatalf("doneCount = %d, want 1", ui.doneCount)
	}
}

func TestRunTurnLengthWithToolCallsFailsAndFinalizesPartialOnce(t *testing.T) {
	ui := &fakeUI{}
	modelClient := &fakeModel{rounds: []fakeRound{{events: []model.StreamEvent{
		{Delta: "partial"},
		{ToolCalls: []message.ToolCall{{ID: "call-read", Name: "Read"}}},
		{Done: true, FinishReason: model.FinishReasonLength},
	}}}}
	runner := NewEngine(modelClient, ui, tool.NewRegistry(), nil, "")

	_, err := runner.RunTurn(context.Background(), "start")
	if err == nil || !strings.Contains(err.Error(), "包含工具调用") {
		t.Fatalf("RunTurn() error = %v, want length tool-call error", err)
	}
	if ui.doneCount != 1 {
		t.Fatalf("doneCount = %d, want 1", ui.doneCount)
	}
}

func TestRunTurnContinuationToolCallFailsAndFinalizesPartialOnce(t *testing.T) {
	ui := &fakeUI{}
	modelClient := &fakeModel{rounds: []fakeRound{
		{events: []model.StreamEvent{{Delta: "partial"}, {Done: true, FinishReason: model.FinishReasonLength}}},
		{events: []model.StreamEvent{
			{ToolCalls: []message.ToolCall{{ID: "call-read", Name: "Read"}}},
			{Done: true, FinishReason: model.FinishReasonToolCalls},
		}},
	}}
	runner := NewEngine(modelClient, ui, tool.NewRegistry(), nil, "")

	_, err := runner.RunTurn(context.Background(), "start")
	if err == nil || !strings.Contains(err.Error(), "续写响应包含工具调用") {
		t.Fatalf("RunTurn() error = %v, want continuation tool-call error", err)
	}
	if ui.doneCount != 1 {
		t.Fatalf("doneCount = %d, want 1", ui.doneCount)
	}
}

func TestRunTurnEmptyContinuationFailsAndFinalizesPartialOnce(t *testing.T) {
	ui := &fakeUI{}
	modelClient := &fakeModel{rounds: []fakeRound{
		{events: []model.StreamEvent{{Delta: "partial"}, {Done: true, FinishReason: model.FinishReasonLength}}},
		{events: []model.StreamEvent{{Done: true, FinishReason: model.FinishReasonStop}}},
	}}
	runner := NewEngine(modelClient, ui, tool.NewRegistry(), nil, "")

	_, err := runner.RunTurn(context.Background(), "start")
	if err == nil || !strings.Contains(err.Error(), "没有返回新文本") {
		t.Fatalf("RunTurn() error = %v, want empty continuation error", err)
	}
	if ui.doneCount != 1 {
		t.Fatalf("doneCount = %d, want 1", ui.doneCount)
	}
}

func TestRunTurnContinuationRequestErrorFailsAndFinalizesPartialOnce(t *testing.T) {
	ui := &fakeUI{}
	modelClient := &fakeModel{rounds: []fakeRound{
		{events: []model.StreamEvent{{Delta: "partial"}, {Done: true, FinishReason: model.FinishReasonLength}}},
		{err: errors.New("dial tcp: connection reset")},
	}}
	runner := NewEngine(modelClient, ui, tool.NewRegistry(), nil, "")

	_, err := runner.RunTurn(context.Background(), "start")
	if err == nil || !strings.Contains(err.Error(), "connection reset") {
		t.Fatalf("RunTurn() error = %v, want continuation request error", err)
	}
	if ui.doneCount != 1 {
		t.Fatalf("doneCount = %d, want 1", ui.doneCount)
	}
}

func TestRunTurnContinuationFlushesBufferedPrefixOnStreamError(t *testing.T) {
	ui := &fakeUI{}
	modelClient := &fakeModel{rounds: []fakeRound{
		{events: []model.StreamEvent{{Delta: "hello"}, {Done: true, FinishReason: model.FinishReasonLength}}},
		{events: []model.StreamEvent{{Delta: " wor"}, {Err: errors.New("stream died")}}},
	}}
	runner := NewEngine(modelClient, ui, tool.NewRegistry(), nil, "")

	_, err := runner.RunTurn(context.Background(), "start")
	if err == nil || !strings.Contains(err.Error(), "stream died") {
		t.Fatalf("RunTurn() error = %v, want stream died", err)
	}
	if got := strings.Join(ui.deltas, ""); got != "hello wor" {
		t.Fatalf("ui deltas = %q, want buffered continuation prefix flushed", got)
	}
	if ui.doneCount != 1 {
		t.Fatalf("doneCount = %d, want 1", ui.doneCount)
	}
}

func TestRunTurnStreamEOFErrorDoesNotStartContinuation(t *testing.T) {
	ui := &fakeUI{}
	modelClient := &fakeModel{rounds: []fakeRound{{events: []model.StreamEvent{{Delta: "partial"}}}}}
	runner := NewEngine(modelClient, ui, tool.NewRegistry(), nil, "")

	_, err := runner.RunTurn(context.Background(), "start")
	if err == nil || !strings.Contains(err.Error(), "未发送完成事件") {
		t.Fatalf("RunTurn() error = %v, want missing Done error", err)
	}
	if len(modelClient.calls) != 1 {
		t.Fatalf("model calls = %d, want no continuation after EOF", len(modelClient.calls))
	}
	if ui.doneCount != 1 {
		t.Fatalf("doneCount = %d, want 1", ui.doneCount)
	}
}

func TestRunTurnContinuationFlushesBufferedPrefixOnChannelEOF(t *testing.T) {
	ui := &fakeUI{}
	modelClient := &fakeModel{rounds: []fakeRound{
		{events: []model.StreamEvent{{Delta: "hello"}, {Done: true, FinishReason: model.FinishReasonLength}}},
		{events: []model.StreamEvent{{Delta: " wor"}}},
	}}
	runner := NewEngine(modelClient, ui, tool.NewRegistry(), nil, "")

	_, err := runner.RunTurn(context.Background(), "start")
	if err == nil || !strings.Contains(err.Error(), "未发送完成事件") {
		t.Fatalf("RunTurn() error = %v, want missing Done error", err)
	}
	if got := strings.Join(ui.deltas, ""); got != "hello wor" {
		t.Fatalf("ui deltas = %q, want buffered continuation prefix flushed", got)
	}
	if ui.doneCount != 1 {
		t.Fatalf("doneCount = %d, want 1", ui.doneCount)
	}
}
