package goal

import (
	"context"
	"sync"
	"testing"
	"time"

	"paw/internal/loop"
)

type fakeGoalSessionHost struct {
	mu        sync.Mutex
	sessionID string
	executor  loop.TurnExecutor
	bindings  map[string]GoalSnapshot
}

func (h *fakeGoalSessionHost) GoalTurnExecutor() loop.TurnExecutor { return h.executor }
func (h *fakeGoalSessionHost) CurrentSessionID() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.sessionID
}
func (h *fakeGoalSessionHost) ActivateGoalFor(_ context.Context, sessionID, id string, status string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.bindings == nil {
		h.bindings = map[string]GoalSnapshot{}
	}
	h.bindings[sessionID] = GoalSnapshot{ID: GoalID(id), SessionID: sessionID, Status: GoalStatus(status)}
	return nil
}
func (h *fakeGoalSessionHost) ClearGoalFor(_ context.Context, sessionID string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.bindings, sessionID)
	return nil
}
func (h *fakeGoalSessionHost) ActiveGoal(_ context.Context, sessionID string) (string, string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	binding, ok := h.bindings[sessionID]
	if !ok {
		return "", "", nil
	}
	return string(binding.ID), string(binding.Status), nil
}

func TestSessionControllerRebindSelectsLatestNonTerminalGoal(t *testing.T) {
	host := &fakeGoalSessionHost{sessionID: "session-a", executor: &fakeExecutor{}, bindings: map[string]GoalSnapshot{}}
	controller := NewSessionController(host, nil, t.TempDir())
	store := controller.store
	older := Goal{ID: "goal-old", SessionID: "session-a", Objective: "old", Status: GoalPaused, CreatedAt: time.Now().Add(-time.Hour), UpdatedAt: time.Now().Add(-time.Hour)}
	newer := Goal{ID: "goal-new", SessionID: "session-a", Objective: "new", Status: GoalRunning, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	terminal := Goal{ID: "goal-done", SessionID: "session-a", Objective: "done", Status: GoalCompleted, CreatedAt: time.Now().Add(time.Minute), UpdatedAt: time.Now().Add(time.Minute)}
	for _, goal := range []Goal{older, newer, terminal} {
		if err := store.Create(context.Background(), goal); err != nil {
			t.Fatalf("Create %s: %v", goal.ID, err)
		}
	}
	if err := controller.Rebind("session-a"); err != nil {
		t.Fatalf("Rebind: %v", err)
	}
	snapshot, err := controller.current()
	if err != nil {
		t.Fatalf("current: %v", err)
	}
	if snapshot.ID != "goal-new" || snapshot.Status != GoalPaused {
		t.Fatalf("snapshot = %#v", snapshot)
	}
	host.mu.Lock()
	binding := host.bindings["session-a"]
	host.mu.Unlock()
	if binding.ID != "goal-new" || binding.Status != GoalPaused {
		t.Fatalf("session binding = %#v", binding)
	}
	host.executor.(*fakeExecutor).mu.Lock()
	executions := len(host.executor.(*fakeExecutor).calls)
	host.executor.(*fakeExecutor).mu.Unlock()
	if executions != 0 {
		t.Fatalf("rebind executed %d turns", executions)
	}
}

func TestSessionControllerRebindKeepsSessionsIsolated(t *testing.T) {
	host := &fakeGoalSessionHost{sessionID: "session-b", executor: &fakeExecutor{}, bindings: map[string]GoalSnapshot{}}
	controller := NewSessionController(host, nil, t.TempDir())
	for _, goal := range []Goal{
		{ID: "goal-a", SessionID: "session-a", Objective: "a", Status: GoalPaused, CreatedAt: time.Now()},
		{ID: "goal-b", SessionID: "session-b", Objective: "b", Status: GoalPaused, CreatedAt: time.Now()},
	} {
		if err := controller.store.Create(context.Background(), goal); err != nil {
			t.Fatal(err)
		}
	}
	if err := controller.Rebind("session-b"); err != nil {
		t.Fatal(err)
	}
	snapshot, err := controller.current()
	if err != nil || snapshot.ID != "goal-b" {
		t.Fatalf("snapshot=%#v err=%v", snapshot, err)
	}
}
