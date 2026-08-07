# Paw Goal Runtime MVP Implementation Plan

> **For Codex workers:** Implement task-by-task. Keep only one step in progress at a time, update the execution todo after each task, edit files with the repository's established tools, and run the exact verification commands listed below. Do not stage or rewrite unrelated changes.

**Goal:** Add a session-scoped Goal Runtime that can keep executing the existing Runner/TaskOrchestrator until the Goal is complete, paused, blocked, cancelled, or budget-exhausted, while preserving ordinary single-turn and Todo-driven Auto-Continue behavior.

**Architecture:** Add a small `internal/goal` domain/runtime layer above the existing `Runner`, `TaskOrchestrator`, `CompletionGate`, and `todo.Broker`. The MVP uses an implicit single-step plan and deterministic completion/risk decisions. Durable journal events, explicit multi-step Plan versions, Evidence Gate, and background execution remain follow-up phases unless the existing interfaces make them low-risk to add without widening scope.

**Tech Stack:** Go 1.25, existing `message.Message`, `todo.Broker`, `session` journal abstractions, Bubble Tea command registry, and existing tool permission/safety boundaries.

---

## Current repository constraints

- `Runner` remains the owner of one model/tool turn; do not move tool execution into `internal/goal`.
- `TaskOrchestrator` remains the owner of cross-turn continuation within one task; Goal Runtime should invoke it rather than duplicate its loop.
- Existing ordinary Auto-Continue defaults (`BaseBudget=2`, `AbsoluteMax=12`, `MaxNoProgress=2`) and behavior must remain compatible.
- `CompletionGate` currently uses Todo, continuation budget, and progress hash. Goal mode may add a Goal-level evaluator, but must not silently change non-Goal evaluation.
- `todo.Broker` is the current execution-state source. MVP may use an implicit PlanStep identified by Goal ID; explicit Plan/PlanStep persistence is Phase 2.
- Risk-pause must reuse existing tool permission checks. Goal Runtime must never retry a rejected tool by changing its arguments or bypassing policy.
- A durable Goal must not resume unattended in MVP. Session Goal state can be held in memory and surfaced as paused after process/session termination; journal/checkpoint integration is a later task.
- Existing uncommitted UI and unrelated untracked files must not be staged, reset, or rewritten.

## MVP state and terminology

```text
Goal  = long-lived user objective and lifecycle state
Task  = one invocation of the existing TaskOrchestrator
Turn  = one model/tool execution inside Task
Todo  = current short-term execution queue
```

Minimum lifecycle:

```text
draft -> running -> completed
running -> paused
running -> blocked
running -> failed
running -> cancelled
paused -> running
blocked -> running
```

Minimum pause reasons:

```go
permission_required
dangerous_command
no_progress
budget_exhausted
blocked
verification_failed
plan_stale
user_input_required
```

MVP completion is intentionally conservative:

```text
Goal completed =
    evaluator says no unfinished Todo
    AND no unresolved pause/block reason
```

The implementation must leave a seam for adding acceptance criteria and Evidence Gate without changing the public command/runtime shape.

---

## Task 1: Establish the Goal domain types and deterministic state transitions

Files:

- Create `internal/goal/types.go`
- Create `internal/goal/state.go`
- Create `internal/goal/types_test.go`
- Create `internal/goal/state_test.go`

Steps:

- [ ] Write table tests for valid status transitions, invalid transitions, pause/resume, cancel, and terminal states.
- [ ] Run `go test ./internal/goal -run 'TestGoal|TestGoalState' -count=1` and verify it fails because the package is absent.
- [ ] Define `GoalID`, `GoalStatus`, `PauseReason`, and `GoalBudget` with stable string values.
- [ ] Define `Goal` with ID, session ID, objective, status, current task ID, continuation/no-progress counters, budget, pause reason, last decision, timestamps, and a version/revision field.
- [ ] Define `GoalSnapshot` as a copy-safe read model so UI/commands do not mutate runtime state.
- [ ] Implement state transition validation in one place. Terminal states (`completed`, `failed`, `cancelled`) cannot resume or continue.
- [ ] Implement budget accounting fields for turns, tool calls, continuations, and wall-clock deadline. Keep zero limits as “not configured” only where documented; normalize invalid negative values.
- [ ] Run `go test ./internal/goal -count=1`.

Recommended API shape:

```go
type GoalStore interface {
    Create(context.Context, Goal) error
    Get(context.Context, GoalID) (Goal, bool, error)
    Update(context.Context, Goal) error
    List(context.Context, string) ([]GoalSnapshot, error)
}
```

For MVP, provide an in-memory implementation in `internal/goal/store_memory.go` and tests. The interface must not assume a particular journal backend.

---

## Task 2: Add Goal evaluation, continuation policy, and risk-pause mapping

Files:

- Create `internal/goal/evaluator.go`
- Create `internal/goal/policy.go`
- Create `internal/goal/evaluator_test.go`
- Create `internal/goal/policy_test.go`

Steps:

- [ ] Add tests covering: no Todo signal, pending Todo, completed Todo, budget exhaustion, no-progress threshold, context maintenance, tool permission errors, user-input-required errors, and cancellation.
- [ ] Implement a Goal-level observation containing the latest assistant message, Todo snapshot/availability, `CompletionDecision`, tool/error metadata, changed/progress information, and current Goal counters.
- [ ] Implement a deterministic evaluator that delegates ordinary Todo semantics to the existing `CompletionGate` where possible, then maps the result to Goal actions: `continue`, `compact`, `pause`, `blocked`, `complete`, or `failed`.
- [ ] Add a Goal continuation budget separate from the ordinary `AutoContinueConfig`; the default must be finite and documented. Never interpret “auto-continue” as unlimited execution.
- [ ] Add a progress fingerprint that includes Goal ID, current Todo snapshot, assistant result, and relevant tool outcome. Repeated fingerprints must pause after the configured threshold.
- [ ] Map existing permission/approval/cancellation errors to structured `PauseReason` values without weakening the existing error returned to callers.
- [ ] Treat context maintenance/compaction as recoverable continuation state, not completion.
- [ ] Run `go test ./internal/goal -run 'TestEvaluator|TestPolicy' -count=1` and then the full `internal/goal` package.

Recommended result shape:

```go
type DecisionAction string
const (
    ActionContinue DecisionAction = "continue"
    ActionCompact DecisionAction = "compact"
    ActionComplete DecisionAction = "complete"
    ActionPause DecisionAction = "pause"
    ActionBlocked DecisionAction = "blocked"
    ActionFailed DecisionAction = "failed"
)

type Decision struct {
    Action DecisionAction
    Reason string
    PauseReason PauseReason
    NextPrompt string
}
```

---

## Task 3: Implement the Goal Runtime over the existing TaskOrchestrator

Files:

- Create `internal/goal/runtime.go`
- Create `internal/goal/events.go`
- Create `internal/goal/runtime_test.go`
- Modify only the smallest required adapter surface in `internal/loop` if tests show an existing method is inaccessible

Steps:

- [ ] Define a narrow executor adapter around the existing Runner/TaskOrchestrator. Do not duplicate `runSingleTurnWithTiming` or tool-loop logic.
- [ ] Add `Runtime.Start`, `Runtime.Resume`, `Runtime.Pause`, `Runtime.Cancel`, `Runtime.Status`, and `Runtime.Run` methods with context cancellation support.
- [ ] On `Run`, create one Task for the Goal and invoke the existing orchestrator. The evaluator receives each completed turn and determines whether another task continuation is needed.
- [ ] Build continuation input from structured Goal state: objective, current status, unfinished Todo items, last decision, and the next useful action. Do not emit a context-free “continue”.
- [ ] Ensure only one Goal run is active at a time. A second start/resume returns a typed conflict error rather than spawning competing loops.
- [ ] Emit typed in-memory events at Goal start, task start, turn completion, continuation, compaction, pause, block, completion, failure, cancellation, and resume.
- [ ] Make event delivery non-blocking or context-aware so a UI subscriber cannot deadlock execution. Preserve the latest state even when there are no subscribers.
- [ ] Ensure `Pause` cancels the active run context and leaves a resumable snapshot; `Resume` starts a new Task using the same Goal state and does not reset completed work/progress counters.
- [ ] Ensure an already completed/cancelled Goal cannot be resumed, and a paused Goal cannot auto-resume without an explicit call.
- [ ] Add fake executor/evaluator tests for continuation, completion, pause, cancellation, budget exhaustion, no progress, and concurrent start protection.
- [ ] Run `go test ./internal/goal -run TestRuntime -count=1` and then `go test ./internal/goal -count=1`.

Recommended dependency boundary:

```go
type TaskRunner interface {
    RunTask(context.Context, message.Message, *loop.TurnTiming) (loop.TurnExecution, error)
}
```

If the current Runner does not expose this exact method, add an internal adapter in `internal/loop` rather than exporting Runner internals or reimplementing the loop.

---

## Task 4: Integrate Goal Runtime into command/session ownership without changing ordinary turns

Files:

- Inspect and modify `cmd/agent/main.go` only where the Runner/session lifetime is assembled
- Modify `internal/ui/bubble/command_registry.go`
- Modify `internal/ui/bubble/commands.go` or the existing command dispatch file as required
- Modify `internal/ui/bubble/bubble.go` only to add an optional Goal controller setter
- Create/update focused command/UI tests under `internal/ui/bubble`

Steps:

- [ ] Add a `GoalController` interface that exposes start, status, pause, resume, cancel/stop, and optionally plan/evidence placeholders without coupling Bubble Tea to `internal/goal` structs.
- [ ] Inject the controller after UI/model construction, following the existing `SetTodoBroker`/controller pattern so existing `newModel` call sites remain compatible.
- [ ] Register `/goal` with subcommands: `start <objective>`, `status`, `pause`, `resume`, `stop` (alias `cancel`). Register it as allowed while a Goal is running only for control operations that are safe while active.
- [ ] Format status with objective, lifecycle state, current task, continuation usage/limit, no-progress count, and pause reason/recovery command.
- [ ] `/goal start` must reject an empty objective and a conflicting active Goal. It must not start a second background loop for the same session.
- [ ] `/goal pause` and `/goal stop` must be idempotent and report the resulting state.
- [ ] `/goal resume` must require explicit user action and must not silently resume a Goal after a process/session restart.
- [ ] Keep normal `RunTurn` behavior, regular Todo Auto-Continue, existing `/setting`, and existing command completion unchanged.
- [ ] Add tests for command lookup, help/completion, empty objective, status formatting, lifecycle commands, and nil-controller behavior.
- [ ] Run `go test ./internal/ui/bubble -run 'Test.*Goal|TestCommand' -count=1` and then the package tests.

MVP UI must be informational and controllable; do not add a new transcript renderer or persistent Goal panel in this phase.

---

## Task 5: Add observation hooks and compatibility tests at the Runner boundary

Files:

- Modify `internal/loop/task_orchestrator.go` only if needed to expose Goal-safe task events
- Modify `internal/loop/completion_gate.go` only to add reusable observation/adaptation helpers; preserve current defaults
- Create/update focused tests under `internal/loop`

Steps:

- [ ] Verify ordinary `runTurnWithTiming` with Auto-Continue enabled still uses `TaskOrchestrator` and current completion semantics when no Goal is attached.
- [ ] Verify ordinary `runTurnWithTiming` with Auto-Continue disabled remains a single turn.
- [ ] Verify Goal Runtime receives continuation/paused/completed events without relying on UI notifications or parsing notification strings.
- [ ] Verify Goal Runtime does not reset `lastProgressHash` or counters unexpectedly when resuming a paused Goal.
- [ ] Verify tool errors and permission denials remain visible as original errors while Goal state records the structured pause/block reason.
- [ ] Add regression tests for no Todo broker, missing Todo snapshot, context compaction, model errors, and cancellation.
- [ ] Run `go test ./internal/loop -count=1` and `go test ./internal/goal ./internal/loop -count=1`.

Do not add Goal-specific branches to the core tool execution loop unless a typed event cannot otherwise be observed.

---

## Task 6: Add first-class tests for safety, lifecycle, and termination guarantees

Files:

- Create `internal/goal/integration_test.go`
- Create/update `internal/goal/fuzz_test.go` only if the package's existing test conventions support it

Steps:

- [ ] Test that a permission-required or dangerous-command result always pauses and is never automatically retried.
- [ ] Test that repeated identical progress fingerprints pause instead of creating a continuation loop.
- [ ] Test that a finite Goal budget always terminates with `budget_exhausted`.
- [ ] Test that context cancellation terminates the active run and leaves state as paused/cancelled according to the caller operation.
- [ ] Test that completion requires no pending/in-progress Todo and that a model final message alone cannot complete a Goal with pending work.
- [ ] Test that a completed Goal performs no extra model/tool turn.
- [ ] Test concurrent `Status`, `Pause`, and `Resume` calls under `-race`.
- [ ] Run `go test ./internal/goal -count=1`, `go test -race ./internal/goal ./internal/loop`, and fix only Goal-related races/failures.

---

## Task 7: Verification, documentation, and rollout controls

Files:

- Update `docs/goal-mode-design.md` only for behavior/API decisions made during implementation
- Create `docs/goal-mode-mvp.md` with user-facing commands, state semantics, limits, and known exclusions
- Modify configuration/settings only if the existing settings architecture has a low-risk, backward-compatible hook

Steps:

- [ ] Document that MVP Goal continuation is finite, risk-pausing, session-scoped, and explicitly resumed.
- [ ] Document the distinction between ordinary Auto-Continue and Goal Runtime.
- [ ] Document excluded Phase 2 features: durable Goal journal/checkpoint, explicit multi-step Plan versions, Plan approval mode, GoalInput queue, Evidence Gate, and background execution.
- [ ] Add a feature flag or constructor option if required to keep Goal mode opt-in; default existing sessions to current behavior.
- [ ] Run `go test ./... -count=1`.
- [ ] Run `go vet ./...` if supported by the repository's current toolchain.
- [ ] Run `git diff --check` and inspect `git diff --stat`/`git status --short`.
- [ ] Verify no unrelated files were staged or modified and no existing Auto-Continue tests/regressions were masked.

---

## Phase 2 follow-up (not part of this MVP)

1. Add `Plan`, `PlanStep`, dependencies, `PlanDraft -> PlanApproved -> PlanExecuting`, and plan/build permission separation inspired by OpenCode.
2. Add `GoalInput` queue for steer, approval, clarification, pause, resume, and replan inputs inspired by Codex.
3. Add `Evidence`, `VerificationSpec`, stale evidence invalidation, and Todo + Evidence completion gate.
4. Add journal-backed `GoalCheckpoint`, control events, recovery to `paused/ready_to_resume`, and explicit `/goal resume` across processes.
5. Add plan versioning/replanning while preserving committed execution history.
6. Add detach/attach and background resource quotas only after lifecycle and recovery metrics are stable.

## MVP acceptance criteria

1. `/goal start <objective>` creates exactly one active Goal for the session.
2. A Goal with unfinished Todo and available budget automatically continues through the existing TaskOrchestrator.
3. A Goal completes only when no required Todo remains and the deterministic evaluator says complete.
4. Permission, dangerous-action, blocked, no-progress, cancellation, and budget conditions produce structured paused/blocked states.
5. Paused Goals can be explicitly resumed without losing counters or completed work.
6. Completed, failed, or cancelled Goals never execute another turn.
7. Ordinary non-Goal Runner behavior and current Auto-Continue defaults remain unchanged.
8. `go test ./... -count=1` and focused race tests pass.
9. The implementation does not claim durable recovery, explicit Plan approval, or Evidence Gate until those phases are implemented.
