package goal

import (
	"context"
	"errors"
	"paw/internal/loop"
	"paw/internal/message"
	"paw/internal/todo"
	"testing"
	"time"
)

func TestEvaluatorRiskErrorPausesWithoutContinuation(t *testing.T) {
	e := NewEvaluator(DefaultPolicy())
	decision := e.Evaluate(Observation{
		HasTodo:   true,
		Todo:      todo.Snapshot{Items: []todo.Item{{ID: "step", Status: todo.StatusInProgress}}},
		ToolError: errors.New("unsafe command rejected by policy"),
	})
	if decision.Action != ActionPause || decision.PauseReason != PauseDangerousCommand {
		t.Fatalf("decision = %+v, want dangerous-command pause", decision)
	}
	if decision.NextPrompt.Content != "" {
		t.Fatal("risk pause must not create a continuation prompt")
	}
}

func TestEvaluatorFiniteBudgetTerminates(t *testing.T) {
	policy := DefaultPolicy()
	policy.Budget.MaxContinuations = 2
	policy.Budget.MaxNoProgress = 10
	e := NewEvaluator(policy)
	observation := Observation{GoalID: "g1", HasTodo: true, Todo: todo.Snapshot{Items: []todo.Item{{ID: "step", Status: todo.StatusPending}}}}
	for i := 0; i < 2; i++ {
		decision := e.Evaluate(observation)
		if decision.Action != ActionContinue {
			t.Fatalf("iteration %d decision = %+v", i, decision)
		}
		observation.ContinuationUsed++
		observation.Assistant.Content = string(rune('a' + i))
	}
	decision := e.Evaluate(observation)
	if decision.Action != ActionPause || decision.PauseReason != PauseBudgetExhausted {
		t.Fatalf("final decision = %+v, want budget pause", decision)
	}
}

func TestRuntimeCancelStopsBlockedExecutor(t *testing.T) {
	started := make(chan struct{})
	executor := &cancelAwareExecutor{started: started}
	runtime := NewRuntime(RuntimeConfig{Executor: executor})
	snapshot, err := runtime.Start(context.Background(), Goal{Objective: "cancel me"})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("executor did not start")
	}
	if err := runtime.Cancel(context.Background(), snapshot.ID); err != nil {
		t.Fatal(err)
	}
	got := waitGoal(t, runtime, snapshot.ID, GoalCancelled)
	if got.Status != GoalCancelled {
		t.Fatalf("status = %s", got.Status)
	}
}

type cancelAwareExecutor struct{ started chan struct{} }

func (e *cancelAwareExecutor) ExecuteTurn(ctx context.Context, _ message.Message, _ *loop.TurnTiming) (loop.TurnExecution, error) {
	select {
	case e.started <- struct{}{}:
	default:
	}
	<-ctx.Done()
	return loop.TurnExecution{}, ctx.Err()
}
