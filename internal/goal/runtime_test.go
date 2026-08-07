package goal

import (
	"context"
	"errors"
	"paw/internal/loop"
	"paw/internal/message"
	"paw/internal/todo"
	"sync"
	"testing"
	"time"
)

type fakeExecutor struct {
	mu      sync.Mutex
	calls   []message.Message
	results []loop.TurnExecution
	err     error
}

func (f *fakeExecutor) ExecuteTurn(_ context.Context, input message.Message, _ *loop.TurnTiming) (loop.TurnExecution, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, input)
	if f.err != nil {
		return loop.TurnExecution{}, f.err
	}
	if len(f.results) == 0 {
		return loop.TurnExecution{Message: message.Message{Role: message.RoleAssistant, Content: "done"}}, nil
	}
	result := f.results[0]
	f.results = f.results[1:]
	return result, nil
}

func (f *fakeExecutor) count() int { f.mu.Lock(); defer f.mu.Unlock(); return len(f.calls) }

func waitGoal(t *testing.T, r *Runtime, id GoalID, want GoalStatus) GoalSnapshot {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		snapshot, err := r.Status(context.Background(), id)
		if err == nil && snapshot.Status == want {
			return snapshot
		}
		time.Sleep(5 * time.Millisecond)
	}
	snapshot, err := r.Status(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	t.Fatalf("goal status = %s, want %s", snapshot.Status, want)
	return snapshot
}

func TestRuntimeCompletesGoalWithoutPendingTodo(t *testing.T) {
	executor := &fakeExecutor{}
	runtime := NewRuntime(RuntimeConfig{Policy: Policy{Budget: GoalBudget{MaxContinuations: 1, MaxNoProgress: 2}}, Executor: executor, Todo: func() (todo.Snapshot, bool) { return todo.Snapshot{}, false }})
	snapshot, err := runtime.Start(context.Background(), Goal{SessionID: "s1", Objective: "finish"})
	if err != nil {
		t.Fatal(err)
	}
	got := waitGoal(t, runtime, snapshot.ID, GoalCompleted)
	if executor.count() != 1 {
		t.Fatalf("executor calls = %d, want 1", executor.count())
	}
	if got.LastDecision == "" {
		t.Fatal("completion decision was not recorded")
	}
}

func TestRuntimePausesForPendingTodoAndResumesExplicitly(t *testing.T) {
	executor := &fakeExecutor{results: []loop.TurnExecution{{Message: message.Message{Role: message.RoleAssistant, Content: "working"}}, {Message: message.Message{Role: message.RoleAssistant, Content: "done"}}}}
	pending := true
	runtime := NewRuntime(RuntimeConfig{Policy: Policy{Budget: GoalBudget{MaxContinuations: 1, MaxNoProgress: 2}}, Executor: executor, Todo: func() (todo.Snapshot, bool) {
		if pending {
			return todo.Snapshot{Items: []todo.Item{{ID: "one", Content: "step", Status: todo.StatusInProgress}}}, true
		}
		return todo.Snapshot{Items: []todo.Item{{ID: "one", Content: "step", Status: todo.StatusCompleted}}}, true
	}})
	snapshot, err := runtime.Start(context.Background(), Goal{Objective: "work"})
	if err != nil {
		t.Fatal(err)
	}
	waitGoal(t, runtime, snapshot.ID, GoalPaused)
	pending = false
	if err := runtime.Resume(context.Background(), snapshot.ID); err != nil {
		t.Fatal(err)
	}
	waitGoal(t, runtime, snapshot.ID, GoalCompleted)
	if executor.count() != 3 {
		t.Fatalf("executor calls = %d, want 3", executor.count())
	}
}

func TestRuntimeRejectsConcurrentStart(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	executor := &blockingExecutor{started: started, release: release}
	runtime := NewRuntime(RuntimeConfig{Executor: executor})
	first, err := runtime.Start(context.Background(), Goal{Objective: "first"})
	if err != nil {
		t.Fatal(err)
	}
	<-started
	if _, err := runtime.Start(context.Background(), Goal{Objective: "second"}); !errors.Is(err, ErrGoalActive) {
		t.Fatalf("error = %v, want ErrGoalActive", err)
	}
	close(release)
	waitGoal(t, runtime, first.ID, GoalCompleted)
}

type blockingExecutor struct {
	started chan struct{}
	release chan struct{}
}

func (b *blockingExecutor) ExecuteTurn(ctx context.Context, _ message.Message, _ *loop.TurnTiming) (loop.TurnExecution, error) {
	select {
	case b.started <- struct{}{}:
	default:
	}
	select {
	case <-b.release:
		return loop.TurnExecution{Message: message.Message{Role: message.RoleAssistant, Content: "done"}}, nil
	case <-ctx.Done():
		return loop.TurnExecution{}, ctx.Err()
	}
}
