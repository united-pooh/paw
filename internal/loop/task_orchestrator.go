package loop

import (
	"context"
	"fmt"
	"paw/internal/message"
)

// TaskStatus describes the lifecycle of a task-level orchestration run.
type TaskStatus string

const (
	TaskRunning   TaskStatus = "running"
	TaskCompleted TaskStatus = "completed"
	TaskPaused    TaskStatus = "paused"
	TaskFailed    TaskStatus = "failed"
)

// Task is the in-memory state carried across independent model turns.
// It deliberately contains no persistence concerns; the turn executor owns
// journal writes and the caller may persist a checkpoint in a later phase.
type Task struct {
	ID               string
	Input            message.Message
	Status           TaskStatus
	ContinuationUsed int
	NoProgressCount  int
	Turns            []Turn
	LastExecution    TurnExecution
}

// Turn is one model execution, including an automatically generated
// continuation turn.
type Turn struct {
	Number    int
	Input     message.Message
	Execution TurnExecution
}

// TaskEvent is emitted at task/turn boundaries. Consumers can use it for UI,
// tracing, metrics, or a future durable task event log.
type TaskEvent struct {
	Type             string
	TaskID           string
	TurnNumber       int
	ContinuationUsed int
	Decision         CompletionDecision
	Execution        TurnExecution
	Error            error
}

const (
	TaskEventStarted   = "task.started"
	TaskEventTurnStart = "turn.started"
	TaskEventTurnDone  = "turn.completed"
	TaskEventContinued = "task.continued"
	TaskEventCompleted = "task.completed"
	TaskEventPaused    = "task.paused"
	TaskEventFailed    = "task.failed"
)

// TurnExecutor runs exactly one model/tool turn. It must not implement task
// continuation itself.
type TurnExecutor interface {
	ExecuteTurn(context.Context, message.Message, *TurnTiming) (TurnExecution, error)
}

// CompletionEvaluation is the boundary between task orchestration and the
// application's completion signals (todo, diff, validation, context state).
type CompletionEvaluation struct {
	Decision   CompletionDecision
	Snapshot   any
	HasSignal  bool
	NextInput  message.Message
	NoProgress int
}

// CompletionEvaluator evaluates one completed turn and returns the next input
// when the task should continue.
type CompletionEvaluator interface {
	EvaluateCompletion(message.Message, int, int) CompletionEvaluation
}

// TaskEventSink receives lifecycle events. A nil sink is valid.
type TaskEventSink func(TaskEvent)

// TaskOrchestrator owns the task-level continuation state machine.
type TaskOrchestrator struct {
	Executor  TurnExecutor
	Evaluator CompletionEvaluator
	Events    TaskEventSink
}

// Run executes the initial input and any evaluator-approved continuations.
// The orchestrator stops on completion, pause, blocked/failed decisions, or an
// executor error. Existing Runner behavior is preserved by treating a missing
// completion signal as completion.
func (o TaskOrchestrator) Run(ctx context.Context, task *Task) (TurnExecution, error) {
	if task == nil {
		return TurnExecution{}, fmt.Errorf("task is nil")
	}
	if o.Executor == nil {
		return TurnExecution{}, fmt.Errorf("task executor is nil")
	}
	if task.Status == "" {
		task.Status = TaskRunning
	}
	o.emit(TaskEvent{Type: TaskEventStarted, TaskID: task.ID})

	input := task.Input
	var timing *TurnTiming
	for {
		turnNumber := len(task.Turns) + 1
		o.emit(TaskEvent{Type: TaskEventTurnStart, TaskID: task.ID, TurnNumber: turnNumber, ContinuationUsed: task.ContinuationUsed})
		execution, err := o.Executor.ExecuteTurn(ctx, input, timing)
		if err != nil {
			task.Status = TaskFailed
			o.emit(TaskEvent{Type: TaskEventFailed, TaskID: task.ID, TurnNumber: turnNumber, Error: err})
			return execution, err
		}
		task.LastExecution = execution
		task.Turns = append(task.Turns, Turn{Number: turnNumber, Input: input, Execution: execution})
		o.emit(TaskEvent{Type: TaskEventTurnDone, TaskID: task.ID, TurnNumber: turnNumber, ContinuationUsed: task.ContinuationUsed, Execution: execution})

		if o.Evaluator == nil {
			task.Status = TaskCompleted
			o.emit(TaskEvent{Type: TaskEventCompleted, TaskID: task.ID, TurnNumber: turnNumber, Execution: execution})
			return execution, nil
		}
		evaluation := o.Evaluator.EvaluateCompletion(execution.Message, task.ContinuationUsed, task.NoProgressCount)
		task.NoProgressCount = evaluation.NoProgress
		decision := evaluation.Decision
		if !evaluation.HasSignal || decision.Action == CompletionComplete {
			task.Status = TaskCompleted
			o.emit(TaskEvent{Type: TaskEventCompleted, TaskID: task.ID, TurnNumber: turnNumber, Decision: decision, Execution: execution})
			return execution, nil
		}
		if decision.Action != CompletionContinue {
			task.Status = TaskPaused
			o.emit(TaskEvent{Type: TaskEventPaused, TaskID: task.ID, TurnNumber: turnNumber, Decision: decision, Execution: execution})
			return execution, nil
		}
		if evaluation.NextInput.Role == "" {
			evaluation.NextInput.Role = message.RoleUser
		}
		task.ContinuationUsed++
		input = evaluation.NextInput
		timing = nil
		o.emit(TaskEvent{Type: TaskEventContinued, TaskID: task.ID, TurnNumber: turnNumber, ContinuationUsed: task.ContinuationUsed, Decision: decision})
	}
}

func (o TaskOrchestrator) emit(event TaskEvent) {
	if o.Events != nil {
		o.Events(event)
	}
}

// runnerTurnExecutor adapts Runner's existing single-turn implementation to
// the task orchestration boundary without exposing Runner internals publicly.
type runnerTurnExecutor struct{ runner *Runner }

func (e runnerTurnExecutor) ExecuteTurn(ctx context.Context, input message.Message, timing *TurnTiming) (TurnExecution, error) {
	return e.runner.runSingleTurnWithTiming(ctx, input, timing)
}

// runnerCompletionEvaluator adapts the existing Completion Gate signals.
type runnerCompletionEvaluator struct{ runner *Runner }

func (e runnerCompletionEvaluator) EvaluateCompletion(assistant message.Message, used, noProgress int) CompletionEvaluation {
	decision, snapshot, hasTodo, nextNoProgress := e.runner.evaluateCompletion(assistant, false, used, noProgress)
	return CompletionEvaluation{
		Decision:   decision,
		Snapshot:   snapshot,
		HasSignal:  hasTodo,
		NoProgress: nextNoProgress,
		NextInput:  message.Message{Role: message.RoleUser, Content: buildContinuationPrompt(decision, snapshot, nextNoProgress)},
	}
}

func (runner *Runner) taskOrchestrator() TaskOrchestrator {
	return TaskOrchestrator{
		Executor:  runnerTurnExecutor{runner: runner},
		Evaluator: runnerCompletionEvaluator{runner: runner},
		Events: func(event TaskEvent) {
			if event.Type == TaskEventContinued || event.Type == TaskEventPaused {
				runner.notifyAutoContinue(event.Decision)
			}
		},
	}
}

func (runner *Runner) runTask(ctx context.Context, userInput message.Message, timing *TurnTiming) (TurnExecution, error) {
	task := &Task{ID: taskIDFromTiming(timing), Input: userInput, Status: TaskRunning}
	return runner.taskOrchestrator().Run(ctx, task)
}

func taskIDFromTiming(timing *TurnTiming) string {
	if timing != nil && timing.TurnID != "" {
		return timing.TurnID
	}
	return "task"
}

var _ TurnExecutor = runnerTurnExecutor{}
var _ CompletionEvaluator = runnerCompletionEvaluator{}

// GoalTurnExecutor exposes the existing single-turn adapter to higher-level
// goal orchestration without exposing Runner's tool-loop internals.
func (runner *Runner) GoalTurnExecutor() TurnExecutor {
	if runner == nil {
		return nil
	}
	return runnerTurnExecutor{runner: runner}
}
