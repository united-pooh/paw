# TUI Visual Hierarchy Implementation Plan

> **For Codex workers:** Implement task-by-task, preserve unrelated changes, and run the checks below. This plan executes the user's approved visual proposal; no installation or push is requested.

**Goal:** Put answers and input ahead of configuration, decoration, and telemetry.

**Architecture:** Keep Bubble Tea's viewport, cell widths, projection, and stored messages. Change presentation in existing renderers; model disclosure is transient entry state, using the existing click and Ctrl+O paths. Use the existing ANSI-to-HTML fixture exporter for visual evidence.

**Tech Stack:** Go, Bubble Tea, Lip Gloss, existing Playwright installation.

## 1. Compact model notification

- [x] Test one-line collapsed render, complete expanded details, click collapse/expand, Ctrl+O, narrow widths in `internal/ui/bubble/model_card_test.go`.
- [x] Replace `renderModelSwitchCard` default with `renderModelSwitchNotice(body, width, false)`; preserve details behind the disclosure. Wire entry-local overrides in `types.go`, `transcript.go`, `selection.go` and `model_card.go`.
- [x] Run `go test ./internal/ui/bubble -run 'Model.*(Card|Notice)|ModelNotice' -count=1`.

## 2. Quiet Markdown and message styling

- [x] Keep bold in the palette foreground (`MarkdownBold: fg`), use neutral readable summary text, and a softly filled user row. Preserve custom theme roles and syntax colors.
- [x] In `markdown.go`, render header, one separator, then wrapped rows with spaces at the former border positions. Honor declared alignment. Cap ordinary prose at 104 cells without limiting tables/code. Keep the same outer gutter and hit-test coordinates.
- [x] Update table assertions to test content, wrapping, alignment and ANSI integrity; remove only obsolete border expectations.
- [x] Run `go test ./internal/ui/bubble -run 'Markdown|Theme|Transcript.*(User|Gutter)|InputToken' -count=1`.

## 3. Clear input and telemetry

- [x] Replace the input-top token band with `dockRuleStyle.Render(strings.Repeat("─", width))`. Keep token counts in the footer, explicitly labeling context occupancy and cache proportion. Fit shorter labeled forms on narrow terminals.
- [x] Keep the existing input projection/cursor math; always render the empty input prompt instead of hiding it after first interaction.
- [x] Use plain spaces between footer segments instead of a second horizontal rule.
- [x] Run `go test ./internal/ui/bubble -run 'Dock|Status|Input|Cursor|Token|Layout' -count=1`.

## User refinement: sticky current-turn input

- [x] Cache user entry locations alongside rendered spans; update only the changed suffix and binary-search by viewport top.
- [x] When the current user entry is above the viewport, overlay its first rendered line in transcript row zero. The original in-view row suppresses the overlay. Keep subsequent content rows and viewport bounds unchanged.
- [x] Use a tinted full-row background. Clicking the sticky row returns to its source; exclude this control row from transcript text/tool hit tests to avoid selecting hidden text.
- [x] Test multi-turn scroll transitions, multiline inputs, incremental updates, resizing, bottom content and mouse coordinates; capture sticky and original-in-view states.

## Final checks

- [x] Capture one Chinese disk-cleanup fixture at 60, 100 and 140 columns using `appModel.View()`; assert frame size, absence of overflow and available input. Compare with HEAD using a temporary detached worktree.
- [x] Capture actual ANSI output in the existing browser fixture workflow, take fresh screenshots, and record dimensions/observations under `.agent/visual/`.
- [x] Run `go test ./internal/ui/bubble/... ./internal/ui/theme/...`, `go vet ./internal/ui/bubble/... ./internal/ui/theme/...`, `go build ./...`, and `git diff --check`. Update expected golden snapshots only for intentional presentation changes.
- [x] Record completion and limitations in `memory/progress.md` and `memory/verify.md`. Leave unrelated changes and the installed user binary intact.
