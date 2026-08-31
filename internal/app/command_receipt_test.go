package app

import (
	"context"
	"testing"

	"paw/internal/message"
	"paw/internal/session"
)

func TestCommandReceiptMakesCreateIdempotentAcrossServiceRestart(t *testing.T) {
	ctx := context.Background()
	store, err := session.NewJSONLStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	command := CreateSessionCommand{CommandID: "create-command"}
	service := NewSessionService(store, NewWorkspaceCoordinator())
	first, err := service.Create(ctx, command)
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.Create(ctx, command)
	if err != nil {
		t.Fatal(err)
	}
	if first != second || first.SessionID == "" {
		t.Fatalf("create receipts = %#v / %#v", first, second)
	}
	restarted := NewSessionService(store, NewWorkspaceCoordinator())
	third, err := restarted.Create(ctx, command)
	if err != nil || third != first {
		t.Fatalf("restart Create() = %#v, %v; want %#v", third, err, first)
	}
	records, err := store.LoadResolvedJournalRecords(ctx, first.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	receipts := 0
	for _, record := range records {
		if record.Kind == session.JournalCommandReceipt {
			receipts++
		}
	}
	if receipts != 1 {
		t.Fatalf("receipt records = %d, want 1", receipts)
	}
}

func TestCommandReceiptMakesConcurrentCreateIdempotent(t *testing.T) {
	ctx := context.Background()
	store, err := session.NewJSONLStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	service := NewSessionService(store, NewWorkspaceCoordinator())
	command := CreateSessionCommand{CommandID: "concurrent-create"}
	const callers = 16
	results := make(chan SessionMutationResult, callers)
	errs := make(chan error, callers)
	for range callers {
		go func() {
			result, err := service.Create(ctx, command)
			results <- result
			errs <- err
		}()
	}
	var expected SessionMutationResult
	for range callers {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
		result := <-results
		if expected.SessionID == "" {
			expected = result
		} else if result != expected {
			t.Fatalf("concurrent result = %#v, want %#v", result, expected)
		}
	}
	records, err := store.LoadResolvedJournalRecords(ctx, expected.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	receipts := 0
	for _, record := range records {
		if record.Kind == session.JournalCommandReceipt {
			receipts++
		}
	}
	if receipts != 1 {
		t.Fatalf("receipt records = %d, want 1", receipts)
	}
}

func TestCommandReceiptMakesForkIdempotentAcrossServiceRestart(t *testing.T) {
	ctx := context.Background()
	store, err := session.NewJSONLStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateRoot(ctx, session.CreateRootRequest{SessionID: "parent"}); err != nil {
		t.Fatal(err)
	}
	if err := store.Append(ctx, "parent", message.Message{Role: message.RoleUser, Content: "seed"}); err != nil {
		t.Fatal(err)
	}
	command := ForkSessionCommand{CommandID: "fork-command", ParentSessionID: "parent"}
	service := NewSessionService(store, NewWorkspaceCoordinator())
	first, err := service.Fork(ctx, command)
	if err != nil {
		t.Fatal(err)
	}
	restarted := NewSessionService(store, NewWorkspaceCoordinator())
	second, err := restarted.Fork(ctx, command)
	if err != nil || second != first {
		t.Fatalf("restart Fork() = %#v, %v; want %#v", second, err, first)
	}
	history, err := store.LoadResolvedHistory(ctx, first.SessionID)
	if err != nil || len(history) != 1 || history[0].Content != "seed" {
		t.Fatalf("fork history = %#v, %v", history, err)
	}
}

func TestDeterministicCommandResourceIDHonorsExplicitResource(t *testing.T) {
	got, err := deterministicCommandResourceID(CommandKindCreateSession, "cmd", "explicit")
	if err != nil || got != "explicit" {
		t.Fatalf("deterministicCommandResourceID() = %q, %v", got, err)
	}
	first, err := deterministicCommandResourceID(CommandKindCreateSession, "cmd", "")
	if err != nil {
		t.Fatal(err)
	}
	second, err := deterministicCommandResourceID(CommandKindCreateSession, "cmd", "")
	if err != nil || first != second || len(first) != 32 {
		t.Fatalf("deterministic IDs = %q/%q, %v", first, second, err)
	}
}
