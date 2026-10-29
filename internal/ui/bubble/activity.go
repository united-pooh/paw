package bubble

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
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
	todoHeight, tasksHeight := m.activitySectionHeights(height)
	if todoHeight > 0 {
		top = m.renderActivityTodo(width, todoHeight)
		if tasksHeight > 0 {
			title := todoTitleStyle.Render(copyTasksTitle) + " " + m.currentSubagentCounts().render(false)
			lines := []string{fitStyledCellLine(title, width)}
			for index, snapshot := range m.activity.tasks {
				if index >= tasksHeight-1 {
					break
				}
				lines = append(lines, m.renderActivityTaskHeader(snapshot, width))
			}
			top += "\n" + fitStyledRect(strings.Join(lines, "\n"), width, tasksHeight)
		}
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

func (m appModel) activitySectionHeights(height int) (int, int) {
	topHeight := maxInt(0, height/2)
	if !m.hasCurrentTodo || m.currentTodo.Cleared() {
		return 0, topHeight
	}
	tasksHeight := 0
	if len(m.activity.tasks) > 0 && topHeight >= 4 {
		tasksHeight = minInt(4, minInt(1+len(m.activity.tasks), maxInt(2, topHeight-4)))
	}
	return topHeight - tasksHeight, tasksHeight
}
