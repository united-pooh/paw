package goal

import (
	"context"
	"encoding/json"
	"os"
	"paw/internal/message"
	"paw/internal/runtime/loop"
	"strings"
	"testing"
	"time"
)

func TestMetricRecordingRejectsHistoricalDuplicateAndStaleEvidence(t *testing.T) {
	ctx := context.Background()
	history := metricHistory("old", `{"accuracy":0.99}`)
	runtime := NewRuntime(RuntimeConfig{History: func(context.Context) ([]message.Message, error) { return history, nil }})
	if err := runtime.store.Create(ctx, Goal{ID: "g", SessionID: "s", Objective: "accuracy at least 0.9", Status: GoalRunning}); err != nil {
		t.Fatal(err)
	}
	runtime.active, runtime.activeID = true, "g"
	if err := runtime.ConfigureMetric(ctx, "g", *testMetric()); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.RecordMetric(ctx, "g", "old"); err == nil {
		t.Fatal("accepted pre-definition evidence")
	}
	history = append(history, metricHistory("low", `{"accuracy":0.72}`)...)
	if result, err := runtime.RecordMetric(ctx, "g", "low"); err != nil || result.Value != 0.72 {
		t.Fatalf("low experiment = %+v %v", result, err)
	}
	if _, err := runtime.RecordMetric(ctx, "g", "low"); err == nil {
		t.Fatal("accepted duplicate evidence")
	}
	history = append(history, metricHistory("high", `{"accuracy":0.95}`)...)
	history = append(history, message.Message{Role: message.RoleAssistant, ToolUse: &message.ToolCall{ID: "edit", Name: "Write"}}, message.Message{Role: message.RoleUser, ToolResult: &message.ToolResult{ToolUseID: "edit", Content: "changed"}})
	if _, err := runtime.RecordMetric(ctx, "g", "high"); err == nil {
		t.Fatal("accepted measurement before later mutation")
	}
	history = append(history, metricHistory("fresh", `{"accuracy":0.96}`)...)
	if _, err := runtime.RecordMetric(ctx, "g", "fresh"); err != nil {
		t.Fatal(err)
	}
	weak := *testMetric()
	weak.Target = 0.1
	if err := runtime.ConfigureMetric(ctx, "g", weak); err == nil {
		t.Fatal("allowed lowering the goal target")
	}
}

func TestMetricToolDoesNotAcceptSelfReportedValue(t *testing.T) {
	host := &fakeGoalSessionHost{sessionID: "s"}
	runtime := NewRuntime(RuntimeConfig{})
	if err := runtime.store.Create(context.Background(), Goal{ID: "g", SessionID: "s", Objective: "accuracy", Status: GoalRunning, Metric: testMetric()}); err != nil {
		t.Fatal(err)
	}
	controller := &SessionController{host: host, runtime: runtime, activeID: "g", sessionID: "s"}
	ctx := context.WithValue(context.Background(), goalExecutionContextKey{}, GoalID("g"))
	_, err := NewRecordMetricTool(controller).Run(ctx, json.RawMessage(`{"tool_call_id":"made-up","value":1,"passed":true}`))
	if err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("self-reported value accepted: %v", err)
	}
	if _, err := NewRecordMetricTool(controller).Run(context.Background(), json.RawMessage(`{"tool_call_id":"made-up"}`)); err == nil || !strings.Contains(err.Error(), "not Chat or Plan") {
		t.Fatalf("metric tool was callable outside Goal: %v", err)
	}
}

func TestMetricSetToolRejectsMissingNullAndInvalidPointerBeforeFreeze(t *testing.T) {
	base := `{"name":"accuracy","comparison":"gte","target":0,"unit":"ratio","measurement":{"tool":"Bash","command":"python3 evaluate.py","cwd":"","json_pointer":"/accuracy"}}`
	for _, test := range []struct{ name, raw string }{
		{"missing target", strings.Replace(base, `"target":0,`, "", 1)},
		{"null target", strings.Replace(base, `"target":0`, `"target":null`, 1)},
		{"missing cwd", strings.Replace(base, `"cwd":"",`, "", 1)},
		{"null cwd", strings.Replace(base, `"cwd":""`, `"cwd":null`, 1)},
		{"bad pointer escape", strings.Replace(base, `/accuracy`, `/accuracy~x`, 1)},
		{"trailing pointer escape", strings.Replace(base, `/accuracy`, `/accuracy~`, 1)},
	} {
		t.Run(test.name, func(t *testing.T) {
			host := &fakeGoalSessionHost{sessionID: "s"}
			runtime := NewRuntime(RuntimeConfig{History: func(context.Context) ([]message.Message, error) { return nil, nil }})
			if err := runtime.store.Create(context.Background(), Goal{ID: "g", SessionID: "s", Objective: "accuracy", Status: GoalRunning}); err != nil {
				t.Fatal(err)
			}
			controller := &SessionController{host: host, runtime: runtime, activeID: "g", sessionID: "s"}
			ctx := context.WithValue(context.Background(), goalExecutionContextKey{}, GoalID("g"))
			if _, err := NewSetMetricTool(controller).Run(ctx, json.RawMessage(test.raw)); err == nil {
				t.Fatal("invalid metric contract was frozen")
			}
			goal, _, _ := runtime.store.Get(ctx, "g")
			if goal.Metric != nil {
				t.Fatal("invalid input mutated the frozen goal")
			}
			if _, err := NewSetMetricTool(controller).Run(ctx, json.RawMessage(base)); err != nil {
				t.Fatalf("explicit numeric zero must remain valid: %v", err)
			}
		})
	}
}

func TestMetricGoalBudgetResumeHasFreshWindow(t *testing.T) {
	executor := &fakeExecutor{}
	runtime := NewRuntime(RuntimeConfig{Executor: executor, Policy: Policy{Budget: GoalBudget{MaxTurns: 2, MaxContinuations: 12, MaxNoProgress: 10}}})
	snapshot, err := runtime.Start(context.Background(), Goal{Objective: "reach measured target", Metric: testMetric()})
	if err != nil {
		t.Fatal(err)
	}
	first := waitGoal(t, runtime, snapshot.ID, GoalPaused)
	if first.PauseReason != PauseBudgetExhausted || executor.count() != 2 {
		t.Fatalf("first budget = %+v calls=%d", first, executor.count())
	}
	if err := runtime.Resume(context.Background(), snapshot.ID); err != nil {
		t.Fatal(err)
	}
	second := waitGoal(t, runtime, snapshot.ID, GoalPaused)
	if second.TurnsUsed != 4 || executor.count() != 4 || second.Status == GoalCompleted {
		t.Fatalf("resume did not receive a fresh two-turn window: %+v calls=%d", second, executor.count())
	}
}

func TestMetricGoalRecoveryWithoutMetricStaysUnfinished(t *testing.T) {
	store := NewMemoryStore()
	if err := store.Create(context.Background(), Goal{ID: "legacy", Objective: "legacy work", Status: GoalRunning, Budget: DefaultPolicy().Budget}); err != nil {
		t.Fatal(err)
	}
	runtime := NewRuntime(RuntimeConfig{Store: store, Executor: &fakeExecutor{results: []loop.TurnExecution{{Message: message.Message{Content: "done"}}}}})
	if _, err := runtime.Recover(context.Background(), "legacy"); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Resume(context.Background(), "legacy"); err != nil {
		t.Fatal(err)
	}
	if snapshot := waitGoal(t, runtime, "legacy", GoalPaused); snapshot.PauseReason != PauseUserInputRequired {
		t.Fatalf("legacy goal inferred success: %+v", snapshot)
	}
}

func TestMetricStoreRejectsInvalidEvidenceBeforeWriting(t *testing.T) {
	for _, makeStore := range []func() GoalStore{func() GoalStore { return NewMemoryStore() }, func() GoalStore {
		s, err := NewEventStore(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		return s
	}} {
		store := makeStore()
		ctx := context.Background()
		if err := store.Create(ctx, Goal{ID: "g", SessionID: "s", Objective: "metric", Metric: testMetric()}); err != nil {
			t.Fatal(err)
		}
		goal, _, _ := store.Get(ctx, "g")
		goal.Measurements = []MetricResult{{ToolCallID: "x", Value: 1, RecordedAt: time.Now()}}
		if err := store.Update(ctx, goal); err == nil {
			t.Fatal("invalid evidence was accepted")
		}
		if goals, err := store.List(ctx, "s"); err != nil || len(goals) != 1 {
			t.Fatalf("rejected evidence damaged goal stream: %+v %v", goals, err)
		}
	}
}

func TestMetricStoreListReportsCorruptGoal(t *testing.T) {
	store, err := NewEventStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := store.Create(ctx, Goal{ID: "g", SessionID: "s", Objective: "metric"}); err != nil {
		t.Fatal(err)
	}
	path, _ := store.events.StreamPath("g")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append([]byte("broken-event\n"), raw...), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.List(ctx, "s"); err == nil {
		t.Fatal("corrupt goal silently disappeared from recovery listing")
	}
}
