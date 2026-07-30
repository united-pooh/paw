package loop

import (
	"context"
	"fmt"
	"paw/internal/message"
	"paw/internal/session"
	"time"
)

// TurnTiming carries the timestamp captured when a queued turn actually
// starts executing. It is intentionally not part of message history.
type TurnTiming struct {
	TurnID    string
	StartedAt time.Time
}

// TurnExecution contains the assistant result plus display-only metadata.
// MetadataPersistErr is non-fatal: the answer remains successful and the UI
// can show the footer from this in-memory value while reporting the sidecar
// failure separately.
type TurnExecution struct {
	Message            message.Message
	Metadata           session.TurnMetadata
	MetadataPersistErr error
}

// SequencedHistoryStore is an optional extension implemented by the JSONL
// session store. Legacy history fakes continue to use HistoryStore.Append.
type SequencedHistoryStore interface {
	AppendWithSequences(ctx context.Context, sessionID string, msgs ...message.Message) (firstSeq, lastSeq int64, err error)
}

type sequencedTurnJournal interface {
	AppendAssistantWithSequence(ctx context.Context, sessionID, turnID string, msg message.Message) (int64, error)
}

// TimedRunner is the optional UI-facing capability for recording one turn's
// lifecycle without changing the existing Runner interface.
type TimedRunner interface {
	RunTurnWithTiming(ctx context.Context, input, turnID string, startedAt time.Time) (TurnExecution, error)
	RunRichTurnWithTiming(ctx context.Context, input message.Message, turnID string, startedAt time.Time) (TurnExecution, error)
}

func (runner *Runner) RunTurnWithTiming(ctx context.Context, input, turnID string, startedAt time.Time) (TurnExecution, error) {
	return runner.runTurnWithTiming(ctx, message.Message{Role: message.RoleUser, Content: input}, &TurnTiming{
		TurnID:    turnID,
		StartedAt: startedAt,
	})
}

func (runner *Runner) RunRichTurnWithTiming(ctx context.Context, input message.Message, turnID string, startedAt time.Time) (TurnExecution, error) {
	if input.Role == "" {
		input.Role = message.RoleUser
	}
	if input.Role != message.RoleUser {
		return TurnExecution{}, fmt.Errorf("rich turn 必须使用 user role")
	}
	return runner.runTurnWithTiming(ctx, input, &TurnTiming{
		TurnID:    turnID,
		StartedAt: startedAt,
	})
}

func (runner *Runner) now() time.Time {
	if runner != nil && runner.nowFn != nil {
		return runner.nowFn()
	}
	return time.Now()
}

func (runner *Runner) completeTurnExecution(ctx context.Context, timing *TurnTiming, assistant message.Message, assistantSeq int64) TurnExecution {
	execution := TurnExecution{Message: assistant}
	if timing == nil {
		return execution
	}
	startedAt := timing.StartedAt
	if startedAt.IsZero() {
		startedAt = runner.now()
	}
	responseAt := runner.now().UTC()
	duration := responseAt.Sub(startedAt)
	if duration < 0 {
		duration = 0
	}
	metadata := session.TurnMetadata{
		TurnID:     timing.TurnID,
		StartedAt:  startedAt.UTC(),
		ResponseAt: &responseAt,
		DurationMS: duration.Milliseconds(),
		Status:     session.TurnStatusCompleted,
	}
	if assistantSeq >= 0 {
		seq := assistantSeq
		metadata.AssistantSeq = &seq
	}
	execution.Metadata = metadata
	if metadataStore, ok := runner.store.(session.TurnMetadataStore); ok {
		execution.MetadataPersistErr = persistTurnMetadata(ctx, metadataStore, runner.sessionID, metadata)
	}
	return execution
}

func persistTurnMetadata(ctx context.Context, store session.TurnMetadataStore, sessionID string, metadata session.TurnMetadata) error {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if err := ctx.Err(); err != nil {
			lastErr = err
			break
		}
		if err := store.AppendTurnMetadata(ctx, sessionID, metadata); err == nil {
			return nil
		} else {
			lastErr = err
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("metadata write exhausted retries")
	}
	return fmt.Errorf("保存 turn metadata 失败: %w", lastErr)
}

// LoadTurnMetadata is used only by transcript restoration. The sidecar is
// deliberately never folded into LoadHistory or model prompt construction.
func (runner *Runner) LoadTurnMetadata(ctx context.Context, sessionID string) ([]session.TurnMetadata, error) {
	if runner == nil || runner.store == nil {
		return nil, nil
	}
	metadataStore, ok := runner.store.(session.TurnMetadataStore)
	if !ok {
		return nil, nil
	}
	return metadataStore.LoadTurnMetadata(ctx, sessionID)
}
