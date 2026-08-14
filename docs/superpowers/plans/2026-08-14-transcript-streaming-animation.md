# Transcript Streaming Animation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add persisted transcript output/effect settings and render newly completed assistant lines with optional one-second independent character/reveal animation while preserving canonical transcript content and existing behavior by default.

**Architecture:** Keep the complete assistant text in `streamLineBuffer` and `transcriptEntry.body`; add transient per-reply/per-line animation metadata to `appModel`; capture settings when a new assistant entry is created. Render assistant Markdown normally first, then apply a grapheme/display-cell-safe visual transform to the rendered lines. Add animation-aware cache invalidation driven by the existing cursor-frame loop. The implementation is divided into disjoint feature areas so one fresh subagent can own each area; an integration subagent then resolves the shared seams and runs the full suite.

**Tech Stack:** Go, Bubble Tea, Lip Gloss, existing `paw/internal/settings`, transcript renderer in `internal/ui/bubble`, terminal-cell helpers, Go tests.

## Global Constraints

- Preserve `entry.body`, stream persistence, search, selection, copy, hyperlink lookup, and Markdown source semantics; animation is render-only.
- Defaults are output mode `line` and render effect `normal`.
- Only assistant entries created after configuration capture can animate; historical and non-assistant entries never animate.
- Each committed assistant line starts an independent one-second animation; later lines do not wait for earlier lines.
- A non-newline tail is flushed as the final complete line when the assistant stream ends.
- Character mode progresses by complete grapheme/display units; never split CJK, emoji, combining marks, wide cells, or ANSI sequences.
- Noise is deterministic for a stable line identity and display-unit index; reveal preserves layout and reaches the exact ordinary rendering at completion.
- Never use `time.Sleep` in Bubble Tea update/render code.
- Do not modify or reset unrelated pre-existing working-tree changes; each worker stages only its assigned files.
- Every worker must add focused tests and run the smallest relevant package test before committing.
- Final verification must pass `go build ./...` and `go test ./...`.

---

## File Map and Ownership

### Settings and General UI — Subagent A

- Modify: `internal/settings/settings.go`
- Modify: `internal/ui/bubble/config_center_general.go`
- Test: `internal/settings/settings_test.go` (or the existing settings test file discovered by the worker)
- Test: `internal/ui/bubble/config_center_general_test.go`

Owns persisted field types, defaults, normalization, JSON round-trip, General-page field descriptors, labels, display values, and cycling/editing behavior. Do not touch transcript rendering or app stream lifecycle.

### Stream and Animation State — Subagent B

- Modify: `internal/ui/bubble/transcript.go`
- Modify: the file that defines `appModel`/`transcriptEntry` if needed (identify exact file before editing)
- Test: `internal/ui/bubble/stream_buffer_test.go` and/or a focused new transcript animation state test

Owns capturing settings for a newly created assistant reply, associating committed complete lines with stable animation identities/start times, final tail flushing, lifecycle cleanup, and the rule that character mode changes visual granularity rather than canonical buffering. Do not implement noise/reveal styling or General config fields.

### Post-render Transform — Subagent C

- Create: `internal/ui/bubble/transcript_animation.go`
- Modify: `internal/ui/bubble/terminal_cells.go` only if a reusable safe styled-cell helper is required
- Test: `internal/ui/bubble/transcript_animation_test.go`

Owns rendering complete assistant output first and applying the `normal`, `noise`, and `reveal` transforms. The public/internal seam should be small and explicit, for example:

```go
type transcriptAnimationMode string
type transcriptRenderEffect string

type transcriptAnimationLine struct {
    ID        uint64
    StartedAt time.Time
}

func animateStyledTranscriptText(rendered string, mode transcriptAnimationMode, effect transcriptRenderEffect, line transcriptAnimationLine, now time.Time, duration time.Duration) string
```

The exact names may follow repository conventions, but the implementation must expose a testable pure transform. It must preserve ANSI control sequences, line breaks, terminal widths, and final output. Noise glyph selection must be deterministic; reveal must conceal only unrevealed display cells while retaining widths.

### Integration, Cache, and Frame Scheduling — Subagent D

- Modify: `internal/ui/bubble/transcript.go`
- Modify: `internal/ui/bubble/app.go` only when frame/update integration is required
- Test: `internal/ui/bubble/bubble_test.go` and focused transcript/cache tests

Owns applying the captured state and pure transform to `renderEntryAt`/assistant rendering, adding animation state to render cache keys/signatures, invalidating only active assistant spans on cursor frames, removing completed animation state, and ensuring viewport/selection paths remain correct. This task begins after A, B, and C have landed because it consumes all three interfaces.

### End-to-end Review and Verification — Subagent E

- Modify: only files needed to fix integration defects discovered in tests
- Test: relevant existing packages plus any missing acceptance tests

Owns cross-feature tests for configuration capture timing, concurrent line timelines, final tail behavior, copy/selection canonical text, width changes, historical entries, and full build/test verification. E must not redesign the feature; it fixes only concrete integration defects against the approved spec.

---

## Task 1: Settings Model and General Config Controls — Subagent A

**Dependencies:** None. Can run in parallel with Tasks 2 and 3.

**Interfaces:**
- Produces `settings.Config` fields representing `transcriptOutputMode` and `transcriptRenderEffect`, default constants, normalization, and validation-compatible values.
- Produces General fields with stable keys, human-readable labels, option lists, and setters consumed by `saveAndApplyGeneral`.
- Required values: output mode `line`/`char`; effect `normal`/`noise`/`reveal`.

- [ ] **Step 1: Inspect existing settings tests and General field conventions.**

Run:

```bash
grep -R "func Test.*Normalize\|DefaultConfig\|configGeneralFields" -n internal/settings internal/ui/bubble/*test.go | head -80
```

Record the exact test files and follow their existing temporary-file/controller setup.

- [ ] **Step 2: Add failing settings tests.**

Cover default values, invalid-value normalization, and JSON persistence. The test assertions must establish:

```go
cfg := settings.DefaultConfig()
if cfg.UI.TranscriptOutputMode != settings.TranscriptOutputLine { t.Fatal(...) }
if cfg.UI.TranscriptRenderEffect != settings.TranscriptEffectNormal { t.Fatal(...) }

raw := `{"ui":{"transcript_output_mode":"invalid","transcript_render_effect":"invalid"}}`
// Load/Normalize must return line + normal rather than invalid values.

saved := settings.Config with char + reveal
// Save then Load must return char + reveal.
```

Use the repository's actual field nesting and names if its conventions require a different location, but keep JSON keys stable and documented in the implementation.

- [ ] **Step 3: Run the focused settings tests and verify they fail for the missing fields/normalizers.**

```bash
go test ./internal/settings -run 'Test.*Transcript|Test.*Normalize|Test.*Save' -count=1
```

Expected: failure identifying the absent transcript settings or default behavior.

- [ ] **Step 4: Implement settings types, defaults, normalization, and persistence.**

Add typed constants and fields, initialize them in `DefaultConfig`, normalize case/whitespace and unknown values to `line` and `normal`, and let existing `Load`/`Save` JSON behavior persist them. Do not change unrelated normalization rules.

- [ ] **Step 5: Add failing/then passing General UI tests.**

Assert that `configGeneralFields` contains both stable keys, that labels mention transcript output/effect, that options are exactly the required values, and that `advanceGeneralEdit` cycles each enum and invokes the existing save/apply path. Follow the existing General test fixture for `appModel`.

```bash
go test ./internal/ui/bubble -run 'Test.*General|Test.*Transcript.*Config' -count=1
```

- [ ] **Step 6: Implement General field descriptors and display mappings.**

Add two enum descriptors to `configGeneralFields`, descriptions to `configGeneralPresentations`, and user-facing display values in `configGeneralDisplayValue`. Keep persisted keys/values separate from Chinese presentation text.

- [ ] **Step 7: Run focused tests and commit only the settings/UI files.**

```bash
go test ./internal/settings ./internal/ui/bubble -run 'Test.*(General|Transcript|Normalize|Save)' -count=1
git add internal/settings/settings.go internal/settings/settings_test.go internal/ui/bubble/config_center_general.go internal/ui/bubble/config_center_general_test.go
git commit -m "feat: add transcript animation settings"
```

---

## Task 2: Complete-line Animation Metadata and Stream Lifecycle — Subagent B

**Dependencies:** None. Can run in parallel with Tasks 1 and 3.

**Interfaces:**
- Produces transient metadata that identifies newly committed assistant lines and their `StartedAt` values.
- Provides a finalization path that records a residual non-newline assistant tail as one complete animated line.
- Does not emit character fragments to `entry.body` or persistence.

- [ ] **Step 1: Inspect `streamLineBuffer`, assistant stream finalization, `transcriptEntry`, and existing activity metadata.**

```bash
grep -R "type transcriptEntry\|func (m \*appModel) finalizeAssistantStream\|func (m \*appModel) appendAssistantDelta\|recordTranscriptEntryActivity" -n internal/ui/bubble
```

- [ ] **Step 2: Add failing lifecycle tests.**

Test that a committed delta containing multiple newline-terminated lines creates stable line identities/timestamps, that later lines have later independent starts, that a final residual tail is flushed on `finalizeAssistantStream`, and that the complete body remains intact in character mode. Use a fixed `now` rather than wall-clock sleeps.

- [ ] **Step 3: Run the focused tests and confirm failure.**

```bash
go test ./internal/ui/bubble -run 'Test.*(Assistant|Transcript|Stream).*Animation|Test.*Final.*Tail' -count=1
```

- [ ] **Step 4: Implement transient reply snapshot and line metadata.**

When `ensureAssistantStreamEntry` creates a new assistant entry, copy the current transcript settings into a reply snapshot. For each stable complete line accepted by `appendAssistantDelta`, append metadata keyed by a stable line ID and start time. Keep metadata outside persisted transcript structures if possible; if entry-local transient state is necessary, ensure session serialization ignores it.

Do not start animation for an already existing historical entry. Do not use the mutable current settings when rendering an active reply.

- [ ] **Step 5: Implement final-tail handling without changing canonical buffering.**

Ensure `finalizeAssistantStream` calls `Flush`, passes the returned residual content through the same complete-line registration path, and clears the active stream state after registration. Empty residual content must not create an animation line.

- [ ] **Step 6: Run focused tests and commit.**

```bash
go test ./internal/ui/bubble -run 'Test.*(Assistant|Transcript|Stream).*Animation|Test.*Final.*Tail' -count=1
git add internal/ui/bubble/transcript.go internal/ui/bubble/*test.go
git commit -m "feat: track assistant transcript animation lines"
```

---

## Task 3: Pure Post-render Noise and Reveal Transform — Subagent C

**Dependencies:** None. Can run in parallel with Tasks 1 and 2.

**Interfaces:**
- Produces a pure, deterministic transform used only after ordinary Markdown/ANSI rendering.
- `normal` returns the original rendered string byte-for-byte.
- Completion returns the original rendered string byte-for-byte for `noise` and `reveal`.

- [ ] **Step 1: Inspect terminal-cell and styled-line helpers.**

```bash
grep -R "func .*Styled.*Cell\|terminalCellWidth\|cutStyled\|wrapStyled" -n internal/ui/bubble/terminal_cells.go internal/ui/bubble
```

Use existing ANSI parsing and grapheme-safe logic; do not slice the rendered string by byte index.

- [ ] **Step 2: Add failing pure-transform tests.**

Cover:

```go
normal := animateStyledTranscriptText(input, modeLine, effectNormal, line, t0, time.Second)
if normal != input { t.Fatal("normal changed rendered text") }

start := animateStyledTranscriptText(input, modeChar, effectReveal, line, t0, time.Second)
end := animateStyledTranscriptText(input, modeChar, effectReveal, line, t0.Add(time.Second), time.Second)
if end != input { t.Fatal("reveal did not restore exact final output") }
if terminalCellWidth(ansi.Strip(start)) != terminalCellWidth(ansi.Strip(input)) { t.Fatal("reveal changed layout") }

noiseA := animateStyledTranscriptText(input, modeChar, effectNoise, line, t0.Add(500*time.Millisecond), time.Second)
noiseB := animateStyledTranscriptText(input, modeChar, effectNoise, line, t0.Add(500*time.Millisecond), time.Second)
if noiseA != noiseB { t.Fatal("noise is not deterministic") }
```

Also include CJK, emoji, combining marks, wide cells, ANSI-styled text, and wrapped/multiline input.

- [ ] **Step 3: Run transform tests and confirm failure.**

```bash
go test ./internal/ui/bubble -run 'Test.*Transcript.*(Noise|Reveal|Animation)' -count=1
```

- [ ] **Step 4: Implement grapheme/display-cell tokenization and progress.**

Tokenize the already-rendered text into safe styled display units while retaining ANSI sequences and newline positions. For `line`, preserve the complete line layout and apply the effect over the line timeline without emitting canonical fragments. For `char`, reveal a left-to-right prefix based on display-unit progress over one second. Keep newline boundaries stable.

- [ ] **Step 5: Implement deterministic noise and background-colored reveal.**

Derive a stable pseudo-random index from line ID and display-unit index. Replace concealed cells with terminal-safe noise glyphs for noise. For reveal, preserve cell width and apply the existing background/foreground concealment convention to unrevealed cells. Preserve original style for revealed cells.

- [ ] **Step 6: Run focused tests, format, and commit.**

```bash
gofmt -w internal/ui/bubble/transcript_animation.go internal/ui/bubble/terminal_cells.go internal/ui/bubble/transcript_animation_test.go
go test ./internal/ui/bubble -run 'Test.*Transcript.*(Noise|Reveal|Animation)' -count=1
git add internal/ui/bubble/transcript_animation.go internal/ui/bubble/transcript_animation_test.go internal/ui/bubble/terminal_cells.go
git commit -m "feat: add transcript noise and reveal rendering"
```

---

## Task 4: Integrate Rendering, Cache Invalidation, and Cursor Frames — Subagent D

**Dependencies:** Tasks 1, 2, and 3 must be committed first. Run this task after the parallel foundation tasks.

**Interfaces consumed:** settings fields/constants from Task 1; reply/line metadata from Task 2; pure transform from Task 3.

- [ ] **Step 1: Rebase/inspect the three completed commits and map exact symbols.**

```bash
git log --oneline -5
grep -R "func renderEntryAt\|type transcriptRenderCacheKey\|ensureTranscriptLinesAt\|needsUIAnimationFrames\|flushTranscriptRefreshIfDue" -n internal/ui/bubble
```

Resolve naming differences by updating the integration seam rather than duplicating types.

- [ ] **Step 2: Add failing integration tests.**

Cover: a new assistant reply captures settings at creation; changing settings during that reply does not change its effect; historical entries render normally; `normal` is byte-for-byte unchanged; active line frames change with `animationNow`; completed lines reuse the ordinary cache; and only assistant animated spans are invalidated.

```bash
go test ./internal/ui/bubble -run 'Test.*(Transcript|Assistant).*Animation|Test.*Render.*Cache' -count=1
```

- [ ] **Step 3: Integrate post-render transformation into assistant rendering.**

In the assistant path of `renderEntryAt`, render the complete body with the existing renderer first, then apply the pure transform to only the newly animated assistant lines. Do not transform tool, user, thinking, Todo, system, or status entries. Ensure the footer/turn metadata remains ordinary unless the design explicitly marks it as part of the animated assistant line.

- [ ] **Step 4: Make cache keys/signatures animation-aware.**

Include the captured effect/mode, active line identity, and a frame/progress version or current animation timestamp in the relevant cache key/source signature. A time-only change must not reuse a stale frame. Once a line reaches one second, remove its transient animation state and allow the normal cache entry to be reused.

- [ ] **Step 5: Connect cursor frames without blocking.**

Make `needsUIAnimationFrames` return true while any assistant line animation is active. On each existing cursor frame, refresh the affected transcript spans and preserve viewport anchoring. Do not create an independent timer per line and do not sleep.

- [ ] **Step 6: Preserve selection, copy, hyperlinks, and resize behavior.**

Selection snapshots and copy must continue to derive plain text from canonical transcript content. Resize must re-render animated lines at the new width while retaining their start times and stable IDs. A completed historical reply must not begin animating due to a later cache invalidation.

- [ ] **Step 7: Run focused integration tests and commit.**

```bash
gofmt -w internal/ui/bubble/transcript.go internal/ui/bubble/app.go internal/ui/bubble/*test.go
go test ./internal/ui/bubble -run 'Test.*(Transcript|Assistant).*Animation|Test.*Render.*Cache|Test.*Selection' -count=1
git add internal/ui/bubble/transcript.go internal/ui/bubble/app.go internal/ui/bubble/*test.go
git commit -m "feat: animate assistant transcript rendering"
```

---

## Task 5: Cross-feature Acceptance Review — Subagent E

**Dependencies:** Task 4 must be committed.

- [ ] **Step 1: Run the complete relevant test packages.**

```bash
go test ./internal/settings ./internal/ui/bubble ./internal/model -count=1
```

- [ ] **Step 2: Add any missing acceptance tests before fixing behavior.**

The tests must verify all of the following in one or more focused cases: line mode keeps a complete canonical line; character mode progressively reveals only the rendered view; noise and reveal each run independently for one second; final no-newline tails animate; changing config affects only the next assistant reply; copy returns original text; CJK/emoji/combining text keeps terminal widths; and non-assistant/history entries never animate.

- [ ] **Step 3: Fix only concrete integration defects and re-run focused tests.**

Keep fixes in the owning feature file. Do not broaden the feature or alter approved defaults/timing semantics.

- [ ] **Step 4: Run formatting, build, full tests, and inspect the diff.**

```bash
gofmt -w internal/settings internal/ui/bubble
go build ./...
go test ./...
git diff --check
git status --short
```

- [ ] **Step 5: Commit acceptance fixes.**

```bash
git add internal/settings internal/ui/bubble
git commit -m "test: verify transcript streaming animations"
```

---

## Parallel Execution Order

1. Dispatch **Subagent A**, **Subagent B**, and **Subagent C** concurrently. They own disjoint files and must not edit each other's areas.
2. Review each result and verify its focused tests. If one foundation task fails, fix or re-dispatch it before integration.
3. Dispatch **Subagent D** after A/B/C commits are present. D owns the shared transcript integration seam.
4. Dispatch **Subagent E** after D. E performs acceptance coverage and final verification.
5. The parent session reviews every commit, resolves any cross-task conflicts, and runs the mandatory repository checks before delivery.

## Final Verification Checklist

- [ ] `go build ./...` passes.
- [ ] `go test ./...` passes.
- [ ] Defaults render existing transcript output unchanged.
- [ ] Settings persist and General page cycles both options.
- [ ] Full canonical content exists before character animation reveals it.
- [ ] Each complete line has an independent one-second timeline.
- [ ] Final no-newline tail is animated as a line.
- [ ] Noise is deterministic and reveal preserves layout/final styling.
- [ ] Historical and non-assistant entries never animate.
- [ ] Selection/copy/search use canonical content.
- [ ] No unrelated pre-existing working-tree changes were staged.
