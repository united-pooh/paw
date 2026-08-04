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
	if prompt == "" || !containsAll(prompt, "run tests", "todo remains") {
		t.Fatalf("prompt = %q", prompt)
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
