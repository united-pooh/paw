// 本文件定义聊天历史 transcript 的追加、刷新和逐条渲染逻辑。
package bubble

import (
	"github.com/charmbracelet/lipgloss"
	"strings"
	"time"
)

const toolExpandDuration = 650 * time.Millisecond

// appendAssistantDelta 将模型流式增量追加到当前 assistant 消息，必要时新建消息。
func (m *appModel) appendAssistantDelta(delta string) {
	if delta == "" {
		return
	}
	if m.activeAssistant < 0 || m.activeAssistant >= len(m.transcript) || m.transcript[m.activeAssistant].kind != entryAssistant {
		m.transcript = append(m.transcript, transcriptEntry{
			kind:      entryAssistant,
			title:     "assistant",
			createdAt: m.animationNow(),
		})
		m.activeAssistant = len(m.transcript) - 1
	}
	m.transcript[m.activeAssistant].body += delta
	m.refreshViewport()
}

// appendThinkingDelta 将模型 thinking 增量追加到 transcript，渲染时由 showThinking 控制显隐。
func (m *appModel) appendThinkingDelta(delta string) {
	if delta == "" {
		return
	}
	m.activeAssistant = -1
	if len(m.transcript) == 0 || m.transcript[len(m.transcript)-1].kind != entryThinking {
		m.transcript = append(m.transcript, transcriptEntry{
			kind:      entryThinking,
			title:     "thinking",
			createdAt: m.animationNow(),
		})
	}
	m.transcript[len(m.transcript)-1].body += delta
	m.refreshViewport()
}

// addEntry 追加一条 transcript 记录并刷新滚动区。
func (m *appModel) addEntry(entry transcriptEntry) {
	if entry.createdAt.IsZero() {
		entry.createdAt = m.animationNow()
	}
	m.transcript = append(m.transcript, entry)
	m.refreshViewport()
}

func (m appModel) animationNow() time.Time {
	if !m.cursorFrameAt.IsZero() {
		return m.cursorFrameAt
	}
	return time.Now()
}

// refreshViewport 将 transcript 重新渲染到 viewport，并滚动到底部。
func (m *appModel) refreshViewport() {
	content := m.renderTranscriptContent()
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
	content := m.renderTranscriptContent()
	if m.selectionActive {
		content = m.renderTranscriptSelection(content)
	}
	m.viewport.SetContent(content)
	m.viewport.SetYOffset(offset)
}

func (m appModel) renderTranscriptContent() string {
	return renderTranscriptAt(m.transcript, maxInt(20, m.viewport.Width), m.showThinking, m.animationNow())
}

func (m appModel) hasActiveTranscriptAnimation() bool {
	now := m.animationNow()
	for _, entry := range m.transcript {
		if entry.kind == entryTool && !entry.createdAt.IsZero() && now.Sub(entry.createdAt) < toolExpandDuration {
			return true
		}
	}
	return false
}

// renderTranscript 把多条 transcript 记录渲染为 viewport 内容。
func renderTranscript(entries []transcriptEntry, width int, showThinking bool) string {
	return renderTranscriptAt(entries, width, showThinking, time.Time{})
}

func renderTranscriptAt(entries []transcriptEntry, width int, showThinking bool, at time.Time) string {
	if len(entries) == 0 {
		return ""
	}
	parts := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.kind == entryThinking && !showThinking {
			continue
		}
		parts = append(parts, renderEntryAt(entry, width, at))
	}
	return strings.Join(parts, "\n\n")
}

// renderEntry 渲染一条带标签和统一缩进的 transcript 记录。
func renderEntry(entry transcriptEntry, width int) string {
	return renderEntryAt(entry, width, time.Time{})
}

func renderEntryAt(entry transcriptEntry, width int, at time.Time) string {
	const entryGutter = "  "
	label := labelStyle(entry.kind).Render(entry.title)
	bodyWidth := maxInt(20, width-lipgloss.Width(entryGutter))
	body := renderEntryBodyAt(entry, bodyWidth, at)
	if body == "" {
		return entryGutter + label
	}
	return indentLines(label+"\n"+body, entryGutter)
}

// renderEntryBody 按消息类型渲染正文，assistant 消息会走 Markdown 渲染。
func renderEntryBody(entry transcriptEntry, width int) string {
	return renderEntryBodyAt(entry, width, time.Time{})
}

func renderEntryBodyAt(entry transcriptEntry, width int, at time.Time) string {
	body := strings.TrimRight(entry.body, "\n")
	if body == "" {
		return ""
	}
	if entry.kind == entryThinking {
		return thinkingBodyStyle.Width(width).Render(body)
	}
	if entry.kind == entryAssistant {
		return renderMarkdown(body, width)
	}
	if entry.kind == entryTool {
		isError := entry.isError
		isResult := entry.title == "result"
		prog := toolExpandProgress(entry, at)
		switch {
		case isError:
			return renderToolEntryBodyWithStyle(entry.body, width, prog, toolErrorBorderStyle)
		case isResult:
			return renderToolEntryBodyWithStyle(entry.body, width, prog, toolResultBorderStyle)
		default:
			return renderToolEntryBody(entry.body, width, prog)
		}
	}
	return bodyStyle.Width(width).Render(body)
}

// renderToolEntryBodyWithStyle renders a tool entry in blockquote style using the given border style.
func renderToolEntryBodyWithStyle(body string, width int, progress float64, borderStyle lipgloss.Style) string {
	body = strings.TrimRight(body, "\n")
	summary, detail := splitToolSummary(body)
	header := toolHeaderStyle.Render("▾ " + summary)

	if detail == "" || progress <= 0 {
		return borderStyle.Width(width - 3).Render(header)
	}

	detailLines := strings.Split(detail, "\n")
	visibleLines := maxInt(1, int(float64(len(detailLines))*progress+0.999))
	if visibleLines > len(detailLines) {
		visibleLines = len(detailLines)
	}
	prefixed := make([]string, visibleLines)
	for i, l := range detailLines[:visibleLines] {
		prefixed[i] = "> " + l
	}
	content := header + "\n" + toolDetailStyle.Width(width-4).Render(strings.Join(prefixed, "\n"))
	return borderStyle.Width(width - 3).Render(content)
}

// renderToolEntryBody renders a tool call entry with the default orange border.
// Called from renderEntryBodyAt for the "tool" title.
func renderToolEntryBody(body string, width int, progress float64) string {
	return renderToolEntryBodyWithStyle(body, width, progress, toolCallBorderStyle)
}

func splitToolSummary(body string) (string, string) {
	lines := strings.Split(strings.TrimRight(body, "\n"), "\n")
	if len(lines) == 0 {
		return "", ""
	}
	summary := strings.TrimSpace(lines[0])
	if len(lines) == 1 {
		return summary, ""
	}
	return summary, strings.Join(lines[1:], "\n")
}

func toolExpandProgress(entry transcriptEntry, at time.Time) float64 {
	if at.IsZero() || entry.createdAt.IsZero() {
		return 1
	}
	elapsed := at.Sub(entry.createdAt)
	if elapsed <= 0 {
		return 0
	}
	if elapsed >= toolExpandDuration {
		return 1
	}
	return easeOutCubic(float64(elapsed) / float64(toolExpandDuration))
}

// labelStyle 根据消息类型选择左侧标签样式。
func labelStyle(kind entryKind) lipgloss.Style {
	switch kind {
	case entryUser:
		return labelUserStyle
	case entryAssistant:
		return labelAssistantStyle
	case entryThinking:
		return labelThinkingStyle
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
