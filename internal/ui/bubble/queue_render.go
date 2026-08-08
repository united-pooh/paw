package bubble

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

const queuePanelMaxHeight = 8

func (m appModel) queuePanelHeight() int {
	if m.chatQueue.Len() == 0 && m.queueMode != queueModeEditing {
		return 0
	}
	// The compact summary is rendered in the input dock's bottom rule. Only the
	// interactive queue views need a separate panel below the input.
	switch m.queueMode {
	case queueModeSelecting:
		items := m.chatQueue.Items()
		// header + up to four rows + hint; keep the input usable on short terminals.
		return minInt(queuePanelMaxHeight, 1+2+len(items))
	case queueModeEditing:
		return 1 + 1
	default:
		return 0
	}
}

func (m appModel) queueInlineSummaryHeight() int {
	if m.selectionDock != nil || m.queueMode != queueModeInactive || m.chatQueue.Len() == 0 {
		return 0
	}
	return 1
}

func (m appModel) queuePanelContent(width int) string {
	if width <= 0 {
		return ""
	}
	items := m.chatQueue.Items()
	switch m.queueMode {
	case queueModeSelecting:
		selected := m.queueSelectedIndex()
		if selected < 0 {
			selected = 0
		}
		lines := []string{queueSelectingStyle.Render(fmt.Sprintf("⏳ %d 个任务排队中 · %d/%d", len(items), selected+1, len(items)))}
		visibleRows := maxInt(0, queuePanelMaxHeight-2)
		if visibleRows > len(items) {
			visibleRows = len(items)
		}
		start := 0
		if selected >= visibleRows {
			start = selected - visibleRows + 1
		}
		end := minInt(len(items), start+visibleRows)
		for i := start; i < end; i++ {
			prefix := "  "
			style := queueItemStyle
			if i == selected {
				// Render the selection marker and a full-width highlighted row. The
				// previous implementation styled only the visible text, which made
				// the active item easy to miss against the queue panel.
				prefix = "▸ "
				style = queueSelectedStyle
			}
			line := prefix + fmt.Sprintf("%d · %s", i+1, queueItemText(items[i], maxInt(1, width-4)))
			line = fitStyledCellLine(truncateStyledCellLine(line, width), width)
			lines = append(lines, style.Render(line))
		}
		lines = append(lines, queueHintStyle.Render(truncateStyledCellLine("↑/↓ 选择 · i 编辑 · d 删除 · c 清空 · alt/command+k/j 调整 · esc 退出", width)))
		return strings.Join(lines, "\n")
	case queueModeEditing:
		position := 1
		if m.queueEdit != nil {
			position = m.queueEdit.originalAt + 1
		}
		return queueEditStyle.Render(truncateStyledCellLine(fmt.Sprintf("✎ 编辑排队任务 #%d · Enter 保存到队尾 · Esc 取消", position), width))
	default:
		if len(items) == 0 {
			return ""
		}
		return queueSummaryStyle.Render(truncateStyledCellLine(fmt.Sprintf("⏳ %d 个任务排队中 · ↓ 查看", len(items)), width))
	}
}

func queueItemText(item queuedChatItem, width int) string {
	text := strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(item.Draft.Text), "\n", " ↵ "), "\r", "")
	imageCount := len(imageTokensInDraft(item.Draft))
	if imageCount > 0 {
		text = fmt.Sprintf("[image ×%d] %s", imageCount, text)
	}
	return truncateStyledCellLine(text, maxInt(1, width))
}

func (m appModel) renderQueuePanel(width, height int) string {
	if height <= 0 {
		return ""
	}
	return fitStyledRect(m.queuePanelContent(width), width, height)
}

func renderQueueInlineBottomBorder(view string, width int, summary, lineColor string) string {
	if width <= 0 {
		return ""
	}
	lines := strings.Split(summary, "\n")
	if len(lines) > 0 {
		summary = lines[0]
	}
	maxSummaryWidth := maxInt(1, width-2)
	summary = truncateStyledCellLine(summary, maxSummaryWidth)
	summaryWidth := terminalCellWidth(summary)
	leftWidth := maxInt(1, (width-summaryWidth)/2)
	if leftWidth+summaryWidth > width {
		leftWidth = maxInt(0, width-summaryWidth)
	}
	lineStyle := lipgloss.NewStyle()
	if lineColor != "" {
		lineStyle = lineStyle.Foreground(lipgloss.Color(lineColor))
	}
	line := lineStyle.Render(strings.Repeat("─", leftWidth)) + summary + lineStyle.Render(strings.Repeat("─", maxInt(0, width-leftWidth-summaryWidth)))
	viewLines := strings.Split(view, "\n")
	if len(viewLines) == 0 {
		return fitStyledRect(line, width, 1)
	}
	viewLines[len(viewLines)-1] = fitStyledCellLine(line, width)
	return strings.Join(viewLines, "\n")
}

var (
	queueSummaryStyle   = inputHintStyle.Copy().Bold(true)
	queueSelectingStyle = modeChatStyle.Copy().Bold(true)
	queueSelectedStyle  = selectedTranscriptLineStyle.Copy().Bold(true)
	queueItemStyle      = contextFreeStyle.Copy()
	queueEditStyle      = inputPromptStyle.Copy().Bold(true)
	queueHintStyle      = inputHintStyle.Copy()
)
