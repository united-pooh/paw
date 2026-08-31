package message

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type AssistantPartType string

const (
	AssistantPartReasoning AssistantPartType = "reasoning"
	AssistantPartText      AssistantPartType = "text"
	AssistantPartToolCall  AssistantPartType = "tool_call"
)

type AssistantPartStatus string

const (
	AssistantPartCompleted AssistantPartStatus = "completed"
	AssistantPartPartial   AssistantPartStatus = "partial"
)

type MessageOrigin struct {
	Provider  string `json:"provider,omitempty"`
	ProfileID string `json:"profile_id,omitempty"`
	Transport string `json:"transport"`
	Adapter   string `json:"adapter,omitempty"`
	Model     string `json:"model"`
}

type AssistantTextPart struct {
	Text string `json:"text"`
}

type ReasoningPart struct {
	Text                  string          `json:"text,omitempty"`
	Redacted              bool            `json:"redacted,omitempty"`
	ProviderStateComplete bool            `json:"provider_state_complete,omitempty"`
	ProviderData          json.RawMessage `json:"provider_data,omitempty"`
	StartedAt             *time.Time      `json:"started_at,omitempty"`
	FinishedAt            *time.Time      `json:"finished_at,omitempty"`
}

type AssistantPart struct {
	Type      AssistantPartType   `json:"type"`
	Status    AssistantPartStatus `json:"status,omitempty"`
	Text      *AssistantTextPart  `json:"text,omitempty"`
	Reasoning *ReasoningPart      `json:"reasoning,omitempty"`
	ToolCall  *ToolCall           `json:"tool_call,omitempty"`
}

func ValidateAssistantParts(msg Message) error {
	if len(msg.AssistantParts) == 0 {
		return nil
	}
	if msg.Role != RoleAssistant {
		return fmt.Errorf("assistant_parts require assistant role, got %q", msg.Role)
	}
	if msg.GeneratedBy != nil {
		if strings.TrimSpace(msg.GeneratedBy.Transport) == "" {
			return fmt.Errorf("generated_by transport is empty")
		}
		if strings.TrimSpace(msg.GeneratedBy.Model) == "" {
			return fmt.Errorf("generated_by model is empty")
		}
	}
	for i, part := range msg.AssistantParts {
		if err := validateAssistantPart(part); err != nil {
			return fmt.Errorf("assistant_parts[%d]: %w", i, err)
		}
	}
	return nil
}

func validateAssistantPart(part AssistantPart) error {
	switch part.Status {
	case "", AssistantPartCompleted, AssistantPartPartial:
	default:
		return fmt.Errorf("unknown status %q", part.Status)
	}

	switch part.Type {
	case AssistantPartText:
		if part.Text == nil {
			return fmt.Errorf("text payload is missing")
		}
		if part.Reasoning != nil || part.ToolCall != nil {
			return fmt.Errorf("text part contains a non-text payload")
		}
	case AssistantPartReasoning:
		if part.Reasoning == nil {
			return fmt.Errorf("reasoning payload is missing")
		}
		if part.Text != nil || part.ToolCall != nil {
			return fmt.Errorf("reasoning part contains a non-reasoning payload")
		}
		if part.Reasoning.Redacted && part.Reasoning.Text != "" {
			return fmt.Errorf("redacted reasoning contains readable text")
		}
		if len(part.Reasoning.ProviderData) != 0 && !json.Valid(part.Reasoning.ProviderData) {
			return fmt.Errorf("reasoning provider_data is not valid JSON")
		}
	case AssistantPartToolCall:
		if part.ToolCall == nil {
			return fmt.Errorf("tool_call payload is missing")
		}
		if part.Text != nil || part.Reasoning != nil {
			return fmt.Errorf("tool_call part contains a non-tool payload")
		}
		if strings.TrimSpace(part.ToolCall.Name) == "" {
			return fmt.Errorf("tool_call name is empty")
		}
		if len(part.ToolCall.Input) != 0 && !json.Valid(part.ToolCall.Input) {
			return fmt.Errorf("tool_call input is not valid JSON")
		}
	default:
		return fmt.Errorf("unknown type %q", part.Type)
	}
	return nil
}

func ProjectAssistantCompatibility(msg Message) (Message, error) {
	if err := ValidateAssistantParts(msg); err != nil {
		return Message{}, err
	}
	projected := CloneMessage(msg)
	if len(projected.AssistantParts) == 0 {
		return projected, nil
	}

	var content strings.Builder
	calls := make([]ToolCall, 0)
	for _, part := range projected.AssistantParts {
		switch part.Type {
		case AssistantPartText:
			content.WriteString(part.Text.Text)
		case AssistantPartToolCall:
			calls = append(calls, cloneToolCall(*part.ToolCall))
		}
	}
	projected.Content = content.String()
	projected.ToolUse = nil
	projected.ToolUses = nil
	switch len(calls) {
	case 1:
		call := calls[0]
		projected.ToolUse = &call
	case 2:
		projected.ToolUses = calls
	default:
		if len(calls) > 2 {
			projected.ToolUses = calls
		}
	}
	return projected, nil
}

func MaterializeAssistantParts(msg Message) ([]AssistantPart, error) {
	if len(msg.AssistantParts) != 0 {
		if err := ValidateAssistantParts(msg); err != nil {
			return nil, err
		}
		return CloneAssistantParts(msg.AssistantParts), nil
	}
	if msg.Role != RoleAssistant {
		return nil, nil
	}

	parts := make([]AssistantPart, 0, 1+len(msg.ToolUses))
	if msg.Content != "" {
		parts = append(parts, AssistantPart{
			Type:   AssistantPartText,
			Status: AssistantPartCompleted,
			Text:   &AssistantTextPart{Text: msg.Content},
		})
	}
	calls := msg.ToolUses
	if len(calls) == 0 && msg.ToolUse != nil {
		calls = []ToolCall{*msg.ToolUse}
	}
	for _, call := range calls {
		call := cloneToolCall(call)
		part := AssistantPart{
			Type:     AssistantPartToolCall,
			Status:   AssistantPartCompleted,
			ToolCall: &call,
		}
		if err := validateAssistantPart(part); err != nil {
			return nil, fmt.Errorf("legacy assistant tool call: %w", err)
		}
		parts = append(parts, part)
	}
	return parts, nil
}

func CloneMessage(msg Message) Message {
	cloned := msg
	cloned.Parts = cloneContentParts(msg.Parts)
	cloned.AssistantParts = CloneAssistantParts(msg.AssistantParts)
	if msg.GeneratedBy != nil {
		origin := *msg.GeneratedBy
		cloned.GeneratedBy = &origin
	}
	if msg.ToolUse != nil {
		call := cloneToolCall(*msg.ToolUse)
		cloned.ToolUse = &call
	}
	cloned.ToolUses = cloneToolCalls(msg.ToolUses)
	if msg.ToolResult != nil {
		result := *msg.ToolResult
		cloned.ToolResult = &result
	}
	cloned.ToolResults = append([]ToolResult(nil), msg.ToolResults...)
	cloned.ProviderData = append(json.RawMessage(nil), msg.ProviderData...)
	return cloned
}

func CloneMessages(messages []Message) []Message {
	if messages == nil {
		return nil
	}
	cloned := make([]Message, len(messages))
	for i := range messages {
		cloned[i] = CloneMessage(messages[i])
	}
	return cloned
}

func CloneAssistantParts(parts []AssistantPart) []AssistantPart {
	if parts == nil {
		return nil
	}
	cloned := make([]AssistantPart, len(parts))
	for i, part := range parts {
		cloned[i] = part
		if part.Text != nil {
			text := *part.Text
			cloned[i].Text = &text
		}
		if part.Reasoning != nil {
			reasoning := *part.Reasoning
			reasoning.ProviderData = append(json.RawMessage(nil), part.Reasoning.ProviderData...)
			reasoning.StartedAt = cloneTime(part.Reasoning.StartedAt)
			reasoning.FinishedAt = cloneTime(part.Reasoning.FinishedAt)
			cloned[i].Reasoning = &reasoning
		}
		if part.ToolCall != nil {
			call := cloneToolCall(*part.ToolCall)
			cloned[i].ToolCall = &call
		}
	}
	return cloned
}

func cloneContentParts(parts []ContentPart) []ContentPart {
	if parts == nil {
		return nil
	}
	cloned := make([]ContentPart, len(parts))
	for i, part := range parts {
		cloned[i] = part
		if part.Image != nil {
			image := *part.Image
			image.Data = append([]byte(nil), part.Image.Data...)
			cloned[i].Image = &image
		}
	}
	return cloned
}

func cloneToolCalls(calls []ToolCall) []ToolCall {
	if calls == nil {
		return nil
	}
	cloned := make([]ToolCall, len(calls))
	for i, call := range calls {
		cloned[i] = cloneToolCall(call)
	}
	return cloned
}

func cloneToolCall(call ToolCall) ToolCall {
	call.Input = append(json.RawMessage(nil), call.Input...)
	return call
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
