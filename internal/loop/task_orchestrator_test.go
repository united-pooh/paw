package loop

import (
	"context"
	"errors"
	"paw/internal/message"
	"testing"
)

type orchestratorExecutor struct {
	inputs []message.Message
	turns  []TurnExecution
	err    error
}

func (e *orchestratorExecutor) ExecuteTurn(_ context.Context, input message.Message, _ *TurnTiming) (TurnExecution, error) {
	e.inputs = append(e.inputs, input)
	if e.err != nil {
		return TurnExecution{}, e.err
	}
	index := len(e.inputs) - 1
	return e.turns[index], nil
}

type orchestratorEvaluator struct {
	evaluations []CompletionEvaluation
	calls       []struct{ used, noProgress int }
}

func (e *orchestratorEvaluator) EvaluateCompletion(_ message.Message, used, noProgress int) CompletionEvaluation {
	e.calls = append(e.calls, struct{ used, noProgress int }{used, noProgress})
	return e.evaluations[len(e.calls)-1]
}

func TestTaskOrchestratorContinuesAndRecordsTurns(t *testing.T) {
	executor := &orchestratorExecutor{turns: []TurnExecution{
		{Message: message.Message{Role: message.RoleAssistant, Content: "first"}},
		{Message: message.Message{Role: message.RoleAssistant, Content: "final"}},
	}}
	evaluator := &orchestratorEvaluator{evaluations: []CompletionEvaluation{
		{HasSignal: true, Decision: CompletionDecision{Action: CompletionContinue, Reason: "work remains"}, NoProgress: 1, NextInput: message.Message{Role: message.RoleUser, Content: "continue"}},
		{HasSignal: true, Decision: CompletionDecision{Action: CompletionComplete, Reason: "done"}},
	}}
	var events []TaskEvent
	task := &Task{ID: "task-1", Input: message.Message{Role: message.RoleUser, Content: "start"}}

	result, err := (TaskOrchestrator{Executor: executor, Evaluator: evaluator, Events: func(event TaskEvent) {
		events = append(events, event)
	}}).Run(context.Background(), task)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Message.Content != "final" || task.Status != TaskCompleted {
		t.Fatalf("result=%q status=%q", result.Message.Content, task.Status)
	}
	if len(task.Turns) != 2 || task.ContinuationUsed != 1 || task.NoProgressCount != 0 {
		t.Fatalf("task state = %#v", task)
	}
	if len(executor.inputs) != 2 || executor.inputs[1].Content != "continue" {
		t.Fatalf("executor inputs = %#v", executor.inputs)
	}
	if eventTypes(events)[0] != TaskEventStarted || eventTypes(events)[len(events)-1] != TaskEventCompleted {
		t.Fatalf("event types = %#v", eventTypes(events))
	}
}

func TestTaskOrchestratorPausesWithoutReturningError(t *testing.T) {
	executor := &orchestratorExecutor{turns: []TurnExecution{{Message: message.Message{Content: "blocked"}}}}
	evaluator := &orchestratorEvaluator{evaluations: []CompletionEvaluation{{
		HasSignal: true,
		Decision:  CompletionDecision{Action: CompletionPause, Reason: "budget exhausted"},
	}}}
	task := &Task{ID: "task-2", Input: message.Message{Role: message.RoleUser, Content: "start"}}

	result, err := (TaskOrchestrator{Executor: executor, Evaluator: evaluator}).Run(context.Background(), task)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Message.Content != "blocked" || task.Status != TaskPaused {
		t.Fatalf("result=%q status=%q", result.Message.Content, task.Status)
	}
}

func TestTaskOrchestratorMarksExecutorFailure(t *testing.T) {
	wantErr := errors.New("model failed")
	executor := &orchestratorExecutor{err: wantErr}
	task := &Task{ID: "task-3", Input: message.Message{Role: message.RoleUser, Content: "start"}}

	_, err := (TaskOrchestrator{Executor: executor}).Run(context.Background(), task)
	if !errors.Is(err, wantErr) || task.Status != TaskFailed {
		t.Fatalf("err=%v status=%q", err, task.Status)
	}
}

func TestTaskOrchestratorDefaultsContinuationRoleToUser(t *testing.T) {
	executor := &orchestratorExecutor{turns: []TurnExecution{
		{Message: message.Message{Content: "first"}},
		{Message: message.Message{Content: "done"}},
	}}
	evaluator := &orchestratorEvaluator{evaluations: []CompletionEvaluation{
		{HasSignal: true, Decision: CompletionDecision{Action: CompletionContinue}, NextInput: message.Message{Content: "next"}},
		{Decision: CompletionDecision{Action: CompletionComplete}, HasSignal: true},
	}}
	task := &Task{Input: message.Message{Role: message.RoleUser, Content: "start"}}
	if _, err := (TaskOrchestrator{Executor: executor, Evaluator: evaluator}).Run(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	if executor.inputs[1].Role != message.RoleUser {
		t.Fatalf("continuation role = %q", executor.inputs[1].Role)
	}
}

func eventTypes(events []TaskEvent) []string {
	result := make([]string, 0, len(events))
	for _, event := range events {
		result = append(result, event.Type)
	}
	return result
}

var _ TurnExecutor = (*orchestratorExecutor)(nil)
var _ CompletionEvaluator = (*orchestratorEvaluator)(nil)
