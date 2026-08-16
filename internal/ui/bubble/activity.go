package bubble

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"paw/internal/todo"
)

func (m appModel) renderActivityBox() string {
	if m.taskPicker == nil {
		return ""
	}
	panelWidth := m.activityPanelWidth()
	panelHeight := m.activityPanelHeight()
	contentWidth := maxInt(1, panelWidth-wizardPanelStyle.GetHorizontalFrameSize()-wizardPanelStyle.GetHorizontalPadding())
	contentHeight := maxInt(1, panelHeight-wizardPanelStyle.GetVerticalFrameSize()-wizardPanelStyle.GetVerticalPadding())

	tasksTab := " Tasks "
	todoTab := " Todo "
	if m.taskPicker.tab == activityTabTasks {
		tasksTab = selectedProviderStyle.Render(tasksTab)
		todoTab = unselectedProviderStyle.Render(todoTab)
	} else {
		tasksTab = unselectedProviderStyle.Render(tasksTab)
		todoTab = selectedProviderStyle.Render(todoTab)
	}

	lines := []string{
		wizardTitleStyle.Render("Activity"),
		tasksTab + " " + todoTab,
	}
	switch m.taskPicker.tab {
	case activityTabTodo:
		lines = append(lines, "", m.renderActivityTodo(contentWidth, maxInt(1, contentHeight-len(lines))))
	default:
		lines = append(lines, "", m.renderActivityTasks(contentWidth, maxInt(1, contentHeight-len(lines))))
	}
	lines = append(lines, "", unselectedProviderStyle.Render("Tab/←/→ switch  ↑/↓ select  Enter open  Esc close"))
	return renderFixedStyledPanel(wizardPanelStyle, panelWidth, panelHeight, strings.Join(lines, "\n"))
}

// renderActivityTodo 在 Activity 面板中渲染当前 todo 列表。
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

// renderTodoItemLine 渲染单个 todo 项。
func renderTodoItemLine(item todo.Item, width, contentWidth int) []string {
	icon, iconStyle := todoStatusDisplay(item.Status)
	mark := iconStyle.Render(icon + " ")
	remainingWidth := maxInt(1, width-terminalCellWidth(mark)-2)
	content := truncateStyledCellLine(item.Content, remainingWidth)
	prefix := "  "
	line := prefix + mark + content
	return []string{fitStyledCellLine(line, width)}
}

// todoStatusDisplay 根据 todo 状态返回图标与样式。
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

// activityPanelWidth 是右侧 Activity 面板的显示宽度（含边框）：贴右边界，
// 最多占内容区一半（上限 60 列），把左侧留给 transcript。
func (m appModel) activityPanelWidth() int {
	layout := m.currentLayout()
	return minInt(60, maxInt(1, layout.contentWidth/2))
}

// activityPanelHeight 是右侧 Activity 面板的显示高度（含边框）：最高占
// transcript 区域（上下各留 1 行呼吸空间）。
func (m appModel) activityPanelHeight() int {
	layout := m.currentLayout()
	return maxInt(1, layout.transcriptHeight-2)
}

func (m appModel) renderActivityTasks(width, height int) string {
	if m.taskController == nil {
		return labelErrorStyle.Render("Task controller is unavailable.")
	}
	if len(m.taskEntries()) == 0 {
		return unselectedProviderStyle.Render("No tasks yet.")
	}
	return lipgloss.NewStyle().
		Width(maxInt(1, width)).
		MaxWidth(maxInt(1, width)).
		MaxHeight(maxInt(1, height)).
		Render(m.renderTasksCardContentHeight(width, height))
}
