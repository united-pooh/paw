package app

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"paw/internal/loop"
	"paw/internal/message"
	"paw/internal/session"
)

type fakeTurnRunner struct {
	mu      sync.Mutex
	current string
	started chan struct{}
	release chan struct{}
	result  string
	err     error
}

func (r *fakeTurnRunner) CurrentSessionID() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.current
}
func (r *fakeTurnRunner) LoadSession(_ context.Context, sessionID string) (loop.SessionLoadResult, error) {
	r.mu.Lock()
	r.current = sessionID
	r.mu.Unlock()
	return loop.SessionLoadResult{}, nil
}
func (r *fakeTurnRunner) RunTurnWithTiming(ctx context.Context, _ string, turnID string, _ time.Time) (loop.TurnExecution, error) {
	close(r.started)
	select {
	case <-r.release:
	case <-ctx.Done():
		return loop.TurnExecution{}, ctx.Err()
	}
	return loop.TurnExecution{Message: message.Message{Role: message.RoleAssistant, Content: r.result}}, r.err
}

func TestSubmitStartsFixedTurnAndIsIdempotent(t *testing.T) {
	store, err := session.NewJSONLStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateRoot(context.Background(), session.CreateRootRequest{SessionID: "s1"}); err != nil {
		t.Fatal(err)
	}
	coordinator := NewWorkspaceCoordinator()
	hub := newTestEventHub(t, EventHubConfig{WorkspaceID: "workspace", StreamID: "stream"})
	runner := &fakeTurnRunner{current: "other", started: make(chan struct{}), release: make(chan struct{}), result: "answer"}
	service := newTurnService(runner, store, coordinator, hub, nil)
	command := SubmitCommand{CommandID: "submit-1", SessionID: "s1", SessionVersion: 0, Text: "hello"}
	receipt, err := service.Submit(context.Background(), command)
	if err != nil {
		t.Fatal(err)
	}
	<-runner.started
	if state := coordinator.WorkspaceSnapshot(); state.ActiveSessionID != "s1" || state.ActiveTurnID != receipt.ResourceID {
		t.Fatalf("active state = %#v", state)
	}
	retry, err := service.Submit(context.Background(), command)
	if err != nil || retry != receipt {
		t.Fatalf("retry = %#v, %v want %#v", retry, err, receipt)
	}
	close(runner.release)
	deadline := time.Now().Add(time.Second)
	for coordinator.Activity().ActiveTurn != 0 {
		if time.Now().After(deadline) {
			t.Fatal("turn did not reach terminal state")
		}
		time.Sleep(time.Millisecond)
	}
	records, err := store.LoadResolvedJournalRecords(context.Background(), "s1")
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
		t.Fatalf("receipt records = %d", receipts)
	}
}

func TestSubmitRejectsVersionMismatchAndWorkspaceBusy(t *testing.T) {
	store, err := session.NewJSONLStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"s1", "s2"} {
		if _, err := store.CreateRoot(context.Background(), session.CreateRootRequest{SessionID: id}); err != nil {
			t.Fatal(err)
		}
	}
	coordinator := NewWorkspaceCoordinator()
	hub := newTestEventHub(t, EventHubConfig{WorkspaceID: "workspace", StreamID: "stream"})
	runner := &fakeTurnRunner{current: "s1", started: make(chan struct{}), release: make(chan struct{})}
	service := newTurnService(runner, store, coordinator, hub, nil)
	if _, err := service.Submit(context.Background(), SubmitCommand{CommandID: "bad-version", SessionID: "s1", SessionVersion: 9, Text: "x"}); !errors.Is(err, ErrSessionVersionChanged) {
		t.Fatalf("version error = %v", err)
	}
	if _, err := service.Submit(context.Background(), SubmitCommand{CommandID: "first", SessionID: "s1", SessionVersion: 0, Text: "x"}); err != nil {
		t.Fatal(err)
	}
	<-runner.started
	if _, err := service.Submit(context.Background(), SubmitCommand{CommandID: "second", SessionID: "s2", SessionVersion: 0, Text: "x"}); !errors.Is(err, ErrWorkspaceBusy) {
		t.Fatalf("busy error = %v", err)
	}
	close(runner.release)
}
