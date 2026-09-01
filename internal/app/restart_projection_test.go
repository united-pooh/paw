package app

import (
	"context"
	"testing"

	"paw/internal/message"
	"paw/internal/session"
)

func TestRestartMarksUnfinishedTurnsInterrupted(t *testing.T) {
	ctx := context.Background()
	store, err := session.NewJSONLStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	workspace := mustCanonicalWorkspace(t, t.TempDir())
	hub, err := NewEventHub(EventHubConfig{WorkspaceID: workspace.ID, StreamID: "stream-test"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = hub.Close() })

	if _, err := store.CreateRoot(ctx, session.CreateRootRequest{SessionID: "s1"}); err != nil {
		t.Fatal(err)
	}
	if err := store.BeginTurn(ctx, "s1", "turn-done", message.Message{Role: message.RoleUser, Content: "ok"}); err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteTurn(ctx, "s1", "turn-done"); err != nil {
		t.Fatal(err)
	}
	if err := store.BeginTurn(ctx, "s1", "turn-lost", message.Message{Role: message.RoleUser, Content: "lost"}); err != nil {
		t.Fatal(err)
	}

	coordinator := NewWorkspaceCoordinator()
	if _, err := coordinator.BeginTurn("s1", "turn-lost"); err != nil {
		t.Fatal(err)
	}
	records, err := store.LoadResolvedJournalRecords(ctx, "s1")
	if err != nil {
		t.Fatal(err)
	}
	restoreUnfinishedTurns("s1", records, coordinator, hub, workspace.ID)

	state := coordinator.WorkspaceSnapshot()
	if state.ActiveTurnID != "" || state.ActiveSessionID != "" {
		t.Fatalf("interrupted turn still active: %#v", state)
	}
	subscription, err := hub.Subscribe(EventCursor{StreamID: "stream-test"})
	if err != nil {
		t.Fatal(err)
	}
	defer subscription.Close()
	interrupted := false
	for _, event := range subscription.Replay {
		if event.Type == EventTurnInterrupted && event.TurnID == "turn-lost" {
			interrupted = true
		}
	}
	if !interrupted {
		t.Fatal("turn.interrupted event was not published")
	}
	if _, err := coordinator.BeginTurn("s1", "turn-next"); err != nil {
		t.Fatalf("workspace stayed busy after restart projection: %v", err)
	}
}

func TestRestoreQueuedInputsKeepsQueue(t *testing.T) {
	ctx := context.Background()
	store, err := session.NewJSONLStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	workspace := mustCanonicalWorkspace(t, t.TempDir())
	hub, err := NewEventHub(EventHubConfig{WorkspaceID: workspace.ID, StreamID: "stream-test"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = hub.Close() })
	if _, err := store.CreateRoot(ctx, session.CreateRootRequest{SessionID: "s1"}); err != nil {
		t.Fatal(err)
	}
	receipt := session.CommandReceipt{CommandID: "q1", Kind: CommandKindQueueTurn, ResourceID: "turn-a", Status: CommandStatusAccepted, SessionVersion: 2}
	input := session.CommandInput{CommandID: "q1", Kind: CommandKindQueueTurn, TurnID: "turn-a", Content: "later"}
	if _, err := store.AppendCommand(ctx, "s1", &input, receipt); err != nil {
		t.Fatal(err)
	}
	coordinator := NewWorkspaceCoordinator()
	if err := restoreQueuedInputs(ctx, store, coordinator, workspace.ID, hub); err != nil {
		t.Fatal(err)
	}
	if queue := coordinator.SessionSnapshot("s1").Queue; len(queue) != 1 || queue[0].CommandID != "q1" {
		t.Fatalf("queue = %#v", queue)
	}
}
