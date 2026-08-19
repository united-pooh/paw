package plan

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"paw/internal/loop"
	"paw/internal/message"
)

// fakeExecutor simulates a plan-mode turn: it records the scoped tool filter
// and system supplement, and optionally finalizes the plan document during the
// turn (as the agent would after the user picks 执行).
type fakeExecutor struct {
	mu              sync.Mutex
	executions      int
	sawFilter       bool
	sawInstructions bool
	finalize        func(ctx context.Context, input message.Message) error
}

func (e *fakeExecutor) ExecuteTurn(ctx context.Context, input message.Message, timing *loop.TurnTiming) (loop.TurnExecution, error) {
	e.mu.Lock()
	e.executions++
	finalize := e.finalize
	e.mu.Unlock()
	if finalize != nil {
		if err := finalize(ctx, input); err != nil {
			return loop.TurnExecution{}, err
		}
	}
	return loop.TurnExecution{Message: message.Message{Role: message.RoleAssistant, Content: "plan drafted"}}, nil
}

func (e *fakeExecutor) SetTurnToolFilter(filter loop.ToolFilter) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if filter != nil {
		e.sawFilter = true
	}
}

func (e *fakeExecutor) SystemSupplement() string { return "" }

func (e *fakeExecutor) SetSystemSupplement(supplement string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if supplement == Instructions {
		e.sawInstructions = true
	}
}

var _ loop.TurnExecutor = (*fakeExecutor)(nil)
var _ loop.ToolFilterApplier = (*fakeExecutor)(nil)

func newTestRuntime(executor loop.TurnExecutor, store *FileStore) *Runtime {
	return NewRuntime(RuntimeConfig{
		Store:    store,
		Executor: executor,
		Filter:   ModeFilter(store.Dir()),
	})
}

func TestStartCreatesClarifyingSession(t *testing.T) {
	store := newTestStore(t)
	runtime := newTestRuntime(&fakeExecutor{}, store)
	session, err := runtime.Start(context.Background(), "fix login")
	if err != nil {
		t.Fatal(err)
	}
	if session.Status != SessionClarifying || session.Requirement != "fix login" {
		t.Fatalf("session = %#v", session)
	}
	if runtime.ActiveID() != session.ID {
		t.Fatalf("active = %s, want %s", runtime.ActiveID(), session.ID)
	}
	if err := runtime.Cancel(context.Background(), session.ID); err != nil {
		t.Fatal(err)
	}
}

func TestRestoreAttachesSessionSnapshotWithoutRunning(t *testing.T) {
	store := newTestStore(t)
	executor := &fakeExecutor{}
	runtime := NewRuntime(RuntimeConfig{Store: store, Executor: executor, Filter: ModeFilter(store.Dir())})
	snapshot := Session{
		ID: "plan-restore", SessionID: "session-a", Status: SessionDrafting,
		Requirement: "finish p5", TurnsUsed: 3, Continuations: 2,
		MaxTurns: 8, MaxContinuations: 4, MaxNoProgress: 2,
	}
	if err := runtime.Restore(snapshot); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	executor.mu.Lock()
	if executor.executions != 0 {
		t.Fatalf("restore executed %d turns, want 0", executor.executions)
	}
	executor.mu.Unlock()
	restored, err := runtime.Status(context.Background(), snapshot.ID)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if restored.Status != SessionPaused || restored.ResumeStatus != SessionDrafting || restored.SessionID != "session-a" || restored.TurnsUsed != 3 {
		t.Fatalf("restored = %#v", restored)
	}
	if err := runtime.Resume(context.Background(), snapshot.ID); err != nil {
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
	t.Fatal("resume did not continue restored session")
}

func TestRestorePreservesCompletePausedSessionSnapshot(t *testing.T) {
	store := newTestStore(t)
	createdAt := time.Date(2026, 8, 18, 10, 0, 0, 0, time.UTC)
	updatedAt := createdAt.Add(3 * time.Hour)
	want := Session{
		ID: "plan-complete", SessionID: "session-a", Status: SessionPaused, ResumeStatus: SessionDrafting,
		Requirement: "finish p5", Continuations: 3, NoProgress: 2,
		PauseReason: PauseBlocked, LastDecision: "waiting for approval", CurrentTaskID: "plan-task-7",
		TurnsUsed: 5, ToolCallsUsed: 11, MaxTurns: 12, MaxContinuations: 6, MaxNoProgress: 4,
		CreatedAt: createdAt, UpdatedAt: updatedAt,
	}
	var persisted Session
	runtime := NewRuntime(RuntimeConfig{
		Store: store, Executor: &fakeExecutor{}, SessionID: "session-a",
		Snapshot: func(_ context.Context, snapshot Session) error {
			persisted = snapshot
			return nil
		},
	})
	if err := runtime.Restore(want); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	got, err := runtime.Status(context.Background(), want.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got != want || persisted != want {
		t.Fatalf("restored=%#v persisted=%#v want=%#v", got, persisted, want)
	}
}

func TestStartRejectsEmptyRequirement(t *testing.T) {
	runtime := newTestRuntime(&fakeExecutor{}, newTestStore(t))
	if _, err := runtime.Start(context.Background(), "   "); err == nil {
		t.Fatal("empty requirement accepted")
	}
}

func TestRunFinalizesWhenDocApproved(t *testing.T) {
	store := newTestStore(t)
	dir := store.Dir()
	ctx := context.Background()

	var finalized atomic.Pointer[PlanDoc]
	executor := &fakeExecutor{}
	runtime := NewRuntime(RuntimeConfig{
		Store:       store,
		Executor:    executor,
		Filter:      ModeFilter(dir),
		OnFinalized: func(doc PlanDoc) { finalized.Store(&doc) },
	})
	// Deterministic approval: the executor finalizes during its turn, exactly
	// like the real plan_finalize tool call inside the runner tool loop. The
	// agent writes the plan file first, then approves it.
	executor.finalize = func(ctx context.Context, input message.Message) error {
		const marker = "计划文件必须写入："
		idx := strings.Index(input.Content, marker)
		if idx < 0 {
			return nil // not a plan turn input; skip
		}
		path := strings.TrimSpace(input.Content[idx+len(marker):])
		path = strings.TrimSpace(strings.SplitN(path, "\n", 2)[0])
		id := PlanID(strings.TrimSuffix(filepath.Base(path), ".md"))
		if err := os.WriteFile(path, []byte("# Plan\n\n- step 1\n"), 0o644); err != nil {
			return err
		}
		_, err := runtime.Finalize(ctx, id, path)
		return err
	}

	session, err := runtime.Start(ctx, "fix login")
	if err != nil {
		t.Fatal(err)
	}
	_ = session
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if finalized.Load() != nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if finalized.Load() == nil {
		t.Fatal("OnFinalized not invoked after approval")
	}
	if finalized.Load().Status != PlanApproved {
		t.Fatalf("finalized status = %s", finalized.Load().Status)
	}
	doc, ok, err := store.Get(ctx, finalized.Load().ID)
	if err != nil || !ok {
		t.Fatalf("doc = ok:%v err:%v", ok, err)
	}
	if doc.Status != PlanApproved {
		t.Fatalf("persisted status = %s, want approved", doc.Status)
	}
	// The tool filter and instructions must have been scoped onto the executor
	// during the plan turns (they are restored afterwards).
	executor.mu.Lock()
	defer executor.mu.Unlock()
	if !executor.sawInstructions {
		t.Fatal("plan instructions were not injected")
	}
	if !executor.sawFilter {
		t.Fatal("tool filter was not applied")
	}
}

func TestFinalizeRejectsPathOutsideDir(t *testing.T) {
	store := newTestStore(t)
	runtime := newTestRuntime(&fakeExecutor{}, store)
	ctx := context.Background()
	session, err := runtime.Start(ctx, "fix login")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.Finalize(ctx, session.ID, filepath.Join(t.TempDir(), "x.md")); err == nil {
		t.Fatal("outside path accepted")
	}
	_ = runtime.Cancel(ctx, session.ID)
}

func TestFinalizeRequiresActiveSession(t *testing.T) {
	runtime := newTestRuntime(&fakeExecutor{}, newTestStore(t))
	if _, err := runtime.Finalize(context.Background(), "plan-none", "x.md"); err != ErrNoActivePlan {
		t.Fatalf("err = %v, want ErrNoActivePlan", err)
	}
}

func TestStateTransitions(t *testing.T) {
	s := Session{Status: SessionClarifying}
	if !CanTransition(SessionClarifying, SessionDrafting) {
		t.Fatal("clarifying -> drafting not allowed")
	}
	if !CanTransition(SessionDrafting, SessionAwaitingApprov) {
		t.Fatal("drafting -> awaiting_approval not allowed")
	}
	if !CanTransition(SessionAwaitingApprov, SessionApproved) {
		t.Fatal("awaiting_approval -> approved not allowed")
	}
	if !CanTransition(SessionAwaitingApprov, SessionDrafting) {
		t.Fatal("awaiting_approval -> drafting (revise) not allowed")
	}
	if CanTransition(SessionApproved, SessionDrafting) {
		t.Fatal("approved -> drafting allowed")
	}
	if err := s.Transition(SessionApproved, ""); err != nil {
		t.Fatal(err)
	}
}

func TestFinalizeToolInputValidation(t *testing.T) {
	tool := NewFinalizeTool(func(ctx context.Context, id PlanID, path string) (PlanDoc, error) {
		return PlanDoc{ID: id, Path: path, Status: PlanApproved}, nil
	})
	out, err := tool.Run(context.Background(), json.RawMessage(`{"plan_id":"p1","path":"/tmp/p.md"}`))
	if err != nil || !strings.Contains(out, "approved") {
		t.Fatalf("out=%q err=%v", out, err)
	}
	if _, err := tool.Run(context.Background(), json.RawMessage(`{}`)); err == nil {
		t.Fatal("empty input accepted")
	}
	if _, err := tool.Run(context.Background(), json.RawMessage(`{"plan_id":"p1"}`)); err == nil {
		t.Fatal("missing path accepted")
	}
}
