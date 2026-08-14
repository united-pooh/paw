# Transcript Streaming Animation Design

- Date: 2026-08-14
- Status: Approved design; implementation planning follows

## Context

Paw currently stores assistant stream content in the transcript and renders it through the existing Markdown/ANSI-aware transcript renderer. The feature must add optional visual animation without changing the transcript's canonical content or breaking search, copy, selection, Markdown, terminal-cell width handling, or viewport behavior.

## Goals

1. Add two persisted General config settings:
   - `transcriptOutputMode`: `line` (default) or `char`.
   - `transcriptRenderEffect`: `normal` (default), `noise`, or `reveal`.
2. Apply settings only to assistant replies that start after the settings are saved.
3. Keep the complete content in the stream buffer/transcript before rendering it.
4. Support complete-line independent animations, with each line using a one-second timeline and animations running concurrently for later lines.
5. Process a stream tail without a newline as a complete final line when the assistant stream ends.
6. Preserve normal rendering exactly when the effect is `normal`.

## Non-goals

- No animation for user, tool, thinking, Todo, system, or status entries.
- No replay of completed historical assistant content when settings change.
- No blocking sleeps in the Bubble Tea event loop.
- No changes to transcript search/copy semantics: canonical and selected text remain the original content.
- No animation-specific changes to persisted transcript records.

## User-visible configuration

The General config page exposes two independently changeable options:

- `Transcript output`: `Line` / `Character`
- `Transcript effect`: `Normal` / `Noise` / `Reveal`

Defaults are `Line` and `Normal`, preserving current behavior. Values are persisted using the existing settings/config migration and validation conventions. Invalid or missing persisted values normalize to the defaults.

A saved configuration is captured when a new assistant reply is created. The captured snapshot remains fixed for that reply; changing the config while it streams does not change the active reply. Existing completed replies remain final and are never replayed after a config change.

## Stream and line semantics

The stream buffer remains authoritative for deciding when content is complete. In `line` mode, complete lines are committed to the transcript as they are currently. In `char` mode, the buffer still accumulates and commits complete content/lines; the mode changes only the visual reveal granularity. It must not emit one-character fragments into the canonical transcript or persistence layer.

When the assistant stream finishes, any non-empty residual buffer without a trailing newline is flushed as one final complete line and receives the same behavior as a newline-terminated line.

Each committed assistant line records an animation start timestamp (or equivalent immutable timing data) associated with the active reply's animation state. Every line has an independent one-second interval. A later line starts its own interval when committed; it does not wait for earlier lines to finish.

## Rendering design

Use the selected post-render approach:

1. Render the complete assistant entry through the existing Markdown renderer, preserving ANSI styles, code blocks, links, tables, wrapping, and terminal-cell widths.
2. Apply a visual animation transform to the rendered result only for newly animated assistant lines.
3. Keep the original `entry.body` unchanged.
4. Once a line reaches one second, remove its animation state and cache the normal rendered result.

The transform must operate on grapheme-safe display units/terminal cells. It must not split a Unicode grapheme, wide character, emoji, or ANSI style sequence. Existing terminal-cell helpers should be reused or extended for safe styled slicing.

### Output mode

- `line`: a committed line is revealed as a whole visual line at the beginning of its one-second animation. The line's effect may still animate its visual appearance, but it does not add canonical stream events per character.
- `char`: the complete rendered line is progressively revealed from left to right by grapheme/display unit over the one-second interval. All source content is already buffered/committed before it is shown progressively.

### Effect

- `normal`: bypass animation and use the current renderer unchanged.
- `noise`: unrevealed display units are replaced by deterministic, terminal-safe noise glyphs. The noise must be derived from stable line identity and display-unit index (not fresh randomness every frame), so adjacent frames progress rather than jitter unpredictably. At completion, the exact normal rendered text is shown.
- `reveal`: unrevealed display units retain layout but use the terminal background color (or the existing equivalent concealment style), while the revealed prefix retains its original per-span styling. At completion, the exact normal rendered text is shown.

When an entry contains wrapped/multiline rendered output, reveal progress is measured in rendered display units in reading order, while line breaks and layout remain stable. The implementation must avoid exposing Markdown syntax as a side effect of partial rendering.

## Animation scheduling and caching

Use existing Bubble Tea animation/frame scheduling, such as the cursor frame interval and `animationNow()`. Never call `time.Sleep` from Update or rendering code.

While any active assistant line is animating, frame updates invalidate only the affected transcript cache spans (or otherwise ensure the affected lines are regenerated). Unchanged entries continue to use their existing render cache. When all active animations finish, the final normal rendering is cached and the animation state is discarded.

The transcript render signature/cache key must include the relevant immutable animation snapshot or an animation-frame version for active lines; otherwise time-only changes could incorrectly reuse a stale frame. Configuration changes must not invalidate completed history or alter an active reply's captured snapshot.

Viewport anchoring, auto-scroll, selection snapshots, hyperlinks, and terminal width changes must continue to operate on the post-transform rendered lines. Selection/copy must use the canonical plain transcript content rather than noise/concealment glyphs.

## Data model and lifecycle

Add the smallest state needed to the active assistant reply, for example:

- captured output/effect settings on reply start;
- per-line animation start time and stable line identity;
- an active-animation flag/version used by cache invalidation.

Do not put transient animation timestamps into persisted transcript events. Clear transient state when a reply completes, when a session is replaced/resumed, or when the active assistant entry is discarded. Completed entries render normally even if their transient state is absent.

## Config UI and persistence tests

Add tests for:

- default values and normalization of missing/invalid values;
- round-trip persistence;
- General-page labels and option cycling;
- saving config and applying it only to the next assistant reply;
- current reply retaining its captured settings.

## Rendering and stream tests

Add tests for:

- complete content remaining in the transcript/buffer in character mode;
- newline-terminated and final non-newline tails both becoming animated lines;
- independent one-second line timestamps and concurrent animations;
- grapheme-safe handling of CJK, emoji, combining marks, and wide cells;
- deterministic noise for the same line/frame inputs;
- reveal preserving final styled output exactly;
- normal effect matching pre-feature rendering;
- animation cache invalidation across frames and reuse after completion;
- selection/copy returning original text, not rendered noise or concealment;
- viewport width changes and wrapped Markdown content.

## Acceptance criteria

- With defaults, existing transcript behavior and tests remain unchanged.
- A user can select and persist both options from the General config panel.
- In character mode, a full line is present in the buffer/transcript before progressive display begins.
- Noise and reveal each animate every newly committed assistant line independently for one second.
- No animation is applied to historical content or non-assistant entries.
- The final visible output is exactly the existing normal rendering, and copy/search continue to return canonical text.
- `go build ./...` and `go test ./...` pass.
