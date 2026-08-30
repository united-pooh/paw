package bubble

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"paw/internal/todo"
)

var activityPages = []struct {
	id    activityTab
	title string
}{
	{id: activityTabTasks, title: "Tasks"},
	{id: activityTabTodo, title: "Todo"},
}

var (
	activityFocusStyle = lipgloss.NewStyle().Foreground(colorManager.LipglossColor(colorSignal)).Bold(true)
	activityHintStyle  = lipgloss.NewStyle().Foreground(colorManager.LipglossColor(colorContextFree))
)

func (m appModel) renderActivityPane(width, height int) string {
	width = maxInt(1, width)
	height = maxInt(1, height)
	headerHeight := minInt(2, height)
	footerHeight := 0
	if height >= 4 {
		footerHeight = 1
	}
	bodyHeight := maxInt(0, height-headerHeight-footerHeight)

	focus := ""
	if m.activity.focus == activityFocusPanel {
		focus = activityFocusStyle.Render("FOCUSED")
	}
	title := renderSidebarRow(width, "Activity", focus, wizardTitleStyle, activityFocusStyle)
	parts := []string{title}
	if headerHeight > 1 {
		parts = append(parts, m.renderActivityTabs(width))
	}

	body := ""
	switch m.activity.tab {
	case activityTabTodo:
		body = m.renderActivityTodo(width, bodyHeight)
	default:
		body = m.renderActivityTasks(width, bodyHeight)
	}
	if bodyHeight > 0 {
		parts = append(parts, fitStyledRect(body, width, bodyHeight))
	}
	if footerHeight > 0 {
		parts = append(parts, activityHintStyle.Render(truncateStyledCellLine(m.activityFooterHint(), width)))
	}
	return fitStyledRect(strings.Join(parts, "\n"), width, height)
}

func (m appModel) renderActivityTabs(width int) string {
	labels := make([]string, 0, len(activityPages))
	for _, page := range activityPages {
		label := page.title
		switch page.id {
		case activityTabTasks:
			label += " " + strconv.Itoa(len(m.activity.tasks))
		case activityTabTodo:
			if m.hasCurrentTodo && !m.currentTodo.Cleared() {
				label += fmt.Sprintf(" %d/%d", m.currentTodo.CompletedCount(), m.currentTodo.TotalCount())
			}
		}
		style := unselectedProviderStyle
		if page.id == m.activity.tab {
			style = m.styles.SelectionSelected
		}
		labels = append(labels, style.Render(" "+label+" "))
	}
	return truncateStyledCellLine(strings.Join(labels, " "), width)
}

func (m appModel) activityFooterHint() string {
	if m.activity.tab == activityTabTodo {
		return "Tab/←/→ page · Esc main"
	}
	return "↑/↓ select · Enter preview · Tab page · Esc main"
}

func (m appModel) renderActivityFullscreenBottomContent(width int) string {
	return truncateStyledCellLine("ACTIVITY · Esc/Ctrl+G back", width)
}

func (m appModel) renderActivityTodo(width, height int) string {
	if !m.hasCurrentTodo || m.currentTodo.Cleared() {
		return unselectedProviderStyle.Render("No active todo list")
	}
	snapshot := m.currentTodo.Clone()
	contentWidth := maxInt(1, width-2)
	count := fmt.Sprintf("%d/%d", snapshot.CompletedCount(), snapshot.TotalCount())
	title := todoTitleStyle.Render("Todo")
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
	for _, item := range snapshot.Items {
		lines = append(lines, renderTodoItemLine(item, width, contentWidth)...)
	}
	if len(lines) > height {
		lines = lines[:height-1]
		lines = append(lines, unselectedProviderStyle.Render("↑↓ scroll for more"))
	}
	return strings.Join(lines, "\n")
}

func renderTodoItemLine(item todo.Item, width, contentWidth int) []string {
	icon, iconStyle := todoStatusDisplay(item.Status)
	mark := iconStyle.Render(icon + " ")
	remainingWidth := maxInt(1, width-terminalCellWidth(mark)-2)
	content := truncateStyledCellLine(item.Content, remainingWidth)
	prefix := "  "
	line := prefix + mark + content
	return []string{fitStyledCellLine(line, width)}
}

func todoStatusDisplay(status todo.Status) (string, lipgloss.Style) {
	switch status {
	case todo.StatusCompleted:
		return "✓", todoCompletedStyle
	case todo.StatusInProgress:
		return "◌", todoInProgressStyle
	default:
		return "○", todoPendingStyle
	}
}
