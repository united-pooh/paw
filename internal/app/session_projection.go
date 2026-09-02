package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"paw/internal/message"
	"paw/internal/session"
)

type SessionSummary struct {
	SessionID      string    `json:"session_id"`
	CreatedAt      time.Time `json:"created_at"`
	LastUsedAt     time.Time `json:"last_used_at"`
	Title          string    `json:"title,omitempty"`
	TranscriptSize int64     `json:"transcript_size"`
}

type TurnProjection struct {
	TurnID     string            `json:"turn_id"`
	Messages   []message.Message `json:"messages"`
	StartedAt  time.Time         `json:"started_at,omitempty"`
	ResponseAt *time.Time        `json:"response_at,omitempty"`
	DurationMS int64             `json:"duration_ms,omitempty"`
	Status     string            `json:"status,omitempty"`
	Error      string            `json:"error,omitempty"`
	// InputTokens/OutputTokens 为本轮展示用 token 增量（来自 turn sidecar）。
	InputTokens  int `json:"input_tokens,omitempty"`
	OutputTokens int `json:"output_tokens,omitempty"`
}

type SessionProjection struct {
	SessionID      string                 `json:"session_id"`
	Meta           session.Meta           `json:"meta"`
	SessionVersion uint64                 `json:"session_version"`
	Turns          []TurnProjection       `json:"turns"`
	EarlierCursor  string                 `json:"earlier_cursor,omitempty"`
	Recovery       *session.RecoveryState `json:"recovery,omitempty"`
	ActiveTurnID   string                 `json:"active_turn_id,omitempty"`
	Queue          []InputDraft           `json:"queue,omitempty"`
	Pending        []InteractionState     `json:"pending,omitempty"`
}

type turnPageCursor struct {
	Before int `json:"before"`
}

func projectSession(ctx context.Context, store *session.JSONLStore, coordinator *WorkspaceCoordinator, sessionID string, request SnapshotRequest) (SessionProjection, error) {
	if sessionID == "" {
		return SessionProjection{}, fmt.Errorf("session ID is required")
	}
	meta, err := store.GetMeta(ctx, sessionID)
	if err != nil {
		return SessionProjection{}, err
	}
	records, err := store.LoadResolvedJournalRecords(ctx, sessionID)
	if err != nil {
		return SessionProjection{}, err
	}
	metadata, err := store.LoadTurnMetadata(ctx, sessionID)
	if err != nil {
		return SessionProjection{}, err
	}
	turns := projectTurns(records, metadata)
	limit := request.Limit
	if limit <= 0 {
		limit = 30
	}
	if limit > 100 {
		limit = 100
	}
	end := len(turns)
	if strings.TrimSpace(request.Before) != "" {
		cursor, err := decodeTurnPageCursor(request.Before)
		if err != nil {
			return SessionProjection{}, err
		}
		if cursor.Before < end {
			end = cursor.Before
		}
	}
	start := end - limit
	if start < 0 {
		start = 0
	}
	projection := SessionProjection{SessionID: sessionID, Meta: meta, Turns: append([]TurnProjection(nil), turns[start:end]...)}
	if start > 0 {
		projection.EarlierCursor, err = encodeTurnPageCursor(start)
		if err != nil {
			return SessionProjection{}, err
		}
	}
	if snapshot, snapshotErr := store.LoadSnapshot(ctx, sessionID); snapshotErr == nil {
		projection.Recovery = snapshot.Recovery
	}
	if coordinator != nil {
		state := coordinator.WorkspaceSnapshot()
		sessionState := coordinator.SessionSnapshot(sessionID)
		projection.SessionVersion = sessionState.SessionVersion
		projection.Queue = sessionState.Queue
		projection.Pending = sessionState.Pending
		if state.ActiveSessionID == sessionID {
			projection.ActiveTurnID = state.ActiveTurnID
		}
	}
	return projection, nil
}

func projectTurns(records []session.Record, metadata []session.TurnMetadata) []TurnProjection {
	byID := make(map[string]*TurnProjection)
	order := make([]string, 0)
	ensure := func(turnID string) *TurnProjection {
		turn := byID[turnID]
		if turn == nil {
			turn = &TurnProjection{TurnID: turnID}
			byID[turnID] = turn
			order = append(order, turnID)
		}
		return turn
	}
	for _, record := range records {
		turnID := strings.TrimSpace(record.TurnID)
		if turnID == "" {
			// receipt 等管理记录没有 turn 归属，不属于对话投影。
			continue
		}
		turn := ensure(turnID)
		switch record.Kind {
		case session.JournalTurnStarted:
			turn.Status = "running"
			if turn.StartedAt.IsZero() {
				turn.StartedAt = record.CreatedAt
			}
		case "", session.JournalMessage, session.JournalAssistant, session.JournalAssistantPartial:
			turn.Messages = append(turn.Messages, message.CloneMessage(record.Message))
		case session.JournalToolResult:
			if record.ToolResult != nil {
				result := *record.ToolResult
				turn.Messages = append(turn.Messages, message.Message{Role: message.RoleUser, ToolResult: &result})
			}
		case session.JournalTurnCompleted:
			turn.Status = "completed"
		case session.JournalTurnFailed:
			turn.Status = "failed"
			turn.Error = record.Error
		case session.JournalTurnStopped:
			turn.Status = "cancelled"
			turn.Error = record.Error
		}
	}
	for _, item := range metadata {
		turn := ensure(item.TurnID)
		turn.StartedAt = item.StartedAt
		turn.ResponseAt = item.ResponseAt
		turn.DurationMS = item.DurationMS
		turn.InputTokens = item.InputTokens
		turn.OutputTokens = item.OutputTokens
		if item.Status != "" {
			turn.Status = string(item.Status)
		}
	}
	turns := make([]TurnProjection, 0, len(order))
	seen := make(map[string]bool, len(order))
	for _, turnID := range order {
		if seen[turnID] {
			continue
		}
		seen[turnID] = true
		turns = append(turns, *byID[turnID])
	}
	sort.SliceStable(turns, func(i, j int) bool {
		if turns[i].StartedAt.IsZero() || turns[j].StartedAt.IsZero() {
			return false
		}
		return turns[i].StartedAt.Before(turns[j].StartedAt)
	})
	return turns
}

func encodeTurnPageCursor(before int) (string, error) {
	data, err := json.Marshal(turnPageCursor{Before: before})
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func decodeTurnPageCursor(cursor string) (turnPageCursor, error) {
	data, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(cursor))
	if err != nil {
		return turnPageCursor{}, fmt.Errorf("invalid turn cursor: %w", err)
	}
	var decoded turnPageCursor
	if err := json.Unmarshal(data, &decoded); err != nil || decoded.Before < 0 {
		return turnPageCursor{}, fmt.Errorf("invalid turn cursor")
	}
	return decoded, nil
}
