package bubble

import (
	"fmt"
	"strings"

	selecttool "paw/internal/tool/select"
)

const selectionDockMaxVisibleLines = inputMaxVisibleLines

func (d *selectionDock) preferredHeight(width int) int {
	if d == nil {
		return inputMinVisibleLines
	}
	width = maxInt(1, width)
	height := 2 + len(wrapDisplayWidthLine(sanitizeTerminalText(d.request.Prompt), width)) + 1
	for _, o := range d.request.Options {
		height++
		if o.Description != "" {
			height += len(wrapDisplayWidthLine(sanitizeTerminalText(o.Description), maxInt(1, width-4)))
		}
	}
	if d.errorText != "" {
		height++
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

	title := fmt.Sprintf("Select · %s", d.request.Mode)
	position := fmt.Sprintf("%d/%d", d.highlighted+1, len(d.request.Options))
	if terminalCellWidth(title)+terminalCellWidth(position)+1 <= width {
		title += strings.Repeat(" ", width-terminalCellWidth(title)-terminalCellWidth(position)) + position
	}
	lines := []string{m.styles.LabelTool.Render(truncateDisplayWidth(title, width))}
	for _, line := range wrapDisplayWidthLine(sanitizeTerminalText(d.request.Prompt), width) {
		lines = append(lines, m.styles.Body.Render(line))
	}

	hint := "↑↓ move  enter submit  esc cancel"
	if d.request.Mode == selecttool.ModeMultiple {
		hint = "↑↓ move  space toggle  enter submit  esc cancel"
	}
	reservedTail := 1
	if d.errorText != "" {
		reservedTail++
	}
	optionBudget := maxInt(0, height-len(lines)-reservedTail)

	heights := make([]int, len(d.request.Options))
	optionLines := make([][]string, len(d.request.Options))
	for i, option := range d.request.Options {
		prefix := "  "
		if i == d.highlighted {
			prefix = "› "
		}
		if d.request.Mode == selecttool.ModeMultiple {
			mark := "[ ] "
			if d.selected[option.ID] {
				mark = "[x] "
			}
			prefix += mark
		}
		labelWidth := maxInt(1, width-terminalCellWidth(prefix))
		rows := []string{prefix + truncateDisplayWidth(sanitizeTerminalText(option.Label), labelWidth)}
		if option.Description != "" {
			indent := strings.Repeat(" ", terminalCellWidth(prefix))
			for _, description := range wrapDisplayWidthLine(sanitizeTerminalText(option.Description), maxInt(1, width-terminalCellWidth(indent))) {
				rows = append(rows, indent+description)
			}
		}
		optionLines[i] = rows
		heights[i] = len(rows)
	}

	start, end := d.visibleRange(heights, optionBudget)
	for range 2 {
		indicators := 0
		if start > 0 {
			indicators++
		}
		if end < len(d.request.Options) {
			indicators++
		}
		start, end = d.visibleRange(heights, maxInt(0, optionBudget-indicators))
	}
	if start > 0 && len(lines) < height-reservedTail {
		lines = append(lines, m.styles.StatusMuted.Render("↑ more"))
	}
	for i := start; i < end && len(lines) < height-reservedTail; i++ {
		for rowIndex, row := range optionLines[i] {
			if len(lines) >= height-reservedTail {
				break
			}
			style := m.styles.Unselected
			if i == d.highlighted {
				style = m.styles.Selected
			} else if rowIndex > 0 {
				style = m.styles.StatusMuted
			}
			lines = append(lines, style.Render(truncateDisplayWidth(row, width)))
		}
	}
	if end < len(d.request.Options) && len(lines) < height-reservedTail {
		lines = append(lines, m.styles.StatusMuted.Render("↓ more"))
	}
	if d.errorText != "" && len(lines) < height-1 {
		lines = append(lines, m.styles.StatusError.Render(truncateDisplayWidth(d.errorText, width)))
	}
	if len(lines) < height {
		lines = append(lines, m.styles.InputHint.Render(truncateDisplayWidth(hint, width)))
	}
	return fitStyledRect(strings.Join(lines, "\n"), width, height)
}
