package goal

import (
	"context"
	"errors"
	"paw/internal/loop"
	"paw/internal/message"
	"paw/internal/todo"
	"testing"
)

func pendingGoalTodo() todo.Snapshot {
	return todo.Snapshot{Items: []todo.Item{{ID: "step-1", Content: "run tests", Status: todo.StatusInProgress}}}
}

func TestEvaluatorContinuesForPendingTodo(t *testing.T) {
	d := NewEvaluator(DefaultPolicy()).Evaluate(Observation{GoalID: "g1", HasTodo: true, Todo: pendingGoalTodo()})
	if d.Action != ActionContinue || d.NextPrompt.Content == "" {
		t.Fatalf("decision = %+v", d)
	}
	if d.NextPrompt.Synthetic != message.SyntheticAutoContinue {
		t.Fatalf("next prompt synthetic = %q, want %q", d.NextPrompt.Synthetic, message.SyntheticAutoContinue)
	}
}

func TestEvaluatorCompletesOnlyWithoutPendingTodo(t *testing.T) {
	d := NewEvaluator(DefaultPolicy()).Evaluate(Observation{HasTodo: true, Todo: todo.Snapshot{Items: []todo.Item{{ID: "step-1", Status: todo.StatusCompleted}}}})
	if d.Action != ActionComplete {
		t.Fatalf("action = %q", d.Action)
	}
}

func TestEvaluatorMapsMaintenanceToCompact(t *testing.T) {
	d := NewEvaluator(DefaultPolicy()).Evaluate(Observation{HasTodo: true, Todo: pendingGoalTodo(), ContextNeedsMaintenance: true})
	if d.Action != ActionCompact {
		t.Fatalf("action = %q", d.Action)
	}
}

func TestEvaluatorPausesAtBudget(t *testing.T) {
	p := DefaultPolicy()
	p.Budget.MaxContinuations = 1
	d := NewEvaluator(p).Evaluate(Observation{HasTodo: true, Todo: pendingGoalTodo(), ContinuationUsed: 1})
	if d.Action != ActionPause || d.PauseReason != PauseBudgetExhausted {
		t.Fatalf("decision = %+v", d)
	}
}

func TestEvaluatorPausesAfterRepeatedFingerprint(t *testing.T) {
	p := DefaultPolicy()
	p.Budget.MaxNoProgress = 2
	e := NewEvaluator(p)
	o := Observation{GoalID: "g1", HasTodo: true, Todo: pendingGoalTodo(), Assistant: message.Message{Content: "same"}}
	first, second, third := e.Evaluate(o), e.Evaluate(o), e.Evaluate(o)
	if first.Action != ActionContinue || second.Action != ActionContinue {
		t.Fatalf("first two = %+v, %+v", first, second)
	}
	if third.Action != ActionPause || third.PauseReason != PauseNoProgress {
		t.Fatalf("third = %+v", third)
	}
}

func TestEvaluatorHonorsExistingGateDecision(t *testing.T) {
	d := NewEvaluator(DefaultPolicy()).Evaluate(Observation{HasTodo: true, Todo: pendingGoalTodo(), GateDecision: loop.CompletionDecision{Action: loop.CompletionBlocked, Reason: "policy blocked"}})
	if d.Action != ActionBlocked || d.PauseReason != PauseBlocked {
		t.Fatalf("decision = %+v", d)
	}
}

func TestClassifyError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want PauseReason
	}{
		{"permission", errors.New("permission denied by policy"), PausePermissionRequired},
		{"unsafe", errors.New("unsafe command rejected"), PauseDangerousCommand},
		{"blocked", errors.New("blocked: command is not allowed"), PauseBlocked},
		{"cancelled", context.Canceled, PauseUserInputRequired},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ClassifyError(tc.err); got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}
