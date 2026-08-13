# Plan

## Token Tracer high-density workspace

1. [x] Finalize and review the approved design specification.
2. [x] Write an implementation plan after explicit specification approval.
3. [ ] Implement one bounded vertical slice at a time with tests first.
4. [ ] Complete frontend, Go, runtime, and visual verification before delivery.

Design: `docs/superpowers/specs/2026-08-13-token-tracer-docking-workspace-design.md`.
Implementation plan: `docs/codex/plans/2026-08-13-token-tracer-docking-workspace.md`.

## llm-metadata context windows (2026-08-13)

- Embed basellm/llm-metadata context windows (330 models) as `internal/model/metadata/context_windows.json`.
- Resolution: explicit per-model > profile > llm-metadata > default 128k.
- Regenerate via `scripts/gen-llm-metadata.sh`.
