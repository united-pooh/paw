# Token Monitor Implementation Plan

> **For Codex workers:** Implement task-by-task, keep edits scoped, use apply_patch for manual edits, and execute the verification commands below. The user approved implementation on 2026-10-04; no further design approval is required.

**Goal:** Restore the context bar and add a btop-inspired Tokens page to Ctrl+G Activity, with compact and fullscreen layouts.

**Architecture:** Reuse the existing token frontier renderer, Activity geometry, provider usage ledger, and input-atom estimates. A bounded asynchronous LedgerReader projection provides current-session/descendant totals, tool input exposure and recent requests. A separate UI arrival sampler records visible output speed and first visible text, never substitutes estimates for provider billing, and never stores text.

**Tech Stack:** Go, Bubble Tea, Lip Gloss, existing JSONL telemetry, existing ANSI-to-HTML visual fixtures; no new dependencies.

## Approved behavior

- Context bar above the composer: cached/other/remaining segments and width-aware labels; not task completion percentage.
- Activity tabs cycle Tasks → Todo → Tokens; f expands Tokens, f/Esc returns, Ctrl+G closes; preserve draft, task preview, focus and width.
- Ledger scope is current session plus provably linked descendant tasks in the workspace. Session switches discard old in-flight results.
- Unknown provider fields display —; partial coverage and read errors remain visible. Reasoning is an output subset. Tool input exposures are estimates and are not added to provider totals.
- TPS is visible-text estimation over a rolling three-second window, with a 60-second bounded history; first text is timed from request start. Completed requests retain average/latency but current speed returns to zero.
- Ledger IO occurs outside Update/View and only while Tokens is visible; at most one query in flight. Sampler storage and tool lifecycle tracking are bounded.

## Slice 1: Context and ledger contracts

- [x] Add and observe failures for child-token contamination and restored bar glyphs; fix `engine.go` context separation and `status_line.go` display. Keep historical input+output occupancy semantics.
- [x] Add `internal/tokentracer/monitor_test.go` cases for nested session ancestry, unrelated workspaces, response snapshot merging, explicit zero/unknown, and repeated tool input exposure.
- [x] Implement `(*LedgerReader).Monitor(workspace, sessionID string) (MonitorSnapshot, error)` using existing scan and aggregation helpers. Bound output to 12 tools and 12 requests.
- [x] Run `go test ./internal/tokentracer ./internal/runtime/loop -run 'Test(Monitor|ContextStats)' -count=1`.

## Slice 2: Live sampling and async UI state

- [x] Add `token_monitor_live_test.go`: fragmented text has the same estimate as whole text, first-visible-text excludes reasoning, rolling speed becomes zero without output, finish/reset behavior and bounds.
- [x] Implement live sampling with arrival timestamps, fractional ASCII/4 plus non-ASCII estimate, fixed 60-second buckets, and deduplicated tool execution tracking.
- [x] Add `token_monitor_test.go`: single query at a time, results rejected after session change, no IO on hidden page, failure retention, and UI callback delivery.
- [x] Add model-start/usage receiver callbacks and timestamped text arrival messages. Keep transcript message handling unchanged.
- [x] Run `go test ./internal/ui/bubble -run 'TestTokenMonitor' -count=1`.

## Slice 3: Terminal rendering and navigation

- [x] Add pure renderer tests for 32/52/80/120/160 columns, short heights, missing/partial data, theme behavior and selected-request detail.
- [x] Implement `renderTokenMonitor(tokenMonitorView, width, height)` and reuse terminal-cell width helpers; no charts generated from decorative sine waves.
- [x] Extend existing Activity state/navigation/geometry with Tokens and explicit fullscreen, including regression tests preserving input and preview.
- [x] Add new actual appModel.View capture fixtures; verify both docked and fullscreen screenshots independently.
- [x] Run `go test ./internal/ui/bubble -count=1`.

## Slice 4: Integration and delivery

- [x] Use an isolated synthetic HTTP/SSE provider to verify request-start → UI speed → final ledger totals without sending private conversation data.
- [x] Run `go test -race ./internal/tokentracer ./internal/ui/bubble -count=1`, targeted loop tests, `go vet ./...`, `make check-web-dist`, and build an isolated binary under `.agent/bin/token-monitor/`.
- [x] Run full Go suite serially; preserve and report any independently confirmed existing failure instead of silently dropping checks.
- [x] Save fresh screenshots and a `.agent/visual/token-monitor.md` evidence note with files, viewport, source fixture and observed results. Update usage/UI documentation and repo memory status.
- [x] Independent review of scope/unknown semantics, navigation/session races, and visual captures; fix issues before delivery. No commit, push or global install unless separately requested.

## Native PTY follow-up

- [x] Fix the preexisting lone-Esc input-reader stall exposed by the actual fullscreen/return smoke test. Bound continuation waiting, preserve file flags, and test delivery boundaries as well as final byte concatenation.
- [x] Rebuild the isolated binary and repeat the local synthetic-provider PTY run: streaming/final ledger, Ctrl+G/Tab/f/Esc, 80×24 resize, preserved draft and clean exit.

## User correction: whole foreground turn and tool recovery (2026-10-05)

This supersedes the primary per-request average: `Turn avg` is cumulative main-request output divided by foreground task wall time, including tool execution and waiting. Provider output snapshots reconcile estimates; missing usage and newly received tails remain marked approximate. Individual response completion does not freeze the turn. Completion of chat/task/goal/plan freezes it, and a new foreground task resets it. The 60-second graph remains secondary.

The user also authorized silent recovery from malformed tool protocols. Keep strict atomic validation before execution, try existing deterministic argument repair, then request fresh native calls with ephemeral correction context. Retain completed tool results; never replay them from the runtime. Log failure/retry/recovered/exhausted metadata, allow up to 10 consecutive retries and reset after a valid response. Normal execution errors remain tool results that the model can correct; cancellation, denied permission and uncertain execution status are not bypassed.

- [x] Observe failing regression tests and implement turn-level accounting and lifecycle wiring.
- [x] Add typed argument failures and normal/recovered loop protocol recovery, covering no duplicate effects, cancellation, budgets, privacy and ordinary tool-error continuation.
- [x] Independently review both paths and run complete affected-package race checks, vet and isolated build.
- [x] Native PTY passes 19 checks with 2.005 seconds of real Read waiting, 120 total output, 7.6 seconds elapsed and 15.7 tok/s frozen after completion. Four fresh PNGs and structured evidence: `.agent/visual/turn-rate-tool-recovery.md`, `.agent/verification/turn-rate-tool-recovery/验证记录.md`. No install, commit or push.

## User correction: permanent passive sidebar (2026-10-04)

This replaces the focus/tab/fullscreen interaction described above. Default sidebar is visible; bottom half permanently shows token history, top half shows the latest semantically changed Todo or task snapshot. Input keeps all ordinary editing keys. Ctrl+G only toggles visibility, narrow terminals below 65 columns hide the sidebar without hiding input. Reuse ledger/live sampler/CTX bar.

- [x] Implement passive input, geometry, startup/single-flight polling, session reset and idle typing guard.
- [x] Implement semantic update arbitration with durable restore timestamps and static overflow.
- [x] Render a large 60-second chart plus input/output/cache/tool readings in the lower half.
- [x] Validate Bubble suite/race, build, native PTY, fresh screenshots and independent review. Final evidence: `.agent/visual/passive-token-sidebar.md` and `.agent/visual/token-monitor/pty-evidence.md`; 17 native checks pass, full serial suite passes 41 packages with only the previously reproduced config deadlock skipped.
