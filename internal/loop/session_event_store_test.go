package loop

import (
	"context"
	"testing"

	"codex-agent-go/internal/message"
)

func TestInMemorySessionEventStore_AppendAndLoad(t *testing.T) {
	store := NewInMemorySessionEventStore()
	ctx := context.Background()
	sessionID := "test-session-1"

	msg1 := message.Message{Role: message.RoleUser, Content: "hello"}
	msg2 := message.Message{Role: message.RoleAssistant, Content: "world"}
	msg3 := message.Message{Role: message.RoleUser, Content: "again"}

	events := []SessionEvent{
		{Kind: EventKindHistoryMessage, Message: &msg1},
		{Kind: EventKindHistoryMessage, Message: &msg2},
		{Kind: EventKindHistoryMessage, Message: &msg3},
	}

	if err := store.Append(ctx, sessionID, events...); err != nil {
		t.Fatalf("Append failed: %v", err)
	}

	loaded, err := store.Load(ctx, sessionID)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if len(loaded) != 3 {
		t.Fatalf("expected 3 events, got %d", len(loaded))
	}
	for i, ev := range loaded {
		if ev.Kind != EventKindHistoryMessage {
			t.Errorf("event[%d] wrong kind: %v", i, ev.Kind)
		}
	}
	if loaded[0].Message.Content != "hello" {
		t.Errorf("event[0] wrong content: %q", loaded[0].Message.Content)
	}
	if loaded[1].Message.Content != "world" {
		t.Errorf("event[1] wrong content: %q", loaded[1].Message.Content)
	}
	if loaded[2].Message.Content != "again" {
		t.Errorf("event[2] wrong content: %q", loaded[2].Message.Content)
	}
}

func TestInMemorySessionEventStore_EmptyLoad(t *testing.T) {
	store := NewInMemorySessionEventStore()
	ctx := context.Background()

	events, err := store.Load(ctx, "no-such-session")
	if err != nil {
		t.Fatalf("Load returned unexpected error: %v", err)
	}
	if len(events) != 0 {
		t.Errorf("expected empty slice, got %d events", len(events))
	}
}

func TestInMemorySessionEventStore_MultipleSessionsIsolated(t *testing.T) {
	store := NewInMemorySessionEventStore()
	ctx := context.Background()

	msgA := message.Message{Role: message.RoleUser, Content: "session-A"}
	msgB := message.Message{Role: message.RoleUser, Content: "session-B"}

	if err := store.Append(ctx, "session-A", SessionEvent{Kind: EventKindHistoryMessage, Message: &msgA}); err != nil {
		t.Fatalf("Append session-A failed: %v", err)
	}
	if err := store.Append(ctx, "session-B", SessionEvent{Kind: EventKindHistoryMessage, Message: &msgB}); err != nil {
		t.Fatalf("Append session-B failed: %v", err)
	}

	eventsA, err := store.Load(ctx, "session-A")
	if err != nil {
		t.Fatalf("Load session-A failed: %v", err)
	}
	if len(eventsA) != 1 {
		t.Fatalf("expected 1 event for session-A, got %d", len(eventsA))
	}
	if eventsA[0].Message.Content != "session-A" {
		t.Errorf("session-A event has wrong content: %q", eventsA[0].Message.Content)
	}

	eventsB, err := store.Load(ctx, "session-B")
	if err != nil {
		t.Fatalf("Load session-B failed: %v", err)
	}
	if len(eventsB) != 1 {
		t.Fatalf("expected 1 event for session-B, got %d", len(eventsB))
	}
	if eventsB[0].Message.Content != "session-B" {
		t.Errorf("session-B event has wrong content: %q", eventsB[0].Message.Content)
	}
}

func TestJSONLSessionEventStore_AppendAndLoad(t *testing.T) {
	dir := t.TempDir()
	store := NewJSONLSessionEventStore(dir)
	ctx := context.Background()
	sessionID := "jsonl-session"

	msg1 := message.Message{Role: message.RoleUser, Content: "first"}
	msg2 := message.Message{Role: message.RoleAssistant, Content: "second"}

	events := []SessionEvent{
		{ID: "e1", SessionID: sessionID, Kind: EventKindHistoryMessage, Message: &msg1},
		{ID: "e2", SessionID: sessionID, Kind: EventKindHistoryMessage, Message: &msg2},
	}

	if err := store.Append(ctx, sessionID, events...); err != nil {
		t.Fatalf("Append failed: %v", err)
	}

	loaded, err := store.Load(ctx, sessionID)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if len(loaded) != 2 {
		t.Fatalf("expected 2 events, got %d", len(loaded))
	}
	if loaded[0].ID != "e1" {
		t.Errorf("event[0] ID mismatch: got %q", loaded[0].ID)
	}
	if loaded[1].ID != "e2" {
		t.Errorf("event[1] ID mismatch: got %q", loaded[1].ID)
	}
	if loaded[0].Message == nil || loaded[0].Message.Content != "first" {
		t.Errorf("event[0] content mismatch")
	}
	if loaded[1].Message == nil || loaded[1].Message.Content != "second" {
		t.Errorf("event[1] content mismatch")
	}
}

func TestJSONLSessionEventStore_EmptyLoad(t *testing.T) {
	dir := t.TempDir()
	store := NewJSONLSessionEventStore(dir)
	ctx := context.Background()

	events, err := store.Load(ctx, "nonexistent-session")
	if err != nil {
		t.Fatalf("Load on missing file returned error: %v", err)
	}
	if len(events) != 0 {
		t.Errorf("expected empty result, got %d events", len(events))
	}
}
