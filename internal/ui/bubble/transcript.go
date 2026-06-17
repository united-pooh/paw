// 本文件定义聊天历史 transcript 的追加、刷新和逐条渲染逻辑。
package bubble

import (
	"encoding/json"
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
			citations: m.consumePendingToolCitations(),
			createdAt: m.animationNow(),
		})
		m.activeAssistant = len(m.transcript) - 1
	} else if len(m.pendingToolCites) > 0 {
		m.transcript[m.activeAssistant].citations = append(m.transcript[m.activeAssistant].citations, m.consumePendingToolCitations()...)
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

func (m *appModel) consumePendingToolCitations() []toolCitation {
	if len(m.pendingToolCites) == 0 {
		return nil
	}
	cites := append([]toolCitation(nil), m.pendingToolCites...)
	m.pendingToolCites = nil
	return cites
}

func (m *appModel) recordToolCallCitation(toolUseID, name string, input json.RawMessage) {
	cite := newToolCallCitation(toolUseID, name, input)
	if idx := m.lastAssistantCitationHostIndex(); idx >= 0 {
		m.transcript[idx].citations = append(m.transcript[idx].citations, cite)
	} else {
		m.transcript = append(m.transcript, transcriptEntry{
			kind:      entryAssistant,
			title:     "assistant",
			citations: []toolCitation{cite},
			createdAt: m.animationNow(),
		})
	}
	m.refreshViewport()
}

func (m *appModel) recordToolCallEntry(toolUseID, name string, input json.RawMessage) {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "tool"
	}
	m.addEntry(transcriptEntry{
		kind:      entryTool,
		title:     "tool",
		body:      formatRunningToolCallBody(name, input),
		toolUseID: strings.TrimSpace(toolUseID),
		toolName:  name,
		createdAt: m.animationNow(),
	})
}

func (m *appModel) recordToolResultEntry(toolUseID, name, status string, isError bool) {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "tool"
	}
	status = strings.TrimSpace(status)
	if status == "" {
		status = "ok"
	}
	result := toolEntryResult{
		toolUseID: strings.TrimSpace(toolUseID),
		name:      name,
		status:    status,
		isError:   isError,
	}
	for idx := len(m.transcript) - 1; idx >= 0; idx-- {
		entry := &m.transcript[idx]
		if !toolEntryMatchesResult(*entry, result) {
			continue
		}
		entry.title = "tool"
		entry.body = completeRunningToolCallBody(entry.body, status)
		entry.isError = isError
		m.refreshViewport()
		return
	}
	m.addEntry(transcriptEntry{
		kind:      entryTool,
		title:     "tool",
		body:      formatToolResultBody(name, status, ""),
		isError:   isError,
		toolUseID: strings.TrimSpace(toolUseID),
		toolName:  name,
		createdAt: m.animationNow(),
	})
}

func (m *appModel) recordToolResultCitation(toolUseID, name, status, content string, isError bool) {
	cite := newToolResultCitation(toolUseID, name, status, content, isError)
	for entryIdx := len(m.transcript) - 1; entryIdx >= 0; entryIdx-- {
		entry := &m.transcript[entryIdx]
		if entry.kind == entryUser {
			break
		}
		if entry.kind != entryAssistant {
			continue
		}
		for citeIdx := len(entry.citations) - 1; citeIdx >= 0; citeIdx-- {
			if toolCitationMatchesResult(entry.citations[citeIdx], cite) {
				entry.citations[citeIdx].status = cite.status
				entry.citations[citeIdx].preview = cite.preview
				entry.citations[citeIdx].isError = cite.isError
				m.refreshViewport()
				return
			}
		}
	}
	if idx := m.lastAssistantCitationHostIndex(); idx >= 0 {
		m.transcript[idx].citations = append(m.transcript[idx].citations, cite)
	} else {
		m.transcript = append(m.transcript, transcriptEntry{
			kind:      entryAssistant,
			title:     "assistant",
			citations: []toolCitation{cite},
			createdAt: m.animationNow(),
		})
	}
	m.refreshViewport()
}

type toolEntryResult struct {
	toolUseID string
	name      string
	status    string
	isError   bool
}

func toolEntryMatchesResult(entry transcriptEntry, result toolEntryResult) bool {
	if entry.kind != entryTool || entry.title != "tool" {
		return false
	}
	if !strings.Contains(firstToolEntryLine(entry.body), "running") {
		return false
	}
	if entry.toolUseID != "" || result.toolUseID != "" {
		return entry.toolUseID != "" && entry.toolUseID == result.toolUseID
	}
	return strings.EqualFold(entry.toolName, result.name)
}

func toolCitationMatchesResult(running, result toolCitation) bool {
	if running.status != "running" {
		return false
	}
	if running.toolUseID != "" || result.toolUseID != "" {
		return running.toolUseID != "" && running.toolUseID == result.toolUseID
	}
	return strings.EqualFold(running.name, result.name)
}

func (m appModel) lastAssistantCitationHostIndex() int {
	for i := len(m.transcript) - 1; i >= 0; i-- {
		switch m.transcript[i].kind {
		case entryAssistant:
			return i
		case entryUser:
			return -1
		}
	}
	return -1
}

func assistantEntryIsRenderable(entry transcriptEntry) bool {
	if entry.kind != entryAssistant {
		return true
	}
	if sanitizeAssistantVisibleBody(entry.body) != "" {
		return true
	}
	for _, cite := range entry.citations {
		if strings.TrimSpace(cite.name) != "" {
			return true
		}
	}
	return false
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
		if !assistantEntryIsRenderable(entry) {
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
	if entry.kind == entryTool {
		if body == "" {
			return ""
		}
		return indentLines(body, entryGutter)
	}
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
	if entry.kind == entryAssistant {
		body = sanitizeAssistantVisibleBody(body)
		return renderAssistantBodyWithCitations(body, width, entry.citations)
	}
	if body == "" {
		return ""
	}
	if entry.kind == entryThinking {
		return thinkingBodyStyle.Width(width).Render(body)
	}
	if entry.kind == entryTool {
		isError := entry.isError
		isResult := toolEntryHasCompletedStatus(entry)
		prog := toolExpandProgress(entry, at)
		switch {
		case isError:
			return renderToolEntryBodyWithMarker(entry.body, width, prog, toolErrorBorderStyle, "!")
		case isResult:
			return renderToolEntryBodyWithMarker(entry.body, width, prog, toolResultBorderStyle, "✓")
		default:
			return renderToolEntryBody(entry.body, width, prog)
		}
	}
	return bodyStyle.Width(width).Render(body)
}

func toolEntryHasCompletedStatus(entry transcriptEntry) bool {
	line := firstToolEntryLine(entry.body)
	parts := splitToolSummaryParts(line)
	return len(parts) >= 2 && strings.EqualFold(parts[1], "ok")
}

func renderAssistantBodyWithCitations(body string, width int, citations []toolCitation) string {
	citations = compactToolCitations(citations)
	rendered := renderMarkdown(body, width)
	if len(citations) == 0 {
		return rendered
	}
	quote := renderToolCitationQuote(citations, width)
	if quote == "" {
		return rendered
	}
	if rendered == "" {
		return quote
	}
	return rendered + "\n" + quote
}

func newToolCallCitation(toolUseID, name string, input json.RawMessage) toolCitation {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "tool"
	}
	fields := toolInputFields(input)
	return toolCitation{
		toolUseID: strings.TrimSpace(toolUseID),
		name:      name,
		target:    primaryToolInput(name, fields),
		status:    "running",
	}
}

func newToolResultCitation(toolUseID, name, status, content string, isError bool) toolCitation {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "tool"
	}
	status = strings.TrimSpace(status)
	if status == "" {
		status = "ok"
	}
	return toolCitation{
		toolUseID: strings.TrimSpace(toolUseID),
		name:      name,
		status:    status,
		preview:   summarizeToolContent(content),
		isError:   isError,
	}
}

func compactToolCitations(citations []toolCitation) []toolCitation {
	out := make([]toolCitation, 0, len(citations))
	for _, cite := range citations {
		cite.name = strings.TrimSpace(cite.name)
		cite.target = strings.TrimSpace(cite.target)
		cite.status = strings.TrimSpace(cite.status)
		cite.preview = strings.TrimSpace(cite.preview)
		if cite.name == "" {
			continue
		}
		if cite.status == "" {
			cite.status = "ok"
		}
		out = append(out, cite)
	}
	return out
}

func renderToolCitationQuote(citations []toolCitation, width int) string {
	lines := make([]string, 0, len(citations))
	contentWidth := maxInt(1, width-2)
	for _, cite := range citations {
		if line := renderToolCitationQuoteLine(cite, contentWidth); line != "" {
			lines = append(lines, toolCitationQuoteBorderStyle.Render("│")+" "+line)
		}
	}
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n")
}

func renderToolCitationQuoteLine(cite toolCitation, width int) string {
	name := strings.TrimSpace(cite.name)
	if name == "" {
		return ""
	}
	key := toolCitationKeyStyle.Render("[" + name + "]")
	status := toolCitationStatusStyle(cite).Render(toolCitationStatusText(cite))
	target := strings.TrimSpace(cite.target)
	lineWidth := lipgloss.Width("["+name+"] ") + lipgloss.Width(toolCitationStatusText(cite))
	if target == "" {
		return key + " " + status
	}
	targetWidth := maxInt(1, width-lineWidth-lipgloss.Width(" · "))
	return key + " " + status + toolCitationStyle.Render(" · "+truncateDisplayWidth(target, targetWidth))
}

func toolCitationStatusText(cite toolCitation) string {
	switch {
	case cite.isError || strings.EqualFold(cite.status, "error"):
		return "error"
	case strings.EqualFold(cite.status, "running"):
		return "running"
	default:
		return "ok"
	}
}

func toolCitationStatusStyle(cite toolCitation) lipgloss.Style {
	if cite.isError || strings.EqualFold(cite.status, "error") {
		return toolCitationErrorStyle
	}
	if strings.EqualFold(cite.status, "running") {
		return toolCitationStyle
	}
	return toolCitationOKStyle
}

// renderToolEntryBodyWithStyle renders a tool entry in blockquote style using the given border style.
func renderToolEntryBodyWithStyle(body string, width int, progress float64, borderStyle lipgloss.Style) string {
	return renderToolEntryBodyWithMarker(body, width, progress, borderStyle, "▸")
}

func renderToolEntryBodyWithMarker(body string, width int, progress float64, borderStyle lipgloss.Style, marker string) string {
	body = strings.TrimRight(body, "\n")
	summary, detail := splitToolSummary(body)
	contentWidth := toolEntryContentWidth(width, borderStyle)
	headerStyle := toolHeaderStyle
	switch marker {
	case "✓":
		headerStyle = lipgloss.NewStyle().Foreground(colorManager.LipglossColor(colorLabelResult)).Bold(true)
	case "!":
		headerStyle = lipgloss.NewStyle().Foreground(colorManager.LipglossColor(colorLabelError)).Bold(true)
	}
	header := headerStyle.Render(marker + " " + summary)

	if detail == "" || progress <= 0 {
		return borderStyle.Width(contentWidth).Render(header)
	}

	detailLines := strings.Split(detail, "\n")
	visibleLines := maxInt(1, int(float64(len(detailLines))*progress+0.999))
	if visibleLines > len(detailLines) {
		visibleLines = len(detailLines)
	}
	content := header + "\n" + renderToolDetailLines(detailLines[:visibleLines], contentWidth)
	return borderStyle.Width(contentWidth).Render(content)
}

func toolEntryContentWidth(width int, borderStyle lipgloss.Style) int {
	return maxInt(1, width-borderStyle.GetHorizontalFrameSize()-toolEntryNoWrapSafetyCells)
}

const toolEntryNoWrapSafetyCells = transcriptPanelHorizontalFrame + 1

func renderToolDetailLines(lines []string, width int) string {
	diffRowWidth := diffDetailRowsWidth(lines, width)
	rendered := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		style := toolDetailStyle
		renderedLine := "  " + trimmed
		isDiffLine := false
		switch {
		case strings.HasPrefix(trimmed, "@@"), strings.HasPrefix(trimmed, "---"), strings.HasPrefix(trimmed, "+++"):
			style = toolCitationStyle
		case diffDetailLineMarker(trimmed) == "+":
			style = lipgloss.NewStyle().
				Foreground(lipgloss.Color("194")).
				Background(lipgloss.Color("22")).
				Bold(true)
			renderedLine = trimmed
			isDiffLine = true
		case diffDetailLineMarker(trimmed) == "-":
			style = lipgloss.NewStyle().
				Foreground(lipgloss.Color("224")).
				Background(lipgloss.Color("52")).
				Bold(true)
			renderedLine = trimmed
			isDiffLine = true
		}
		if isDiffLine {
			renderedLine = padDisplayWidth(truncateDisplayWidth(renderedLine, diffRowWidth), diffRowWidth)
			rendered = append(rendered, style.Render(renderedLine))
			continue
		}
		renderedLine = truncateDisplayWidth(renderedLine, width)
		rendered = append(rendered, style.Width(width).Render(renderedLine))
	}
	return strings.Join(rendered, "\n")
}

func diffDetailRowsWidth(lines []string, width int) int {
	maxAllowed := maxInt(1, width-2)
	maxLineWidth := 0
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if diffDetailLineMarker(trimmed) == "" {
			continue
		}
		maxLineWidth = maxInt(maxLineWidth, lipgloss.Width(truncateDisplayWidth(trimmed, maxAllowed)))
	}
	if maxLineWidth == 0 {
		return maxAllowed
	}
	return minInt(maxAllowed, maxLineWidth)
}

func diffDetailLineMarker(line string) string {
	if strings.HasPrefix(line, "+") {
		return "+"
	}
	if strings.HasPrefix(line, "-") {
		return "-"
	}
	fields := strings.Fields(line)
	if len(fields) < 2 || !isDecimalNumber(fields[0]) {
		return ""
	}
	switch fields[1] {
	case "+", "-":
		return fields[1]
	default:
		return ""
	}
}

func isDecimalNumber(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
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

// turnsCount 返回当前会话的用户消息轮次数。
func (m appModel) turnsCount() int {
	n := 0
	for _, e := range m.transcript {
		if e.kind == entryUser && e.title == "you" {
			n++
		}
	}
	return n
}

// indentLines 给多行文本逐行添加固定前缀。
func indentLines(text, prefix string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = prefix + line
	}
	return strings.Join(lines, "\n")
}
