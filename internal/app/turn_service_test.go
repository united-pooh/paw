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
	inputs  []string
	result  string
	err     error
	steers  []string
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
func (r *fakeTurnRunner) PrepareSteer(input string) (loop.SteerAdmission, bool) {
	return &fakeSteerAdmission{commit: func() {
		r.mu.Lock()
		r.steers = append(r.steers, input)
		r.mu.Unlock()
	}}, true
}

type fakeSteerAdmission struct {
	once   sync.Once
	commit func()
}

func (a *fakeSteerAdmission) Commit() { a.once.Do(a.commit) }
func (a *fakeSteerAdmission) Abort()  { a.once.Do(func() {}) }
func (r *fakeTurnRunner) RunTurnWithTiming(ctx context.Context, input string, turnID string, _ time.Time) (loop.TurnExecution, error) {
	r.mu.Lock()
	r.inputs = append(r.inputs, input)
	r.mu.Unlock()
	r.started <- struct{}{}
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
	runner := &fakeTurnRunner{current: "other", started: make(chan struct{}, 2), release: make(chan struct{}), result: "answer"}
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

func TestSteerQueueAndCancelValidateActiveTurn(t *testing.T) {
	store, err := session.NewJSONLStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateRoot(context.Background(), session.CreateRootRequest{SessionID: "s1"}); err != nil {
		t.Fatal(err)
	}
	coordinator := NewWorkspaceCoordinator()
	hub := newTestEventHub(t, EventHubConfig{WorkspaceID: "workspace", StreamID: "stream"})
	runner := &fakeTurnRunner{current: "s1", started: make(chan struct{}, 2), release: make(chan struct{})}
	service := newTurnService(runner, store, coordinator, hub, nil)
	receipt, err := service.Submit(context.Background(), SubmitCommand{CommandID: "submit", SessionID: "s1", SessionVersion: 0, Text: "start"})
	if err != nil {
		t.Fatal(err)
	}
	<-runner.started
	if _, err := service.Steer(context.Background(), ActiveTurnCommand{CommandID: "steer-bad", SessionID: "s1", ActiveTurnID: "stale", Text: "change"}); !errors.Is(err, ErrActiveTurnChanged) {
		t.Fatalf("stale steer = %v", err)
	}
	steerCommand := ActiveTurnCommand{CommandID: "steer", SessionID: "s1", ActiveTurnID: receipt.ResourceID, Text: "change"}
	steered, err := service.Steer(context.Background(), steerCommand)
	if err != nil {
		t.Fatal(err)
	}
	if retry, err := service.Steer(context.Background(), steerCommand); err != nil || retry != steered {
		t.Fatalf("steer retry = %#v, %v want %#v", retry, err, steered)
	}
	runner.mu.Lock()
	steers := append([]string(nil), runner.steers...)
	runner.mu.Unlock()
	if len(steers) != 1 || steers[0] != "change" {
		t.Fatalf("steers = %#v", steers)
	}
	queueCommand := ActiveTurnCommand{CommandID: "queue", SessionID: "s1", ActiveTurnID: receipt.ResourceID, Text: "later"}
	queued, err := service.Queue(context.Background(), queueCommand)
	if err != nil {
		t.Fatal(err)
	}
	if retry, err := service.Queue(context.Background(), queueCommand); err != nil || retry != queued {
		t.Fatalf("queue retry = %#v, %v want %#v", retry, err, queued)
	}
	if queued.SessionVersion != 2 || len(coordinator.SessionSnapshot("s1").Queue) != 1 {
		t.Fatalf("queue = %#v state=%#v", queued, coordinator.SessionSnapshot("s1"))
	}
	records, err := store.LoadResolvedJournalRecords(context.Background(), "s1")
	if err != nil {
		t.Fatal(err)
	}
	inputs := 0
	for _, record := range records {
		if record.Kind == session.JournalCommandReceipt && record.CommandReceipt != nil && record.CommandReceipt.Input != nil {
			inputs++
		}
	}
	if inputs != 2 {
		t.Fatalf("command input receipts = %d, want steer and queue", inputs)
	}
	queueRecords := 0
	for _, record := range records {
		if record.CommandReceipt != nil && record.CommandReceipt.Kind == CommandKindQueueTurn {
			queueRecords++
		}
	}
	if queueRecords != 1 {
		t.Fatalf("queue receipt records = %d, want atomic single record", queueRecords)
	}
	if _, err := service.Cancel(context.Background(), ActiveTurnCommand{CommandID: "cancel", SessionID: "s1", ActiveTurnID: receipt.ResourceID}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for {
		state := coordinator.WorkspaceSnapshot()
		if state.ActiveTurnID != "" && state.ActiveTurnID != receipt.ResourceID {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("cancel did not advance to queued turn: %#v", state)
		}
		time.Sleep(time.Millisecond)
	}
	select {
	case <-runner.started:
	case <-time.After(time.Second):
		t.Fatal("queued turn did not start")
	}
	runner.mu.Lock()
	inputsRun := append([]string(nil), runner.inputs...)
	runner.mu.Unlock()
	if len(inputsRun) != 2 || inputsRun[1] != "later" {
		t.Fatalf("run inputs = %#v", inputsRun)
	}
	restarted := newTurnService(runner, store, coordinator, hub, nil)
	terminal, err := restarted.Cancel(context.Background(), ActiveTurnCommand{CommandID: "cancel-again", SessionID: "s1", ActiveTurnID: receipt.ResourceID})
	if err != nil || terminal.Status != "cancelled" {
		t.Fatalf("terminal cancel = %#v, %v", terminal, err)
	}
	if _, err := restarted.Cancel(context.Background(), ActiveTurnCommand{CommandID: "cancel-unknown", SessionID: "s1", ActiveTurnID: "missing"}); !errors.Is(err, ErrActiveTurnChanged) {
		t.Fatalf("unknown terminal cancel = %v", err)
	}
	subscription, err := hub.Subscribe(EventCursor{StreamID: "stream"})
	if err != nil {
		t.Fatal(err)
	}
	defer subscription.Close()
	cancelled := false
	for _, event := range subscription.Replay {
		if event.Type == EventTurnCancelled && event.TurnID == receipt.ResourceID {
			cancelled = true
		}
	}
	if !cancelled {
		t.Fatal("turn.cancelled event was not published")
	}
	close(runner.release)
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
	runner := &fakeTurnRunner{current: "s1", started: make(chan struct{}, 2), release: make(chan struct{})}
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
