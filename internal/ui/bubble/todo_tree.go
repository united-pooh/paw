package bubble

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"paw/internal/todo"
)

type todoTreeRow struct {
	text     string
	group    string
	expanded bool
}

type todoTreeGroup struct {
	name  string
	items []todo.Item
}

func (m appModel) todoTreeRows(width int) []todoTreeRow {
	if !m.hasCurrentTodo || m.currentTodo.Cleared() {
		return nil
	}
	snapshot := m.currentTodo
	groups := make([]todoTreeGroup, 0)
	indices := make(map[string]int)
	activeGroup := ""
	hasActive := false
	for _, item := range snapshot.Items {
		if item.Status == todo.StatusInProgress {
			activeGroup = item.Group
			hasActive = true
		}
		if index, ok := indices[item.Group]; item.Group != "" && ok {
			groups[index].items = append(groups[index].items, item)
		} else {
			indices[item.Group] = len(groups)
			groups = append(groups, todoTreeGroup{name: item.Group, items: []todo.Item{item}})
		}
	}
	if !hasActive {
		for _, item := range snapshot.Items {
			if item.Status != todo.StatusCompleted {
				activeGroup = item.Group
				break
			}
		}
	}
	rows := make([]todoTreeRow, 0, len(snapshot.Items)+len(groups))
	ordinal := 0
	for index, group := range groups {
		branch, continuation := "├─ ", "│  "
		if index == len(groups)-1 {
			branch, continuation = "└─ ", "   "
		}
		if group.name == "" {
			rows = append(rows, renderTodoTreeItem(snapshot, group.items[0], width, branch, continuation)...)
			continue
		}
		ordinal++
		completed := 0
		for _, item := range group.items {
			if item.Status == todo.StatusCompleted {
				completed++
			}
		}
		style := todoPendingStyle
		if completed == len(group.items) {
			style = todoCompletedStyle
		} else if group.name == activeGroup {
			style = todoInProgressStyle
		}
		count := fmt.Sprintf(" · %d/%d", completed, len(group.items))
		label := todoGroupOrdinal(ordinal) + ". " + todoTreeLabel(group.name)
		label = truncateStyledCellLine(label, maxInt(1, width-terminalCellWidth(branch+count)))
		expanded, overridden := m.activity.todoExpanded[group.name]
		if !overridden {
			expanded = group.name == activeGroup
		}
		rows = append(rows, todoTreeRow{text: fitStyledCellLine(style.Render(branch+label)+todoCountStyle.Render(count), width), group: group.name, expanded: expanded})
		if !expanded {
			continue
		}
		for itemIndex, item := range group.items {
			childBranch, childContinuation := "├─ ", "│  "
			if itemIndex == len(group.items)-1 {
				childBranch, childContinuation = "└─ ", "   "
			}
			rows = append(rows, renderTodoTreeItem(snapshot, item, width, continuation+childBranch, continuation+childContinuation)...)
		}
	}
	return rows
}

func todoGroupOrdinal(n int) string {
	if n > 20 {
		return fmt.Sprint(n)
	}
	var result strings.Builder
	for _, part := range []struct {
		value int
		text  string
	}{{10, "X"}, {9, "IX"}, {5, "V"}, {4, "IV"}, {1, "I"}} {
		for n >= part.value {
			result.WriteString(part.text)
			n -= part.value
		}
	}
	return result.String()
}

func renderTodoTreeItem(snapshot todo.Snapshot, item todo.Item, width int, branch, continuation string) []todoTreeRow {
	style, icon := todoPendingStyle, "☐"
	switch item.Status {
	case todo.StatusCompleted:
		style, icon = todoCompletedStyle, "✓"
	case todo.StatusInProgress:
		style = todoInProgressStyle
	}
	blockers := snapshot.UnresolvedBlockers(item.ID)
	if len(blockers) > 0 || item.BlockedReason != "" {
		icon = "⊘"
	}
	mark := style.Render(icon)
	if item.Status == todo.StatusCompleted {
		mark = style.Copy().Reverse(true).Render(icon)
	}
	content := truncateStyledCellLine(todoTreeLabel(item.Content), maxInt(1, width-terminalCellWidth(branch)-terminalCellWidth(icon)-1))
	content = style.Copy().Strikethrough(item.Status == todo.StatusCompleted).Render(content)
	rows := []todoTreeRow{{text: fitStyledCellLine(todoPendingStyle.Render(branch)+mark+" "+content, width)}}
	for _, id := range blockers {
		label := "等待 " + todoTreeLabel(id)
		for _, dependency := range snapshot.Items {
			if dependency.ID == id {
				label += " · " + todoTreeLabel(dependency.Content)
				break
			}
		}
		rows = append(rows, todoTreeRow{text: fitStyledCellLine(todoSummaryStyle.Render(continuation+"  "+truncateStyledCellLine(label, maxInt(1, width-terminalCellWidth(continuation)-2))), width)})
	}
	if item.BlockedReason != "" {
		rows = append(rows, todoTreeRow{text: fitStyledCellLine(todoSummaryStyle.Render(continuation+"  "+truncateStyledCellLine("阻塞 · "+todoTreeLabel(item.BlockedReason), maxInt(1, width-terminalCellWidth(continuation)-2))), width)})
	}
	return rows
}

func todoTreeLabel(text string) string {
	return strings.Join(strings.Fields(sanitizeTerminalText(text)), " ")
}

func (m appModel) todoTreeWindow(width, height int) ([]todoTreeRow, int, int) {
	rows := m.todoTreeRows(width)
	capacity := maxInt(0, height-1)
	start := clampInt(m.activity.todoScroll, 0, maxInt(0, len(rows)-capacity))
	return rows, start, minInt(len(rows), start+capacity)
}

func (m appModel) renderActivityTodo(width, height int) string {
	if height <= 0 {
		return ""
	}
	if !m.hasCurrentTodo || m.currentTodo.Cleared() {
		return fitStyledRect(unselectedProviderStyle.Render(copyTodoEmpty), width, height)
	}
	rows, start, end := m.todoTreeWindow(width, height)
	count := fmt.Sprintf(" %d/%d", m.currentTodo.CompletedCount(), m.currentTodo.TotalCount())
	if hidden := len(rows) - (end - start); hidden > 0 {
		count += fmt.Sprintf(" · 另%d行", hidden)
	}
	lines := []string{fitStyledCellLine(todoTitleStyle.Render(copyChipTodo)+todoCountStyle.Render(count), width)}
	for _, row := range rows[start:end] {
		lines = append(lines, row.text)
	}
	return fitStyledRect(strings.Join(lines, "\n"), width, height)
}

func (m appModel) withTodoDock(layout tuiLayout) tuiLayout {
	if layout.activityMode == activityLayoutDocked || !m.hasCurrentTodo || m.currentTodo.Cleared() {
		return layout
	}
	available := maxInt(0, layout.transcriptHeight-3)
	layout.todoHeight = minInt(8, minInt(available, 1+len(m.todoTreeRows(layout.workspaceWidth))))
	layout.transcriptHeight -= layout.todoHeight
	return layout
}

func (m appModel) handleTodoTreeMouse(msg tea.MouseMsg) (appModel, bool) {
	if m.modelWizard != nil || m.settingWizard != nil || m.configCenter != nil || m.themePicker != nil || m.sessionPicker != nil || m.selectionDock != nil {
		return m, false
	}
	layout := m.currentLayout()
	top, left, width, height := 1+layout.headerHeight+layout.transcriptHeight+layout.workStatusHeight, 0, layout.workspaceWidth, layout.todoHeight
	if layout.activityMode == activityLayoutDocked {
		top, left, width = 1, layout.workspaceWidth+layout.activitySeparatorWidth, layout.activityWidth
		height, _ = m.activitySectionHeights(layout.contentHeight)
	}
	if height == 0 || msg.X < left || msg.X >= left+width || msg.Y < top || msg.Y >= top+height {
		return m, false
	}
	rows, start, _ := m.todoTreeWindow(width, height)
	if isMouseWheel(msg) {
		delta := 3
		if msg.Button == tea.MouseButtonWheelUp || msg.Type == tea.MouseWheelUp {
			delta = -3
		}
		m.activity.todoScroll = clampInt(start+delta, 0, maxInt(0, len(rows)-maxInt(0, height-1)))
		return m, true
	}
	index := start + msg.Y - top - 1
	if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft && msg.Y > top && index >= 0 && index < len(rows) && rows[index].group != "" {
		group := rows[index].group
		if m.activity.todoExpanded == nil {
			m.activity.todoExpanded = make(map[string]bool)
		}
		m.activity.todoExpanded[group] = !rows[index].expanded
		m.activity.todoScroll = 0
		wasAtBottom := m.viewport.AtBottom()
		m.relayout()
		m.refreshViewportWithBottomState(wasAtBottom)
	}
	return m, true
}
