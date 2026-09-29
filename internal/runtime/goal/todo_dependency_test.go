package goal

import (
	"encoding/json"
	"paw/internal/todo"
	"strings"
	"testing"
)

func goalDependencySnapshot(t *testing.T, raw string) todo.Snapshot {
	t.Helper()
	var snapshot todo.Snapshot
	if err := json.Unmarshal([]byte(raw), &snapshot); err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func TestTodoDependencyGoalContinuesPrerequisiteThenOriginalItem(t *testing.T) {
	snapshot := goalDependencySnapshot(t, `{"items":[{"id":"work","content":"实现主任务","status":"pending","blocked_by":["repair"]},{"id":"repair","content":"修复前置问题","status":"in_progress"}]}`)
	evaluator := NewEvaluator(DefaultPolicy())
	observation := Observation{GoalID: "g1", Metric: testMetric(), HasTodo: true, Todo: snapshot}
	decision := evaluator.Evaluate(observation)
	ready, waiting, ok := strings.Cut(decision.NextPrompt.Content, "等待前置事项：")
	if decision.Action != ActionContinue || pendingTodos(snapshot) != 2 || !ok || strings.Contains(ready, "实现主任务") || !strings.Contains(ready, "#repair 修复前置问题") || !strings.Contains(waiting, "#work 实现主任务（等待：#repair）") {
		t.Fatalf("goal did not prioritize prerequisite: %+v", decision)
	}
	observation.Todo.Items[1].Status = todo.StatusCompleted
	observation.ContinuationUsed = 1
	decision = evaluator.Evaluate(observation)
	if decision.Action != ActionContinue || !strings.Contains(decision.NextPrompt.Content, "[pending] #work 实现主任务") || strings.Contains(decision.NextPrompt.Content, "等待前置事项：") || strings.Contains(decision.NextPrompt.Content, "修复前置问题") {
		t.Fatalf("goal did not resume original item: %+v", decision)
	}
	observation.Todo.Items[0].Status = todo.StatusCompleted
	observation.ContinuationUsed = 2
	observation.Measurement = &MetricResult{ToolCallID: "measure", Value: 0.95, Digest: "verified"}
	observation.MeasurementValid = true
	if decision := evaluator.Evaluate(observation); decision.Action != ActionComplete {
		t.Fatalf("goal did not complete: %+v", decision)
	}
}

func TestTodoDependencyGoalFingerprintDoesNotCountBookkeepingAsExperiment(t *testing.T) {
	const raw = `{"items":[{"id":"work","content":"实现主任务","status":"pending","blocked_by":["repair","review"]}]}`
	first := Observation{Todo: goalDependencySnapshot(t, raw)}
	removed := Observation{Todo: goalDependencySnapshot(t, strings.ReplaceAll(raw, `,"blocked_by":["repair","review"]`, ""))}
	reversed := Observation{Todo: goalDependencySnapshot(t, strings.ReplaceAll(raw, `["repair","review"]`, `["review","repair"]`))}
	if fingerprint(first) != fingerprint(removed) {
		t.Fatal("dependency bookkeeping incorrectly counted as an experiment")
	}
	if fingerprint(first) != fingerprint(reversed) {
		t.Fatal("dependency order alone changed goal fingerprint")
	}
}
