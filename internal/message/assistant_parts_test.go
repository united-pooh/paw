package message

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"
)

func TestAssistantPartsJSONRoundTripPreservesOrderAndOpaqueData(t *testing.T) {
	started := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	finished := started.Add(3 * time.Second)
	original := Message{
		Role: RoleAssistant,
		AssistantParts: []AssistantPart{
			{
				Type:   AssistantPartReasoning,
				Status: AssistantPartCompleted,
				Reasoning: &ReasoningPart{
					Text:                  "considering",
					ProviderStateComplete: true,
					ProviderData:          json.RawMessage(`{"transport":"anthropic-compatible","version":1,"signature":"opaque"}`),
					StartedAt:             &started,
					FinishedAt:            &finished,
				},
			},
			{Type: AssistantPartText, Text: &AssistantTextPart{Text: "done"}},
			{Type: AssistantPartToolCall, ToolCall: &ToolCall{ID: "call-1", Name: "Read", Input: json.RawMessage(`{"file_path":"go.mod"}`)}},
		},
		GeneratedBy: &MessageOrigin{Transport: "anthropic-compatible", Model: "claude-test"},
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	var restored Message
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if len(restored.AssistantParts) != 3 {
		t.Fatalf("len(AssistantParts) = %d, want 3", len(restored.AssistantParts))
	}
	if got := restored.AssistantParts[0].Type; got != AssistantPartReasoning {
		t.Fatalf("part[0].Type = %q, want reasoning", got)
	}
	if got := restored.AssistantParts[1].Text.Text; got != "done" {
		t.Fatalf("part[1].Text = %q, want done", got)
	}
	if got := restored.AssistantParts[2].ToolCall.Name; got != "Read" {
		t.Fatalf("part[2].ToolCall.Name = %q, want Read", got)
	}
	if !bytes.Equal(restored.AssistantParts[0].Reasoning.ProviderData, original.AssistantParts[0].Reasoning.ProviderData) {
		t.Fatalf("reasoning ProviderData = %s, want %s", restored.AssistantParts[0].Reasoning.ProviderData, original.AssistantParts[0].Reasoning.ProviderData)
	}
	if restored.GeneratedBy == nil || *restored.GeneratedBy != *original.GeneratedBy {
		t.Fatalf("GeneratedBy = %#v, want %#v", restored.GeneratedBy, original.GeneratedBy)
	}
}

func TestProjectAssistantCompatibilityExcludesReasoning(t *testing.T) {
	msg := Message{
		Role:    RoleAssistant,
		Content: "stale",
		ToolUse: &ToolCall{ID: "stale", Name: "Old", Input: json.RawMessage(`{}`)},
		AssistantParts: []AssistantPart{
			{Type: AssistantPartReasoning, Reasoning: &ReasoningPart{Text: "private artifact"}},
			{Type: AssistantPartText, Text: &AssistantTextPart{Text: "first "}},
			{Type: AssistantPartToolCall, ToolCall: &ToolCall{ID: "call-1", Name: "Read", Input: json.RawMessage(`{"file_path":"a"}`)}},
			{Type: AssistantPartText, Text: &AssistantTextPart{Text: "second"}},
			{Type: AssistantPartToolCall, ToolCall: &ToolCall{ID: "call-2", Name: "Grep", Input: json.RawMessage(`{"pattern":"x"}`)}},
		},
	}

	projected, err := ProjectAssistantCompatibility(msg)
	if err != nil {
		t.Fatalf("ProjectAssistantCompatibility() error = %v", err)
	}
	if projected.Content != "first second" {
		t.Fatalf("Content = %q, want %q", projected.Content, "first second")
	}
	if projected.ToolUse != nil {
		t.Fatalf("ToolUse = %#v, want nil for multiple calls", projected.ToolUse)
	}
	if len(projected.ToolUses) != 2 || projected.ToolUses[0].Name != "Read" || projected.ToolUses[1].Name != "Grep" {
		t.Fatalf("ToolUses = %#v, want Read/Grep", projected.ToolUses)
	}
	if bytes.Contains([]byte(projected.Content), []byte("private")) {
		t.Fatalf("reasoning leaked into Content: %q", projected.Content)
	}
}

func TestProjectAssistantCompatibilityUsesSingularToolCall(t *testing.T) {
	msg := Message{
		Role: RoleAssistant,
		AssistantParts: []AssistantPart{
			{Type: AssistantPartToolCall, ToolCall: &ToolCall{ID: "call-1", Name: "Read", Input: json.RawMessage(`{}`)}},
		},
	}
	projected, err := ProjectAssistantCompatibility(msg)
	if err != nil {
		t.Fatalf("ProjectAssistantCompatibility() error = %v", err)
	}
	if projected.ToolUse == nil || projected.ToolUse.Name != "Read" || len(projected.ToolUses) != 0 {
		t.Fatalf("singular projection = ToolUse %#v ToolUses %#v", projected.ToolUse, projected.ToolUses)
	}
}

func TestMaterializeAssistantPartsForLegacyMessage(t *testing.T) {
	legacy := Message{
		Role:    RoleAssistant,
		Content: "legacy text",
		ToolUses: []ToolCall{
			{ID: "call-1", Name: "Read", Input: json.RawMessage(`{}`)},
			{ID: "call-2", Name: "Grep", Input: json.RawMessage(`{}`)},
		},
	}
	parts, err := MaterializeAssistantParts(legacy)
	if err != nil {
		t.Fatalf("MaterializeAssistantParts() error = %v", err)
	}
	if len(parts) != 3 || parts[0].Type != AssistantPartText || parts[1].Type != AssistantPartToolCall || parts[2].Type != AssistantPartToolCall {
		t.Fatalf("parts = %#v, want text then two calls", parts)
	}
	if parts[0].Text.Text != "legacy text" {
		t.Fatalf("legacy text = %q", parts[0].Text.Text)
	}
}

func TestValidateAssistantPartsRejectsInvalidShapes(t *testing.T) {
	tests := []struct {
		name string
		msg  Message
	}{
		{name: "non assistant", msg: Message{Role: RoleUser, AssistantParts: []AssistantPart{{Type: AssistantPartText, Text: &AssistantTextPart{Text: "x"}}}}},
		{name: "unknown type", msg: Message{Role: RoleAssistant, AssistantParts: []AssistantPart{{Type: "mystery"}}}},
		{name: "reasoning missing payload", msg: Message{Role: RoleAssistant, AssistantParts: []AssistantPart{{Type: AssistantPartReasoning}}}},
		{name: "redacted readable", msg: Message{Role: RoleAssistant, AssistantParts: []AssistantPart{{Type: AssistantPartReasoning, Reasoning: &ReasoningPart{Text: "leak", Redacted: true}}}}},
		{name: "text with reasoning payload", msg: Message{Role: RoleAssistant, AssistantParts: []AssistantPart{{Type: AssistantPartText, Text: &AssistantTextPart{Text: "x"}, Reasoning: &ReasoningPart{Text: "y"}}}}},
		{name: "tool missing name", msg: Message{Role: RoleAssistant, AssistantParts: []AssistantPart{{Type: AssistantPartToolCall, ToolCall: &ToolCall{ID: "call-1"}}}}},
		{name: "bad status", msg: Message{Role: RoleAssistant, AssistantParts: []AssistantPart{{Type: AssistantPartText, Status: "broken", Text: &AssistantTextPart{Text: "x"}}}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := ValidateAssistantParts(tt.msg); err == nil {
				t.Fatal("ValidateAssistantParts() error = nil, want error")
			}
		})
	}
}

func TestCloneMessageDeepCopiesAssistantPartsAndOrigin(t *testing.T) {
	started := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	original := Message{
		Role: RoleAssistant,
		AssistantParts: []AssistantPart{
			{Type: AssistantPartReasoning, Reasoning: &ReasoningPart{Text: "think", ProviderData: json.RawMessage(`{"signature":"opaque"}`), StartedAt: &started}},
			{Type: AssistantPartToolCall, ToolCall: &ToolCall{ID: "call-1", Name: "Read", Input: json.RawMessage(`{"file_path":"go.mod"}`)}},
		},
		GeneratedBy: &MessageOrigin{Transport: "anthropic-compatible", Model: "claude-test"},
	}
	cloned := CloneMessage(original)

	cloned.AssistantParts[0].Reasoning.ProviderData[2] = 'X'
	cloned.AssistantParts[0].Reasoning.StartedAt = ptrTime(started.Add(time.Hour))
	cloned.AssistantParts[1].ToolCall.Input[2] = 'X'
	cloned.GeneratedBy.Model = "changed"

	if string(original.AssistantParts[0].Reasoning.ProviderData) != `{"signature":"opaque"}` {
		t.Fatalf("original reasoning ProviderData mutated: %s", original.AssistantParts[0].Reasoning.ProviderData)
	}
	if !original.AssistantParts[0].Reasoning.StartedAt.Equal(started) {
		t.Fatalf("original StartedAt mutated: %v", original.AssistantParts[0].Reasoning.StartedAt)
	}
	if string(original.AssistantParts[1].ToolCall.Input) != `{"file_path":"go.mod"}` {
		t.Fatalf("original tool input mutated: %s", original.AssistantParts[1].ToolCall.Input)
	}
	if original.GeneratedBy.Model != "claude-test" {
		t.Fatalf("original origin mutated: %#v", original.GeneratedBy)
	}
}

func ptrTime(value time.Time) *time.Time {
	return &value
}
