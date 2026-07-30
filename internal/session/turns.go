package session

import (
	"context"
	"time"
)

// TurnStatus is the terminal state recorded for one foreground model turn.
type TurnStatus string

const (
	TurnStatusCompleted TurnStatus = "completed"
	TurnStatusFailed    TurnStatus = "failed"
	TurnStatusStopped   TurnStatus = "stopped"
)

// TurnMetadata is persisted beside the message transcript. It is display
// metadata only and must never be included in the model history.
type TurnMetadata struct {
	TurnID       string     `json:"turn_id"`
	AssistantSeq *int64     `json:"assistant_seq,omitempty"`
	StartedAt    time.Time  `json:"started_at"`
	ResponseAt   *time.Time `json:"response_at,omitempty"`
	DurationMS   int64      `json:"duration_ms"`
	Status       TurnStatus `json:"status"`
}

// TurnMetadataStore is an optional session capability. Keeping it separate
// from Store preserves compatibility with existing history-only fakes.
type TurnMetadataStore interface {
	AppendTurnMetadata(ctx context.Context, sessionID string, metadata TurnMetadata) error
	LoadTurnMetadata(ctx context.Context, sessionID string) ([]TurnMetadata, error)
}
