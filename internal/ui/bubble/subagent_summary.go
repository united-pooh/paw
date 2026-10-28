package bubble

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"paw/internal/runtime/task"
)

type subagentCounts struct {
	running, completed, failed, stopped, interrupted, unknown int
}

func (m appModel) currentSubagentCounts() subagentCounts {
	var counts subagentCounts
	if strings.TrimSpace(m.sessionID) == "" {
		return counts
	}
	tasks := m.activity.tasks
	if !m.activity.summaryTasksSeeded || m.activity.summarySessionID != m.sessionID {
		tasks = m.taskEntries()
	}
	for _, entry := range tasks {
		switch entry.Status {
		case task.TaskRunning:
			counts.running++
		case task.TaskCompleted:
			counts.completed++
		case task.TaskFailed:
			counts.failed++
		case task.TaskStopped:
			counts.stopped++
		case task.TaskInterrupted:
			counts.interrupted++
		default:
			counts.unknown++
		}
	}
	return counts
}

func (counts subagentCounts) total() int {
	return counts.running + counts.completed + counts.failed + counts.stopped + counts.interrupted + counts.unknown
}

func (counts subagentCounts) render(compact bool) string {
	states := []struct {
		count int
		label string
		glyph string
		color colorRole
	}{
		{counts.running, "工作中", "⠋", colorContextUsed},
		{counts.completed, "已完成", "✓", colorWorktreeClean},
		{counts.failed, "失败", "✗", colorStatusError},
		{counts.stopped, "已停止", "■", colorContextFree},
		{counts.interrupted, "已中断", "!", colorStatusWarning},
		{counts.unknown, "未知", "?", colorStatusWarning},
	}
	parts := make([]string, 0, len(states))
	for _, state := range states {
		if state.count == 0 {
			continue
		}
		text := fmt.Sprintf("%d %s", state.count, state.label)
		if compact {
			text = fmt.Sprintf("%d%s", state.count, state.glyph)
		}
		style := lipgloss.NewStyle().Foreground(colorManager.LipglossColor(state.color))
		parts = append(parts, style.Render(text))
	}
	separator := contextFreeStyle.Render(" · ")
	if compact {
		separator = " "
	}
	return strings.Join(parts, separator)
}

func (m appModel) collapsedSubagentSummary(width int) string {
	fullWidth := m.width
	if fullWidth <= 0 {
		fullWidth = width
	}
	if computeActivityGeometry(fullWidth, m.activity.visible, m.activity.widthColumns).mode == activityLayoutDocked {
		return ""
	}
	counts := m.currentSubagentCounts()
	if counts.total() == 0 {
		return ""
	}
	// Reserve enough cells for the context percentage before shortening labels.
	budget := width - minInt(14, maxInt(4, width/2)) - 2
	label := contextFreeStyle.Render("子代理 ")
	for _, text := range []string{
		label + counts.render(false),
		label + counts.render(true),
		counts.render(true),
		label + fmt.Sprint(counts.total()),
	} {
		if terminalCellWidth(text) <= budget {
			return text
		}
	}
	return ""
}

func (m appModel) bottomDockSubagentRight(width int) string {
	summary := m.collapsedSubagentSummary(width)
	if summary == "" {
		return ""
	}
	worktree := m.renderBottomDockWorktree(width)
	if worktree != "" && width-terminalCellWidth(summary)-terminalCellWidth(worktree)-4 >= 72 {
		return summary + "  " + worktree
	}
	return summary
}
