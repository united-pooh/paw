package goal

import (
	"context"
	"fmt"
	"paw/internal/message"
	"paw/internal/runtime/loop"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestGoalSubmitRejectsInputAfterCompletionAdmissionCloses(t *testing.T) {
	ctx := context.Background()
	runtime := NewRuntime(RuntimeConfig{})
	if err := runtime.store.Create(ctx, Goal{ID: "g", SessionID: "s", Objective: "measurable result", Status: GoalRunning}); err != nil {
		t.Fatal(err)
	}
	runtime.active, runtime.activeID = true, "g"
	controller := &SessionController{host: &fakeGoalSessionHost{sessionID: "s"}, runtime: runtime, activeID: "g", sessionID: "s"}
	adapter := &completionAdapter{runtime: runtime, goalID: "g", evaluator: NewEvaluator(DefaultPolicy())}
	decision := adapter.EvaluateCompletion(loop.TurnExecution{}, 0, 0)
	if decision.Decision.Action != loop.CompletionPause {
		t.Fatalf("expected final pause decision, got %+v", decision)
	}
	if _, err := controller.Submit("must not be lost at the final boundary"); err == nil {
		t.Fatal("accepted input after the final dequeue, before paused state persisted")
	}
	if runtime.nextInput != "" {
		t.Fatalf("rejected input leaked into pending queue: %q", runtime.nextInput)
	}
}

func TestGoalFailedResumePreservesPreviouslyAcceptedInput(t *testing.T) {
	runtime := NewRuntime(RuntimeConfig{})
	runtime.nextInput = "accepted earlier"
	if err := runtime.ResumeWithInput(context.Background(), "missing", "rejected new input"); err == nil {
		t.Fatal("resumed missing goal")
	}
	if runtime.nextInput != "accepted earlier" {
		t.Fatalf("failed resume replaced accepted input: %q", runtime.nextInput)
	}
}

func TestMetricGoalReachingTargetAtBudgetBoundaryCompletes(t *testing.T) {
	evaluator := NewEvaluator(Policy{Budget: GoalBudget{MaxTurns: 2, MaxToolCalls: 3, MaxContinuations: 1}})
	decision := evaluator.Evaluate(Observation{Metric: testMetric(), Measurement: &MetricResult{Value: .94}, MeasurementValid: true, TurnsUsed: 2, ToolCallsUsed: 3, ContinuationUsed: 1})
	if decision.Action != ActionComplete {
		t.Fatalf("last allowed experiment met target but was not completed: %+v", decision)
	}
}

func TestGoalQueuedSupplementCannotBypassExhaustedBudget(t *testing.T) {
	ctx := context.Background()
	history := metricHistory("verified", `{"accuracy":0.94}`)
	_, digest, err := extractMetric(*testMetric(), "verified", history)
	if err != nil {
		t.Fatal(err)
	}
	runtime := NewRuntime(RuntimeConfig{History: func(context.Context) ([]message.Message, error) { return history, nil }})
	if err := runtime.store.Create(ctx, Goal{ID: "g", SessionID: "s", Objective: "measurable result", Status: GoalRunning, Metric: testMetric(), TurnsUsed: 2}); err != nil {
		t.Fatal(err)
	}
	goal, _, _ := runtime.store.Get(ctx, "g")
	goal.Measurements = []MetricResult{{ToolCallID: "verified", Value: .94, Digest: digest, RecordedAt: time.Now()}}
	if err := runtime.store.Update(ctx, goal); err != nil {
		t.Fatal(err)
	}
	runtime.active, runtime.activeID, runtime.nextInput = true, "g", "additional user constraint"
	adapter := &completionAdapter{runtime: runtime, goalID: "g", evaluator: NewEvaluator(Policy{Budget: GoalBudget{MaxTurns: 2}})}
	decision := adapter.EvaluateCompletion(loop.TurnExecution{}, 0, 0)
	if decision.Decision.Action != loop.CompletionPause || runtime.nextInput != "additional user constraint" {
		t.Fatalf("pending input exceeded budget or disappeared: %+v queue=%q", decision, runtime.nextInput)
	}
}

type goalStoppingExecutor struct {
	started chan struct{}
	release chan struct{}
}

func (e goalStoppingExecutor) ExecuteTurn(ctx context.Context, _ message.Message, _ *loop.TurnTiming) (loop.TurnExecution, error) {
	close(e.started)
	<-ctx.Done()
	<-e.release
	return loop.TurnExecution{}, ctx.Err()
}

func TestGoalStopEventWaitsForExecutorToExit(t *testing.T) {
	for _, cancel := range []bool{false, true} {
		t.Run(fmt.Sprint("cancel=", cancel), func(t *testing.T) {
			started, release := make(chan struct{}), make(chan struct{})
			var releaseOnce sync.Once
			defer releaseOnce.Do(func() { close(release) })
			stopped := make(chan Event, 4)
			runtime := NewRuntime(RuntimeConfig{Executor: goalStoppingExecutor{started: started, release: release}, Events: func(event Event) {
				if event.Type == EventPaused || event.Type == EventCancelled {
					stopped <- event
				}
			}})
			snapshot, err := runtime.Start(context.Background(), Goal{Objective: "measured goal"})
			if err != nil {
				t.Fatal(err)
			}
			<-started
			if cancel {
				err = runtime.Cancel(context.Background(), snapshot.ID)
			} else {
				err = runtime.Pause(context.Background(), snapshot.ID)
			}
			if err != nil {
				t.Fatal(err)
			}
			select {
			case event := <-stopped:
				t.Fatalf("stopped callback fired while executor still owns session: %+v", event)
			default:
			}
			releaseOnce.Do(func() { close(release) })
			select {
			case <-stopped:
				runtime.mu.Lock()
				active := runtime.active
				runtime.mu.Unlock()
				if active {
					t.Fatal("stopped callback preceded active cleanup")
				}
			case <-time.After(time.Second):
				t.Fatal("stopped callback was lost after executor exit")
			}
			select {
			case event := <-stopped:
				t.Fatalf("duplicate stopped callback: %+v", event)
			default:
			}
		})
	}
}

func TestGoalStopNotificationBlocksNewAdmissionUntilDelivered(t *testing.T) {
	for _, action := range []string{"start", "resume", "submit"} {
		t.Run(action, func(t *testing.T) {
			entered, release := make(chan struct{}), make(chan struct{})
			var releaseOnce, notifyOnce sync.Once
			defer releaseOnce.Do(func() { close(release) })
			var runtime *Runtime
			runtime = NewRuntime(RuntimeConfig{Executor: &fakeExecutor{}, Events: func(event Event) {
				if event.Type == EventPaused {
					notifyOnce.Do(func() {
						_, _ = runtime.Status(context.Background(), event.GoalID)
						runtime.mu.Lock()
						runtime.mu.Unlock()
						close(entered)
						<-release
					})
				}
			}})
			snapshot, err := runtime.Start(context.Background(), Goal{SessionID: "s", Objective: "quantified goal"})
			if err != nil {
				t.Fatal(err)
			}
			select {
			case <-entered:
			case <-time.After(time.Second):
				t.Fatal("callback blocked on a runtime lock or was never delivered")
			}
			switch action {
			case "start":
				_, err = runtime.Start(context.Background(), Goal{Objective: "new goal"})
			case "resume":
				err = runtime.Resume(context.Background(), snapshot.ID)
			case "submit":
				controller := &SessionController{host: &fakeGoalSessionHost{sessionID: "s"}, runtime: runtime, activeID: snapshot.ID, sessionID: "s"}
				_, err = controller.Submit("supplement")
			}
			if err == nil {
				t.Fatal("accepted a new run before the old stopped callback was delivered")
			}
			releaseOnce.Do(func() { close(release) })
			deadline := time.Now().Add(time.Second)
			for time.Now().Before(deadline) {
				if err := runtime.Resume(context.Background(), snapshot.ID); err == nil {
					return
				}
				time.Sleep(time.Millisecond)
			}
			t.Fatal("goal could not resume after stopped callback returned")
		})
	}
}

func TestGoalQueuedInputPreservesObjectiveMetricAndSupplement(t *testing.T) {
	ctx := context.Background()
	queue := NewMemoryGoalInputQueue()
	if err := queue.Enqueue(ctx, GoalInput{ID: "input", GoalID: "g", Kind: GoalInputClarify, Content: "queued clarification"}); err != nil {
		t.Fatal(err)
	}
	executor := &fakeExecutor{}
	runtime := NewRuntime(RuntimeConfig{Executor: executor, Inputs: queue})
	if err := runtime.store.Create(ctx, Goal{ID: "g", Objective: "original objective", Status: GoalRunning, Metric: testMetric(), Budget: GoalBudget{MaxTurns: 1}}); err != nil {
		t.Fatal(err)
	}
	runtime.nextInput = "accepted supplement"
	if err := runtime.Run(ctx, "g"); err != nil {
		t.Fatal(err)
	}
	executor.mu.Lock()
	defer executor.mu.Unlock()
	if len(executor.calls) != 1 {
		t.Fatalf("turns = %d", len(executor.calls))
	}
	for _, want := range []string{"original objective", "accuracy", "python3 evaluate.py", "accepted supplement", "queued clarification"} {
		if !strings.Contains(executor.calls[0].Content, want) {
			t.Errorf("initial prompt lost %q: %s", want, executor.calls[0].Content)
		}
	}
}
