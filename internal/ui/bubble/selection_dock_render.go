package bubble

import (
	"fmt"
	"strings"

	selecttool "paw/internal/tool/select"
)

const selectionDockMaxVisibleLines = 16

func (d *selectionDock) preferredHeight(width int) int {
	if d == nil {
		return inputMinVisibleLines
	}
	width = maxInt(1, width)
	// Title, prompt, answer status, scroll status, separator, two fixed actions,
	// and the keyboard hint are independent of the caller-provided answers.
	height := 7 + len(wrapDisplayWidthLine(sanitizeTerminalText(d.request.Prompt), width))
	for _, option := range d.request.Options {
		height++
		if option.Description != "" {
			height += len(wrapDisplayWidthLine(sanitizeTerminalText(option.Description), maxInt(1, width-8)))
		}
	}
	return clampInt(height, 1, selectionDockMaxVisibleLines)
}

func (m appModel) renderSelectionDock(width, height int) string {
	d := m.selectionDock
	if d == nil {
		return ""
	}
	width = maxInt(1, width)
	height = maxInt(1, height)

	promptLines := wrapDisplayWidthLine(sanitizeTerminalText(d.request.Prompt), width)
	if len(promptLines) == 0 {
		promptLines = []string{""}
	}

	answerLines, answerHeights := selectionAnswerRows(d, width)
	// In a normal dock, reserve the range/status rows, fixed action area, and
	// hint before assigning space to caller answers. Tiny terminals may crop
	// lower-priority header/answer content, but never overflow their rectangle.
	fixedRows := 1 + len(promptLines) + 1 + 1 + 3 + 1
	answerBudget := maxInt(0, height-fixedRows)
	start, end := d.visibleRange(answerHeights, answerBudget)

	title := "SELECT · " + strings.ToUpper(string(d.request.Mode))
	selectionSummary := fmt.Sprintf("selected %d / max %d", d.selectedCount(), d.request.MaxSelect)
	title = alignSelectionDockEnds(title, selectionSummary, width)

	lines := make([]string, 0, height)
	lines = append(lines, m.styles.LabelTool.Render(truncateDisplayWidth(title, width)))
	for _, line := range promptLines {
		lines = append(lines, m.styles.Body.Render(truncateDisplayWidth(line, width)))
	}
	lines = append(lines, m.styles.StatusMuted.Render(truncateDisplayWidth(answerStatusLine(d, start, end), width)))

	answerLimit := maxInt(0, height-5) // Keep separator, actions, and hint when possible.
	for i := start; i < end && len(lines) < answerLimit; i++ {
		for rowIndex, row := range answerLines[i] {
			if len(lines) >= answerLimit {
				break
			}
			style := m.styles.Unselected
			if d.focus.kind == selectionFocusAnswer && d.focus.answerIndex == i {
				style = m.styles.Selected
			} else if rowIndex > 0 {
				style = m.styles.StatusMuted
			}
			lines = append(lines, style.Render(truncateDisplayWidth(row, width)))
		}
	}

	if len(lines) < height-4 {
		lines = append(lines, m.styles.StatusMuted.Render(truncateDisplayWidth(answerScrollLine(start, end, len(d.request.Options), width), width)))
	}
	if len(lines) < height-3 {
		lines = append(lines, m.styles.StatusMuted.Render(strings.Repeat("─", width)))
	}
	if len(lines) < height-2 {
		lines = append(lines, renderSelectionCustomAction(m, d, width))
	}
	if len(lines) < height-1 {
		lines = append(lines, renderSelectionChatAction(m, d, width))
	}
	if len(lines) < height {
		hint := selectionDockHint(d)
		if d.errorText != "" {
			lines = append(lines, m.styles.StatusError.Render(truncateDisplayWidth(d.errorText, width)))
		} else {
			lines = append(lines, m.styles.InputHint.Render(truncateDisplayWidth(hint, width)))
		}
	}
	return fitStyledRect(strings.Join(lines, "\n"), width, height)
}

func selectionAnswerRows(d *selectionDock, width int) ([][]string, []int) {
	rows := make([][]string, len(d.request.Options))
	heights := make([]int, len(d.request.Options))
	for i, option := range d.request.Options {
		focusMark := "  "
		if d.focus.kind == selectionFocusAnswer && d.focus.answerIndex == i {
			focusMark = "› "
		}
		selectedMark := "[ ] "
		if d.selected[option.ID] {
			selectedMark = "[x] "
		}
		prefix := focusMark + selectedMark
		labelWidth := maxInt(1, width-terminalCellWidth(prefix))
		optionRows := []string{prefix + truncateDisplayWidth(sanitizeTerminalText(option.Label), labelWidth)}
		if option.Description != "" {
			indent := strings.Repeat(" ", terminalCellWidth(prefix))
			for _, description := range wrapDisplayWidthLine(sanitizeTerminalText(option.Description), maxInt(1, width-terminalCellWidth(indent))) {
				optionRows = append(optionRows, indent+description)
			}
		}
		rows[i] = optionRows
		heights[i] = len(optionRows)
	}
	return rows, heights
}

func answerStatusLine(d *selectionDock, start, end int) string {
	total := len(d.request.Options)
	rangeText := "showing 0-0"
	if end > start {
		rangeText = fmt.Sprintf("showing %d-%d", start+1, end)
	}
	constraint := "choose any number"
	if d.request.Mode == selecttool.ModeSingle {
		constraint = "choose 1"
	} else {
		switch {
		case d.request.MinSelect > 0 && d.request.MinSelect == d.request.MaxSelect:
			constraint = fmt.Sprintf("choose exactly %d", d.request.MinSelect)
		case d.request.MinSelect > 0:
			constraint = fmt.Sprintf("choose %d-%d", d.request.MinSelect, d.request.MaxSelect)
		case d.request.MaxSelect < total:
			constraint = fmt.Sprintf("choose up to %d", d.request.MaxSelect)
		}
	}
	return fmt.Sprintf("%d answers · %s · %s", total, rangeText, constraint)
}

func answerScrollLine(start, end, total, width int) string {
	above := fmt.Sprintf("↑ %d answers above", start)
	below := fmt.Sprintf("↓ %d answers below", maxInt(0, total-end))
	return alignSelectionDockEnds(above, below, width)
}

func renderSelectionCustomAction(m appModel, d *selectionDock, width int) string {
	focusMark := "  "
	if d.focus.kind == selectionFocusCustom {
		focusMark = "› "
	}
	selectedMark := "[ ] "
	if strings.TrimSpace(d.customLabel) != "" {
		selectedMark = "[x] "
	}
	line := focusMark + selectedMark + "Custom option"
	if d.editingCustom {
		prefix := line + "  "
		d.customInput.Width = maxInt(1, width-terminalCellWidth(prefix))
		line = prefix + d.customInput.View()
	} else if d.customLabel != "" {
		line += " · " + sanitizeTerminalText(d.customLabel)
	}
	style := m.styles.Unselected
	if d.focus.kind == selectionFocusCustom {
		style = m.styles.Selected
	}
	return style.Render(truncateDisplayWidth(line, width))
}

func renderSelectionChatAction(m appModel, d *selectionDock, width int) string {
	prefix := "  ◌ "
	style := m.styles.Unselected
	if d.focus.kind == selectionFocusChat {
		prefix = "› ◌ "
		style = m.styles.Selected
	}
	return style.Render(truncateDisplayWidth(prefix+"Chat about this", width))
}

func selectionDockHint(d *selectionDock) string {
	if d.editingCustom {
		return "enter save  esc stop editing  ctrl+c cancel"
	}
	if d.request.Mode == selecttool.ModeMultiple {
		return "↑↓ move  space toggle  enter submit  esc cancel"
	}
	return "↑↓ move  space select  enter submit  esc cancel"
}

func alignSelectionDockEnds(left, right string, width int) string {
	left = truncateDisplayWidth(left, width)
	remaining := width - terminalCellWidth(left)
	if remaining <= 1 || terminalCellWidth(right)+1 > remaining {
		return left
	}
	return left + strings.Repeat(" ", remaining-terminalCellWidth(right)) + right
}
