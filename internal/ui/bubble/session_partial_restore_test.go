package bubble

import (
	"context"
	"errors"
	"strings"
	"testing"

	"paw/internal/loop"
	"paw/internal/message"
	"paw/internal/session"
)

func TestSessionRestoreShowsPartialAssistantPlainAndRecoveryError(t *testing.T) {
	ctx := context.Background()
	store, err := session.NewJSONLStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	turnID := "turn-partial"
	if err := store.BeginTurn(ctx, "s1", turnID, message.Message{Role: message.RoleUser, Content: "hello"}); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendPartialAssistant(ctx, "s1", turnID, message.Message{Role: message.RoleAssistant, Content: "partial **markdown"}); err != nil {
		t.Fatal(err)
	}
	if err := store.FailTurn(ctx, "s1", turnID, errors.New("stream truncated")); err != nil {
		t.Fatal(err)
	}

	msg, ok := loadSessionHistoryCmd(ctx, &partialRestoreRunner{store: store}, store, "s1")().(sessionRestoredMsg)
	if !ok {
		t.Fatalf("restore cmd returned %#v, want sessionRestoredMsg", msg)
	}
	if msg.err != nil {
		t.Fatalf("restore error = %v", msg.err)
	}
	var partial *transcriptEntry
	var recovery *transcriptEntry
	for index := range msg.entries {
		entry := &msg.entries[index]
		if entry.kind == entryAssistant && entry.body == "partial **markdown" {
			partial = entry
		}
		if entry.kind == entryError && entry.title == "recovery" {
			recovery = entry
		}
	}
	if partial == nil {
		t.Fatalf("entries=%#v, want restored partial assistant", msg.entries)
	}
	if partial.renderMode != transcriptRenderPlain {
		t.Fatalf("partial renderMode=%v, want plain", partial.renderMode)
	}
	if recovery == nil || !strings.Contains(recovery.body, "stream truncated") {
		t.Fatalf("recovery entry=%#v, want visible recovery error", recovery)
	}
}

type partialRestoreRunner struct {
	store *session.JSONLStore
}

func (r *partialRestoreRunner) RunTurn(context.Context, string) (message.Message, error) {
	return message.Message{}, nil
}

func (r *partialRestoreRunner) ResetHistory() {}

func (r *partialRestoreRunner) LoadHistory(ctx context.Context, sessionID string) ([]message.Message, error) {
	return r.store.LoadResolvedHistory(ctx, sessionID)
}

func (r *partialRestoreRunner) LoadSession(ctx context.Context, sessionID string) (loop.SessionLoadResult, error) {
	snapshot, err := r.store.LoadSnapshot(ctx, sessionID)
	if err != nil {
		return loop.SessionLoadResult{}, err
	}
	return loop.SessionLoadResult{
		Messages: append([]message.Message(nil), snapshot.Messages...),
		Recovery: snapshot.Recovery,
	}, nil
}
