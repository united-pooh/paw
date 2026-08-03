package bubble

import (
	"fmt"
	"strings"
)

const queuePanelMaxHeight = 8

func (m appModel) queuePanelHeight() int {
	if m.chatQueue.Len() == 0 && m.queueMode != queueModeEditing {
		return 0
	}
	switch m.queueMode {
	case queueModeSelecting:
		items := m.chatQueue.Items()
		// header + up to four rows + hint; keep the input usable on short terminals.
		return minInt(queuePanelMaxHeight, 2+len(items))
	case queueModeEditing:
		return 1
	default:
		return 1
	}
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
				prefix = "› "
				style = queueSelectedStyle
			}
			line := prefix + fmt.Sprintf("%d · %s", i+1, queueItemText(items[i], maxInt(1, width-4)))
			lines = append(lines, style.Render(truncateDisplayWidth(line, width)))
		}
		lines = append(lines, queueHintStyle.Render(truncateDisplayWidth("↑/↓ 选择 · i 编辑 · d 删除 · c 清空 · alt/command+k/j 调整 · esc 退出", width)))
		return strings.Join(lines, "\n")
	case queueModeEditing:
		position := 1
		if m.queueEdit != nil {
			position = m.queueEdit.originalAt + 1
		}
		return queueEditStyle.Render(truncateDisplayWidth(fmt.Sprintf("✎ 编辑排队任务 #%d · Enter 保存到队尾 · Esc 取消", position), width))
	default:
		if len(items) == 0 {
			return ""
		}
		return queueSummaryStyle.Render(truncateDisplayWidth(fmt.Sprintf("⏳ %d 个任务排队中 · ↓ 查看", len(items)), width))
	}
}

func queueItemText(item queuedChatItem, width int) string {
	text := strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(item.Draft.Text), "\n", " ↵ "), "\r", "")
	imageCount := len(imageTokensInDraft(item.Draft))
	if imageCount > 0 {
		text = fmt.Sprintf("[image ×%d] %s", imageCount, text)
	}
	return truncateDisplayWidth(text, maxInt(1, width))
}

func (m appModel) renderQueuePanel(width, height int) string {
	if height <= 0 {
		return ""
	}
	return fitStyledRect(m.queuePanelContent(width), width, height)
}

var (
	queueSummaryStyle   = inputHintStyle.Copy().Bold(true)
	queueSelectingStyle = modeChatStyle.Copy().Bold(true)
	queueSelectedStyle  = selectedTranscriptLineStyle.Copy().Bold(true)
	queueItemStyle      = contextFreeStyle.Copy()
	queueEditStyle      = inputPromptStyle.Copy().Bold(true)
	queueHintStyle      = inputHintStyle.Copy()
)
