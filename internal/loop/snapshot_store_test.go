package loop

import (
	"context"
	"testing"

	"codex-agent-go/internal/message"
	"codex-agent-go/internal/model"
)

func TestInMemorySnapshotStore_SaveAndLoad(t *testing.T) {
	store := NewInMemorySnapshotStore()
	ctx := context.Background()

	snap := SessionSnapshot{
		SessionID:  "snap-session",
		LastSeq:    42,
		History:    []message.Message{{Role: message.RoleUser, Content: "hello"}},
		Usage:      model.Usage{InputTokens: 100, OutputTokens: 50},
		UsageKnown: true,
		SessionUsage: model.Usage{
			PromptTokens:     200,
			CompletionTokens: 80,
		},
		SessionUsageKnown: true,
		Supplements:       []string{"extra context"},
	}

	if err := store.Save(ctx, snap); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	loaded, ok, err := store.Load(ctx, "snap-session")
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if !ok {
		t.Fatal("expected snapshot to be found, ok=false")
	}

	if loaded.SessionID != snap.SessionID {
		t.Errorf("SessionID mismatch: got %q, want %q", loaded.SessionID, snap.SessionID)
	}
	if loaded.LastSeq != snap.LastSeq {
		t.Errorf("LastSeq mismatch: got %d, want %d", loaded.LastSeq, snap.LastSeq)
	}
	if len(loaded.History) != 1 {
		t.Fatalf("expected 1 history message, got %d", len(loaded.History))
	}
	if loaded.History[0].Content != "hello" {
		t.Errorf("History[0].Content mismatch: got %q", loaded.History[0].Content)
	}
	if loaded.Usage.InputTokens != 100 {
		t.Errorf("Usage.InputTokens mismatch: got %d", loaded.Usage.InputTokens)
	}
	if !loaded.UsageKnown {
		t.Error("expected UsageKnown=true")
	}
	if loaded.SessionUsage.PromptTokens != 200 {
		t.Errorf("SessionUsage.PromptTokens mismatch: got %d", loaded.SessionUsage.PromptTokens)
	}
	if !loaded.SessionUsageKnown {
		t.Error("expected SessionUsageKnown=true")
	}
	if len(loaded.Supplements) != 1 || loaded.Supplements[0] != "extra context" {
		t.Errorf("Supplements mismatch: got %v", loaded.Supplements)
	}
}

func TestInMemorySnapshotStore_LoadMissing(t *testing.T) {
	store := NewInMemorySnapshotStore()
	ctx := context.Background()

	snap, ok, err := store.Load(ctx, "nonexistent")
	if err != nil {
		t.Fatalf("Load returned unexpected error: %v", err)
	}
	if ok {
		t.Error("expected ok=false for missing session")
	}
	// snap should be zero value
	if snap.SessionID != "" || snap.LastSeq != 0 || len(snap.History) != 0 {
		t.Errorf("expected zero-value snapshot, got: %+v", snap)
	}
}

func TestInMemorySnapshotStore_OverwriteExisting(t *testing.T) {
	store := NewInMemorySnapshotStore()
	ctx := context.Background()

	snap1 := SessionSnapshot{
		SessionID: "overwrite-session",
		LastSeq:   10,
	}
	snap2 := SessionSnapshot{
		SessionID: "overwrite-session",
		LastSeq:   20,
		History:   []message.Message{{Role: message.RoleUser, Content: "updated"}},
	}

	if err := store.Save(ctx, snap1); err != nil {
		t.Fatalf("Save snap1 failed: %v", err)
	}
	if err := store.Save(ctx, snap2); err != nil {
		t.Fatalf("Save snap2 failed: %v", err)
	}

	loaded, ok, err := store.Load(ctx, "overwrite-session")
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if !ok {
		t.Fatal("expected snapshot to be found")
	}
	if loaded.LastSeq != 20 {
		t.Errorf("expected LastSeq=20 after overwrite, got %d", loaded.LastSeq)
	}
	if len(loaded.History) != 1 || loaded.History[0].Content != "updated" {
		t.Errorf("expected updated history after overwrite, got %v", loaded.History)
	}
}
