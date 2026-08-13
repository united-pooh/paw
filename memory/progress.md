# Progress

## Token Tracer high-density workspace

- [x] Inspect the existing Token Tracer implementation and data contracts.
- [x] Compare Calls Table, Token Heatmap, and Folded Flame concepts.
- [x] Confirm IDE-style docking, persistence, linked highlighting, single-instance panels, and visual direction.
- [x] Write and self-review the design specification.
- [x] Obtain final user review of the written specification.
- [x] Write the implementation plan.
- [x] Implement and verify the feature.
  - [x] Frontend: React 19 + TypeScript + Vite + Dockview 7 dashboard under `internal/tokentracer/dashboard` (33 unit/component tests green).
  - [x] Go embed: `dashboard_embed.go` serves built `dist/`; legacy `dashboard.go` and `legacyDashboardHTML` removed.
  - [x] E2E: Go fixture server + 6 Playwright browser tests green.
  - [x] Visual evidence: `.agent/visual/token-tracer-{desktop,narrow}.png` + evidence note.

## llm-metadata context windows

- [x] Generate embedded data (330 models) + generator script.
- [x] MetadataContextLimit lookup (case-insensitive, provider/ prefix stripped).
- [x] EffectiveContextLimitTokens: metadata layer; default 256k -> 128k.
- [x] Tests: llm_metadata_test.go, config_test.go, bubble_test.go assertion.
- [x] Full `go build ./...` and `go test ./...` verification green.
