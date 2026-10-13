package bubble

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"paw/internal/todo"
)

var (
	activityFocusStyle = lipgloss.NewStyle().Foreground(colorManager.LipglossColor(colorSignal)).Bold(true)
	activityHintStyle  = lipgloss.NewStyle().Foreground(colorManager.LipglossColor(colorContextFree))
)

func (m appModel) renderActivityPane(width, height int) string {
	width = maxInt(1, width)
	height = maxInt(1, height)
	topHeight := height / 2
	top := ""
	if m.activity.tab == activityTabTodo {
		top = m.renderActivityTodo(width, topHeight)
	} else if topHeight > 0 {
		title := wizardTitleStyle.Render(fmt.Sprintf("%s %d", copyTasksTitle, len(m.activity.tasks)))
		top = title + "\n" + m.renderActivityTasks(width, topHeight-1)
	}
	bottom := renderTokenHistory(m.tokenMonitorView(), width, height-topHeight)
	if topHeight == 0 {
		return bottom
	}
	return fitStyledRect(top, width, topHeight) + "\n" + bottom
}

func (m appModel) renderActivityTodo(width, height int) string {
	if height <= 0 {
		return ""
	}
	if !m.hasCurrentTodo || m.currentTodo.Cleared() {
		return unselectedProviderStyle.Render(copyTodoEmpty)
	}
	snapshot := m.currentTodo.Clone()
	contentWidth := maxInt(1, width-2)
	count := fmt.Sprintf("%d/%d", snapshot.CompletedCount(), snapshot.TotalCount())
	title := todoTitleStyle.Render(copyChipTodo)
	gap := maxInt(1, contentWidth-terminalCellWidth(title)-terminalCellWidth(count))
	lines := []string{fitStyledCellLine(title+strings.Repeat(" ", gap)+todoCountStyle.Render(count), width)}
	if snapshot.Explanation != "" {
		lines = append(lines, "")
		for _, line := range wrapStyledCellLine(snapshot.Explanation, contentWidth) {
			lines = append(lines, fitStyledCellLine("  "+todoExplanationStyle.Render(line), width))
		}
	}
	if len(snapshot.Items) > 0 {
		lines = append(lines, "")
	}
	if len(lines) >= height && len(snapshot.Items) > 0 {
		lines = lines[:maxInt(1, height-2)]
	}
	for index, item := range snapshot.Items {
		remaining := len(snapshot.Items) - index
		if len(lines) >= height-1 && remaining > 1 {
			lines = append(lines, unselectedProviderStyle.Render(fmt.Sprintf("+%d more items", remaining)))
			break
		}
		itemLines := renderTodoItemLine(snapshot, item, width)
		available := height - len(lines)
		if remaining > 1 {
			available--
		}
		if available <= 0 {
			break
		}
		if len(itemLines) > available {
			if available > 1 {
				hidden := len(itemLines) - available + 1
				itemLines[available-1] = fitStyledCellLine(todoSummaryStyle.Render(fmt.Sprintf("    +%d more blockers", hidden)), width)
			}
			itemLines = itemLines[:available]
		}
		lines = append(lines, itemLines...)
	}
	return fitStyledRect(strings.Join(lines, "\n"), width, height)
}

func renderTodoItemLine(snapshot todo.Snapshot, item todo.Item, width int) []string {
	icon, iconStyle := todoStatusDisplay(item.Status)
	var blockers []string
	if item.Status == todo.StatusPending {
		blockers = snapshot.UnresolvedBlockers(item.ID)
		if len(blockers) > 0 {
			icon, iconStyle = "⊘", todoInProgressStyle
		}
	}
	mark := iconStyle.Render(icon + " ")
	remainingWidth := maxInt(1, width-terminalCellWidth(mark)-2)
	content := truncateStyledCellLine(item.Content, remainingWidth)
	prefix := "  "
	line := prefix + mark + content
	lines := []string{fitStyledCellLine(line, width)}
	for _, id := range blockers {
		label := "等待 " + id
		for _, dependency := range snapshot.Items {
			if dependency.ID == id {
				label += " · " + dependency.Content
				break
			}
		}
		label = truncateStyledCellLine(label, maxInt(1, width-4))
		lines = append(lines, fitStyledCellLine("    "+todoSummaryStyle.Render(label), width))
	}
	return lines
}

func todoStatusDisplay(status todo.Status) (string, lipgloss.Style) {
	switch status {
	case todo.StatusCompleted:
		return glyphSuccess, todoCompletedStyle
	case todo.StatusInProgress:
		return glyphActive, todoInProgressStyle
	default:
		return "○", todoPendingStyle
	}
}
