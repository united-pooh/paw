# Bash Tool Interrupt and Live Output Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Allow the first `Ctrl+C` to interrupt the active Bash tool while preserving partial output, the second `Ctrl+C` to cancel the whole turn, and show Bash stdout/stderr live in the transcript.

**Architecture:** Keep `tool.Tool.Run(ctx, input)` as the compatibility interface for every existing tool. Add an optional Bash streaming capability implemented only by `internal/tool/exec.BashTool`; the loop detects that capability, registers the active tool cancel function with `Runner`, and forwards output chunks through optional UI events. Bubble Tea renders a running Bash entry incrementally and marks it interrupted on cancellation. The existing one-shot result path remains the fallback for all non-Bash tools and headless/subagent consumers.

**Tech Stack:** Go, `context`, `os/exec`, process groups, Bubble Tea, existing `ui.UI` events, existing transcript/tool rendering and `go test`.

## Global Constraints

- Keep the existing `tool.Tool` interface source-compatible; streaming is an optional capability.
- First `Ctrl+C` cancels only an active Bash tool; a second `Ctrl+C` cancels the active turn.
- Preserve already received stdout/stderr after Bash interruption and mark the tool `interrupted` rather than successful.
- Terminate the Bash process group and child processes on cancellation or timeout.
- Do not add live stdout/stderr support for MCP or subagent tools in this change.
- Live output is UI-only; send one final tool result to the model/history.
- Do not force the transcript to the bottom when the user has scrolled away from the bottom.
- Keep the existing output size limit and expose a truncation indicator when the live buffer exceeds it.

---

## File Map

- Modify `internal/tool/tool.go`: define the optional streaming capability and stream event types without changing `Tool`.
- Modify `internal/tool/exec/bash.go`: implement streaming Bash execution, process-group termination, timeout/cancellation mapping, and bounded output collection.
- Create `internal/tool/exec/bash_stream_test.go`: unit tests for stdout/stderr chunks, cancellation, timeout, non-zero exit, and child-process termination.
- Modify `internal/loop/runner.go`: store the active tool cancellation handle and expose `CancelCurrentTool()` / `CancelTurn()`.
- Modify `internal/loop/tool_execution.go`: route Bash through the streaming capability, emit live output and final results, and clear the active tool handle on every exit path.
- Create or modify `internal/loop/tool_execution_test.go`: test tool-level cancellation, final interrupted result semantics, and fallback behavior for ordinary tools.
- Modify `internal/ui/ui.go`: add optional live tool output and tool lifecycle event types; leave the required `UI` methods compatible.
- Modify `internal/ui/headless/headless.go`: ignore or minimally print live events so headless and subagent execution remain safe.
- Modify `internal/ui/bubble/types.go`: add live-output fields to `transcriptEntry` and `tool` event message types, including stream labels, truncation, and interrupted state.
- Modify `internal/ui/bubble/bubble.go`: forward the new optional UI events to Bubble Tea.
- Modify `internal/ui/bubble/app.go`: handle live tool start/output/finish events and route `Ctrl+C` with tool-first semantics.
- Modify `internal/ui/bubble/commands.go`: add the runner cancellation calls and reset cancellation state after completion.
- Modify `internal/ui/bubble/transcript.go` and the relevant tool display/render helpers: render live output, stderr styling, truncation, and interrupted status while retaining existing completed-tool behavior.
- Modify `internal/ui/bubble/tool_track_test.go` and/or create `internal/ui/bubble/live_tool_output_test.go`: test lifecycle matching, incremental rendering, scrolling behavior, and interrupted state.
- Modify `internal/ui/bubble/bubble_test.go`: test first/second `Ctrl+C` routing and no-tool fallback.

---

### Task 1: Add the optional streaming tool contract

**Files:**
- Modify: `internal/tool/tool.go`
- Test: `internal/tool/exec/bash_stream_test.go` (contract usage will be exercised in Task 2)

**Interfaces:**
- Produces:
  ```go
  type ToolOutputStream string
  const (
      ToolOutputStdout ToolOutputStream = "stdout"
      ToolOutputStderr ToolOutputStream = "stderr"
  )

  type ToolOutputEvent struct {
      Stream ToolOutputStream
      Chunk  string
  }

  type StreamTool interface {
      Stream(ctx context.Context, input json.RawMessage, emit func(ToolOutputEvent) error) (output string, interrupted bool, err error)
  }
  ```
- `Tool` itself remains unchanged.

- [ ] **Step 1: Add the capability types and interface.**

  Use `emit` as a synchronous callback so the executor can stop when the UI/loop is canceled. The returned `output` is the final bounded combined result for the model; `interrupted` distinguishes cancellation from ordinary failure.

- [ ] **Step 2: Run the focused package test/build.**

  Run: `go test ./internal/tool/...`

  Expected: PASS; no existing tool implementation needs to change because the new interface is optional.

- [ ] **Step 3: Commit the contract.**

  ```bash
  git add internal/tool/tool.go
  git commit -m "feat: add optional streaming tool contract"
  ```

### Task 2: Implement cancellable Bash streaming

**Files:**
- Modify: `internal/tool/exec/bash.go`
- Create: `internal/tool/exec/bash_stream_test.go`

**Interfaces:**
- Consumes: `tool.StreamTool`, `tool.ToolOutputEvent`.
- Produces: `(*BashTool).Stream(context.Context, json.RawMessage, func(tool.ToolOutputEvent) error) (string, bool, error)`.

- [ ] **Step 1: Write failing tests for separate stdout/stderr chunks.**

  Use a temporary workspace and command:
  ```go
  func TestBashStreamEmitsStdoutAndStderr(t *testing.T) {
      root := t.TempDir()
      bash := &BashTool{Root: root}
      var got []tool.ToolOutputEvent
      output, interrupted, err := bash.Stream(context.Background(), json.RawMessage(`{"command":"printf out; printf err >&2"}`), func(event tool.ToolOutputEvent) error {
          got = append(got, event)
          return nil
      })
      if err != nil || interrupted { t.Fatalf("Stream() = output=%q interrupted=%v err=%v", output, interrupted, err) }
      if len(got) == 0 { t.Fatal("expected output events") }
      if !containsStreamText(got, tool.ToolOutputStdout, "out") || !containsStreamText(got, tool.ToolOutputStderr, "err") {
          t.Fatalf("events = %#v", got)
      }
  }
  ```
  Add a small test helper in the same package to inspect event stream/text without assuming OS pipe scheduling order.

- [ ] **Step 2: Run the test and verify it fails because `Stream` is absent.**

  Run: `go test ./internal/tool/exec -run TestBashStreamEmitsStdoutAndStderr -v`

  Expected: FAIL to compile with the missing `Stream` method.

- [ ] **Step 3: Write the minimal process-group streaming implementation.**

  In `Stream`:
  1. Decode and safety-check the existing Bash input and resolve the workspace directory using the existing helpers.
  2. Create the timeout context from the caller context.
  3. Start `bash -c` with `exec.Command`/`CommandContext`, set `SysProcAttr.Setpgid = true` on Unix variants, and connect separate stdout/stderr pipes.
  4. Read both pipes concurrently. For every non-empty read, call `emit` with the appropriate stream and append it to a bounded collector.
  5. If `emit` returns an error, cancel the command context and return that error after waiting for the process.
  6. After the process exits, classify `context.Canceled` as `interrupted=true`, classify deadline as the existing timeout error, and keep non-zero exits in the final model output as the existing `[exit status: ...]` text.
  7. On cancellation/timeout, send a process-group kill (`-pid`) before waiting so descendants cannot survive.
  8. Preserve the existing output limit and make the collector report whether content was truncated.

  Keep platform-specific process-group setup/termination in small files such as `bash_process_unix.go` and `bash_process_windows.go` if the current build matrix requires it; do not put Unix-only `syscall.SysProcAttr` fields in a cross-platform file.

- [ ] **Step 4: Run the focused tests.**

  Run: `go test ./internal/tool/exec -run 'TestBashStream|Test.*Bash.*Cancel|Test.*Bash.*Timeout' -v`

  Expected: PASS.

- [ ] **Step 5: Add cancellation and child-process tests.**

  Add tests that start a command which prints once and then sleeps, cancel the context, and assert `interrupted` is true and the first output remains. Add a shell command that starts a background sleep and assert the process group is gone after return on platforms supporting process groups. Add non-zero-exit and timeout tests and assert they are not reported as successful completion.

- [ ] **Step 6: Run race-enabled package tests.**

  Run: `go test -race ./internal/tool/exec`

  Expected: PASS with no race reports.

- [ ] **Step 7: Commit the Bash implementation.**

  ```bash
  git add internal/tool/exec/bash.go internal/tool/exec/bash_stream.go internal/tool/exec/bash_process_*.go internal/tool/exec/bash_stream_test.go
  git commit -m "feat: stream and interrupt bash output"
  ```

### Task 3: Add Runner tool cancellation and loop event forwarding

**Files:**
- Modify: `internal/loop/runner.go`
- Modify: `internal/loop/tool_execution.go`
- Create/modify: `internal/loop/tool_execution_test.go`

**Interfaces:**
- Produces on `Runner`:
  ```go
  func (runner *Runner) CancelCurrentTool() bool
  func (runner *Runner) CancelTurn()
  ```
- Consumes: `tool.StreamTool`, `ui.ToolOutputEvent` (or the final event shape selected in Task 4).

- [ ] **Step 1: Write failing Runner cancellation tests.**

  Test a fake streaming tool that blocks after emitting `"partial"`. Start `runToolCallWithCheckpoint` in a goroutine, wait until the fake tool reports started, call `runner.CancelCurrentTool()`, and assert the returned `message.ToolResult` is an error/interrupted result containing `partial`. Add a test asserting `CancelCurrentTool()` returns false when no tool is registered.

- [ ] **Step 2: Run the focused test and verify failure.**

  Run: `go test ./internal/loop -run 'Test.*CancelCurrentTool|Test.*InterruptedTool' -v`

  Expected: FAIL because Runner has no active-tool handle and execution still calls only `Tool.Run`.

- [ ] **Step 3: Add synchronized active-tool state to Runner.**

  Add a mutex-protected field containing the active tool ID/name and `context.CancelFunc`. Implement registration and clear helpers privately. `CancelCurrentTool()` must atomically take a snapshot, call the cancel function outside the lock, and return whether the active tool was Bash/stream-capable. `CancelTurn()` must cancel the active turn context through the existing turn cancellation path.

- [ ] **Step 4: Route streaming tools through the optional capability.**

  In `runResolvedToolCallWithCheckpoint`, emit the normal tool call first, register the active Bash cancel function, call `Stream`, forward each output chunk to the optional UI receiver, then clear the active handle before emitting exactly one final `OnToolResult`. Convert `interrupted=true` to a final result with `IsError=true` and content that starts with `interrupted` and includes the bounded partial output. Keep `executeResolvedToolCall` unchanged for ordinary tools and preserve file mutation/checkpoint behavior.

  For concurrency-safe batches, do not register more than one active tool in the single Runner slot. Either exclude streaming Bash from concurrent batching via `IsConcurrencySafe` handling or run a streaming Bash batch sequentially; document and test the chosen behavior. The selected behavior must ensure `Ctrl+C` identifies the Bash currently visible as running.

- [ ] **Step 5: Run loop tests and the race detector.**

  Run: `go test ./internal/loop -run 'Test.*Tool|Test.*Cancel|Test.*Stream' -v`

  Then run: `go test -race ./internal/loop`

  Expected: PASS with no duplicate final-result events and no races around active-tool state.

- [ ] **Step 6: Commit the loop cancellation layer.**

  ```bash
  git add internal/loop/runner.go internal/loop/tool_execution.go internal/loop/tool_execution_test.go
  git commit -m "feat: cancel active streaming tool from runner"
  ```

### Task 4: Extend UI event plumbing for live tool output

**Files:**
- Modify: `internal/ui/ui.go`
- Modify: `internal/ui/bubble/bubble.go`
- Modify: `internal/ui/headless/headless.go`
- Modify: `internal/loop/tool_execution.go` (call the optional receiver)

**Interfaces:**
- Produces:
  ```go
  type ToolOutputEvent struct {
      ToolUseID string
      Name      string
      Stream    string // "stdout" or "stderr"
      Chunk     string
  }
  type ToolLifecycleEvent struct {
      ToolUseID string
      Name      string
      Status    string // "running", "completed", "failed", "interrupted"
      Truncated bool
  }
  type LiveToolOutputReceiver interface {
      OnToolStarted(event ToolLifecycleEvent) error
      OnToolOutput(event ToolOutputEvent) error
      OnToolFinished(event ToolLifecycleEvent) error
  }
  ```
- The required `ui.UI` interface is not expanded; headless and subagent implementations remain source-compatible.

- [ ] **Step 1: Write event-construction tests.**

  Test that a UI implementing `LiveToolOutputReceiver` receives started, ordered output chunks, and exactly one finished event for a streamed Bash call. Test that a UI without the optional interface still receives the normal final tool result and does not panic.

- [ ] **Step 2: Add the optional event types and Bubble forwarding methods.**

  `internal/ui/bubble/bubble.go` should clone string/ID fields and use `u.send(...)` exactly as existing tool call/result forwarding does. `headless.UI` should implement no-op methods (or a concise line-oriented output) so tests can use it without special adapters; it must not dump unbounded live output by default.

- [ ] **Step 3: Connect loop events to the optional receiver.**

  Add helper methods in `tool_execution.go` that type-assert `runner.ui` to `ui.LiveToolOutputReceiver` and ignore the absence of that capability. Propagate receiver errors only when the existing UI event path would propagate them; cancellation must still clear the active tool handle.

- [ ] **Step 4: Run UI and loop tests.**

  Run: `go test ./internal/ui/... ./internal/loop/...`

  Expected: PASS.

- [ ] **Step 5: Commit event plumbing.**

  ```bash
  git add internal/ui/ui.go internal/ui/bubble/bubble.go internal/ui/headless/headless.go internal/loop/tool_execution.go
  git commit -m "feat: expose live tool output UI events"
  ```

### Task 5: Render running Bash output and interrupted state in Bubble Tea

**Files:**
- Modify: `internal/ui/bubble/types.go`
- Modify: `internal/ui/bubble/app.go`
- Modify: `internal/ui/bubble/transcript.go`
- Modify: `internal/ui/bubble/tool_display.go` and/or the existing style file selected by current renderer organization
- Create/modify: `internal/ui/bubble/live_tool_output_test.go`

**Interfaces:**
- Consumes: `ToolLifecycleEvent`, `ToolOutputEvent`.
- Produces: a `transcriptEntry` whose `toolStatus` is `running`, `completed`, `failed`, or `interrupted`, whose `toolResult`/live buffer contains bounded output, and whose renderer can distinguish stdout/stderr.

- [ ] **Step 1: Write failing transcript tests.**

  Add tests that construct a running Bash entry, apply an stdout event and an stderr event, and assert the rendered text contains both contents, an stderr marker, and no final-result-only placeholder. Add a finish event with `Status: "interrupted"` and assert the rendered text contains the interrupted label while preserving both partial chunks.

- [ ] **Step 2: Run the focused tests and verify failure.**

  Run: `go test ./internal/ui/bubble -run 'Test.*LiveTool|Test.*InterruptedTool' -v`

  Expected: FAIL because the app model has no handlers for live tool events.

- [ ] **Step 3: Add bounded live output state.**

  Extend `transcriptEntry` with separate bounded stdout/stderr buffers or a single ordered list of `{stream, text}` chunks, plus `toolOutputTruncated`. Keep `toolResult` as the final model-facing result; do not use the UI-only live buffer as model history. Match live events by `toolUseID`, falling back to tool name only when the ID is empty, and ignore stale/duplicate finished events.

- [ ] **Step 4: Handle live lifecycle events in `appModel.Update`.**

  On started, create or mark the matching tool entry as running and expanded. On output, append bounded content and refresh the viewport while preserving `wasAtBottom := m.viewport.AtBottom()`. On finished, set the final status, preserve output, apply truncation metadata, and return to existing completed-tool folding rules. If the user was at the bottom before output, keep the viewport at the bottom; otherwise preserve its offset.

- [ ] **Step 5: Update rendering and styles.**

  Render stdout in the normal tool-detail style and stderr with the existing error color plus an explicit `stderr:` label. Render a truncation line when the bounded buffer dropped older content. Render `interrupted` as a distinct status, without treating it as a normal successful result. Include all live state in `transcriptRenderCacheKey` so incremental updates invalidate cached rows.

- [ ] **Step 6: Run transcript tests and race tests.**

  Run: `go test ./internal/ui/bubble -run 'Test.*Tool|Test.*Transcript' -v`

  Then run: `go test -race ./internal/ui/bubble`

  Expected: PASS; output updates do not force scroll jumps or produce data races.

- [ ] **Step 7: Commit the Bubble renderer.**

  ```bash
  git add internal/ui/bubble/types.go internal/ui/bubble/app.go internal/ui/bubble/transcript.go internal/ui/bubble/tool_display.go internal/ui/bubble/live_tool_output_test.go
  git commit -m "feat: render live bash output in transcript"
  ```

### Task 6: Implement first/second Ctrl+C routing

**Files:**
- Modify: `internal/ui/bubble/app.go`
- Modify: `internal/ui/bubble/commands.go`
- Modify: `internal/ui/bubble/bubble.go` (Runner adapter calls if needed)
- Modify: `internal/ui/bubble/bubble_test.go`

**Interfaces:**
- Consumes: `Runner.CancelCurrentTool() bool`, `Runner.CancelTurn()`.
- Produces: deterministic key behavior: active Bash → tool cancellation; canceled Bash/other active turn → turn cancellation; idle state → existing double-Ctrl+C quit behavior.

- [ ] **Step 1: Write failing key-routing tests.**

  Use a fake `Runner` that records calls. Assert:
  ```go
  first := modelWithActiveBash.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
  // fake runner: currentToolCancels == 1, turnCancels == 0

  second := modelWithCanceledBashAndActiveTurn.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
  // fake runner: currentToolCancels == 1, turnCancels == 1
  ```
  Also assert an active non-Bash model operation falls through to turn cancellation, and idle double-Ctrl+C retains the existing quit behavior.

- [ ] **Step 2: Run the focused tests and verify failure.**

  Run: `go test ./internal/ui/bubble -run 'Test.*CtrlC|Test.*Cancel' -v`

  Expected: FAIL because the current handler only calls `cancelModelWork` and has no tool-first branch.

- [ ] **Step 3: Implement the key state machine.**

  In the `ctrl+c` branch, first call `m.runner.CancelCurrentTool()` when `m.isModelWorkRunning()` and an active tool is known. Record that a tool cancellation was requested and keep the turn alive. If no tool was canceled, call the existing turn/model cancellation path. A subsequent Ctrl+C while the turn is still working calls `m.runner.CancelTurn()`/the existing active model cancel. Do not let the idle double-press quit timer combine with a working-state press.

- [ ] **Step 4: Reset cancellation flags on lifecycle completion.**

  Clear tool-cancel state only when the corresponding interrupted/finished event is processed. Preserve the existing `turnFinishedMsg` behavior for restoring drafts and removing only the appropriate interrupted user entry; do not delete the interrupted Bash transcript entry.

- [ ] **Step 5: Run Bubble tests.**

  Run: `go test ./internal/ui/bubble -run 'Test.*CtrlC|Test.*Cancel|Test.*ToolTrack' -v`

  Expected: PASS.

- [ ] **Step 6: Commit key handling.**

  ```bash
  git add internal/ui/bubble/app.go internal/ui/bubble/commands.go internal/ui/bubble/bubble.go internal/ui/bubble/bubble_test.go
  git commit -m "feat: make ctrl-c cancel bash before turn"
  ```

### Task 7: Full integration verification and documentation

**Files:**
- Modify: `docs/superpowers/specs/2026-08-07-bash-tool-interrupt-live-output-design.md` only if implementation details need a final clarification.
- Test: all affected Go packages.

- [ ] **Step 1: Run focused package tests.**

  ```bash
  go test ./internal/tool/exec ./internal/loop ./internal/ui/bubble ./internal/ui/headless
  ```

  Expected: PASS.

- [ ] **Step 2: Run race-enabled tests for the concurrent output paths.**

  ```bash
  go test -race ./internal/tool/exec ./internal/loop ./internal/ui/bubble
  ```

  Expected: PASS with no race reports.

- [ ] **Step 3: Run the full repository test suite.**

  ```bash
  go test ./...
  ```

  Expected: PASS. If an unrelated pre-existing failure appears, record the exact package and failure without weakening the new tests.

- [ ] **Step 4: Run static checks and inspect the diff.**

  ```bash
  go vet ./...
  git diff --check
  git status --short
  ```

  Expected: no vet errors, no whitespace errors, and only files belonging to this feature (plus any pre-existing user modifications) are changed.

- [ ] **Step 5: Commit final integration changes.**

  ```bash
  git add internal/tool internal/loop internal/ui docs/superpowers/specs/2026-08-07-bash-tool-interrupt-live-output-design.md
  git commit -m "feat: support bash interruption and live transcript output"
  ```

## Spec Coverage Self-Review

- Tool-first `Ctrl+C`: Tasks 3 and 6 add active-tool cancellation and key routing.
- Second `Ctrl+C` turn cancellation: Task 6 integrates with existing turn cancellation.
- Live stdout/stderr: Tasks 2, 4, and 5 cover separate pipes, events, and rendering.
- Partial output after interruption: Tasks 2, 3, and 5 preserve and display bounded output.
- Process-group termination: Task 2 tests and implements descendant cleanup.
- Non-Bash compatibility: Tasks 1, 3, and 4 preserve optional interfaces and fallback paths.
- Scroll/folding/truncation/cache invalidation: Task 5 covers all transcript behaviors.
- Idempotent final events and cancellation cleanup: Tasks 3 and 5 cover both loop and UI layers.
- Regression and race coverage: Task 7 runs focused, race, full, and vet checks.

## Plan Self-Review

- No `TBD`, `TODO`, or vague “write tests” placeholders remain in the plan steps.
- All cross-task interfaces are named with concrete Go types and method signatures.
- The required `ui.UI` and `tool.Tool` interfaces remain backward compatible.
- The plan keeps the change to one cohesive feature across tool execution, loop cancellation, and transcript presentation; MCP/subagent streaming remains explicitly out of scope.
