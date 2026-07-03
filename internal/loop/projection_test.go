package loop

import (
	"testing"

	"codex-agent-go/internal/message"
	"codex-agent-go/internal/model"
)

// ---------------------------------------------------------------------------
// ApplyHistoryProjection tests
// ---------------------------------------------------------------------------

func TestApplyHistoryProjection_EmptyEvents(t *testing.T) {
	history := ApplyHistoryProjection(nil, nil)
	if len(history) != 0 {
		t.Errorf("expected empty history, got %d messages", len(history))
	}
}

func TestApplyHistoryProjection_MessageEvents(t *testing.T) {
	msg1 := message.Message{Role: message.RoleUser, Content: "msg1"}
	msg2 := message.Message{Role: message.RoleAssistant, Content: "msg2"}
	msg3 := message.Message{Role: message.RoleUser, Content: "msg3"}

	events := []SessionEvent{
		{Kind: EventKindHistoryMessage, Message: &msg1},
		{Kind: EventKindHistoryMessage, Message: &msg2},
		{Kind: EventKindHistoryMessage, Message: &msg3},
	}

	history := ApplyHistoryProjection(nil, events)
	if len(history) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(history))
	}
	if history[0].Content != "msg1" {
		t.Errorf("history[0] wrong content: %q", history[0].Content)
	}
	if history[1].Content != "msg2" {
		t.Errorf("history[1] wrong content: %q", history[1].Content)
	}
	if history[2].Content != "msg3" {
		t.Errorf("history[2] wrong content: %q", history[2].Content)
	}
}

func TestApplyHistoryProjection_ResetThenMessage(t *testing.T) {
	msgA := message.Message{Role: message.RoleUser, Content: "before-reset-1"}
	msgB := message.Message{Role: message.RoleUser, Content: "before-reset-2"}
	msgC := message.Message{Role: message.RoleUser, Content: "after-reset"}

	events := []SessionEvent{
		{Kind: EventKindHistoryMessage, Message: &msgA},
		{Kind: EventKindHistoryMessage, Message: &msgB},
		{Kind: EventKindHistoryReset},
		{Kind: EventKindHistoryMessage, Message: &msgC},
	}

	history := ApplyHistoryProjection(nil, events)
	if len(history) != 1 {
		t.Fatalf("expected 1 message after reset, got %d", len(history))
	}
	if history[0].Content != "after-reset" {
		t.Errorf("expected %q, got %q", "after-reset", history[0].Content)
	}
}

func TestApplyHistoryProjection_FromSnapshot(t *testing.T) {
	snapMsg1 := message.Message{Role: message.RoleUser, Content: "snap-msg-1"}
	snapMsg2 := message.Message{Role: message.RoleAssistant, Content: "snap-msg-2"}
	newMsg := message.Message{Role: message.RoleUser, Content: "incremental"}

	base := &SessionSnapshot{
		SessionID: "test",
		History:   []message.Message{snapMsg1, snapMsg2},
	}

	events := []SessionEvent{
		{Kind: EventKindHistoryMessage, Message: &newMsg},
	}

	history := ApplyHistoryProjection(base, events)
	if len(history) != 3 {
		t.Fatalf("expected 3 messages (2 from snapshot + 1 event), got %d", len(history))
	}
	if history[0].Content != "snap-msg-1" {
		t.Errorf("history[0] wrong content: %q", history[0].Content)
	}
	if history[1].Content != "snap-msg-2" {
		t.Errorf("history[1] wrong content: %q", history[1].Content)
	}
	if history[2].Content != "incremental" {
		t.Errorf("history[2] wrong content: %q", history[2].Content)
	}
}

func TestApplyHistoryProjection_NilMessagePayloadSkipped(t *testing.T) {
	msg := message.Message{Role: message.RoleUser, Content: "real"}
	events := []SessionEvent{
		{Kind: EventKindHistoryMessage, Message: nil}, // nil payload — should be skipped
		{Kind: EventKindHistoryMessage, Message: &msg},
	}

	history := ApplyHistoryProjection(nil, events)
	if len(history) != 1 {
		t.Fatalf("expected 1 message (nil payload skipped), got %d", len(history))
	}
	if history[0].Content != "real" {
		t.Errorf("expected %q, got %q", "real", history[0].Content)
	}
}

// ---------------------------------------------------------------------------
// ApplyUsageProjection tests
// ---------------------------------------------------------------------------

func TestApplyUsageProjection_UsageUpdate(t *testing.T) {
	usage := model.Usage{InputTokens: 150, OutputTokens: 50}

	events := []SessionEvent{
		{
			Kind:  EventKindUsageUpdate,
			Usage: &SessionUsagePayload{Usage: usage, IsSession: false},
		},
	}

	state := ApplyUsageProjection(nil, events)
	if !state.UsageKnown {
		t.Error("expected UsageKnown=true")
	}
	if state.Usage.InputTokens != 150 {
		t.Errorf("Usage.InputTokens mismatch: got %d", state.Usage.InputTokens)
	}
	if state.Usage.OutputTokens != 50 {
		t.Errorf("Usage.OutputTokens mismatch: got %d", state.Usage.OutputTokens)
	}
	// session usage should remain unset
	if state.SessionUsageKnown {
		t.Error("expected SessionUsageKnown=false when only turn usage was updated")
	}
}

func TestApplyUsageProjection_SessionUsage(t *testing.T) {
	sessionUsage := model.Usage{PromptTokens: 300, CompletionTokens: 100}

	events := []SessionEvent{
		{
			Kind:  EventKindUsageUpdate,
			Usage: &SessionUsagePayload{Usage: sessionUsage, IsSession: true},
		},
	}

	state := ApplyUsageProjection(nil, events)
	if !state.SessionUsageKnown {
		t.Error("expected SessionUsageKnown=true")
	}
	if state.SessionUsage.PromptTokens != 300 {
		t.Errorf("SessionUsage.PromptTokens mismatch: got %d", state.SessionUsage.PromptTokens)
	}
	if state.SessionUsage.CompletionTokens != 100 {
		t.Errorf("SessionUsage.CompletionTokens mismatch: got %d", state.SessionUsage.CompletionTokens)
	}
	// turn usage should remain unset
	if state.UsageKnown {
		t.Error("expected UsageKnown=false when only session usage was updated")
	}
}

func TestApplyUsageProjection_EmptyEvents(t *testing.T) {
	state := ApplyUsageProjection(nil, nil)
	if state.UsageKnown || state.SessionUsageKnown {
		t.Error("expected all usage flags false for empty events")
	}
}

func TestApplyUsageProjection_FromSnapshot(t *testing.T) {
	base := &SessionSnapshot{
		SessionID:         "snap",
		Usage:             model.Usage{InputTokens: 100},
		UsageKnown:        true,
		SessionUsage:      model.Usage{PromptTokens: 200},
		SessionUsageKnown: true,
	}

	// An incremental update to turn usage only
	updatedUsage := model.Usage{InputTokens: 500, OutputTokens: 200}
	events := []SessionEvent{
		{
			Kind:  EventKindUsageUpdate,
			Usage: &SessionUsagePayload{Usage: updatedUsage, IsSession: false},
		},
	}

	state := ApplyUsageProjection(base, events)
	if !state.UsageKnown {
		t.Error("expected UsageKnown=true")
	}
	// The event overwrites the snapshot's turn usage
	if state.Usage.InputTokens != 500 {
		t.Errorf("Usage.InputTokens mismatch: got %d, want 500", state.Usage.InputTokens)
	}
	// Session usage comes from snapshot, unchanged
	if !state.SessionUsageKnown {
		t.Error("expected SessionUsageKnown=true from snapshot")
	}
	if state.SessionUsage.PromptTokens != 200 {
		t.Errorf("SessionUsage.PromptTokens mismatch: got %d, want 200", state.SessionUsage.PromptTokens)
	}
}

func TestApplyUsageProjection_NilUsagePayloadSkipped(t *testing.T) {
	events := []SessionEvent{
		{Kind: EventKindUsageUpdate, Usage: nil}, // nil payload should be skipped
	}

	state := ApplyUsageProjection(nil, events)
	if state.UsageKnown || state.SessionUsageKnown {
		t.Error("nil usage payload should be skipped, expected no usage recorded")
	}
}
