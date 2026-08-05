package bubble

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

type todoPage struct {
	offset int
}

func newTodoPage() *todoPage { return &todoPage{} }

func (p *todoPage) resetForSnapshot() {
	if p != nil {
		p.offset = 0
	}
}

func (m *appModel) openTodoPage() (tea.Model, tea.Cmd) {
	if m == nil {
		return appModel{}, nil
	}
	m.todoPage = newTodoPage()
	m.completion = nil
	m.input.Blur()
	m.relayout()
	if m.cursorAnchor != nil {
		m.cursorAnchor.clear()
	}
	return *m, nil
}

func (m *appModel) closeTodoPage() (tea.Model, tea.Cmd) {
	if m == nil {
		return appModel{}, nil
	}
	m.todoPage = nil
	m.relayout()
	return *m, m.input.Focus()
}

func (m *appModel) handleTodoPageKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m == nil || m.todoPage == nil {
		return *m, nil
	}
	visibleHeight := maxInt(1, m.currentLayout().contentHeight-2)
	maxOffset := m.todoPageMaxOffset(visibleHeight)
	switch msg.String() {
	case "esc", "ctrl+p":
		return m.closeTodoPage()
	case "up", "k":
		m.todoPage.offset--
	case "down", "j":
		m.todoPage.offset++
	case "pgup":
		m.todoPage.offset -= visibleHeight
	case "pgdown":
		m.todoPage.offset += visibleHeight
	case "home", "g":
		m.todoPage.offset = 0
	case "end", "G":
		m.todoPage.offset = maxOffset
	}
	m.todoPage.offset = clampInt(m.todoPage.offset, 0, maxOffset)
	return *m, nil
}

func (m *appModel) handleTodoPageMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if m == nil || m.todoPage == nil {
		return *m, nil
	}
	visibleHeight := maxInt(1, m.currentLayout().contentHeight-2)
	switch msg.Button {
	case tea.MouseButtonWheelUp:
		m.todoPage.offset -= 3
	case tea.MouseButtonWheelDown:
		m.todoPage.offset += 3
	}
	m.todoPage.offset = clampInt(m.todoPage.offset, 0, m.todoPageMaxOffset(visibleHeight))
	return *m, nil
}

func (m appModel) todoPageMaxOffset(visibleHeight int) int {
	return maxInt(0, len(m.todoPageBodyLines(maxInt(1, m.currentLayout().contentWidth)))-maxInt(1, visibleHeight))
}

func (m appModel) renderTodoPage(width, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	footerHeight := 1
	bodyHeight := maxInt(0, height-footerHeight)
	body := m.todoPageBodyLines(width)
	offset := 0
	if m.todoPage != nil {
		offset = clampInt(m.todoPage.offset, 0, maxInt(0, len(body)-bodyHeight))
	}
	visible := make([]string, 0, bodyHeight)
	if bodyHeight > 0 && offset < len(body) {
		end := minInt(len(body), offset+bodyHeight)
		visible = append(visible, body[offset:end]...)
	}
	for len(visible) < bodyHeight {
		visible = append(visible, "")
	}
	footer := fitStyledCellLine(todoSummaryStyle.Render("↑↓ scroll  esc close"), width)
	return fitStyledRect(strings.Join(append(visible, footer), "\n"), width, height)
}

func (m appModel) todoPageBodyLines(width int) []string {
	width = maxInt(1, width)
	if !m.hasCurrentTodo || m.currentTodo.Cleared() {
		return []string{
			fitStyledCellLine(todoTitleStyle.Render("Todo"), width),
			"",
			fitStyledCellLine(bodyStyle.Render("No active todo list"), width),
			"",
			fitStyledCellLine(todoExplanationStyle.Render("The agent creates one automatically for complex tasks."), width),
		}
	}
	snapshot := m.currentTodo.Clone()
	count := fmt.Sprintf("%d/%d", snapshot.CompletedCount(), snapshot.TotalCount())
	title := todoTitleStyle.Render("Todo")
	gap := maxInt(1, width-terminalCellWidth(title)-terminalCellWidth(count))
	lines := []string{fitStyledCellLine(title+strings.Repeat(" ", gap)+todoCountStyle.Render(count), width)}
	if snapshot.Explanation != "" {
		lines = append(lines, "")
		for _, line := range wrapStyledCellLine(snapshot.Explanation, maxInt(1, width-2)) {
			lines = append(lines, fitStyledCellLine("  "+todoExplanationStyle.Render(line), width))
		}
	}
	lines = append(lines, "")
	for _, item := range snapshot.Items {
		lines = append(lines, renderTodoItem(item, width, width >= 24)...)
	}
	return lines
}
