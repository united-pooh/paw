package app

import (
	"encoding/json"
	"fmt"
	"time"
)

const AppEventSchemaVersion uint16 = 1

type EventType string

const (
	EventWorkspaceUpdated       EventType = "workspace.updated"
	EventWorkspaceClosing       EventType = "workspace.closing"
	EventSessionCreated         EventType = "session.created"
	EventSessionUpdated         EventType = "session.updated"
	EventTurnStarted            EventType = "turn.started"
	EventTurnCompleted          EventType = "turn.completed"
	EventTurnFailed             EventType = "turn.failed"
	EventTurnCancelled          EventType = "turn.cancelled"
	EventTurnInterrupted        EventType = "turn.interrupted"
	EventUserMessage            EventType = "user.message"
	EventAssistantPartStarted   EventType = "assistant.part.started"
	EventAssistantDelta         EventType = "assistant.delta"
	EventAssistantPartCompleted EventType = "assistant.part.completed"
	EventReasoningStarted       EventType = "reasoning.started"
	EventReasoningDelta         EventType = "reasoning.delta"
	EventReasoningCompleted     EventType = "reasoning.completed"
	EventToolStarted            EventType = "tool.started"
	EventToolCompleted          EventType = "tool.completed"
	EventToolFailed             EventType = "tool.failed"
	EventTaskUpdated            EventType = "task.updated"
	EventQuestionRequested      EventType = "question.requested"
	EventQuestionResolved       EventType = "question.resolved"
	EventPermissionRequested    EventType = "permission.requested"
	EventPermissionResolved     EventType = "permission.resolved"
	EventInteractionExpired     EventType = "interaction.expired"
	EventQueueUpdated           EventType = "queue.updated"
	EventSystemMessage          EventType = "system.message"
	EventResetRequired          EventType = "event.reset_required"
)

type EventCursor struct {
	StreamID string `json:"stream_id"`
	Sequence uint64 `json:"sequence"`
}

type AppEvent struct {
	SchemaVersion uint16          `json:"schema_version"`
	StreamID      string          `json:"stream_id"`
	Sequence      uint64          `json:"sequence"`
	WorkspaceID   WorkspaceID     `json:"workspace_id"`
	SessionID     string          `json:"session_id,omitempty"`
	TurnID        string          `json:"turn_id,omitempty"`
	Type          EventType       `json:"type"`
	Time          time.Time       `json:"time"`
	EntityVersion uint64          `json:"entity_version,omitempty"`
	Payload       json.RawMessage `json:"payload"`
}

func NewAppEvent(workspaceID WorkspaceID, sessionID, turnID string, eventType EventType, at time.Time, entityVersion uint64, payload any) (AppEvent, error) {
	if workspaceID == "" {
		return AppEvent{}, fmt.Errorf("workspace ID is required")
	}
	if eventType == "" {
		return AppEvent{}, fmt.Errorf("event type is required")
	}
	if at.IsZero() {
		at = time.Now().UTC()
	} else {
		at = at.UTC()
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return AppEvent{}, fmt.Errorf("encode %s payload: %w", eventType, err)
	}
	return AppEvent{
		SchemaVersion: AppEventSchemaVersion,
		WorkspaceID:   workspaceID, SessionID: sessionID, TurnID: turnID,
		Type: eventType, Time: at, EntityVersion: entityVersion, Payload: raw,
	}, nil
}

type AssistantDeltaPayload struct {
	PartID string `json:"part_id"`
	Offset int    `json:"offset"`
	Text   string `json:"text"`
}

type ToolCompletedPayload struct {
	ToolUseID     string    `json:"tool_use_id"`
	Name          string    `json:"name"`
	ResultSummary string    `json:"result_summary"`
	DetailID      string    `json:"detail_id,omitempty"`
	FinishedAt    time.Time `json:"finished_at"`
	DurationMS    int64     `json:"duration_ms"`
}

type QuestionOptionPayload struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

type QuestionRequestedPayload struct {
	RequestID string                  `json:"request_id"`
	Prompt    string                  `json:"prompt"`
	Mode      string                  `json:"mode"`
	Options   []QuestionOptionPayload `json:"options"`
	CreatedAt time.Time               `json:"created_at"`
}

type PermissionRequestedPayload struct {
	RequestID       string    `json:"request_id"`
	Operation       string    `json:"operation"`
	CanonicalTarget string    `json:"canonical_target"`
	CreatedAt       time.Time `json:"created_at"`
}

type ResetRequiredPayload struct {
	Reason          string `json:"reason"`
	CurrentStreamID string `json:"current_stream_id"`
	LatestSequence  uint64 `json:"latest_sequence"`
}
