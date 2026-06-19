package session

import (
	"context"
	"codex-agent-go/internal/message"
	"time"
)

type Meta struct {
	SessionID       string    `json:"session_id"`
	ParentSessionID string    `json:"parent_session_id,omitempty"`
	ForkFromSeq     int64     `json:"fork_from_seq,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
}

type CreateRootRequest struct {
	// 创建请求的约束性质数据结构
	SessionID string
}

type ForkRequest struct {
	// 同约束性质数据结构
	SessionID       string
	ParentSessionID string
	ForkFromSeq     int64
}

type Store interface {
	CreateRoot(ctx context.Context, request CreateRootRequest) (Meta, error)
	Fork(ctx context.Context, request ForkRequest) (Meta, error)
	GetMeta(ctx context.Context, sessionID string) (Meta, error)
	Exists(ctx context.Context, sessionID string) (bool, error)
	Append(ctx context.Context, sessionID string, msgs ...message.Message) error
	LoadResolvedHistory(ctx context.Context, sessionID string) ([]message.Message, error)
}
