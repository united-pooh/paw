package loop

import (
	"encoding/json"
	"time"

	"codex-agent-go/internal/message"
	"codex-agent-go/internal/model"
)

// SessionEventKind is a discriminator for SessionEvent payload.
type SessionEventKind string

const (
	// --- History / state events (used by projections) ---

	// EventKindHistoryMessage records a committed turn message (user/assistant/tool_result).
	EventKindHistoryMessage SessionEventKind = "history_message"
	// EventKindUsageUpdate records a token-usage snapshot after a model response.
	EventKindUsageUpdate SessionEventKind = "usage_update"
	// EventKindHistoryReset records that the conversation history was cleared.
	EventKindHistoryReset SessionEventKind = "history_reset"
	// EventKindSupplementAdded records an injected supplement text.
	EventKindSupplementAdded SessionEventKind = "supplement_added"

	// --- Intra-turn audit events (true event path; not used by projections) ---

	// EventKindUserInput records the raw user input that started a turn.
	EventKindUserInput SessionEventKind = "user_input"
	// EventKindAssistantDelta records the assistant's full streamed text, emitted
	// once per turn when the stream completes (not per streaming chunk).
	EventKindAssistantDelta SessionEventKind = "assistant_delta"
	// EventKindToolCallFired records a single tool call dispatched during a turn.
	EventKindToolCallFired SessionEventKind = "tool_call_fired"
	// EventKindToolResult records the result returned for a tool call.
	EventKindToolResult SessionEventKind = "tool_result"
	// EventKindTurnCommitted records that a full turn was committed to history.
	EventKindTurnCommitted SessionEventKind = "turn_committed"
	// EventKindSystemMessage records a runtime/system notification.
	EventKindSystemMessage SessionEventKind = "system_message"
	// EventKindDeltaChunk records a single streaming text chunk from the assistant,
	// emitted per chunk during streaming (not once per turn like EventKindAssistantDelta).
	EventKindDeltaChunk SessionEventKind = "delta_chunk"
	// EventKindSubagentsSnapshot pushes a full snapshot of all persona slots to the client.
	EventKindSubagentsSnapshot SessionEventKind = "subagents_snapshot"
)

// SessionEvent is a single immutable fact recorded for a session.
// Exactly one payload field is non-nil per event (except EventKindHistoryReset
// and EventKindTurnCommitted which carry no payload, or a minimal one).
type SessionEvent struct {
	ID        string           `json:"id"`
	SessionID string           `json:"session_id"`
	Seq       int64            `json:"seq"`
	Kind      SessionEventKind `json:"kind"`
	CreatedAt time.Time        `json:"created_at"`

	// Payload fields — only one non-nil per event.
	Message           *message.Message                 `json:"message,omitempty"`
	Usage             *SessionUsagePayload             `json:"usage,omitempty"`
	Supplement        *SessionSupplementPayload        `json:"supplement,omitempty"`
	UserInput         *SessionUserInputPayload         `json:"user_input,omitempty"`
	AssistantDelta    *SessionAssistantDeltaPayload    `json:"assistant_delta,omitempty"`
	ToolCall          *SessionToolCallPayload          `json:"tool_call,omitempty"`
	ToolResult        *SessionToolResultPayload        `json:"tool_result,omitempty"`
	TurnCommit        *SessionTurnCommitPayload        `json:"turn_commit,omitempty"`
	SystemMessage     *SessionSystemMessagePayload     `json:"system_message,omitempty"`
	DeltaChunk        *SessionDeltaChunkPayload        `json:"delta_chunk,omitempty"`
	SubagentsSnapshot *SessionSubagentsSnapshotPayload `json:"subagents_snapshot,omitempty"`
}

// SessionUsagePayload carries token-usage data.
type SessionUsagePayload struct {
	Usage     model.Usage `json:"usage"`
	IsSession bool        `json:"is_session"` // true = session cumulative, false = turn current
}

// SessionSupplementPayload carries an injected supplement text.
type SessionSupplementPayload struct {
	Text string `json:"text"`
}

// SessionUserInputPayload carries the raw user input that started a turn.
type SessionUserInputPayload struct {
	Text          string `json:"text"`
	TargetAgentID string `json:"target_agent_id,omitempty"`
}

// SessionAssistantDeltaPayload carries the full streamed assistant text for one turn.
// Emitted once per turn when the stream completes, not per streaming chunk.
type SessionAssistantDeltaPayload struct {
	Text string `json:"text"`
}

// SessionToolCallPayload carries a single tool call dispatched during a turn.
type SessionToolCallPayload struct {
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input,omitempty"`
}

// SessionToolResultPayload carries the result returned for a tool call.
type SessionToolResultPayload struct {
	ToolUseID string `json:"tool_use_id"`
	Name      string `json:"name"`
	Content   string `json:"content"`
	IsError   bool   `json:"is_error,omitempty"`
}

// SessionTurnCommitPayload records metadata about a committed turn.
type SessionTurnCommitPayload struct {
	// MessageCount is the number of new messages committed in this turn.
	MessageCount int `json:"message_count"`
}

// SessionSystemMessagePayload carries system/runtime notifications.
type SessionSystemMessagePayload struct {
	Title     string `json:"title"`
	Body      string `json:"body"`
	Color     string `json:"color,omitempty"`
	TaskID    string `json:"task_id,omitempty"`
	AgentID   string `json:"agent_id,omitempty"`
	AgentName string `json:"agent_name,omitempty"`
	Status    string `json:"status,omitempty"`
	IsError   bool   `json:"is_error,omitempty"`
}

// SessionDeltaChunkPayload carries a single streaming text chunk from the assistant.
// Unlike SessionAssistantDeltaPayload (emitted once per completed turn), this is
// emitted once per streaming chunk for real-time display.
type SessionDeltaChunkPayload struct {
	Text string `json:"text"`
}

// AgentInfo is the state of a single persona slot in a subagents_snapshot event.
type AgentInfo struct {
	ID                    string     `json:"id"`
	Name                  string     `json:"name"`
	Color                 string     `json:"color"`
	Status                string     `json:"status"` // "idle" | "pending" | "running" | "done" | "failed" | "stopped"
	TaskID                string     `json:"task_id,omitempty"`
	StartedAt             *time.Time `json:"started_at,omitempty"`
	FinishedAt            *time.Time `json:"finished_at,omitempty"`
	ConversationAvailable bool       `json:"conversation_available,omitempty"`
}

// SessionSubagentsSnapshotPayload is the payload for EventKindSubagentsSnapshot.
type SessionSubagentsSnapshotPayload struct {
	Agents []AgentInfo `json:"agents"`
}
