# Working Animation Frame Wakeup Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make all working-state TUI animations advance independently of model output, then let the context-usage ripple leave the bar continuously before idle redraws stop.

**Architecture:** Add a demand-driven, de-duplicated Bubble Tea frame scheduler to `appModel`. Model-work entry points wake the scheduler, each consumed frame decides whether to schedule one successor, and the existing absolute-time ripple records a finite post-work deadline at the end of its current cycle so its head and tail leave without resetting.

**Tech Stack:** Go, Bubble Tea `tea.Cmd`/`tea.Tick`, Lip Gloss rendering, Go `testing` package.

## Global Constraints

- Do not run a permanent ticker or keep Bubble Tea redrawing in the idle state.
- Preserve Ghostty IME behavior by stopping full-frame redraws after finite animations complete.
- Do not change spinner, equalizer, context-meter, or ripple colors, glyphs, speeds, or amplitudes.
- Keep the real terminal cursor animation independent in `anchoredOutput`.
- Model waiting, streaming, tool-call, and synchronous subagent phases must all continue at `cursorFrameInterval` without relying on output events.
- Work completion, cancellation, and errors must preserve the current ripple phase and velocity until the entire tail leaves the context usage bar.
- Use controlled timestamps in tests; do not add wall-clock sleeps.

---

## File Structure

- Modify `internal/ui/bubble/types.go` to store whether one Bubble Tea animation frame is already scheduled.
- Modify `internal/ui/bubble/cursor_animation.go` to own the scheduling helper and retain `needsUIAnimationFrames` as the redraw-demand predicate.
- Modify `internal/ui/bubble/app.go` to consume/re-arm frame commands and wake finite exit animations.
- Modify `internal/ui/bubble/input.go` to wake frames when a normal model turn starts.
- Modify `internal/ui/bubble/commands.go` to wake frames when a queued turn starts.
- Modify `internal/ui/bubble/command_helpers.go` to wake frames for synchronous subagent work.
- Modify `internal/ui/bubble/status_line.go` to make the full-tail exit deadline explicit and testable.
- Create `internal/ui/bubble/animation_frame_test.go` for scheduler lifecycle tests.
- Extend `internal/ui/bubble/status_line_test.go` for ripple continuity and full-tail exit tests.

### Task 1: Add a de-duplicated animation-frame scheduler

**Files:**
- Modify: `internal/ui/bubble/types.go:321-408`
- Modify: `internal/ui/bubble/cursor_animation.go:80-100`
- Modify: `internal/ui/bubble/app.go:36-151`
- Create: `internal/ui/bubble/animation_frame_test.go`

**Interfaces:**
- Produces: `func (m *appModel) scheduleUIAnimationFrame() tea.Cmd`
- Produces: `appModel.uiAnimationFrameScheduled bool`
- Consumes: existing `cursorFrameTick() tea.Cmd`
- Consumes: existing `needsUIAnimationFrames(now time.Time) bool`

- [ ] **Step 1: Write failing scheduler de-duplication tests**

Create `internal/ui/bubble/animation_frame_test.go`:

```go
package bubble

import (
    "testing"
    "time"
)

func TestScheduleUIAnimationFrameDeduplicatesPendingTick(t *testing.T) {
    model := newTestModel(&fakeRunner{})
    model.uiAnimationFrameScheduled = false

    first := model.scheduleUIAnimationFrame()
    second := model.scheduleUIAnimationFrame()

    if first == nil {
        t.Fatal("first frame wakeup returned nil")
    }
    if second != nil {
        t.Fatal("second frame wakeup scheduled a duplicate tick")
    }
    if !model.uiAnimationFrameScheduled {
        t.Fatal("frame should remain marked scheduled until cursorFrameMsg arrives")
    }
}

func TestCursorFrameStopsWhenIdle(t *testing.T) {
    model := newTestModel(&fakeRunner{})
    model.uiAnimationFrameScheduled = true
    model.waveAmpStartedAt = time.Time{}
    model.tokenRippleHideAt = time.Time{}
    model.transcriptRefreshPending = false

    at := time.Unix(100, 0)
    next, _ := model.Update(cursorFrameMsg(at))
    model = next.(appModel)

    // Update may still return the one-shot pipeline poll command. The scheduler
    // marker is the source of truth for whether a successor animation tick exists.
    if model.uiAnimationFrameScheduled {
        t.Fatal("consumed idle frame should clear the scheduled marker")
    }
}
```

- [ ] **Step 2: Run the focused tests and verify they fail**

Run:

```bash
go test ./internal/ui/bubble -run 'TestScheduleUIAnimationFrameDeduplicatesPendingTick|TestCursorFrameStopsWhenIdle' -count=1
```

Expected: build failure because `uiAnimationFrameScheduled` and `scheduleUIAnimationFrame` do not exist.

- [ ] **Step 3: Add scheduler state and helper**

In `internal/ui/bubble/types.go`, add the field next to `cursorFrameAt`:

```go
cursorFrameAt            time.Time
uiAnimationFrameScheduled bool
```

In `internal/ui/bubble/cursor_animation.go`, import Bubble Tea and add the helper before `needsUIAnimationFrames`:

```go
import (
    "time"

    tea "github.com/charmbracelet/bubbletea"
)

// scheduleUIAnimationFrame ensures at most one Bubble Tea animation tick is in flight.
func (m *appModel) scheduleUIAnimationFrame() tea.Cmd {
    if m == nil || m.uiAnimationFrameScheduled {
        return nil
    }
    m.uiAnimationFrameScheduled = true
    return cursorFrameTick()
}
```

In `newModel`, initialize the marker to match the tick returned by `Init`:

```go
cursorFrameAt:              now,
uiAnimationFrameScheduled:  true,
```

Keep `Init` scheduling exactly one initial tick:

```go
func (m appModel) Init() tea.Cmd {
    cmds := []tea.Cmd{m.input.Focus(), cursorFrameTick()}
    // existing worktree command remains unchanged
    return tea.Batch(cmds...)
}
```

At the beginning of the `cursorFrameMsg` case in `Update`, consume the pending marker, and use the helper for the successor:

```go
case cursorFrameMsg:
    m.uiAnimationFrameScheduled = false
    m.cursorFrameAt = time.Time(msg)
    m.spinnerFrameIdx++
    m.applyCursorAnimation()
    m.updateContextMeterAnimation()
    m.updateWaveAmp(time.Time(msg))
    m.refreshActivityTasks()
    m.refreshSubagentPreviewFromTasks()
    m.refreshRunningToolProgress(time.Time(msg))
    if m.transcriptRefreshPending {
        if m.viewport.AtBottom() {
            m.refreshViewport()
        } else {
            m.refreshViewportPreservingOffset()
        }
    }
    var frameCmd tea.Cmd
    if m.needsUIAnimationFrames(time.Time(msg)) {
        frameCmd = m.scheduleUIAnimationFrame()
    }
    pollCmd := pipelinePollCmd(m.pipelineActiveAfter)
    if frameCmd == nil {
        return m, pollCmd
    }
    return m, tea.Batch(frameCmd, pollCmd)
```

- [ ] **Step 4: Run the focused tests and verify they pass**

Run:

```bash
go test ./internal/ui/bubble -run 'TestScheduleUIAnimationFrameDeduplicatesPendingTick|TestCursorFrameStopsWhenIdle' -count=1
```

Expected: PASS.

- [ ] **Step 5: Run the existing cursor and IME-related tests**

Run:

```bash
go test ./internal/ui/bubble -run 'Cursor|IME|Anchor' -count=1
```

Expected: PASS, confirming the scheduler does not alter anchored terminal-cursor behavior.

- [ ] **Step 6: Commit the scheduler foundation**

```bash
git add internal/ui/bubble/types.go internal/ui/bubble/cursor_animation.go internal/ui/bubble/app.go internal/ui/bubble/animation_frame_test.go
git commit -m "fix: add demand-driven UI animation scheduler"
```

### Task 2: Wake the scheduler at every foreground model-work entry point

**Files:**
- Modify: `internal/ui/bubble/input.go:275-296`
- Modify: `internal/ui/bubble/commands.go:164-184`
- Modify: `internal/ui/bubble/command_helpers.go:150-176`
- Extend: `internal/ui/bubble/animation_frame_test.go`

**Interfaces:**
- Consumes: `func (m *appModel) scheduleUIAnimationFrame() tea.Cmd` from Task 1
- Produces: normal turns, queued turns, and synchronous subagents return a `tea.Batch` containing their work command and at most one frame command
- Preserves: background subagent launches do not mark the foreground query guard as model-running and do not require this foreground wakeup

- [ ] **Step 1: Write failing work-entry wakeup tests**

Append to `internal/ui/bubble/animation_frame_test.go`:

```go
func TestStartChatTurnWakesAnimationBeforeFirstDelta(t *testing.T) {
    model := newTestModel(&fakeRunner{})
    model.uiAnimationFrameScheduled = false

    next, cmd := model.startChatTurn("hello")

    if cmd == nil {
        t.Fatal("model turn should return work and animation commands")
    }
    if !next.queryGuard.IsModelRunning() {
        t.Fatal("model guard should be running")
    }
    if !next.uiAnimationFrameScheduled {
        t.Fatal("model turn should wake animation before the first delta")
    }
}

func TestQueuedTurnWakesAnimationBeforeFirstDelta(t *testing.T) {
    model := newTestModel(&fakeRunner{})
    model.uiAnimationFrameScheduled = false
    if !model.chatQueue.Enqueue("queued") {
        t.Fatal("failed to enqueue test turn")
    }

    cmd := model.startNextQueuedTurn()

    if cmd == nil {
        t.Fatal("queued turn should return work and animation commands")
    }
    if !model.queryGuard.IsModelRunning() {
        t.Fatal("queued turn should start model guard")
    }
    if !model.uiAnimationFrameScheduled {
        t.Fatal("queued turn should wake animation")
    }
}

func TestWorkingCursorFrameSchedulesSuccessorWithoutModelDelta(t *testing.T) {
    model := newTestModel(&fakeRunner{})
    model.uiAnimationFrameScheduled = true
    if !model.queryGuard.StartModel() {
        t.Fatal("failed to start model guard")
    }
    model.syncRunningFlags()

    next, cmd := model.Update(cursorFrameMsg(time.Unix(200, 0)))
    model = next.(appModel)

    if cmd == nil {
        t.Fatal("working frame should schedule a successor without a model delta")
    }
    if !model.uiAnimationFrameScheduled {
        t.Fatal("successor frame should be marked scheduled")
    }
}
```

Reuse the existing `fakeSubagentController` from `bubble_test.go`. The test must call `handleSubagentCommand` but must not execute the returned batch:

```go
func TestSyncSubagentWakesAnimation(t *testing.T) {
    model := newTestModel(&fakeRunner{})
    model.subagents = &fakeSubagentController{}
    model.uiAnimationFrameScheduled = false

    cmd := model.handleSubagentCommand("/subagent --sync inspect")

    if cmd == nil {
        t.Fatal("sync subagent should return work and animation commands")
    }
    if !model.queryGuard.IsModelRunning() {
        t.Fatal("sync subagent should start model guard")
    }
    if !model.uiAnimationFrameScheduled {
        t.Fatal("sync subagent should wake animation")
    }
}
```

- [ ] **Step 2: Run the work-entry tests and verify they fail**

Run:

```bash
go test ./internal/ui/bubble -run 'TestStartChatTurnWakesAnimationBeforeFirstDelta|TestQueuedTurnWakesAnimationBeforeFirstDelta|TestWorkingCursorFrameSchedulesSuccessorWithoutModelDelta|TestSyncSubagentWakesAnimation' -count=1
```

Expected: the entry-point tests fail because starting model work does not call the scheduler.

- [ ] **Step 3: Wake frames from normal chat turns**

In `startChatTurn`, schedule after the guard and legacy flags are active, then batch the commands:

```go
m.syncRunningFlags()
workCmd := runTurnCmd(m.beginModelWorkContext(), m.runner, draft, m.turnID, m.turnStartedAt)
frameCmd := m.scheduleUIAnimationFrame()
return m, tea.Batch(workCmd, frameCmd)
```

Do not schedule on empty input or when `StartModel` fails.

- [ ] **Step 4: Wake frames from queued turns**

In `startNextQueuedTurn`, replace the direct return with:

```go
m.syncRunningFlags()
workCmd := runTurnCmd(m.beginModelWorkContext(), m.runner, draft, m.turnID, m.turnStartedAt)
frameCmd := m.scheduleUIAnimationFrame()
return tea.Batch(workCmd, frameCmd)
```

The helper suppresses a duplicate when the outgoing turn already has a frame in flight.

- [ ] **Step 5: Wake frames from synchronous subagent turns**

In `handleSubagentCommand`, leave background launches unchanged. For the synchronous path, batch the work and frame commands:

```go
m.syncRunningFlags()
m.addEntry(transcriptEntry{kind: entrySystem, title: "subagent", body: "started sync subagent"})
workCmd := runSubagentCmd(m.beginModelWorkContext(), m.subagents, req)
frameCmd := m.scheduleUIAnimationFrame()
return tea.Batch(workCmd, frameCmd)
```

- [ ] **Step 6: Run the work-entry tests and verify they pass**

Run:

```bash
go test ./internal/ui/bubble -run 'TestStartChatTurnWakesAnimationBeforeFirstDelta|TestQueuedTurnWakesAnimationBeforeFirstDelta|TestWorkingCursorFrameSchedulesSuccessorWithoutModelDelta|TestSyncSubagentWakesAnimation' -count=1
```

Expected: PASS.

- [ ] **Step 7: Run existing turn, queue, StreamMA, and subagent tests**

Run:

```bash
go test ./internal/ui/bubble -run 'Submit|Queued|Queue|StreamMA|Subagent' -count=1
```

Expected: PASS. `tea.Batch` must preserve all existing work commands while adding only the frame wakeup.

- [ ] **Step 8: Commit work-entry wakeups**

```bash
git add internal/ui/bubble/input.go internal/ui/bubble/commands.go internal/ui/bubble/command_helpers.go internal/ui/bubble/animation_frame_test.go
git commit -m "fix: keep UI animations running during model work"
```

### Task 3: Make ripple exit continuity and full-tail completion explicit

**Files:**
- Modify: `internal/ui/bubble/status_line.go:33-52,89-94,220-243`
- Modify: `internal/ui/bubble/app.go:200-310`
- Extend: `internal/ui/bubble/status_line_test.go`
- Extend: `internal/ui/bubble/animation_frame_test.go`

**Interfaces:**
- Produces: `func tokenRippleRemainingUntilExit(now time.Time) time.Duration`
- Preserves: `func tokenRipplePhase(now time.Time) time.Duration`
- Preserves: `func (m *appModel) startTokenRippleExit(now time.Time)` as the state mutation used by completion, error, cancellation, and synchronous subagent completion
- Consumes: `func (m *appModel) scheduleUIAnimationFrame() tea.Cmd` from Task 1

- [ ] **Step 1: Write failing ripple deadline and continuity tests**

Append to `internal/ui/bubble/status_line_test.go`:

```go
func TestTokenRippleRemainingUntilExitUsesCurrentCycle(t *testing.T) {
    epoch := time.Unix(0, 0)
    cases := []struct {
        name string
        at   time.Time
        want time.Duration
    }{
        {name: "cycle start needs full travel and tail exit", at: epoch, want: tokenRippleCycle},
        {name: "mid travel keeps remaining travel and exit", at: epoch.Add(time.Second), want: tokenRippleCycle - time.Second},
        {name: "mid exit keeps only remaining tail exit", at: epoch.Add(tokenRippleTravel + 200*time.Millisecond), want: tokenRippleExit - 200*time.Millisecond},
    }
    for _, tc := range cases {
        t.Run(tc.name, func(t *testing.T) {
            if got := tokenRippleRemainingUntilExit(tc.at); got != tc.want {
                t.Fatalf("remaining = %v, want %v", got, tc.want)
            }
        })
    }
}

func TestStartTokenRippleExitPreservesPhaseAndVelocity(t *testing.T) {
    model := newTestModel(&fakeRunner{})
    now := time.Unix(0, 0).Add(1250 * time.Millisecond)
    before := tokenRippleHead(4, 40, tokenRipplePhase(now))

    model.startTokenRippleExit(now)
    after := tokenRippleHead(4, 40, tokenRipplePhase(now.Add(cursorFrameInterval)))

    if after < before {
        t.Fatalf("ripple moved backward on exit: before=%d after=%d", before, after)
    }
    wantHideAt := now.Add(tokenRippleRemainingUntilExit(now))
    if !model.tokenRippleHideAt.Equal(wantHideAt) {
        t.Fatalf("hideAt = %v, want %v", model.tokenRippleHideAt, wantHideAt)
    }
}

func TestTokenRippleExitRemainsActiveUntilCycleTailCompletes(t *testing.T) {
    model := newTestModel(&fakeRunner{})
    now := time.Unix(0, 0).Add(tokenRippleTravel + 200*time.Millisecond)
    model.startTokenRippleExit(now)

    if !model.tokenRippleActive(model.tokenRippleHideAt.Add(-time.Nanosecond)) {
        t.Fatal("ripple should remain active until the full tail exits")
    }
    if model.tokenRippleActive(model.tokenRippleHideAt) {
        t.Fatal("ripple should stop when the full tail has exited")
    }
}
```

Append to `internal/ui/bubble/animation_frame_test.go`:

```go
func TestTurnCompletionKeepsFramesAliveForRippleExit(t *testing.T) {
    model := newTestModel(&fakeRunner{})
    model.uiAnimationFrameScheduled = false
    if !model.queryGuard.StartModel() {
        t.Fatal("failed to start model guard")
    }
    model.syncRunningFlags()
    model.cursorFrameAt = time.Unix(0, 0).Add(time.Second)

    next, cmd := model.Update(turnFinishedMsg{})
    model = next.(appModel)

    if cmd == nil {
        t.Fatal("turn completion should wake a frame for ripple exit")
    }
    if model.tokenRippleHideAt.IsZero() {
        t.Fatal("turn completion should record a ripple exit deadline")
    }
    if !model.uiAnimationFrameScheduled {
        t.Fatal("ripple exit frame should be marked scheduled")
    }
}

func TestRippleExitFrameStopsAfterDeadline(t *testing.T) {
    model := newTestModel(&fakeRunner{})
    model.uiAnimationFrameScheduled = true
    model.waveAmpStartedAt = time.Time{}
    model.tokenRippleHideAt = time.Unix(500, 0)

    next, _ := model.Update(cursorFrameMsg(model.tokenRippleHideAt))
    model = next.(appModel)

    // Ignore the unrelated one-shot pipeline poll command; only the scheduler
    // marker identifies an animation successor.
    if model.uiAnimationFrameScheduled {
        t.Fatal("completed ripple exit should clear scheduled marker")
    }
}
```

- [ ] **Step 2: Run ripple-focused tests and verify they fail**

Run:

```bash
go test ./internal/ui/bubble -run 'TestTokenRippleRemainingUntilExitUsesCurrentCycle|TestStartTokenRippleExitPreservesPhaseAndVelocity|TestTokenRippleExitRemainsActiveUntilCycleTailCompletes|TestTurnCompletionKeepsFramesAliveForRippleExit|TestRippleExitFrameStopsAfterDeadline' -count=1
```

Expected: build failure for the missing deadline helper and failure because completion does not explicitly wake a stopped frame chain.

- [ ] **Step 3: Extract the explicit full-tail deadline calculation**

In `internal/ui/bubble/status_line.go`, add:

```go
// tokenRippleRemainingUntilExit returns the time until the active cycle's head
// and complete tail have passed the context meter's right edge.
func tokenRippleRemainingUntilExit(now time.Time) time.Duration {
    remaining := tokenRippleCycle - tokenRipplePhase(now)
    if remaining <= 0 {
        return tokenRippleCycle
    }
    return remaining
}
```

Update `startTokenRippleExit` without changing phase or speed:

```go
func (m *appModel) startTokenRippleExit(now time.Time) {
    if m == nil || now.IsZero() {
        return
    }
    m.tokenRippleHideAt = now.Add(tokenRippleRemainingUntilExit(now))
}
```

Keep `tokenRippleActive` using the existing semantics:

```go
func (m appModel) tokenRippleActive(now time.Time) bool {
    if m.isAgentWorking() {
        return true
    }
    return !m.tokenRippleHideAt.IsZero() && now.Before(m.tokenRippleHideAt)
}
```

Do not introduce a relative ripple position; rendering must continue calling `tokenRipplePhase(now)`.

- [ ] **Step 4: Wake the finite exit animation on all foreground completion paths**

In both `turnFinishedMsg` and `subagentFinishedMsg` cases, immediately after `startTokenRippleExit`, append the shared scheduler command:

```go
if wasWorking && !m.isAgentWorking() {
    m.startTokenRippleExit(m.animationNow())
    if frameCmd := m.scheduleUIAnimationFrame(); frameCmd != nil {
        cmds = append(cmds, frameCmd)
    }
}
```

This code path already covers successful completion, model errors, expected cancellation, and synchronous subagent completion. Do not add separate outcome-specific branches.

- [ ] **Step 5: Run ripple-focused tests and verify they pass**

Run:

```bash
go test ./internal/ui/bubble -run 'TestTokenRippleRemainingUntilExitUsesCurrentCycle|TestStartTokenRippleExitPreservesPhaseAndVelocity|TestTokenRippleExitRemainsActiveUntilCycleTailCompletes|TestTurnCompletionKeepsFramesAliveForRippleExit|TestRippleExitFrameStopsAfterDeadline' -count=1
```

Expected: PASS.

- [ ] **Step 6: Add cancellation and error regression cases**

Add a table-driven test to `animation_frame_test.go` proving all turn outcomes use the same exit lifecycle:

```go
func TestTurnOutcomesWakeRippleExit(t *testing.T) {
    cases := []struct {
        name string
        msg  turnFinishedMsg
    }{
        {name: "success", msg: turnFinishedMsg{}},
        {name: "error", msg: turnFinishedMsg{err: errors.New("boom")}},
        {name: "expected cancellation", msg: turnFinishedMsg{err: context.Canceled}},
    }
    for _, tc := range cases {
        t.Run(tc.name, func(t *testing.T) {
            model := newTestModel(&fakeRunner{})
            model.uiAnimationFrameScheduled = false
            if !model.queryGuard.StartModel() {
                t.Fatal("failed to start model guard")
            }
            model.syncRunningFlags()
            model.cursorFrameAt = time.Unix(0, 0).Add(2 * time.Second)
            if errors.Is(tc.msg.err, context.Canceled) {
                model.modelCancelRequested = true
            }

            next, cmd := model.Update(tc.msg)
            model = next.(appModel)

            if cmd == nil || !model.uiAnimationFrameScheduled {
                t.Fatal("turn outcome should wake ripple exit animation")
            }
            if model.tokenRippleHideAt.IsZero() {
                t.Fatal("turn outcome should set ripple exit deadline")
            }
        })
    }
}
```

Add `context` and `errors` imports to the test file.

- [ ] **Step 7: Run all animation and status-line tests**

Run:

```bash
go test ./internal/ui/bubble -run 'Animation|Ripple|Status|Equalizer|TurnOutcomes' -count=1
```

Expected: PASS.

- [ ] **Step 8: Commit ripple exit behavior**

```bash
git add internal/ui/bubble/status_line.go internal/ui/bubble/app.go internal/ui/bubble/status_line_test.go internal/ui/bubble/animation_frame_test.go
git commit -m "fix: let context ripple finish its exit"
```

### Task 4: Verify the complete UI lifecycle and repository health

**Files:**
- Verify: `internal/ui/bubble/*.go`
- Verify: repository-wide Go packages

**Interfaces:**
- Consumes: all scheduler and ripple behavior from Tasks 1-3
- Produces: a tested, formatted implementation with no idle ticker regression

- [ ] **Step 1: Format all modified Go files**

Run:

```bash
gofmt -w \
  internal/ui/bubble/types.go \
  internal/ui/bubble/cursor_animation.go \
  internal/ui/bubble/app.go \
  internal/ui/bubble/input.go \
  internal/ui/bubble/commands.go \
  internal/ui/bubble/command_helpers.go \
  internal/ui/bubble/status_line.go \
  internal/ui/bubble/animation_frame_test.go \
  internal/ui/bubble/status_line_test.go
```

Expected: command exits successfully.

- [ ] **Step 2: Run the complete Bubble Tea UI test package**

Run:

```bash
go test ./internal/ui/bubble -count=1
```

Expected: PASS.

- [ ] **Step 3: Run the full repository test suite**

Run:

```bash
go test ./... -count=1
```

Expected: PASS.

- [ ] **Step 4: Run static analysis and diff validation**

Run:

```bash
go vet ./...
git diff --check
git status --short
```

Expected: `go vet` and `git diff --check` succeed; status shows only intended implementation/test changes.

- [ ] **Step 5: Review the final diff against the design constraints**

Run:

```bash
git diff --stat HEAD~3..HEAD
git diff HEAD~3..HEAD -- internal/ui/bubble
```

Verify explicitly:

- No permanent ticker or goroutine was added.
- `cursorFrameMsg` clears the pending marker before deciding on a successor.
- Work entry points schedule frames before the first model delta.
- `needsUIAnimationFrames` remains the single continuation predicate.
- Ripple exit uses absolute phase and stops at the current cycle deadline.
- Completion, error, cancellation, and synchronous subagent paths all wake finite exit frames.
- Terminal cursor animation code in `anchor.go` remains untouched.

- [ ] **Step 6: Commit any formatting or verification-only corrections**

If formatting or final review required changes:

```bash
git add internal/ui/bubble
git commit -m "test: verify working animation lifecycle"
```

If no files changed after the earlier commits, do not create an empty commit.
