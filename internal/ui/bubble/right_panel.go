// 本文件定义 Activity modal 使用的 Subagents 内容渲染逻辑。
package bubble

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"paw/internal/subagent"
)

// renderSubagentsCardContent 渲染 Subagents 内容（Task 4 实现）。
func (m appModel) renderSubagentsCardContent(width int) string {
	return m.renderSubagentsCardContentHeight(width, 0)
}

func (m appModel) renderSubagentsCardContentHeight(width, height int) string {
	hdrStyle := lipgloss.NewStyle().Foreground(colorManager.LipglossColor(colorHeaderForeground)).Bold(true)
	mutedStyle := lipgloss.NewStyle().Foreground(colorManager.LipglossColor(colorContextFree))
	hdr := renderSidebarRow(width, "subagents", "", hdrStyle, mutedStyle)
	maxLines := height

	if m.subagents == nil {
		if maxLines == 1 {
			return hdr
		}
		return hdr + "\n" + mutedStyle.Italic(true).Render(fitStyledCellLine("none", width))
	}
	tasks := m.subagentTasks()
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
	if m.subagentPicker != nil && availableTaskRows > 0 {
		selected := clampInt(m.subagentPicker.selectedIndex, 0, len(tasks)-1)
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
		case subagent.TaskRunning:
			dot = spinnerFrames[m.spinnerFrameIdx%len(spinnerFrames)]
			status = formatElapsedTime(time.Since(t.StartedAt))
			dotStyle = dotRunStyle
			lineStyle = lipgloss.NewStyle().Foreground(colorManager.LipglossColor(colorWorktreeClean))
		case subagent.TaskFailed:
			dot = "✗"
			status = "failed"
			dotStyle = dotFailStyle
			lineStyle = lipgloss.NewStyle().Foreground(colorManager.LipglossColor(colorLabelError))
		case subagent.TaskStopped:
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
		if m.subagentPicker != nil && idx == clampInt(m.subagentPicker.selectedIndex, 0, len(tasks)-1) {
			dot = ">"
			dotStyle = selectedProviderStyle
			nameStyle = selectedProviderStyle
			lineStyle = selectedProviderStyle
		}
		lines = append(lines, renderSubagentSidebarRow(width, dot, taskDisplayName(t), status, dotStyle, nameStyle, lineStyle))
	}
	if moreCount > 0 {
		lines = append(lines, renderSidebarRow(width, fmt.Sprintf("+%d more", moreCount), "", mutedStyle, mutedStyle))
	}
	return strings.Join(lines, "\n")
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

func renderSubagentSidebarRow(width int, dot, name, right string, dotStyle, nameStyle, rightStyle lipgloss.Style) string {
	width = maxInt(1, width)
	right = strings.TrimSpace(right)
	if width <= 2 || right == "" {
		return renderSubagentSidebarLeft(width, dot, name, dotStyle, nameStyle)
	}
	right = truncateStyledCellLine(right, maxInt(1, width/2))
	rightWidth := terminalCellWidth(right)
	leftMax := maxInt(1, width-rightWidth-1)
	left := renderSubagentSidebarLeft(leftMax, dot, name, dotStyle, nameStyle)
	gap := maxInt(0, width-terminalCellWidth(left)-rightWidth)
	return left + strings.Repeat(" ", gap) + rightStyle.Render(right)
}

func renderSubagentSidebarLeft(width int, dot, name string, dotStyle, nameStyle lipgloss.Style) string {
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
