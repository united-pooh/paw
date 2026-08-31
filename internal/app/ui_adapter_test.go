package app

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"paw/internal/ui"
)

func TestUIAdapterProjectsOrderedCoreEventsAndDetails(t *testing.T) {
	coordinator := NewWorkspaceCoordinator()
	if _, err := coordinator.BeginTurn("session", "turn"); err != nil {
		t.Fatal(err)
	}
	hub := newTestEventHub(t, EventHubConfig{WorkspaceID: "workspace", StreamID: "stream"})
	adapter := NewUIAdapter("workspace", coordinator, hub)
	now := time.Date(2026, 8, 31, 20, 0, 0, 0, time.UTC)
	adapter.now = func() time.Time { return now }
	if err := adapter.BindTurn("session", "turn"); err != nil {
		t.Fatal(err)
	}
	if err := adapter.OnReasoningStart(2, false); err != nil {
		t.Fatal(err)
	}
	if err := adapter.OnReasoningDelta(2, "think"); err != nil {
		t.Fatal(err)
	}
	if err := adapter.OnReasoningEnd(2); err != nil {
		t.Fatal(err)
	}
	if err := adapter.OnAssistantDelta("hello "); err != nil {
		t.Fatal(err)
	}
	if err := adapter.OnAssistantDelta("world"); err != nil {
		t.Fatal(err)
	}
	startedAt := now.Add(-81 * time.Millisecond)
	if err := adapter.OnToolCall(ui.ToolCallEvent{ID: "call-1", Name: "Read", Input: json.RawMessage(`{"file_path":"README.md"}`), ArgsGenStartedAt: startedAt}); err != nil {
		t.Fatal(err)
	}
	fullResult := strings.Repeat("result ", 80)
	if err := adapter.OnToolResult(ui.ToolResultEvent{ToolUseID: "call-1", Name: "Read", Content: fullResult}); err != nil {
		t.Fatal(err)
	}
	if err := adapter.OnDone(); err != nil {
		t.Fatal(err)
	}

	subscription, err := hub.Subscribe(EventCursor{StreamID: "stream"})
	if err != nil {
		t.Fatal(err)
	}
	defer subscription.Close()
	wantTypes := []EventType{
		EventReasoningStarted, EventReasoningDelta, EventReasoningCompleted,
		EventAssistantPartStarted, EventToolStarted, EventToolCompleted,
		EventAssistantDelta, EventAssistantPartCompleted,
	}
	if len(subscription.Replay) != len(wantTypes) {
		t.Fatalf("event count = %d, events=%#v", len(subscription.Replay), subscription.Replay)
	}
	for index, want := range wantTypes {
		if subscription.Replay[index].Type != want {
			t.Fatalf("event[%d] type = %s, want %s", index, subscription.Replay[index].Type, want)
		}
	}
	var toolPayload ToolCompletedPayload
	if err := json.Unmarshal(subscription.Replay[5].Payload, &toolPayload); err != nil {
		t.Fatal(err)
	}
	if toolPayload.DetailID == "" || toolPayload.DurationMS != 81 || len([]rune(toolPayload.ResultSummary)) > 241 {
		t.Fatalf("tool payload = %#v", toolPayload)
	}
	if detail, ok := adapter.Detail(toolPayload.DetailID); !ok || detail.Content != fullResult {
		t.Fatalf("tool detail = %#v / %v", detail, ok)
	}
	var delta AssistantDeltaPayload
	if err := json.Unmarshal(subscription.Replay[6].Payload, &delta); err != nil {
		t.Fatal(err)
	}
	if delta.PartID != "turn:assistant:0" || delta.Offset != 0 || delta.Text != "hello world" {
		t.Fatalf("assistant delta = %#v", delta)
	}
	state := coordinator.WorkspaceSnapshot()
	if part := state.Parts["turn:assistant:0"]; part.Text != "hello world" || !part.Completed {
		t.Fatalf("assistant part = %#v", part)
	}
}

func TestUIAdapterProjectsToolFailureAndSystemMessage(t *testing.T) {
	coordinator := NewWorkspaceCoordinator()
	if _, err := coordinator.BeginTurn("session", "turn"); err != nil {
		t.Fatal(err)
	}
	hub := newTestEventHub(t, EventHubConfig{WorkspaceID: "workspace", StreamID: "stream"})
	adapter := NewUIAdapter("workspace", coordinator, hub)
	adapter.now = func() time.Time { return time.Date(2026, 8, 31, 20, 0, 0, 0, time.UTC) }
	if err := adapter.BindTurn("session", "turn"); err != nil {
		t.Fatal(err)
	}
	if err := adapter.OnToolCall(ui.ToolCallEvent{ID: "call-1", Name: "Bash", Input: json.RawMessage(`{"command":"false"}`)}); err != nil {
		t.Fatal(err)
	}
	if err := adapter.OnToolResult(ui.ToolResultEvent{ToolUseID: "call-1", Name: "Bash", Content: "exit status 1", IsError: true}); err != nil {
		t.Fatal(err)
	}
	if err := adapter.OnSystemMessage(ui.SystemEvent{Title: "task", Body: "finished"}); err != nil {
		t.Fatal(err)
	}
	subscription, err := hub.Subscribe(EventCursor{StreamID: "stream"})
	if err != nil {
		t.Fatal(err)
	}
	defer subscription.Close()
	if len(subscription.Replay) != 3 || subscription.Replay[1].Type != EventToolFailed || subscription.Replay[2].Type != EventSystemMessage {
		t.Fatalf("events = %#v", subscription.Replay)
	}
}

func TestUIAdapterRequiresBoundTurn(t *testing.T) {
	hub := newTestEventHub(t, EventHubConfig{WorkspaceID: "workspace", StreamID: "stream"})
	adapter := NewUIAdapter("workspace", NewWorkspaceCoordinator(), hub)
	if err := adapter.OnAssistantDelta("x"); err == nil {
		t.Fatal("unbound adapter accepted delta")
	}
}
