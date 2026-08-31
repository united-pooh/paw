// 本文件定义 Activity modal 使用的 Tasks 内容渲染逻辑。
package bubble

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"paw/internal/task"
)

// renderTasksCardContent 渲染 Tasks 内容（Task 4 实现）。
func (m appModel) renderTasksCardContent(width int) string {
	return m.renderTasksCardContentHeight(width, 0)
}

func (m appModel) renderTasksCardContentHeight(width, height int) string {
	hdrStyle := lipgloss.NewStyle().Foreground(colorManager.LipglossColor(colorHeaderForeground)).Bold(true)
	mutedStyle := lipgloss.NewStyle().Foreground(colorManager.LipglossColor(colorContextFree))
	hdr := renderSidebarRow(width, "taskController", "", hdrStyle, mutedStyle)
	maxLines := height

	if m.taskController == nil {
		if maxLines == 1 {
			return hdr
		}
		return hdr + "\n" + mutedStyle.Italic(true).Render(fitStyledCellLine("none", width))
	}
	tasks := m.taskEntries()
	if len(tasks) == 0 {
		if maxLines == 1 {
			return hdr
		}
		empty := mutedStyle.Italic(true).Render(fitStyledCellLine("none", width))
		return hdr + "\n" + empty
	}

	dotRunStyle := lipgloss.NewStyle().Foreground(colorManager.LipglossColor(colorWorktreeClean))
	dotDoneStyle := lipgloss.NewStyle().Foreground(colorManager.LipglossColor(colorMarkdownQuote))
	dotFailStyle := lipgloss.NewStyle().Foreground(colorManager.LipglossColor(colorLabelError))
	dotStopStyle := lipgloss.NewStyle().Foreground(colorManager.LipglossColor(colorContextCache))
	spinnerFrames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

	availableTaskRows := len(tasks)
	windowStart := 0
	moreCount := 0
	if maxLines > 0 {
		availableTaskRows = maxInt(0, maxLines-1)
		if len(tasks) > availableTaskRows {
			if availableTaskRows <= 1 {
				moreCount = len(tasks)
				availableTaskRows = 0
			} else {
				availableTaskRows--
				moreCount = len(tasks) - availableTaskRows
			}
		}
	}
	if m.activity.visible && availableTaskRows > 0 {
		selected := clampInt(m.activity.selectedIndex, 0, len(tasks)-1)
		if selected < windowStart {
			windowStart = selected
		}
		if selected >= windowStart+availableTaskRows {
			windowStart = selected - availableTaskRows + 1
		}
		maxStart := maxInt(0, len(tasks)-availableTaskRows)
		if windowStart > maxStart {
			windowStart = maxStart
		}
		if windowStart > 0 || windowStart+availableTaskRows < len(tasks) {
			moreCount = windowStart + maxInt(0, len(tasks)-(windowStart+availableTaskRows))
		}
	}

	lines := []string{hdr}
	windowEnd := minInt(len(tasks), windowStart+availableTaskRows)
	for idx := windowStart; idx < windowEnd; idx++ {
		t := tasks[idx]
		var dot, status string
		dotStyle := dotDoneStyle
		lineStyle := lipgloss.NewStyle().Foreground(colorManager.LipglossColor(colorBody))
		switch t.Status {
		case task.TaskRunning:
			dot = spinnerFrames[m.spinnerFrameIdx%len(spinnerFrames)]
			status = formatElapsedTime(time.Since(t.StartedAt))
			dotStyle = dotRunStyle
			lineStyle = lipgloss.NewStyle().Foreground(colorManager.LipglossColor(colorWorktreeClean))
		case task.TaskFailed:
			dot = "✗"
			status = "failed"
			dotStyle = dotFailStyle
			lineStyle = lipgloss.NewStyle().Foreground(colorManager.LipglossColor(colorLabelError))
		case task.TaskStopped:
			dot = "■"
			status = "stopped"
			dotStyle = dotStopStyle
			lineStyle = lipgloss.NewStyle().Foreground(colorManager.LipglossColor(colorContextCache))
		default:
			dot = "✓"
			status = "done"
		}
		nameStyle := lineStyle
		if color := strings.TrimSpace(t.Color); color != "" {
			nameStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(color))
		}
		if m.activity.visible && idx == clampInt(m.activity.selectedIndex, 0, len(tasks)-1) {
			dot = ">"
			dotStyle = m.styles.SelectionSelected
			nameStyle = m.styles.SelectionSelected
			lineStyle = m.styles.SelectionSelected
		}
		lines = append(lines, rendertaskSidebarRow(width, dot, taskDisplayName(t), status, dotStyle, nameStyle, lineStyle))
	}
	if moreCount > 0 {
		lines = append(lines, renderSidebarRow(width, fmt.Sprintf("+%d more", moreCount), "", mutedStyle, mutedStyle))
	}
	return strings.Join(lines, "\n")
}

var activityTaskSpinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

func (m appModel) renderActivityTasks(width, height int) string {
	if height <= 0 {
		return ""
	}
	if m.taskController == nil {
		return fitStyledRect(labelErrorStyle.Render("Task controller is unavailable."), width, height)
	}
	tasks := m.activity.tasks
	if len(tasks) == 0 {
		return fitStyledRect(unselectedProviderStyle.Render("No tasks yet."), width, height)
	}

	selected := clampInt(m.activity.selectedIndex, 0, len(tasks)-1)
	lines := make([]string, 0, len(tasks)+height)
	selectedStart, selectedEnd := 0, 0
	for index, snapshot := range tasks {
		if index == selected {
			selectedStart = len(lines)
		}
		lines = append(lines, m.renderActivityTaskHeader(snapshot, width, index == selected))
		if index == selected {
			lines = append(lines, m.renderSelectedActivityTaskDetails(snapshot, width)...)
			selectedEnd = len(lines)
		}
	}

	start := activityTaskViewportStart(len(lines), height, selectedStart, selectedEnd)
	end := minInt(len(lines), start+height)
	return fitStyledRect(strings.Join(lines[start:end], "\n"), width, height)
}

func activityTaskViewportStart(total, height, selectedStart, selectedEnd int) int {
	if height <= 0 || total <= height {
		return 0
	}
	selectedHeight := maxInt(1, selectedEnd-selectedStart)
	if selectedHeight >= height {
		return clampInt(selectedStart, 0, total-height)
	}
	start := selectedStart - (height-selectedHeight)/2
	start = clampInt(start, 0, total-height)
	if selectedStart < start {
		start = selectedStart
	}
	if selectedEnd > start+height {
		start = selectedEnd - height
	}
	return clampInt(start, 0, total-height)
}

func (m appModel) renderActivityTaskHeader(snapshot task.TaskSnapshot, width int, selected bool) string {
	glyph, status, glyphStyle, statusStyle := m.activityTaskStatus(snapshot)
	nameStyle := unselectedProviderStyle.Copy().Bold(true)
	fillStyle := unselectedProviderStyle
	if color := strings.TrimSpace(snapshot.Color); color != "" {
		nameStyle = nameStyle.Foreground(lipgloss.Color(color))
	}
	if selected {
		fillStyle = m.styles.SelectionSelected
		glyphStyle = fillStyle
		statusStyle = fillStyle
		nameStyle = fillStyle.Copy()
		if color := strings.TrimSpace(snapshot.Color); color != "" {
			nameStyle = nameStyle.Foreground(lipgloss.Color(color))
		}
	}
	return renderActivityTaskHeaderRow(width, glyph, taskDisplayName(snapshot), status, fillStyle, glyphStyle, nameStyle, statusStyle)
}

func (m appModel) activityTaskStatus(snapshot task.TaskSnapshot) (string, string, lipgloss.Style, lipgloss.Style) {
	switch snapshot.Status {
	case task.TaskRunning:
		elapsed := formatElapsedTime(time.Since(snapshot.StartedAt))
		return activityTaskSpinnerFrames[m.spinnerFrameIdx%len(activityTaskSpinnerFrames)], "running · " + elapsed, m.styles.StatusRunning, m.styles.StatusRunning
	case task.TaskFailed:
		return "✗", "failed", m.styles.StatusError, m.styles.StatusError
	case task.TaskStopped:
		return "■", "stopped", m.styles.StatusMuted, m.styles.StatusMuted
	case task.TaskInterrupted:
		return "!", "interrupted", m.styles.StatusWarning, m.styles.StatusWarning
	default:
		return "✓", "completed", m.styles.StatusSuccess, m.styles.StatusSuccess
	}
}

func (m appModel) renderSelectedActivityTaskDetails(snapshot task.TaskSnapshot, width int) []string {
	description := summarizeToolContent(snapshot.Description)
	if description == "" {
		description = summarizeToolContent(snapshot.Prompt)
	}
	if description == "" {
		description = "No task description."
	}

	contentWidth := maxInt(1, width-2)
	wrapped := strings.Split(wrapCompact(ansi.Strip(description), contentWidth), "\n")
	lines := make([]string, 0, len(wrapped)+1)
	for _, line := range wrapped {
		lines = append(lines, m.styles.SelectionSelected.Render(fitStyledCellLine("  "+line, width)))
	}

	meta := make([]string, 0, 2)
	if snapshot.UsedTokens > 0 {
		meta = append(meta, formatCompactTokenCount(snapshot.UsedTokens)+" tokens")
	}
	if m.taskPreview != nil && strings.TrimSpace(m.taskPreview.task.ID) == strings.TrimSpace(snapshot.ID) {
		preview := "previewing on left"
		if width < 40 {
			preview = "previewing"
		}
		meta = append(meta, preview)
	}
	if len(meta) > 0 {
		text := "  " + truncateStyledCellLine(strings.Join(meta, " · "), contentWidth)
		lines = append(lines, m.styles.SelectionSelected.Render(fitStyledCellLine(text, width)))
	}
	return lines
}

func renderActivityTaskHeaderRow(width int, glyph, name, status string, fillStyle, glyphStyle, nameStyle, statusStyle lipgloss.Style) string {
	width = maxInt(1, width)
	status = strings.TrimSpace(status)
	if status == "" {
		status = "unknown"
	}
	status = truncateStyledCellLine(status, maxInt(1, width/2))
	statusWidth := terminalCellWidth(status)
	glyph = truncateStyledCellLine(glyph, width)
	glyphWidth := terminalCellWidth(glyph)
	nameWidth := maxInt(0, width-glyphWidth-statusWidth-2)
	name = truncateStyledCellLine(name, nameWidth)
	leftWidth := glyphWidth + 1 + terminalCellWidth(name)
	gap := maxInt(1, width-leftWidth-statusWidth)
	row := glyphStyle.Render(glyph) + fillStyle.Render(" ") + nameStyle.Render(name) +
		fillStyle.Render(strings.Repeat(" ", gap)) + statusStyle.Render(status)
	return fitStyledCellLine(row, width)
}

func renderSidebarRow(width int, left, right string, leftStyle, rightStyle lipgloss.Style) string {
	width = maxInt(1, width)
	if width <= 2 || strings.TrimSpace(right) == "" {
		return leftStyle.Render(fitStyledCellLine(truncateStyledCellLine(left, width), width))
	}
	right = truncateStyledCellLine(right, maxInt(1, width/2))
	rightWidth := terminalCellWidth(right)
	leftMax := maxInt(1, width-rightWidth-1)
	left = truncateStyledCellLine(left, leftMax)
	gap := maxInt(0, width-terminalCellWidth(left)-rightWidth)
	return leftStyle.Render(left) + strings.Repeat(" ", gap) + rightStyle.Render(right)
}

func rendertaskSidebarRow(width int, dot, name, right string, dotStyle, nameStyle, rightStyle lipgloss.Style) string {
	width = maxInt(1, width)
	right = strings.TrimSpace(right)
	if width <= 2 || right == "" {
		return rendertaskSidebarLeft(width, dot, name, dotStyle, nameStyle)
	}
	right = truncateStyledCellLine(right, maxInt(1, width/2))
	rightWidth := terminalCellWidth(right)
	leftMax := maxInt(1, width-rightWidth-1)
	left := rendertaskSidebarLeft(leftMax, dot, name, dotStyle, nameStyle)
	gap := maxInt(0, width-terminalCellWidth(left)-rightWidth)
	return left + strings.Repeat(" ", gap) + rightStyle.Render(right)
}

func rendertaskSidebarLeft(width int, dot, name string, dotStyle, nameStyle lipgloss.Style) string {
	width = maxInt(1, width)
	dot = truncateStyledCellLine(dot, width)
	dotWidth := terminalCellWidth(dot)
	if width <= dotWidth+1 {
		return dotStyle.Render(fitStyledCellLine(dot, width))
	}
	nameWidth := maxInt(1, width-dotWidth-1)
	name = truncateStyledCellLine(name, nameWidth)
	left := dotStyle.Render(dot) + " " + nameStyle.Render(name)
	if terminalCellWidth(left) >= width {
		return left
	}
	return left + strings.Repeat(" ", width-terminalCellWidth(left))
}

func formatElapsedTime(d time.Duration) string {
	s := int(d.Seconds())
	if s < 0 {
		s = 0
	}
	if s < 60 {
		return fmt.Sprintf("%ds", s)
	}
	h := s / 3600
	m := (s % 3600) / 60
	sec := s % 60
	if h > 0 {
		return fmt.Sprintf("%dh %dm %ds", h, m, sec)
	}
	return fmt.Sprintf("%dm %ds", m, sec)
}
