# Code Block Title on Border Implementation Plan

> **For Codex workers:** Implement task-by-task. Use `update_plan` to track progress, keep only one step in progress at a time, edit files with the repo's established tools and `apply_patch` for manual changes, and run the exact verification commands listed below. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Move every fenced code block language label onto a centered Signal Cyan title chip in the top border, removing one rendered line without changing code content rendering.

**Architecture:** Keep the existing pure Markdown rendering path. `renderCodeBlock` will pass an optional trimmed language label into the existing panel renderer; the panel will calculate width from both body and label, render a centered top-border chip, and leave body wrapping, padding, and line limits unchanged. A dedicated Lipgloss style will give the chip its Signal Cyan background and contrasting terminal-background foreground.

**Tech Stack:** Go, Bubble Tea UI package, Lipgloss, Charm terminal cell-width helpers, Go `testing`.

---

## Current implementation map

- `internal/ui/bubble/markdown.go:108-165` currently emits a standalone language line, then calls `renderCodeBlockPanel`; `markdownCodeBlockWidth` only considers code content.
- `internal/ui/bubble/styles.go:69-80` contains Markdown code and border styles but no background style for a language chip.
- `internal/ui/bubble/color_manager.go:75-112` already defines `colorTerminalBackground` and `colorSignal`; no palette change is required.
- `internal/ui/bubble/bubble_test.go:1336-1349` covers large code blocks, and `3418-3482` covers wrapping, compact width, language labels, and language-only output.

### Task 1: Add failing geometry, label, and style tests

**Files:**
- Modify: `internal/ui/bubble/bubble_test.go:3456-3482`
- Modify: `internal/ui/bubble/bubble_test.go` immediately after the existing code-block language tests

- [ ] **Step 1: Replace the standalone-label expectation with top-border expectations**

Rename `TestMarkdownCodeBlockKeepsLanguageLabelOutsideBlock` to `TestMarkdownCodeBlockKeepsLanguageLabelOnTopBorder`. The test must assert the label is in the first rendered line, the first line has both top-border corners, no body line contains the label, and the code body remains present:

~~~go
func TestMarkdownCodeBlockKeepsLanguageLabelOnTopBorder(t *testing.T) {
	rendered := ansi.Strip(renderMarkdown("```json\n{\"ok\": true}\n```", 80))
	lines := strings.Split(rendered, "\n")
	if len(lines) != 3 {
		t.Fatalf("rendered code block lines = %d, want 3:\n%s", len(lines), rendered)
	}
	if !strings.Contains(lines[0], " json ") || !strings.Contains(lines[0], "┌") || !strings.Contains(lines[0], "┐") {
		t.Fatalf("top border = %q, want centered json label", lines[0])
	}
	if strings.Contains(lines[1], "json") {
		t.Fatalf("body line = %q, should not repeat language label", lines[1])
	}
	if !strings.Contains(rendered, "{\"ok\": true}") {
		t.Fatalf("rendered code block = %q, want json body", rendered)
	}
}
~~~

- Also update the existing Python-label test so it checks the top border while allowing multiple body lines:

~~~go
func TestMarkdownCodeBlockShowsOnlyLanguageLabel(t *testing.T) {
	rendered := ansi.Strip(renderMarkdown("```python\ndef hanoi():\n    pass\n```", 80))
	lines := strings.Split(rendered, "\n")
	if len(lines) < 3 || !strings.Contains(lines[0], " python ") {
		t.Fatalf("rendered code block = %q, want python label on top border", rendered)
	}
	if strings.Contains(rendered, "code python") {
		t.Fatalf("rendered code block = %q, should not include code prefix", rendered)
	}
}
~~~

- [ ] **Step 2: Add tests for centering, no-language behavior, narrow labels, and Signal Cyan**

Add these tests to the same file. They use the existing `terminalCellWidth`, `assertRenderedLineWidthsAtMost`, and `colorManager` helpers:

~~~go
func TestMarkdownCodeBlockCentersLanguageLabel(t *testing.T) {
	rendered := ansi.Strip(renderMarkdown("```text\nhello\n```", 80))
	lines := strings.Split(rendered, "\n")
	if len(lines) != 3 {
		t.Fatalf("rendered code block lines = %d, want 3:\n%s", len(lines), rendered)
	}
	const chip = " text "
	start := strings.Index(lines[0], chip)
	if start < 0 {
		t.Fatalf("top border = %q, want %q", lines[0], chip)
	}
	leftWidth := terminalCellWidth(lines[0][:start])
	rightWidth := terminalCellWidth(lines[0][start+len(chip):])
	diff := leftWidth - rightWidth
	if diff < -1 || diff > 1 {
		t.Fatalf("top border is not centered: left=%d right=%d line=%q", leftWidth, rightWidth, lines[0])
	}
}

func TestMarkdownCodeBlockOmitsDefaultLabelWithoutLanguage(t *testing.T) {
	rendered := ansi.Strip(renderMarkdown("```\nhello\n```", 80))
	lines := strings.Split(rendered, "\n")
	if len(lines) != 3 {
		t.Fatalf("rendered code block lines = %d, want 3:\n%s", len(lines), rendered)
	}
	if strings.Contains(lines[0], "code") || strings.Contains(rendered, "code hello") {
		t.Fatalf("rendered code block = %q, should not invent a code label", rendered)
	}
}

func TestMarkdownCodeBlockTruncatesLongLanguageLabelWithinWidth(t *testing.T) {
	const width = 12
	rendered := renderMarkdown("```this-is-a-very-long-language\nx\n```", width)
	stripped := ansi.Strip(rendered)
	lines := strings.Split(stripped, "\n")
	if len(lines) != 3 {
		t.Fatalf("rendered code block lines = %d, want 3:\n%s", len(lines), stripped)
	}
	assertRenderedLineWidthsAtMost(t, rendered, width)
	if !strings.Contains(lines[0], "…") {
		t.Fatalf("top border = %q, want truncated language marker", lines[0])
	}
}

func TestMarkdownCodeBlockLabelStyleUsesSignalCyan(t *testing.T) {
	if got := markdownCodeBlockLabelStyle.GetBackground(); got != colorManager.LipglossColor(colorSignal) {
		t.Fatalf("label background = %#v, want %#v", got, colorManager.LipglossColor(colorSignal))
	}
	if got := markdownCodeBlockLabelStyle.GetForeground(); got != colorManager.LipglossColor(colorTerminalBackground) {
		t.Fatalf("label foreground = %#v, want %#v", got, colorManager.LipglossColor(colorTerminalBackground))
	}
	if !markdownCodeBlockLabelStyle.GetBold() {
		t.Fatal("label style must be bold")
	}
}
~~~

- [ ] **Step 3: Run the new focused tests and confirm they fail for the old renderer**

Run:

~~~sh
go test ./internal/ui/bubble -run 'TestMarkdownCodeBlock(KeepsLanguageLabelOnTopBorder|CentersLanguageLabel|OmitsDefaultLabelWithoutLanguage|TruncatesLongLanguageLabelWithinWidth|LabelStyleUsesSignalCyan)$' -count=1
~~~

Expected: FAIL because the current renderer still returns a standalone label, still invents `code` for an empty language, and does not define `markdownCodeBlockLabelStyle`.

### Task 2: Add the Signal Cyan title-chip style

**Files:**
- Modify: `internal/ui/bubble/styles.go:76-80`

- [ ] **Step 1: Define the label style beside the existing code-block styles**

Insert this style after `markdownCodeBlockBorderStyle`:

~~~go
markdownCodeBlockLabelStyle = lipgloss.NewStyle().
	Foreground(colorManager.LipglossColor(colorTerminalBackground)).
	Background(colorManager.LipglossColor(colorSignal)).
	Bold(true)
~~~

The renderer will supply the two visible spaces itself, so this style must not add Lipgloss padding.

- [ ] **Step 2: Run the style test**

Run:

~~~sh
go test ./internal/ui/bubble -run 'TestMarkdownCodeBlockLabelStyleUsesSignalCyan$' -count=1
~~~

Expected: PASS, with the foreground equal to the terminal background, the background equal to Signal Cyan, and bold enabled.

### Task 3: Integrate the centered title into code-block rendering

**Files:**
- Modify: `internal/ui/bubble/markdown.go:108-165`

- [ ] **Step 1: Remove the default `code` label and pass a trimmed optional label**

Replace `renderCodeBlock` with:

~~~go
func renderCodeBlock(lang, code string, width int) string {
	width = maxInt(1, width)
	label := strings.TrimSpace(lang)
	body := strings.TrimRight(code, "\n")
	if body == "" {
		body = " "
	}
	return renderCodeBlockPanel(body, width, label)
}
~~~

- [ ] **Step 2: Make the panel width account for the title chip and render the new top border**

Replace `renderCodeBlockPanel` with:

~~~go
func renderCodeBlockPanel(code string, width int, label string) string {
	width = maxInt(1, width)
	blockWidth := markdownCodeBlockWidth(code, label, width)
	if blockWidth < 6 {
		lines := limitRenderedCodeBlockLines(strings.Split(wrapDisplayWidthLines(code, width), "\n"), maxRenderedCodeBlockLines)
		return markdownCodeBlockStyle.Render(strings.Join(lines, "\n"))
	}
	textWidth := maxInt(1, blockWidth-4)
	lines := limitRenderedCodeBlockLines(strings.Split(wrapDisplayWidthLines(code, textWidth), "\n"), maxRenderedCodeBlockLines)
	rendered := make([]string, 0, len(lines)+2)
	rendered = append(rendered, renderCodeBlockTopBorder(label, blockWidth))
	for _, line := range lines {
		body := " " + markdownCodeBlockStyle.Render(padDisplayWidth(line, textWidth)) + " "
		rendered = append(rendered,
			markdownCodeBlockBorderStyle.Render("│")+body+markdownCodeBlockBorderStyle.Render("│"),
		)
	}
	rendered = append(rendered, renderCodeBlockBorderLine("└", "─", "┘", blockWidth))
	return strings.Join(rendered, "\n")
}
~~~

- [ ] **Step 3: Update width calculation to reserve the chip**

Replace `markdownCodeBlockWidth` with:

~~~go
func markdownCodeBlockWidth(code, label string, width int) int {
	maxWidth := maxInt(6, width-4)
	widest := 1
	for _, line := range strings.Split(code, "\n") {
		widest = maxInt(widest, terminalCellWidth(line))
	}
	required := maxInt(6, widest+4)
	if label != "" {
		required = maxInt(required, terminalCellWidth(label)+6)
	}
	return minInt(maxWidth, required)
}
~~~

The `+6` accounts for two border corners, two one-cell side rules, and the two visible spaces surrounding the label.

- [ ] **Step 4: Add the centered top-border helper**

Add this function immediately before `renderCodeBlockBorderLine`:

~~~go
func renderCodeBlockTopBorder(label string, width int) string {
	label = strings.TrimSpace(label)
	if label == "" || width < 7 {
		return renderCodeBlockBorderLine("┌", "─", "┐", width)
	}

	label = truncateDisplayWidth(label, width-6)
	if label == "" {
		return renderCodeBlockBorderLine("┌", "─", "┐", width)
	}

	chipText := " " + label + " "
	chip := markdownCodeBlockLabelStyle.Render(chipText)
	fillWidth := maxInt(0, width-2-terminalCellWidth(chipText))
	leftWidth := fillWidth / 2
	rightWidth := fillWidth - leftWidth
	return markdownCodeBlockBorderStyle.Render("┌"+strings.Repeat("─", leftWidth)) +
		chip +
		markdownCodeBlockBorderStyle.Render(strings.Repeat("─", rightWidth)+"┐")
}
~~~

This keeps left and right rule lengths within one cell, gives the chip background to both spaces and the label, truncates labels by terminal width, and falls back to a normal top border when no title can fit.

- [ ] **Step 5: Run the focused Markdown tests**

Run:

~~~sh
gofmt -w internal/ui/bubble/markdown.go internal/ui/bubble/styles.go internal/ui/bubble/bubble_test.go
go test ./internal/ui/bubble -run 'TestMarkdownCodeBlock|TestAssistantEntryRendersMarkdown' -count=1
~~~

Expected: PASS, including the existing nested-fence, max-line, wrapping, compact-width, and new centered-chip tests.

### Task 4: Run package and repository verification

**Files:**
- Verify: `internal/ui/bubble/markdown.go`
- Verify: `internal/ui/bubble/styles.go`
- Verify: `internal/ui/bubble/bubble_test.go`

- [ ] **Step 1: Run the full Bubble Tea package tests**

Run:

~~~sh
go test ./internal/ui/bubble -count=1
~~~

Expected: PASS with no regressions in layout, transcript, selection, input, style, or Markdown tests.

- [ ] **Step 2: Run the color-disabled package tests**

Run:

~~~sh
NO_COLOR=1 go test ./internal/ui/bubble -count=1
~~~

Expected: PASS; ANSI color removal must not affect line widths or title placement.

- [ ] **Step 3: Run all repository tests**

Run:

~~~sh
go test ./... -count=1
~~~

Expected: PASS for every Go package.

- [ ] **Step 4: Check the final diff**

Run:

~~~sh
git diff --check
git status --short
~~~

Expected: no whitespace errors; only the intended Markdown renderer, style, and test changes are present.

- [ ] **Step 5: Perform the manual TUI check**

Run the real entrypoint:

~~~sh
go run cmd/agent/main.go
~~~

Show code blocks with `go`, `text`, a long language label, Chinese content, and no language at terminal widths near 80, 40, and 20. Confirm the title is centered on the top border, the Signal Cyan background covers both spaces and text, the labeled block is one line shorter than before, and no rendered line exceeds the content width.
