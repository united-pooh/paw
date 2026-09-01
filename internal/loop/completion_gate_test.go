package loop

import (
	"paw/internal/message"
	"paw/internal/todo"
	"strings"
	"testing"
)

func TestCompletionGateContinuesForUnfinishedTodo(t *testing.T) {
	gate := CompletionGate{Config: AutoContinueConfig{Enabled: true, BaseBudget: 2, AbsoluteMax: 12, MaxNoProgress: 2}}
	decision := gate.Evaluate(CompletionObservation{
		HasTodo: true,
		Todo:    todo.Snapshot{Items: []todo.Item{{ID: "one", Content: "finish", Status: todo.StatusInProgress}}},
	})
	if decision.Action != CompletionContinue {
		t.Fatalf("action = %q, want %q", decision.Action, CompletionContinue)
	}
	if decision.BudgetLimit != 3 {
		t.Fatalf("budget limit = %d, want 3", decision.BudgetLimit)
	}
}

func TestCompletionGateCompletesWithoutUnfinishedTodo(t *testing.T) {
	gate := CompletionGate{Config: DefaultAutoContinueConfig()}
	decision := gate.Evaluate(CompletionObservation{
		HasTodo:   true,
		Todo:      todo.Snapshot{Items: []todo.Item{{ID: "one", Content: "finish", Status: todo.StatusCompleted}}},
		Assistant: message.Message{Role: message.RoleAssistant, Content: "done"},
	})
	if decision.Action != CompletionComplete {
		t.Fatalf("action = %q, want %q", decision.Action, CompletionComplete)
	}
}

func TestCompletionGatePausesAfterNoProgress(t *testing.T) {
	gate := CompletionGate{Config: AutoContinueConfig{Enabled: true, BaseBudget: 2, AbsoluteMax: 12, MaxNoProgress: 2}}
	decision := gate.Evaluate(CompletionObservation{
		HasTodo:         true,
		Todo:            todo.Snapshot{Items: []todo.Item{{ID: "one", Content: "finish", Status: todo.StatusPending}}},
		NoProgressCount: 2,
	})
	if decision.Action != CompletionPause {
		t.Fatalf("action = %q, want %q", decision.Action, CompletionPause)
	}
	if decision.PauseKind != PauseNoProgress {
		t.Fatalf("pause kind = %q, want %q", decision.PauseKind, PauseNoProgress)
	}
}

func TestCompletionGatePauseKindClassified(t *testing.T) {
	gate := CompletionGate{Config: AutoContinueConfig{Enabled: true, BaseBudget: 1, AbsoluteMax: 12, MaxNoProgress: 2}}
	pending := todo.Snapshot{Items: []todo.Item{{ID: "one", Status: todo.StatusPending}}}
	budget := gate.Evaluate(CompletionObservation{HasTodo: true, Todo: pending, ContinuationUsed: 2})
	if budget.Action != CompletionPause || budget.PauseKind != PauseBudgetExhausted {
		t.Fatalf("budget decision = %#v, want pause/%q", budget, PauseBudgetExhausted)
	}
	maintenance := gate.Evaluate(CompletionObservation{HasTodo: true, Todo: pending, ContextNeedsMaintenance: true})
	if maintenance.Action != CompletionCompact || maintenance.PauseKind != PauseContextMaintenance {
		t.Fatalf("maintenance decision = %#v, want compact/%q", maintenance, PauseContextMaintenance)
	}
}

func TestProgressFingerprintChangesWithTodo(t *testing.T) {
	first := progressFingerprint(todo.Snapshot{Items: []todo.Item{{ID: "one", Status: todo.StatusPending}}}, message.Message{Content: "check"}, false)
	second := progressFingerprint(todo.Snapshot{Items: []todo.Item{{ID: "one", Status: todo.StatusCompleted}}}, message.Message{Content: "check"}, false)
	if first == second {
		t.Fatal("progress fingerprint did not change after todo status changed")
	}
}

func TestBuildContinuationPromptIncludesPendingItems(t *testing.T) {
	prompt := buildContinuationPrompt(CompletionDecision{Reason: "todo remains"}, todo.Snapshot{Items: []todo.Item{
		{ID: "one", Content: "run tests", Status: todo.StatusPending},
		{ID: "two", Content: "done", Status: todo.StatusCompleted},
	}}, 0)
	if prompt == "" || !containsAll(prompt, "run tests", "todo remains", "直接执行下一项最有价值的工作") {
		t.Fatalf("prompt = %q", prompt)
	}
}

func TestCompletionGateStaleTodoTurnsThreshold(t *testing.T) {
	gate := CompletionGate{Config: AutoContinueConfig{Enabled: true, BaseBudget: 2, AbsoluteMax: 12, MaxNoProgress: 2, StaleTodoThreshold: 3}}
	obs := CompletionObservation{
		HasTodo: true,
		Todo:    todo.Snapshot{Items: []todo.Item{{ID: "one", Status: todo.StatusPending}}},
	}
	obs.StaleTodoTurns = 2
	decision := gate.Evaluate(obs)
	if decision.StaleTodoTurns != 2 || decision.StaleTodoReminder {
		t.Fatalf("below threshold stale = %d reminder=%t, want 2/false", decision.StaleTodoTurns, decision.StaleTodoReminder)
	}
	obs.StaleTodoTurns = 3
	decision = gate.Evaluate(obs)
	if decision.StaleTodoTurns != 3 || !decision.StaleTodoReminder {
		t.Fatalf("at threshold stale = %d reminder=%t, want 3/true", decision.StaleTodoTurns, decision.StaleTodoReminder)
	}
	disabled := CompletionGate{Config: AutoContinueConfig{Enabled: true, BaseBudget: 2, AbsoluteMax: 12, MaxNoProgress: 2}}
	obs.StaleTodoTurns = 5
	decision = disabled.Evaluate(obs)
	if decision.StaleTodoTurns != 5 || decision.StaleTodoReminder {
		t.Fatalf("disabled threshold stale = %d reminder=%t, want 5/false", decision.StaleTodoTurns, decision.StaleTodoReminder)
	}
}

func TestBuildContinuationPromptStaleTodoReminder(t *testing.T) {
	prompt := buildContinuationPrompt(CompletionDecision{Action: CompletionContinue, Reason: "work remains", StaleTodoTurns: 3, StaleTodoReminder: true}, todo.Snapshot{Items: []todo.Item{
		{ID: "one", Content: "run tests", Status: todo.StatusPending},
	}}, 0)
	if !containsAll(prompt, "todo 快照已连续 3 轮未更新", "update_todo") {
		t.Fatalf("prompt = %q", prompt)
	}
	plain := buildContinuationPrompt(CompletionDecision{Action: CompletionContinue, Reason: "work remains", StaleTodoTurns: 3}, todo.Snapshot{Items: []todo.Item{
		{ID: "one", Content: "run tests", Status: todo.StatusPending},
	}}, 0)
	if containsAll(plain, "todo 快照已连续") {
		t.Fatalf("reminder shown without flag: %q", plain)
	}
}

func TestTodoFingerprintChangesWithStatus(t *testing.T) {
	first := todoFingerprint(todo.Snapshot{Items: []todo.Item{{ID: "one", Status: todo.StatusPending}}})
	second := todoFingerprint(todo.Snapshot{Items: []todo.Item{{ID: "one", Status: todo.StatusCompleted}}})
	if first == second {
		t.Fatal("todo fingerprint did not change after status changed")
	}
	third := todoFingerprint(todo.Snapshot{Items: []todo.Item{{ID: "one", Status: todo.StatusCompleted}}})
	if third != second {
		t.Fatal("todo fingerprint not stable for identical snapshot")
	}
}

func containsAll(value string, parts ...string) bool {
	for _, part := range parts {
		if !strings.Contains(value, part) {
			return false
		}
	}
	return true
}
