# Verification

## Token Tracer high-density workspace

Definition of done and detailed checks live in sections 14 and 15 of `docs/superpowers/specs/2026-08-13-token-tracer-docking-workspace-design.md`.

Required command baseline — all green on 2026-08-13:

```bash
npm --prefix internal/tokentracer/dashboard test        # 11 files, 33 tests passed
npm --prefix internal/tokentracer/dashboard run build   # typecheck + vite build ok
cd internal/tokentracer/dashboard && npx playwright test # 6/6 e2e passed
go build ./...                                          # ok
go test ./internal/tokentracer -count=1                 # ok
```

- Browser evidence (fresh, non-empty): `.agent/visual/token-tracer-desktop.png` (1440x1000), `.agent/visual/token-tracer-narrow.png` (760x900).
- Evidence note: `.agent/visual/token-tracer-docking-workspace.md`.
- E2E coverage: dock/tab/resize/persist/close/restore/reset/undo, linked selection without filtering, SSE abort/recovery, corrupted-layout recovery with single backup key, narrow-mode layout preservation, 2,000-event virtualized Events stream.
- Known caveat: the default Dockview layout produced by `applyDefaultLayout` (heatmap+calls left column vs flame right column, inspector+events tabbed) places Calls full-width at the bottom because Dockview 7 splits the whole container for a `below` split; the plan's diagram is illustrative and all proportions remain user-adjustable.

## llm-metadata context windows

- `go build ./...` green on 2026-08-13.
- `go test ./...` green on 2026-08-13.

## Input dock border layout

- Bottom border order: input mode left, Token Usage middle, project/branch right.
- Queue summary temporarily replaces the middle Token Usage region while preserving mode and project/branch anchors.
- Boundary coverage includes 71/72-column worktree visibility, exact terminal-cell width, and long CJK project/branch labels.
- `go build ./...`, `go test ./...`, and `git diff --check` green on 2026-08-14.
- Visual evidence: `.agent/visual/input-dock-layout.png` and `.agent/visual/input-dock-layout.md`.

## Subagent transcript preview (2026-08-15)

- `go build ./...` green.
- `go test ./...` green.
- Real-data check: 783KB envelope-format subagent transcript parses to 38 non-empty entries via `loadSubagentTranscriptEntries` (previously 0).

## Activity right sidebar (2026-08-15)

- `go build ./...` green; `go test ./...` green.
- Visual: `.agent/visual/activity-side-panel-open.png` + `activity-side-panel-card.png` + evidence note; independent pixel analysis confirmed right-edge alignment (x=[436,858] vs card x=[615,858], same right edge 857–858), vertical centering, 6 content lines incl. task list.
- Caveat: card screenshot has a 21px stray vertical line artifact at the capture layer (same pipeline as previous evidence; absent in open shot).
