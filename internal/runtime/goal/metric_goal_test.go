package goal

import (
	"paw/internal/message"
	"paw/internal/todo"
	"testing"
)

func TestMetricGoalWithoutMetricWaitsForClarification(t *testing.T) {
	for _, snapshot := range []todo.Snapshot{{}, {Items: []todo.Item{{ID: "done", Content: "Done", Status: todo.StatusCompleted}}}, {Items: []todo.Item{{ID: "work", Content: "Work", Status: todo.StatusPending}}}} {
		decision := NewEvaluator(DefaultPolicy()).Evaluate(Observation{HasTodo: true, Todo: snapshot})
		if decision.Action != ActionPause || decision.PauseReason != PauseUserInputRequired {
			t.Fatalf("missing measurable acceptance must remain unfinished and request clarification; got %+v", decision)
		}
	}
}

func TestMetricGoalExploresWithoutTodoUntilVerified(t *testing.T) {
	evaluator := NewEvaluator(DefaultPolicy())
	observation := Observation{Metric: testMetric(), ProgressToken: "experiment-1", HadToolActivity: true}
	if decision := evaluator.Evaluate(observation); decision.Action != ActionContinue {
		t.Fatalf("no TODO ended exploration: %+v", decision)
	}
	observation.Measurement = &MetricResult{ToolCallID: "low", Value: 0.7, Digest: "verified-low"}
	observation.MeasurementValid = true
	observation.ProgressToken = "experiment-1,experiment-2"
	if decision := evaluator.Evaluate(observation); decision.Action != ActionContinue {
		t.Fatalf("low metric ended exploration: %+v", decision)
	}
	observation.Measurement = &MetricResult{ToolCallID: "high", Value: 0.94, Digest: "verified-high"}
	observation.MeasurementValid = false
	if decision := evaluator.Evaluate(observation); decision.Action == ActionComplete {
		t.Fatalf("unverified report completed goal: %+v", decision)
	}
	observation.MeasurementValid = true
	if decision := evaluator.Evaluate(observation); decision.Action != ActionComplete {
		t.Fatalf("verified metric did not complete goal: %+v", decision)
	}
}

func TestMetricGoalNarrativeAndTodoChangesAreNotExperiments(t *testing.T) {
	first := Observation{GoalID: "g", Metric: testMetric(), Todo: todo.Snapshot{Items: []todo.Item{{ID: "work", Content: "Work", Status: todo.StatusPending}}}}
	changed := first
	changed.Assistant = message.Message{Content: "A different explanation"}
	changed.Todo = todo.Snapshot{Items: []todo.Item{{ID: "work", Content: "Renamed task", Status: todo.StatusCompleted}}}
	if fingerprint(first) != fingerprint(changed) {
		t.Fatal("narrative or TODO bookkeeping counts as an experiment")
	}
	changed.ProgressToken = "real-failed-experiment-call"
	if fingerprint(first) == fingerprint(changed) {
		t.Fatal("new real experiment did not count as progress")
	}
}

func TestMetricGoalInvestigationCanPrepareMissingMetric(t *testing.T) {
	decision := NewEvaluator(DefaultPolicy()).Evaluate(Observation{HadToolActivity: true, ProgressToken: "read-evaluation-code"})
	if decision.Action != ActionContinue {
		t.Fatalf("useful measurement investigation was prematurely paused: %+v", decision)
	}
}
