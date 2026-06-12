// 本文件定义聊天历史 transcript 的追加、刷新和逐条渲染逻辑。
package bubble

import (
	"github.com/charmbracelet/lipgloss"
	"strings"
)

// appendAssistantDelta 将模型流式增量追加到当前 assistant 消息，必要时新建消息。
func (m *appModel) appendAssistantDelta(delta string) {
	if delta == "" {
		return
	}
	if m.activeAssistant < 0 || m.activeAssistant >= len(m.transcript) || m.transcript[m.activeAssistant].kind != entryAssistant {
		m.transcript = append(m.transcript, transcriptEntry{
			kind:  entryAssistant,
			title: "assistant",
		})
		m.activeAssistant = len(m.transcript) - 1
	}
	m.transcript[m.activeAssistant].body += delta
	m.refreshViewport()
}

// addEntry 追加一条 transcript 记录并刷新滚动区。
func (m *appModel) addEntry(entry transcriptEntry) {
	m.transcript = append(m.transcript, entry)
	m.refreshViewport()
}

// refreshViewport 将 transcript 重新渲染到 viewport，并滚动到底部。
func (m *appModel) refreshViewport() {
	content := renderTranscript(m.transcript, maxInt(20, m.viewport.Width))
	if m.selectionActive {
		content = m.renderTranscriptSelection(content)
	}
	m.viewport.SetContent(content)
	if !m.selectionActive {
		m.viewport.GotoBottom()
	}
}

// refreshViewportPreservingOffset 刷新 transcript 内容，但保留用户当前滚动位置。
func (m *appModel) refreshViewportPreservingOffset() {
	offset := m.viewport.YOffset
	content := renderTranscript(m.transcript, maxInt(20, m.viewport.Width))
	if m.selectionActive {
		content = m.renderTranscriptSelection(content)
	}
	m.viewport.SetContent(content)
	m.viewport.SetYOffset(offset)
}

// renderTranscript 把多条 transcript 记录渲染为 viewport 内容。
func renderTranscript(entries []transcriptEntry, width int) string {
	if len(entries) == 0 {
		return ""
	}
	parts := make([]string, 0, len(entries))
	for _, entry := range entries {
		parts = append(parts, renderEntry(entry, width))
	}
	return strings.Join(parts, "\n\n")
}

// renderEntry 渲染一条带标签和统一缩进的 transcript 记录。
func renderEntry(entry transcriptEntry, width int) string {
	const entryGutter = "  "
	label := labelStyle(entry.kind).Render(entry.title)
	bodyWidth := maxInt(20, width-lipgloss.Width(entryGutter))
	body := renderEntryBody(entry, bodyWidth)
	if body == "" {
		return entryGutter + label
	}
	return indentLines(label+"\n"+body, entryGutter)
}

// renderEntryBody 按消息类型渲染正文，assistant 消息会走 Markdown 渲染。
func renderEntryBody(entry transcriptEntry, width int) string {
	body := strings.TrimRight(entry.body, "\n")
	if body == "" {
		return ""
	}
	if entry.kind == entryAssistant {
		return renderMarkdown(body, width)
	}
	return bodyStyle.Width(width).Render(body)
}

// labelStyle 根据消息类型选择左侧标签样式。
func labelStyle(kind entryKind) lipgloss.Style {
	switch kind {
	case entryUser:
		return labelUserStyle
	case entryAssistant:
		return labelAssistantStyle
	case entryTool:
		return labelToolStyle
	case entryError:
		return labelErrorStyle
	default:
		return labelSystemStyle
	}
}

// indentLines 给多行文本逐行添加固定前缀。
func indentLines(text, prefix string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = prefix + line
	}
	return strings.Join(lines, "\n")
}
