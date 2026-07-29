# Tool Runtime, Transcript Hit Map, and Token Ripple Fix Implementation Plan

> **For Codex workers:** Implement task-by-task. Use `update_plan` to track progress, keep only one step in progress at a time, edit files with the repo's established tools and `apply_patch` for manual changes, and run the exact verification commands listed below. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make long-running tool calls visibly report progress, make every rendered row of a tool transaction clickable for expansion, and give Token Ripple a slower motion with a complete right-edge exit.

**Architecture:** Keep the existing `transcriptEntry` tool transaction as the source of truth. Add an explicit tool start timestamp and a once-per-second cache invalidation path for running elapsed time; derive mouse rows from `tuiLayout` and map all rows in one tool location to the same transaction; replace the clamped Ripple head with a virtual head that travels through a visible phase and an off-screen fade phase.

**Tech Stack:** Go, Bubble Tea, Bubbles viewport, Lip Gloss, `charmbracelet/x/ansi`, table-driven Go tests, real PTY smoke testing.

---

## Current repository constraints

- Repository: `<repository-root>`.
- Branch: `dev`, with the design specification commit `880bbc2` already ahead of `origin/dev`.
- Existing visual redesign changes are intentionally uncommitted. Do not stage, reset, or rewrite them while implementing this plan.
- Relevant existing files already own the required boundaries:
  - `internal/ui/bubble/types.go`: `transcriptEntry` and `appModel` state.
  - `internal/ui/bubble/transcript.go`: tool event mutation, transcript cache, entry rendering.
  - `internal/ui/bubble/tool_inspect.go`: tool locations, hover, expansion, keyboard inspection.
  - `internal/ui/bubble/selection.go`: screen-to-transcript mouse coordinate conversion.
  - `internal/ui/bubble/status_line.go`: Token Ripple and status line.
  - `internal/ui/bubble/bubble_test.go`, `tool_track_test.go`, and `status_line_test.go`: existing behavior coverage.

## Task 1: Make tool running state visible and time-aware

**Files:**

- Modify: `<repository-root>/internal/ui/bubble/types.go`
- Modify: `<repository-root>/internal/ui/bubble/transcript.go`
- Modify: `<repository-root>/internal/ui/bubble/app.go`
- Test: `<repository-root>/internal/ui/bubble/tool_track_test.go`
- Test: `<repository-root>/internal/ui/bubble/bubble_test.go`

- [ ] **Step 1: Write failing lifecycle assertions for running text and elapsed refresh.**

Extend `TestToolTrackLifecycleAndResultVisibility` or add adjacent tests with these assertions:

```go
if entry := model.transcript[entryIndex]; entry.toolStatus != "running" || entry.toolStartedAt.IsZero() {
    t.Fatalf("running entry = %#v, want status and start time", entry)
}

at := model.transcript[entryIndex].toolStartedAt.Add(12 * time.Second)
running := ansi.Strip(renderTranscriptAt(model.transcript, 80, true, at))
if !strings.Contains(running, "running · 12s") {
    t.Fatalf("running transcript = %q, want elapsed status", running)
}
```

Add a turn-failure assertion that a pending running entry is no longer running after `turnFinishedMsg{err: errors.New("tool failed")}`. Keep the existing result matching and collapsed/expanded assertions intact.

- [ ] **Step 2: Run the focused tests to verify the new assertions fail.**

Run:

```bash
go test ./internal/ui/bubble -run 'TestToolTrackLifecycleAndResultVisibility|TestTool.*Running|TestToolResultEntryMatchesByToolUseID' -count=1
```

Expected: FAIL because `transcriptEntry` has no explicit start time and the renderer currently emits only `running` without elapsed data.

- [ ] **Step 3: Add explicit start time and cache-time fields.**

In `transcriptEntry`, add:

```go
toolStartedAt time.Time
```

In `transcriptRenderCacheKey`, add:

```go
toolStartedAtUnixNS int64
toolElapsedSecond   int64
```

Set `toolStartedAt: m.animationNow()` in `recordToolCallEntry`. Preserve the timestamp when `recordToolResultEntry` changes the same entry to `ok` or `error`.

Change `transcriptRenderKey` to accept the render time:

```go
func transcriptRenderKey(entry transcriptEntry, width int, at time.Time) transcriptRenderCacheKey
```

For running tool entries, populate `toolElapsedSecond` from the non-negative whole seconds between `at` and `entry.toolStartedAt`; for non-running entries leave it zero. Include `toolStartedAtUnixNS` so a new running transaction cannot reuse a stale cache entry.

- [ ] **Step 4: Render and refresh the running elapsed status.**

Pass `at` through the existing renderer chain:

```go
body := renderToolTransactionEntry(entry, width, at)
summary := renderCompactToolSummary(entry, innerWidth, at)
```

Add a formatter with deterministic output:

```go
func formatToolElapsed(startedAt, at time.Time) string {
    if startedAt.IsZero() || at.Before(startedAt) {
        return "0s"
    }
    seconds := int(at.Sub(startedAt) / time.Second)
    if seconds < 60 {
        return fmt.Sprintf("%ds", seconds)
    }
    return fmt.Sprintf("%dm%02ds", seconds/60, seconds%60)
}
```

For running tools, append ` · ` plus the elapsed text to the existing `running` status suffix. Keep `ok` and `error` text unchanged.

In `appModel`, add one refresh guard:

```go
lastToolProgressSecond int64
```

On `cursorFrameMsg`, call a helper before the existing pending-refresh block. The helper scans for running tools, computes the maximum current elapsed second, and calls `refreshViewport()` or `refreshViewportPreservingOffset()` only when that value differs from `lastToolProgressSecond`. This keeps elapsed updates at once per second while preserving the existing 30fps cursor and Ripple animation.

When handling `turnFinishedMsg` with a non-nil error, mark every still-running tool transaction as `error`, touch the changed entries, and refresh. Do not alter successfully completed tools.

- [ ] **Step 5: Run the focused lifecycle tests and verify they pass.**

Run:

```bash
go test ./internal/ui/bubble -run 'TestToolTrackLifecycleAndResultVisibility|TestTool.*Running|TestToolResultEntryMatchesByToolUseID' -count=1
```

Expected: PASS, including immediate running visibility, elapsed text, result replacement, and failed-turn convergence.

## Task 2: Correct transcript screen origin and make the whole tool transaction clickable

**Files:**

- Modify: `<repository-root>/internal/ui/bubble/selection.go`
- Modify: `<repository-root>/internal/ui/bubble/tool_inspect.go`
- Test: `<repository-root>/internal/ui/bubble/bubble_test.go`
- Test: `<repository-root>/internal/ui/bubble/tool_track_test.go`

- [ ] **Step 1: Write failing coordinate and expansion tests.**

Create a ready model with a header, a completed tool transaction, and a result long enough to render more than one expanded row. Derive the screen row from the current layout and assert that clicking the preview row and then an expanded detail row toggles the same entry:

```go
layout := model.currentLayout()
locations := transcriptEntryLocations(model.transcript, model.viewport.Width, model.showThinking, model.animationNow())
toolRow := 1 + layout.headerHeight + locations[0].startRow

click := func(y int) {
    next, _ := model.Update(tea.MouseMsg{X: 4, Y: y, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
    model = next.(appModel)
    next, _ = model.Update(tea.MouseMsg{X: 4, Y: y, Action: tea.MouseActionRelease, Button: tea.MouseButtonLeft})
    model = next.(appModel)
}

click(toolRow)
if !model.transcript[0].toolExpanded {
    t.Fatal("preview row did not expand tool transaction")
}

expanded := transcriptEntryLocations(model.transcript, model.viewport.Width, model.showThinking, model.animationNow())
click(1 + layout.headerHeight + expanded[0].startRow + expanded[0].height - 1)
if model.transcript[0].toolExpanded {
    t.Fatal("expanded detail row did not collapse tool transaction")
}
```

Add a scrolled-viewport variant that sets `model.viewport.SetYOffset(1)` and confirms the same global transaction is selected. Retain existing drag-selection tests to guard the no-toggle-on-drag rule.

- [ ] **Step 2: Run the focused mouse tests to verify the old coordinate bug fails.**

Run:

```bash
go test ./internal/ui/bubble -run 'Test.*Tool.*Click|TestTranscriptMouse|TestFullWidthTranscriptMouseClick' -count=1
```

Expected: the new header-offset test fails before the coordinate fix because `transcriptContentRow` currently subtracts only the outer top frame.

- [ ] **Step 3: Derive transcript rows from `tuiLayout`.**

Change `transcriptContentRow` to use the actual transcript screen origin:

```go
func (m appModel) transcriptScreenTop() int {
    return 1 + m.currentLayout().headerHeight
}

func (m appModel) transcriptContentRow(y int) (int, bool) {
    row := y - m.transcriptScreenTop()
    if row < 0 || row >= m.viewport.Height {
        return 0, false
    }
    return row, true
}
```

Keep `transcriptContentColumn` cell-based and unchanged except for any test-required comments. This covers both normal header-bearing layouts and tiny layouts where `headerHeight` is zero.

- [ ] **Step 4: Make tool locations use the same render inputs as the viewport.**

Change `transcriptEntryLocations` to accept `at time.Time` and call it from `toolIndexAtTranscriptRow` and `ensureInspectedToolVisible` with `m.animationNow()`:

```go
func transcriptEntryLocations(entries []transcriptEntry, width int, showThinking bool, at time.Time) []transcriptEntryLocation
```

Use `renderEntryAt(entry, width, at)` inside the location calculation. Keep the existing half-open range check:

```go
if row >= location.startRow && row < location.startRow+location.height {
    return location.transcriptIndex, true
}
```

This makes every row of an expanded tool transaction map to its single transcript index. Leave `toggleToolExpansion` as the one state mutation point and preserve its guard that running tools cannot expand.

- [ ] **Step 5: Run mouse and tool-track tests.**

Run:

```bash
go test ./internal/ui/bubble -run 'Test.*Tool.*Click|TestTranscriptMouse|TestFullWidthTranscriptMouseClick|TestToolTrack' -count=1
```

Expected: PASS for header offset, scroll offset, preview-row click, expanded-detail click, and drag selection.

## Task 3: Replace clamped Ripple motion with a slow travel-and-exit lifecycle

**Files:**

- Modify: `<repository-root>/internal/ui/bubble/status_line.go`
- Test: `<repository-root>/internal/ui/bubble/status_line_test.go`

- [ ] **Step 1: Write failing phase-boundary tests.**

Update the existing Ripple tests and add explicit boundary checks:

```go
func TestTokenFrontierRippleContinuesPastRightEdge(t *testing.T) {
    model := newTestModel(&fakeRunner{stats: loop.ContextStats{UsedTokens: 40000, LimitTokens: 100000}})
    model.isGenerating = true
    model.cursorFrameAt = time.Unix(0, int64(tokenRippleTravel))
    atEdge := ansi.Strip(model.renderDockStatusLine(100))
    model.cursorFrameAt = time.Unix(0, int64(tokenRippleTravel+tokenRippleExit/2))
    midExit := ansi.Strip(model.renderDockStatusLine(100))
    model.cursorFrameAt = time.Unix(0, int64(tokenRippleCycle-time.Millisecond))
    end := ansi.Strip(model.renderDockStatusLine(100))
    if atEdge == midExit || midExit == end {
        t.Fatalf("ripple did not progress through exit phase: edge=%q mid=%q end=%q", atEdge, midExit, end)
    }
    if tokenRippleFade(tokenRippleTravel+tokenRippleExit/2) <= 0 || tokenRippleFade(tokenRippleCycle-time.Millisecond) >= tokenRippleFade(tokenRippleTravel+tokenRippleExit/2) {
        t.Fatalf("ripple fade does not decrease across exit phase")
    }
}
```

Also retain exact-width checks and the existing idle-static assertion. Update duration assertions to expect `tokenRippleTravel == 3*time.Second`, `tokenRippleExit == 600*time.Millisecond`, and `tokenRippleCycle == tokenRippleTravel+tokenRippleExit`.

- [ ] **Step 2: Run Ripple tests to verify the old clamped implementation fails.**

Run:

```bash
go test ./internal/ui/bubble -run 'TestTokenFrontierRipple|TestStatusLineExactWidthAcrossWidths|TestStatusLineWorkingDuringToolCall' -count=1
```

Expected: the new right-edge progression test fails because the current implementation clamps `head` to `width-1` and keeps the tail stationary during fade.

- [ ] **Step 3: Implement the two-stage virtual-head calculation.**

Replace the current constants with:

```go
const (
    tokenRippleTravel = 3 * time.Second
    tokenRippleExit   = 600 * time.Millisecond
    tokenRippleCycle  = tokenRippleTravel + tokenRippleExit
    tokenRippleTail   = 14
)
```

Compute a floating/rounded head without clamping it to the visible width:

```go
func tokenRippleHead(usedCells, width int, phase time.Duration) int {
    lastVisible := maxInt(usedCells, width-1)
    if phase <= tokenRippleTravel {
        progress := clamp01(float64(phase) / float64(tokenRippleTravel))
        return usedCells + int(float64(lastVisible-usedCells)*progress+0.5)
    }
    exitProgress := clamp01(float64(phase-tokenRippleTravel) / float64(tokenRippleExit))
    return lastVisible + int(float64(tokenRippleTail)*exitProgress+0.5)
}
```

Render only visible tail cells, using `head` even when it is greater than `width-1`. During exit, multiply the existing background-to-signal alpha by `1-exitProgress`. Remove the old post-travel `progress=1` branch and the width clamp. Keep the consumed frontier and idle path unchanged.

- [ ] **Step 4: Run Ripple tests and verify all phase boundaries pass.**

Run:

```bash
go test ./internal/ui/bubble -run 'TestTokenFrontierRipple|TestStatusLineExactWidthAcrossWidths|TestStatusLineWorkingDuringToolCall' -count=1
```

Expected: PASS; the rendered line remains exactly the requested width while the visual tail changes at the right edge and fades out before reset.

## Task 4: Run integration verification and real PTY smoke tests

**Files:**

- Verify: all modified files under `<repository-root>/internal/ui/bubble`
- Verify: `<repository-root>/docs/superpowers/specs/2026-07-28-tool-runtime-hit-ripple-design.md`

- [ ] **Step 1: Run the focused Bubble tests after all three fixes.**

Run:

```bash
go test ./internal/ui/bubble -run 'TestTool|TestTranscriptMouse|TestFullWidthTranscriptMouseClick|TestTokenFrontierRipple|TestStatusLine|TestViewAnchorsTerminalCursor' -count=1
```

Expected: PASS.

- [ ] **Step 2: Run the complete repository checks.**

Run:

```bash
go test ./internal/ui/bubble -count=1
go test ./... -count=1
git diff --check
```

Expected: both Go test commands pass and `git diff --check` produces no output.

- [ ] **Step 3: Smoke-test the real TUI in a PTY.**

Run:

```bash
PAW_TOKEN_TRACER=0 PAW_STREAMMA=0 go run ./cmd/agent/main.go
```

Verify manually at normal width and after resizing:

- a long-running tool visibly keeps its preview row with `running` and elapsed time;
- after completion, the same row becomes `ok` and can be expanded;
- clicking both the preview and expanded result rows toggles the same transaction;
- Ripple moves more slowly, reaches the right edge, continues off-screen, fades, and restarts from the used-token endpoint;
- `ready` leaves Ripple static;
- input cursor and existing worktree/status layout remain stable.

Exit the PTY with two quick `Ctrl+C` keystrokes, then run:

```bash
NO_COLOR=1 PAW_TOKEN_TRACER=0 PAW_STREAMMA=0 go run ./cmd/agent/main.go
```

Confirm the same state transitions do not panic or corrupt the layout under `NO_COLOR=1`.

- [ ] **Step 4: Confirm the final diff is scoped.**

Run:

```bash
git status --short --branch
git diff --stat
git diff --check
```

Expected: only the approved Bubble fixes/tests plus the already-existing uncommitted visual redesign files are present; no generated companion files are staged.
