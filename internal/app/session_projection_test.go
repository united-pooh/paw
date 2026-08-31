package app

import (
	"context"
	"testing"

	"paw/internal/message"
	"paw/internal/session"
)

func TestSessionServiceListSnapshotCreateForkDoNotActivateHost(t *testing.T) {
	ctx := context.Background()
	store, err := session.NewJSONLStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateRoot(ctx, session.CreateRootRequest{SessionID: "active"}); err != nil {
		t.Fatal(err)
	}
	if err := store.BeginTurn(ctx, "active", "turn-1", message.Message{Role: message.RoleUser, Content: "hello"}); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendAssistant(ctx, "active", "turn-1", message.Message{Role: message.RoleAssistant, Content: "world"}); err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteTurn(ctx, "active", "turn-1"); err != nil {
		t.Fatal(err)
	}

	coordinator := NewWorkspaceCoordinator()
	if _, err := coordinator.BeginTurn("active", "foreground-turn"); err != nil {
		t.Fatal(err)
	}
	service := NewSessionService(store, coordinator)
	page, err := service.List(ctx, SessionPageRequest{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].SessionID != "active" {
		t.Fatalf("session page = %#v", page)
	}
	projection, err := service.Snapshot(ctx, "active", SnapshotRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(projection.Turns) != 1 || projection.Turns[0].Messages[0].Content != "hello" || projection.ActiveTurnID != "foreground-turn" {
		t.Fatalf("session projection = %#v", projection)
	}
	created, err := service.Create(ctx, CreateSessionCommand{SessionID: "created"})
	if err != nil || created.SessionID != "created" {
		t.Fatalf("Create() = %#v, %v", created, err)
	}
	forked, err := service.Fork(ctx, ForkSessionCommand{SessionID: "forked", ParentSessionID: "active"})
	if err != nil || forked.SessionID != "forked" {
		t.Fatalf("Fork() = %#v, %v", forked, err)
	}
	state := coordinator.WorkspaceSnapshot()
	if state.ActiveSessionID != "active" || state.ActiveTurnID != "foreground-turn" {
		t.Fatalf("store-only operations changed active coordinator state: %#v", state)
	}
	forkHistory, err := store.LoadResolvedHistory(ctx, "forked")
	if err != nil || len(forkHistory) != 2 || forkHistory[0].Content != "hello" {
		t.Fatalf("fork history = %#v, %v", forkHistory, err)
	}
}

func TestSessionServicePaginatesSessionsAndTurns(t *testing.T) {
	ctx := context.Background()
	store, err := session.NewJSONLStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, sessionID := range []string{"a", "b", "c"} {
		if _, err := store.CreateRoot(ctx, session.CreateRootRequest{SessionID: sessionID}); err != nil {
			t.Fatal(err)
		}
		if err := store.Append(ctx, sessionID, message.Message{Role: message.RoleUser, Content: "title-" + sessionID}); err != nil {
			t.Fatal(err)
		}
	}
	service := NewSessionService(store, NewWorkspaceCoordinator())
	first, err := service.List(ctx, SessionPageRequest{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Items) != 2 || first.NextCursor == "" {
		t.Fatalf("first page = %#v", first)
	}
	second, err := service.List(ctx, SessionPageRequest{Limit: 2, Cursor: first.NextCursor})
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Items) != 1 || second.NextCursor != "" {
		t.Fatalf("second page = %#v", second)
	}

	if _, err := store.CreateRoot(ctx, session.CreateRootRequest{SessionID: "turns"}); err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 5; index++ {
		turnID := "turn-" + string(rune('a'+index))
		if err := store.BeginTurn(ctx, "turns", turnID, message.Message{Role: message.RoleUser, Content: turnID}); err != nil {
			t.Fatal(err)
		}
		if err := store.CompleteTurn(ctx, "turns", turnID); err != nil {
			t.Fatal(err)
		}
	}
	recent, err := service.Snapshot(ctx, "turns", SnapshotRequest{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(recent.Turns) != 2 || recent.EarlierCursor == "" {
		t.Fatalf("recent turns = %#v", recent)
	}
	earlier, err := service.Snapshot(ctx, "turns", SnapshotRequest{Limit: 2, Before: recent.EarlierCursor})
	if err != nil {
		t.Fatal(err)
	}
	if len(earlier.Turns) != 2 {
		t.Fatalf("earlier turns = %#v", earlier)
	}
}
