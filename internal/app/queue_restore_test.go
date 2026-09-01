package app

import (
	"context"
	"testing"

	"paw/internal/session"
)

func TestRestoreQueuedInputsKeepsOnlyUnconsumedCommands(t *testing.T) {
	ctx := context.Background()
	store, err := session.NewJSONLStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateRoot(ctx, session.CreateRootRequest{SessionID: "s1"}); err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct {
		id      string
		content string
		version uint64
	}{{"q1", "first", 2}, {"q2", "second", 3}} {
		receipt := session.CommandReceipt{CommandID: item.id, Kind: CommandKindQueueTurn, ResourceID: "turn-active", Status: CommandStatusAccepted, SessionVersion: item.version}
		input := session.CommandInput{CommandID: item.id, Kind: CommandKindQueueTurn, TurnID: "turn-active", Content: item.content}
		if _, err := store.AppendCommand(ctx, "s1", &input, receipt); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.AppendCommandReceipt(ctx, "s1", session.CommandReceipt{CommandID: "q1:queued", Kind: CommandKindSubmitTurn, ResourceID: "turn-q1", Status: CommandStatusAccepted, SessionVersion: 5}); err != nil {
		t.Fatal(err)
	}
	coordinator := NewWorkspaceCoordinator()
	if err := restoreQueuedInputs(ctx, store, coordinator); err != nil {
		t.Fatal(err)
	}
	state := coordinator.SessionSnapshot("s1")
	if state.SessionVersion != 5 || len(state.Queue) != 1 || state.Queue[0].CommandID != "q2" || state.Queue[0].Content != "second" {
		t.Fatalf("restored state = %#v", state)
	}
}
