# Paw Goal Mode MVP

Goal Mode provides an opt-in, session-scoped execution loop for objectives that may require more than one model/tool turn.

## Commands

```text
/goal start <objective>
/goal status
/goal pause
/goal resume
/goal stop
```

`/goal stop` cancels the current Goal. `/goal resume` is always explicit; a paused Goal is never resumed automatically after interruption.

## Lifecycle

```text
draft -> running -> completed
running -> paused | blocked | failed | cancelled
paused/blocked -> running   # explicit /goal resume
```

A Goal is completed only when the Goal evaluator observes no pending or in-progress Todo item. A final model message by itself is not sufficient when Todo work remains.

## Auto-continue and limits

Goal Mode reuses the existing `TaskOrchestrator` for turn execution and has a finite continuation budget. It is not an unlimited background loop. Repeated progress fingerprints, a deadline/budget limit, context maintenance, cancellation, or a tool/policy error stop the loop.

The default policy is conservative:

- finite continuation budget;
- risk pauses instead of retrying;
- no-progress detection;
- explicit resume after pause;
- at most one active Goal per runtime.

Ordinary chat and the existing Todo-driven Auto-Continue path are unchanged when no Goal is started.

## Risk behavior

Permission, unsafe/dangerous, blocked, and user-input errors are recorded as structured pause/block reasons. Goal Mode never changes rejected tool arguments or bypasses existing tool permission checks.

## MVP scope and exclusions

The MVP keeps Goal state in memory for the current process/session. The following are deliberately deferred:

- journal-backed durable Goal checkpoints and cross-process recovery;
- explicit multi-step Plan/PlanStep versions and dependencies;
- PlanDraft/PlanApproved/PlanExecuting approval mode;
- GoalInput queue for steer/clarify/replan;
- verification Evidence Gate and acceptance specifications;
- detached/background Goals and resource quotas;
- automatic restart after process termination.

These belong to the next implementation phase and must not be implied by the MVP commands or status output.
