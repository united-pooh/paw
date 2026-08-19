package plan

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"paw/internal/loop"
)

type fakePlanSessionHost struct {
	mu        sync.Mutex
	sessionID string
	executor  loop.TurnExecutor
	plans     map[string]Session
}

func (h *fakePlanSessionHost) GoalTurnExecutor() loop.TurnExecutor { return h.executor }
func (h *fakePlanSessionHost) CurrentSessionID() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.sessionID
}
func (h *fakePlanSessionHost) SavePlanFor(_ context.Context, sessionID, id, _ string, snapshot any) error {
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	var value Session
	if err := json.Unmarshal(raw, &value); err != nil {
		return err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.plans == nil {
		h.plans = map[string]Session{}
	}
	h.plans[sessionID] = value
	return nil
}
func (h *fakePlanSessionHost) ClearPlanFor(_ context.Context, sessionID string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.plans, sessionID)
	return nil
}
func (h *fakePlanSessionHost) ActivePlan(_ context.Context, sessionID string) (string, string, json.RawMessage, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	snapshot, ok := h.plans[sessionID]
	if !ok {
		return "", "", nil, nil
	}
	raw, err := json.Marshal(snapshot)
	return string(snapshot.ID), string(snapshot.Status), raw, err
}

func TestSessionControllerStartPersistsCurrentSessionBindingBeforeExecution(t *testing.T) {
	executor := &fakeExecutor{}
	host := &fakePlanSessionHost{sessionID: "session-a", executor: executor, plans: map[string]Session{}}
	controller := NewSessionController(host, t.TempDir(), "")
	id, err := controller.Start("finish p5")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	host.mu.Lock()
	snapshot := host.plans["session-a"]
	host.mu.Unlock()
	if string(snapshot.ID) != id || snapshot.SessionID != "session-a" {
		t.Fatalf("persisted snapshot = %#v, id=%q", snapshot, id)
	}
}

func TestSessionControllerRebindRestoresPausedAndRequiresExplicitResume(t *testing.T) {
	executor := &fakeExecutor{}
	host := &fakePlanSessionHost{
		sessionID: "session-b",
		executor:  executor,
		plans: map[string]Session{
			"session-b": {
				ID: "plan-b", SessionID: "session-b", Status: SessionDrafting,
				Requirement: "restore p5", TurnsUsed: 2, MaxTurns: 8,
			},
		},
	}
	controller := NewSessionController(host, t.TempDir(), "")
	if err := controller.Rebind("session-b"); err != nil {
		t.Fatalf("Rebind: %v", err)
	}
	executor.mu.Lock()
	if executor.executions != 0 {
		t.Fatalf("rebind executed %d turns", executor.executions)
	}
	executor.mu.Unlock()
	if status := controller.Status(); status == "no active plan session" {
		t.Fatalf("Status after rebind = %q", status)
	}
	if err := controller.Resume(); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		executor.mu.Lock()
		executions := executor.executions
		executor.mu.Unlock()
		if executions > 0 {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("explicit resume did not execute")
}

func TestLegacyPlanDocumentRemainsListableButIsNotGuessedAsActive(t *testing.T) {
	plansDir := t.TempDir()
	store := NewFileStore(plansDir)
	legacy := PlanDoc{ID: "legacy-plan", Title: "Legacy", Status: PlanDraft, Content: "# Legacy\n"}
	if err := store.Create(context.Background(), legacy); err != nil {
		t.Fatalf("create legacy plan: %v", err)
	}
	host := &fakePlanSessionHost{sessionID: "session-b", executor: &fakeExecutor{}, plans: map[string]Session{}}
	controller := NewSessionController(host, plansDir, "")
	if err := controller.Rebind("session-b"); err != nil {
		t.Fatalf("Rebind: %v", err)
	}
	if got := controller.Status(); got != "no active plan session" {
		t.Fatalf("Status = %q, legacy plan must not be attached", got)
	}
	if got := controller.List(); !strings.Contains(got, "legacy-plan") {
		t.Fatalf("List = %q, want legacy plan", got)
	}
	if got := controller.Show("legacy-plan"); !strings.Contains(got, "# Legacy") || !strings.Contains(got, "session: ") {
		t.Fatalf("Show = %q, want readable unbound legacy plan", got)
	}
}
