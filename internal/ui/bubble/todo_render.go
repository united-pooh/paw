package bubble

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"paw/internal/todo"
)

func renderTodoEntry(entry transcriptEntry, width int) string {
	if width <= 0 || entry.todoSnapshot == nil {
		return ""
	}
	snapshot := entry.todoSnapshot.Clone()
	if entry.todoCleared || snapshot.Cleared() {
		return renderTodoCollapsed(snapshot, false, true, width)
	}
	if !entry.todoExpanded {
		return renderTodoCollapsed(snapshot, entry.todoCompletedFold, false, width)
	}
	return renderTodoExpanded(snapshot, width)
}

func renderTodoCollapsed(snapshot todo.Snapshot, completedFold, cleared bool, width int) string {
	if width <= 0 {
		return ""
	}
	text := "─ Todo cleared"
	if !cleared {
		if completedFold {
			text = fmt.Sprintf("✓ Todo completed · %d/%d", snapshot.CompletedCount(), snapshot.TotalCount())
		} else {
			text = fmt.Sprintf("▸ Todo updated · %d/%d", snapshot.CompletedCount(), snapshot.TotalCount())
		}
	}
	return fitStyledCellLine(todoSummaryStyle.Render(text), width)
}

func renderTodoExpanded(snapshot todo.Snapshot, width int) string {
	if width <= 0 {
		return ""
	}
	contentWidth := maxInt(1, width-2)
	count := fmt.Sprintf("%d/%d", snapshot.CompletedCount(), snapshot.TotalCount())
	title := todoTitleStyle.Render("Todo")
	gap := maxInt(1, contentWidth-terminalCellWidth(title)-terminalCellWidth(count))
	header := fitStyledCellLine(title+strings.Repeat(" ", gap)+todoCountStyle.Render(count), width)
	lines := []string{header}
	if snapshot.Explanation != "" {
		lines = append(lines, "")
		for _, line := range wrapDisplayWidthLine(snapshot.Explanation, contentWidth) {
			lines = append(lines, fitStyledCellLine("  "+todoExplanationStyle.Render(line), width))
		}
	}
	if len(snapshot.Items) > 0 {
		lines = append(lines, "")
	}
	showStatusLabel := width >= 24
	for _, item := range snapshot.Items {
		lines = append(lines, renderTodoItem(item, width, showStatusLabel)...)
	}
	return strings.Join(lines, "\n")
}

func todoItemBodyStyle(status todo.Status) lipgloss.Style {
	style := bodyStyle
	if status == todo.StatusCompleted {
		style = style.Copy().Strikethrough(true)
	}
	return style
}

func renderTodoItem(item todo.Item, width int, showStatusLabel bool) []string {
	if width <= 0 {
		return nil
	}
	icon, label := todoStatusDisplay(item.Status)
	style := todoPendingStyle
	switch item.Status {
	case todo.StatusCompleted:
		style = todoCompletedStyle
	case todo.StatusInProgress:
		style = todoInProgressStyle
	}
	prefix := icon + " "
	suffix := ""
	if showStatusLabel {
		suffix = "  " + label
	}
	bodyStyleForItem := todoItemBodyStyle(item.Status)
	bodyWidth := maxInt(1, width-terminalCellWidth(prefix)-terminalCellWidth(suffix))
	wrapped := wrapDisplayWidthLine(item.Content, bodyWidth)
	lines := make([]string, 0, len(wrapped))
	for i, line := range wrapped {
		if i == 0 {
			lines = append(lines, fitStyledCellLine(style.Render(prefix)+bodyStyleForItem.Render(line)+style.Render(suffix), width))
			continue
		}
		lines = append(lines, fitStyledCellLine(strings.Repeat(" ", terminalCellWidth(prefix))+bodyStyleForItem.Render(line), width))
	}
	return lines
}

func todoStatusDisplay(status todo.Status) (icon, label string) {
	switch status {
	case todo.StatusCompleted:
		return "✓", "已完成"
	case todo.StatusInProgress:
		return "●", "进行中"
	case todo.StatusPending:
		return "○", "待处理"
	default:
		return "?", "未知"
	}
}
