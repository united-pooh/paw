package loop

import (
	"time"

	"codex-agent-go/internal/message"
	"codex-agent-go/internal/model"
)

// SessionEventKind is a discriminator for SessionEvent payload.
type SessionEventKind string

const (
	EventKindHistoryMessage  SessionEventKind = "history_message"
	EventKindUsageUpdate     SessionEventKind = "usage_update"
	EventKindHistoryReset    SessionEventKind = "history_reset"
	EventKindSupplementAdded SessionEventKind = "supplement_added"
)

// SessionEvent is a single immutable fact recorded for a session.
// Exactly one payload field is non-nil per event (except EventKindHistoryReset
// which carries no payload).
type SessionEvent struct {
	ID        string           `json:"id"`
	SessionID string           `json:"session_id"`
	Seq       int64            `json:"seq"`
	Kind      SessionEventKind `json:"kind"`
	CreatedAt time.Time        `json:"created_at"`

	// Payload fields — only one non-nil per event.
	Message    *message.Message          `json:"message,omitempty"`
	Usage      *SessionUsagePayload      `json:"usage,omitempty"`
	Supplement *SessionSupplementPayload `json:"supplement,omitempty"`
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
