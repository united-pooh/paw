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
	// InputTokens/OutputTokens 记录本轮相对会话累计的增量用量，仅用于
	// 展示（消息页脚），绝不参与模型历史或上下文统计。
	InputTokens  int `json:"input_tokens,omitempty"`
	OutputTokens int `json:"output_tokens,omitempty"`
}

// TurnMetadataStore is an optional session capability. Keeping it separate
// from Store preserves compatibility with existing history-only fakes.
type TurnMetadataStore interface {
	AppendTurnMetadata(ctx context.Context, sessionID string, metadata TurnMetadata) error
	LoadTurnMetadata(ctx context.Context, sessionID string) ([]TurnMetadata, error)
}
