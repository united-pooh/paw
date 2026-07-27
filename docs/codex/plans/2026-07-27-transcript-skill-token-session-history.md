# Transcript Skill Tokens and Session Input History Implementation Plan

> **For Codex workers:** Implement task-by-task. Use update_plan to track progress, keep only one step in progress at a time, edit files with the repo's established tools and apply_patch for manual changes, and run the exact verification commands listed below. Steps use checkbox syntax for tracking.

**Goal:** Make confirmed skill tokens render consistently in the transcript and make /sessions restore the target session's user-input history for Up/Down recall without leaking history between sessions.

**Architecture:** Keep Runner input and session JSONL content unchanged. Carry confirmed inputToken metadata into live user transcript entries, use the same terminal-cell projection and token styles for transcript rendering, and reconstruct only canonical [$skill](path) references when loading older transcript messages. On successful session restore, replace the UI's in-memory inputHistory with drafts derived from the restored user entries and reset the navigation cursor.

**Tech Stack:** Go, Bubble Tea, Bubbles textarea, Lipgloss, existing terminal-cell/grapheme helpers, and the internal/ui/bubble test suite.

---

### Task 1: Add token metadata and a shared token-aware transcript renderer

**Files:**
- Modify: internal/ui/bubble/types.go:44-64
- Modify: internal/ui/bubble/input_token.go:419-660
- Modify: internal/ui/bubble/transcript.go:18-47,595-662
- Modify: internal/ui/bubble/subagent_picker.go:406-413
- Test: internal/ui/bubble/input_token_test.go

- [ ] **Step 1: Extend transcript entries with visual-only token metadata.**

Add inputTokens []inputToken to transcriptEntry. Document that the field is presentation metadata only: body remains the exact submitted/session text, and the field must never be sent to Runner or serialized to JSONL.

Update copyTranscriptEntries to deep-copy inputTokens alongside citations, so session/subagent preview copies cannot share mutable token slices.

- [ ] **Step 2: Make the existing projection usable with and without a cursor.**

Keep projectInput as the single wrapping/terminal-cell implementation, but allow a negative cursor argument to mean “no cursor”. Guard cursor marking and cursor-specific normalization with that flag. Preserve current input-box behavior for a non-negative cursor, including terminalCellWidth, grapheme boundaries, token labels, and width wrapping.

Add a renderer that accepts projected lines plus a base body style and renders each token atom through the existing mapping:

```go
inputTokenCommand, inputTokenSkill -> inputCommandTokenStyle
inputTokenFile                  -> inputFileTokenStyle
plain atoms                     -> caller-provided body style
```

Refactor renderTokenInputContent to use this renderer, retaining cursor styling and terminal-input base styles. Add renderTokenizedTranscriptBody(raw string, tokens []inputToken, width int) string, which calls projectInput(raw, tokens, -1, width, false) and renders without a cursor. A transcript with no tokens must continue through the existing plain bodyStyle.Width(width).Render(body) path.

- [ ] **Step 3: Render user transcript entries through the shared projection.**

In renderEntryBodyAt, after trimming the body and before the generic non-assistant body path, special-case entryUser:

```go
if entry.kind == entryUser && len(entry.inputTokens) > 0 {
    return renderTokenizedTranscriptBody(body, entry.inputTokens, width)
}
```

This must hide the raw Markdown wrapper and display the same skill label and foreground/bold style as the input box, while leaving ordinary user text unchanged.

Add a string field named inputTokens to transcriptRenderCacheKey. Populate it with inputTokenSnapshot(entry.inputTokens), where the snapshot contains each token Kind, Start, End, Label, and AutoSpace in stable order. This prevents a cached user entry from retaining an un-tokenized rendering after metadata is attached.

- [ ] **Step 4: Add a focused rendering regression test.**

In internal/ui/bubble/input_token_test.go, construct a canonical skill reference such as [$design](/tmp/design/SKILL.md) with one inputToken whose range covers the complete reference and whose label is design. Assert all of the following:

```go
rendered := renderEntry(transcriptEntry{
    kind:        entryUser,
    title:       "you",
    body:        rawReference,
    inputTokens: tokens,
}, 80)
if strings.Contains(ansi.Strip(rendered), rawReference) {
    t.Fatalf("transcript leaked raw skill reference: %q", ansi.Strip(rendered))
}
if !strings.Contains(rendered, inputCommandTokenStyle.Render("design")) {
    t.Fatalf("transcript missed skill token style: %q", rendered)
}
```

Also assert that a user entry with inputTokens == nil still renders its original body. Run go test ./internal/ui/bubble -run 'Test.*Transcript.*Token|Test.*Token.*Transcript' -count=1; the new test must pass after the renderer change.

### Task 2: Preserve confirmed live skill metadata when adding user transcript entries

**Files:**
- Modify: internal/ui/bubble/input.go:110-136,228-291
- Test: internal/ui/bubble/input_token_test.go
- Test: internal/ui/bubble/bubble_test.go

- [ ] **Step 1: Add one helper for matching the submitted draft to a transcript line.**

Implement a value-receiver helper near the input-draft helpers:

```go
func (m appModel) submittedTokensForLine(line string) []inputToken {
    draft := trimInputDraft(m.submittedDraft)
    if draft.Text != strings.TrimSpace(line) {
        return nil
    }
    return cloneInputTokens(draft.Tokens)
}

func (m appModel) userTranscriptEntry(title, line string) transcriptEntry {
    return transcriptEntry{
        kind:        entryUser,
        title:       title,
        body:        strings.TrimSpace(line),
        inputTokens: m.submittedTokensForLine(line),
    }
}
```

Use the helper only when submittedDraft.Text exactly matches the body. Do not infer a token from a bare $, /, or @ prefix; manually typed or pasted syntax must remain ordinary text until the existing completion-confirmation metadata exists.

- [ ] **Step 2: Route live user entries through the helper.**

Replace the literal transcriptEntry constructions for entryUser in startChatTurn, submitSupplement, and queueChatInput with userTranscriptEntry calls. Keep the current titles (you, you (supplement), and you (queued)) and keep the original line passed to Runner, SubmitSupplement, and chatQueue.

Do not alter command/system/error entries. Do not change message.Message.Content, session append behavior, or the raw textarea value.

- [ ] **Step 3: Test live submission and history round-trip together.**

Extend the existing skill completion test in internal/ui/bubble/input_token_test.go so it selects a skill, submits it, and asserts:

```go
last := model.transcript[len(model.transcript)-1]
if last.kind != entryUser || len(last.inputTokens) != 1 {
    t.Fatalf("last transcript entry = %#v, want one input token", last)
}
if last.body != "[$design](/tmp/design/SKILL.md)" {
    t.Fatalf("last transcript body = %q", last.body)
}
if len(runner.inputs) != 1 || runner.inputs[0] != last.body {
    t.Fatalf("runner inputs = %#v, want raw transcript body", runner.inputs)
}
```

Then set the model idle, navigate Up, and assert that the raw input is still the Markdown reference while model.inputTokens[0].Label == "design". This proves rendering metadata is parallel to, not a replacement for, the raw submit/history syntax.

### Task 3: Reconstruct canonical skill tokens and input history during session restore

**Files:**
- Modify: internal/ui/bubble/subagent_picker.go:195-205,416-425
- Modify: internal/ui/bubble/input.go:398-422
- Test: internal/ui/bubble/bubble_test.go
- Test: internal/ui/bubble/input_token_test.go

- [ ] **Step 1: Parse only the application's canonical skill-reference form for restored display.**

Add a helper near the input-token utilities that scans a message body for exact application-generated references of the form:

```text
[$<non-empty skill name>](<non-empty path without ')'>)
```

For each match, create one inputToken covering the full reference in rune offsets and set Label to the skill name without the leading $, Kind to inputTokenSkill, and AutoSpace to false. Return normalizeInputTokens(body, tokens).

The helper must not convert a bare $skill, arbitrary Markdown links, or path/file references into a token. It is only a compatibility parser for the canonical syntax already emitted by skillMarkdownReference, because session JSONL currently stores message text but not UI token metadata.

- [ ] **Step 2: Attach reconstructed tokens to restored user entries.**

In transcriptEntriesFromMessage, for message.RoleUser, set inputTokens from the canonical-reference helper while keeping body: content unchanged. Tool-result user messages that do not have ordinary content must continue to produce only their existing tool entries.

This automatically covers /sessions history loading and subagent preview transcript rendering without modifying the session storage schema.

- [ ] **Step 3: Build input drafts from restored user transcript entries.**

Add a helper that converts restored entryUser entries into ordered, deduplicated []inputDraft values:

```go
func inputHistoryFromTranscript(entries []transcriptEntry) []inputDraft
```

For each entryUser, create inputDraft{Text: entry.body, Tokens: cloneInputTokens(entry.inputTokens)}, pass it through trimInputDraft, skip empty text, and suppress only an immediately repeated draft using the same equality semantics as rememberInputHistory. Preserve order and token metadata.

- [ ] **Step 4: Replace history on successful normal session restore.**

In applySessionPickerRestore, after the restored transcript is copied and before the session-switch status entry is added, assign:

```go
m.inputHistory = inputHistoryFromTranscript(msg.entries)
m.resetHistoryNavigation()
```

This must happen even when msg.entries is empty, so an empty target session clears the previous session's history. Leave the current unsent textarea draft behavior unchanged, and do not change the error path or the subagent-preview path.

- [ ] **Step 5: Add restore and leakage regression tests.**

In internal/ui/bubble/bubble_test.go, add tests that send sessionRestoredMsg directly with:

1. A restored user prompt and assistant answer: assert inputHistory contains the prompt, press Up through handleHistoryNavigation(-1), and assert the prompt is inserted.
2. A pre-seeded old history plus a different restored user prompt: assert only the restored prompt remains and Up recalls it.
3. An empty restored session: assert inputHistory is empty and Up is a no-op.

In internal/ui/bubble/input_token_test.go, restore a user message containing [$design](/tmp/design/SKILL.md), assert its transcript entry has one skill token, then recall it and assert the input draft contains the same raw reference plus one skill token.

### Task 4: Run the complete verification bundle and manual acceptance checks

**Files:**
- Test: internal/ui/bubble/input_token_test.go
- Test: internal/ui/bubble/bubble_test.go
- Test: existing repository test suite

- [ ] **Step 1: Run focused tests for the two fixes.**

Run:

```bash
go test ./internal/ui/bubble -run 'Test.*(Skill|Token|Session|History)' -count=1
```

Expected: PASS, including live transcript token rendering, restored canonical skill rendering, Up recall after restore, old-history replacement, and empty-session clearing.

- [ ] **Step 2: Run the package and repository suites.**

Run:

```bash
go test ./internal/ui/bubble -count=1
go test ./... -count=1
NO_COLOR=1 go test ./internal/ui/bubble -count=1
git diff --check
```

Expected: all Go tests pass, the NO_COLOR=1 run has no style-dependent failures, and git diff --check reports no whitespace errors.

- [ ] **Step 3: Perform the interactive acceptance check.**

In the Bubble Tea UI, select a skill from $ completion, submit it, and verify the you transcript row shows the skill label with the same purple/bold token treatment as the input box while Runner still receives [$skill](.../SKILL.md). Open /sessions, restore a session containing a user skill reference, press Up at the input boundary, and verify the raw reference is recalled but displayed as a skill token. Switch to an empty or different session and verify the previous session's input history is not recalled.

