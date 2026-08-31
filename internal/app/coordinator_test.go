package app

import (
	"errors"
	"sync"
	"testing"
)

func TestWorkspaceCoordinatorAllowsOnlyOneActiveTurn(t *testing.T) {
	coordinator := NewWorkspaceCoordinator()
	state, err := coordinator.BeginTurn("session-a", "turn-a")
	if err != nil {
		t.Fatal(err)
	}
	if state.ActiveSessionID != "session-a" || state.ActiveTurnID != "turn-a" || state.SessionVersion["session-a"] != 1 {
		t.Fatalf("state = %#v", state)
	}
	if _, err := coordinator.BeginTurn("session-b", "turn-b"); !errors.Is(err, ErrWorkspaceBusy) {
		t.Fatalf("second BeginTurn() error = %v", err)
	}
	if snapshot := coordinator.SessionSnapshot("session-b"); snapshot.SessionID != "session-b" || snapshot.SessionVersion != 0 {
		t.Fatalf("navigation snapshot = %#v", snapshot)
	}
	if state := coordinator.WorkspaceSnapshot(); state.ActiveSessionID != "session-a" || state.ActiveTurnID != "turn-a" {
		t.Fatalf("navigation changed active turn: %#v", state)
	}
}

func TestWorkspaceCoordinatorValidatesTurnScopedCommands(t *testing.T) {
	coordinator := NewWorkspaceCoordinator()
	if _, err := coordinator.BeginTurn("session-a", "turn-a"); err != nil {
		t.Fatal(err)
	}
	if err := coordinator.Steer("session-a", "turn-stale"); !errors.Is(err, ErrActiveTurnChanged) {
		t.Fatalf("Steer(stale) error = %v", err)
	}
	if _, err := coordinator.QueueInput("session-a", "turn-stale", InputDraft{Content: "queued"}); !errors.Is(err, ErrActiveTurnChanged) {
		t.Fatalf("QueueInput(stale) error = %v", err)
	}
	if _, err := coordinator.CancelTurn("session-a", "turn-stale"); !errors.Is(err, ErrActiveTurnChanged) {
		t.Fatalf("CancelTurn(stale) error = %v", err)
	}
	state, err := coordinator.QueueInput("session-a", "turn-a", InputDraft{CommandID: "cmd-1", Content: "queued"})
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Queue["session-a"]) != 1 || state.SessionVersion["session-a"] != 2 {
		t.Fatalf("queued state = %#v", state)
	}
	if _, err := coordinator.AddInteraction(InteractionState{RequestID: "question-1", SessionID: "session-a", TurnID: "turn-a", Kind: "question"}); err != nil {
		t.Fatal(err)
	}
	state, err = coordinator.CompleteTurn("session-a", "turn-a")
	if err != nil {
		t.Fatal(err)
	}
	if state.ActiveTurnID != "" || len(state.Pending) != 0 {
		t.Fatalf("terminal state = %#v", state)
	}
	if err := coordinator.Steer("session-a", "turn-a"); !errors.Is(err, ErrNoActiveTurn) {
		t.Fatalf("Steer(after terminal) error = %v", err)
	}
}

func TestWorkspaceCoordinatorActivityBlocksEvictionSignals(t *testing.T) {
	coordinator := NewWorkspaceCoordinator()
	if activity := coordinator.Activity(); activity.Busy() {
		t.Fatalf("initial activity = %#v", activity)
	}
	if _, err := coordinator.BeginTurn("session-a", "turn-a"); err != nil {
		t.Fatal(err)
	}
	if _, err := coordinator.QueueInput("session-a", "turn-a", InputDraft{Content: "later"}); err != nil {
		t.Fatal(err)
	}
	if _, err := coordinator.AddInteraction(InteractionState{RequestID: "permission-1", SessionID: "session-a", TurnID: "turn-a", Kind: "permission"}); err != nil {
		t.Fatal(err)
	}
	coordinator.SetActiveTasks(2)
	coordinator.SetActiveWrites(1)
	activity := coordinator.Activity()
	if activity.ActiveTurn != 1 || activity.ActiveTasks != 2 || activity.PendingInteractions != 1 || activity.QueuedInputs != 1 || activity.ActiveWrites != 1 || !activity.Busy() {
		t.Fatalf("activity = %#v", activity)
	}
}

func TestWorkspaceCoordinatorConcurrentSnapshotsAreIndependent(t *testing.T) {
	coordinator := NewWorkspaceCoordinator()
	if _, err := coordinator.BeginTurn("session-a", "turn-a"); err != nil {
		t.Fatal(err)
	}
	const readers = 32
	var wait sync.WaitGroup
	for range readers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			state := coordinator.WorkspaceSnapshot()
			state.SessionVersion["session-a"] = 999
			state.Queue["session-a"] = append(state.Queue["session-a"], InputDraft{Content: "mutation"})
		}()
	}
	wait.Wait()
	state := coordinator.WorkspaceSnapshot()
	if state.SessionVersion["session-a"] != 1 || len(state.Queue["session-a"]) != 0 {
		t.Fatalf("snapshot mutation leaked into coordinator: %#v", state)
	}
}
