package loop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"paw/internal/message"
	"paw/internal/model"
	"paw/internal/tool"
)

func TestLoadSessionRepairsToolCallPairs(t *testing.T) {
	history := []message.Message{
		{Role: message.RoleUser, Content: "inspect"},
		{Role: message.RoleAssistant, ToolUses: []message.ToolCall{
			{ID: "call_1", Name: "Read", Input: json.RawMessage(`{}`)},
		}},
		{Role: message.RoleUser, ToolResults: []message.ToolResult{
			{ToolUseID: "call_1", Content: "module paw"},
			{ToolUseID: "call_ghost", Content: "stale orphan from crashed turn"},
		}},
	}
	store := &fakeStore{history: history}
	runner := NewEngine(&fakeModel{}, &fakeUI{}, tool.NewRegistry(), store, "orphan-session")

	result, err := runner.LoadSession(context.Background(), "orphan-session")
	if err != nil {
		t.Fatalf("LoadSession() error = %v", err)
	}
	if len(result.Messages) != 3 {
		t.Fatalf("result.Messages = %d, want 3", len(result.Messages))
	}
	results := toolResultsFromMessage(result.Messages[2])
	if len(results) != 1 || results[0].ToolUseID != "call_1" {
		t.Fatalf("loaded messages orphan not repaired: %#v", result.Messages[2])
	}

	current := runner.currentHistory()
	if len(current) != 3 {
		t.Fatalf("runner history = %d, want 3", len(current))
	}
	results = toolResultsFromMessage(current[2])
	if len(results) != 1 || results[0].ToolUseID != "call_1" || results[0].Content != "module paw" {
		t.Fatalf("runner history orphan not repaired: %#v", current[2])
	}
}

func TestLoadSessionRepairsDanglingCallOnResume(t *testing.T) {
	// 崩溃恢复场景：assistant 声明了调用但结果丢失，下一轮 user 输入直接跟在后面。
	history := []message.Message{
		{Role: message.RoleUser, Content: "inspect"},
		{Role: message.RoleAssistant, ToolUses: []message.ToolCall{
			{ID: "call_lost", Name: "Read", Input: json.RawMessage(`{}`)},
		}},
		{Role: message.RoleUser, Content: "continue after crash"},
	}
	store := &fakeStore{history: history}
	runner := NewEngine(&fakeModel{}, &fakeUI{}, tool.NewRegistry(), store, "resume-session")

	if _, err := runner.LoadSession(context.Background(), "resume-session"); err != nil {
		t.Fatalf("LoadSession() error = %v", err)
	}
	current := runner.currentHistory()
	results := toolResultsFromMessage(current[2])
	if len(results) != 1 || results[0].ToolUseID != "call_lost" || !results[0].IsError {
		t.Fatalf("dangling call not repaired on resume: %#v", current[2])
	}
	if !strings.Contains(results[0].Content, "not executed") {
		t.Fatalf("synthetic result content = %q", results[0].Content)
	}
}

func TestDecorateToolPairErrorAddsHintOnlyForPairingErrors(t *testing.T) {
	pairingErr := &model.ProviderHTTPError{
		StatusCode: 400,
		Type:       "invalid_request_error",
		Message:    "No tool call found for tool output with call_id call_00_x.",
	}
	wrapped := fmt.Errorf("调用模型接口失败: %w", pairingErr)

	decorated := decorateToolPairError(wrapped)
	if decorated == nil {
		t.Fatal("decorateToolPairError() = nil")
	}
	if !strings.Contains(decorated.Error(), "/compact") || !strings.Contains(decorated.Error(), "/clear") {
		t.Fatalf("decorated error missing hint: %v", decorated)
	}
	if !errors.Is(decorated, pairingErr) {
		t.Fatalf("errors.Is lost underlying error")
	}

	// 普通错误原样返回（同一指针）。
	plain := errors.New("普通错误")
	if got := decorateToolPairError(plain); got != plain {
		t.Fatalf("non-pairing error changed: %v", got)
	}
	if got := decorateToolPairError(nil); got != nil {
		t.Fatalf("nil error changed: %v", got)
	}
}
