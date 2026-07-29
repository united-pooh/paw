// 本文件定义聊天历史 transcript 的追加、刷新和逐条渲染逻辑。
package bubble

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

const transcriptStreamingRefreshInterval = cursorFrameInterval
const maxRenderedToolDetailLines = 12
const transcriptEntryGutter = "  "

type transcriptRenderCacheEntry struct {
	key      transcriptRenderCacheKey
	rendered string
}

type transcriptRenderCacheKey struct {
	kind                 entryKind
	renderMode           transcriptRenderMode
	title                string
	body                 string
	inputTokens          string
	color                string
	isError              bool
	toolUseID            string
	toolName             string
	toolStatus           string
	toolTarget           string
	toolResult           string
	toolResultLen        int
	toolExpanded         bool
	toolFocused          bool
	toolHovered          bool
	toolResultOnly       bool
	citations            string
	width                int
	version              int
	bodyLen              int
	citationCount        int
	createdAtUnixNS      int64
	createdAtIsZero      bool
	toolStartedAtUnixNS  int64
	toolFinishedAtUnixNS int64
	toolElapsedSecond    int64
}

func (m *appModel) ensureAssistantStreamEntry() {
	if m.activeAssistant < 0 || m.activeAssistant >= len(m.transcript) || m.transcript[m.activeAssistant].kind != entryAssistant {
		m.transcript = append(m.transcript, transcriptEntry{
			kind:       entryAssistant,
			title:      "assistant",
			citations:  m.consumePendingToolCitations(),
			createdAt:  m.animationNow(),
			renderMode: transcriptRenderFormatted,
		})
		m.activeAssistant = len(m.transcript) - 1
	} else if len(m.pendingToolCites) > 0 {
		m.transcript[m.activeAssistant].citations = append(m.transcript[m.activeAssistant].citations, m.consumePendingToolCitations()...)
	}
	if m.transcript[m.activeAssistant].renderMode != transcriptRenderFormatted {
		m.transcript[m.activeAssistant].renderMode = transcriptRenderFormatted
		touchTranscriptEntry(&m.transcript[m.activeAssistant])
	}
}

// appendAssistantDelta 将经过流隔离器确认的稳定文本逐行追加到当前 assistant 消息。
func (m *appModel) appendAssistantDelta(delta string) {
	if delta == "" {
		return
	}
	for _, line := range strings.SplitAfter(delta, "\n") {
		m.ensureAssistantStreamEntry()
		m.transcript[m.activeAssistant].body += line
		touchTranscriptEntry(&m.transcript[m.activeAssistant])
		m.recordAssistantActivity(m.activeAssistant)
		m.refreshViewport()
	}
}

func (m *appModel) ensureThinkingStreamEntry() {
	m.activeAssistant = -1
	if m.activeThinking < 0 || m.activeThinking >= len(m.transcript) || m.transcript[m.activeThinking].kind != entryThinking {
		m.transcript = append(m.transcript, transcriptEntry{
			kind:       entryThinking,
			title:      "thinking",
			createdAt:  m.animationNow(),
			renderMode: transcriptRenderPlain,
		})
		m.activeThinking = len(m.transcript) - 1
	}
}

// appendThinkingDelta 将经过流隔离器确认的稳定 thinking 文本追加到 transcript。
func (m *appModel) appendThinkingDelta(delta string) {
	if delta == "" {
		return
	}
	m.ensureThinkingStreamEntry()
	m.transcript[m.activeThinking].body += delta
	touchTranscriptEntry(&m.transcript[m.activeThinking])
	m.refreshViewportForStreaming()
}

func (m *appModel) consumeAssistantStreamDelta(delta string) {
	if delta == "" {
		return
	}
	if m.thinkingStream.HasContent() {
		m.finalizeThinkingStream()
	}
	committed := m.assistantStream.Push(delta, m.streamingBodyWidth())
	if m.assistantStream.HasContent() {
		m.ensureAssistantStreamEntry()
	}
	m.appendAssistantDelta(committed)
}

func (m *appModel) consumeThinkingStreamDelta(delta string) {
	if delta == "" {
		return
	}
	if m.assistantStream.HasContent() {
		m.finalizeAssistantStream(transcriptRenderFormatted)
	}
	committed := m.thinkingStream.Push(delta, m.streamingBodyWidth())
	if m.thinkingStream.HasContent() {
		m.ensureThinkingStreamEntry()
	}
	m.appendThinkingDelta(committed)
}

func (m *appModel) finalizeAssistantStream(mode transcriptRenderMode) int {
	hadContent := m.assistantStream.HasContent()
	committed := m.assistantStream.Flush(m.streamingBodyWidth())
	if hadContent {
		m.ensureAssistantStreamEntry()
	}
	m.appendAssistantDelta(committed)
	finalized := m.activeAssistant
	m.setAssistantRenderMode(finalized, mode)
	m.activeAssistant = -1
	return finalized
}

func (m *appModel) setAssistantRenderMode(index int, mode transcriptRenderMode) {
	if index < 0 || index >= len(m.transcript) || m.transcript[index].kind != entryAssistant {
		return
	}
	if m.transcript[index].renderMode != mode {
		m.transcript[index].renderMode = mode
		touchTranscriptEntry(&m.transcript[index])
		m.refreshViewport()
	}
}

func (m *appModel) finalizeThinkingStream() {
	hadContent := m.thinkingStream.HasContent()
	thinkingIndex := m.activeThinking
	if hadContent {
		m.recordTranscriptEntryActivity(thinkingIndex, true)
	}
	committed := m.thinkingStream.Flush(m.streamingBodyWidth())
	if hadContent {
		m.ensureThinkingStreamEntry()
	}
	m.appendThinkingDelta(committed)
	m.activeThinking = -1
}

func (m *appModel) resizeStreamingBuffers() {
	width := m.streamingBodyWidth()
	if committed := m.assistantStream.Resize(width); committed != "" {
		m.ensureAssistantStreamEntry()
		m.appendAssistantDelta(committed)
	}
	if committed := m.thinkingStream.Resize(width); committed != "" {
		m.ensureThinkingStreamEntry()
		m.appendThinkingDelta(committed)
	}
}

func (m *appModel) resetStreamingBuffers() {
	m.assistantStream.Reset()
	m.thinkingStream.Reset()
	m.activeAssistant = -1
	m.activeThinking = -1
	m.doneAssistant = -1
}

func (m appModel) streamingBodyWidth() int {
	return transcriptBodyWidth(maxInt(20, m.viewport.Width))
}

// addEntry 追加一条 transcript 记录并刷新滚动区。
func (m *appModel) addEntry(entry transcriptEntry) {
	if entry.createdAt.IsZero() {
		entry.createdAt = m.animationNow()
	}
	touchTranscriptEntry(&entry)
	m.transcript = append(m.transcript, entry)
	index := len(m.transcript) - 1
	if entry.kind != entryUser && (entry.kind != entryTool || entry.toolStatus != "running") {
		m.recordTranscriptEntryActivity(index, true)
	}
	m.refreshViewport()
}

func (m appModel) animationNow() time.Time {
	if !m.cursorFrameAt.IsZero() {
		return m.cursorFrameAt
	}
	return time.Now()
}

func touchTranscriptEntry(entry *transcriptEntry) {
	if entry == nil {
		return
	}
	entry.version++
	if entry.version <= 0 {
		entry.version = 1
	}
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
		touchTranscriptEntry(&m.transcript[idx])
	} else {
		entry := transcriptEntry{
			kind:      entryAssistant,
			title:     "assistant",
			citations: []toolCitation{cite},
			createdAt: m.animationNow(),
		}
		touchTranscriptEntry(&entry)
		m.transcript = append(m.transcript, entry)
	}
	m.refreshViewport()
}

func (m *appModel) recordToolCallEntry(toolUseID, name string, input json.RawMessage, oldContent string) {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "tool"
	}
	m.addEntry(transcriptEntry{
		kind:          entryTool,
		title:         "tool",
		body:          formatRunningToolCallBody(name, input, oldContent),
		toolUseID:     strings.TrimSpace(toolUseID),
		toolName:      name,
		toolStatus:    "running",
		toolTarget:    toolSummaryTarget(name, input),
		createdAt:     m.animationNow(),
		toolStartedAt: m.animationNow(),
	})
}

func (m *appModel) recordToolResultEntry(toolUseID, name, status, content string, isError bool) {
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
		entry.toolStatus = status
		entry.toolResult = content
		entry.toolExpanded = isError
		entry.toolResultOnly = false
		entry.toolFinishedAt = m.animationNow()
		touchTranscriptEntry(entry)
		m.recordTranscriptEntryActivity(idx, true)
		m.refreshViewport()
		return
	}
	m.addEntry(transcriptEntry{
		kind:           entryTool,
		title:          "tool",
		body:           formatToolResultBody(name, status, ""),
		isError:        isError,
		toolUseID:      strings.TrimSpace(toolUseID),
		toolName:       name,
		toolStatus:     status,
		toolResult:     content,
		toolExpanded:   isError,
		toolResultOnly: true,
		createdAt:      m.animationNow(),
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
				touchTranscriptEntry(entry)
				m.refreshViewport()
				return
			}
		}
	}
	if idx := m.lastAssistantCitationHostIndex(); idx >= 0 {
		m.transcript[idx].citations = append(m.transcript[idx].citations, cite)
		touchTranscriptEntry(&m.transcript[idx])
	} else {
		entry := transcriptEntry{
			kind:      entryAssistant,
			title:     "assistant",
			citations: []toolCitation{cite},
			createdAt: m.animationNow(),
		}
		touchTranscriptEntry(&entry)
		m.transcript = append(m.transcript, entry)
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
	if !isToolTransaction(entry) || toolEntryStatus(entry) != "running" {
		return false
	}
	if entry.toolUseID != "" && result.toolUseID != "" {
		return entry.toolUseID == result.toolUseID
	}
	return strings.EqualFold(entry.toolName, result.name)
}

func toolCitationMatchesResult(running, result toolCitation) bool {
	if running.status != "running" {
		return false
	}
	if running.toolUseID != "" && result.toolUseID != "" {
		return running.toolUseID == result.toolUseID
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
	if sanitizeAssistantVisibleBody(sanitizeTerminalText(entry.body)) != "" {
		return true
	}
	for _, cite := range entry.citations {
		if strings.TrimSpace(cite.name) != "" {
			return true
		}
	}
	return false
}

// refreshViewport 将 transcript 重新渲染到 viewport。
// 如果刷新前用户位于底部，则继续跟随新内容；否则保留手动滚动位置。
func (m *appModel) refreshViewport() {
	m.refreshViewportWithBottomState(!m.selectionActive && m.viewport.AtBottom())
}

func (m *appModel) refreshViewportWithBottomState(wasAtBottom bool) {
	offset := m.viewport.YOffset
	content := m.renderTranscriptContent()
	if m.selectionActive {
		content = m.renderTranscriptSelection(content)
	}
	m.viewport.SetContent(content)
	if wasAtBottom {
		m.viewport.GotoBottom()
	} else {
		m.viewport.SetYOffset(offset)
	}
	m.markTranscriptRefreshed()
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
	m.markTranscriptRefreshed()
}

func (m *appModel) refreshViewportForStreaming() {
	if m == nil {
		return
	}
	now := time.Now()
	if m.lastTranscriptRefreshAt.IsZero() || now.Sub(m.lastTranscriptRefreshAt) >= transcriptStreamingRefreshInterval {
		m.refreshViewport()
		return
	}
	m.transcriptRefreshPending = true
}

func (m *appModel) markTranscriptRefreshed() {
	if m == nil {
		return
	}
	m.transcriptRefreshPending = false
	m.lastTranscriptRefreshAt = time.Now()
}

func (m *appModel) refreshRunningToolProgress(now time.Time) {
	if m == nil {
		return
	}
	maxElapsed := int64(-1)
	for _, entry := range m.transcript {
		if !isToolTransaction(entry) || toolEntryStatus(entry) != "running" {
			continue
		}
		elapsed := toolElapsedSeconds(entry, now)
		if elapsed > maxElapsed {
			maxElapsed = elapsed
		}
	}
	if maxElapsed < 0 {
		m.lastToolProgressSecond = -1
		return
	}
	if maxElapsed == m.lastToolProgressSecond {
		return
	}
	m.lastToolProgressSecond = maxElapsed
	if m.viewport.AtBottom() {
		m.refreshViewport()
	} else {
		m.refreshViewportPreservingOffset()
	}
}

func (m *appModel) markRunningToolsError(err error) {
	if m == nil {
		return
	}
	message := "tool failed"
	if err != nil && strings.TrimSpace(err.Error()) != "" {
		message = err.Error()
	}
	changed := false
	for index := range m.transcript {
		entry := &m.transcript[index]
		if !isToolTransaction(*entry) || toolEntryStatus(*entry) != "running" {
			continue
		}
		entry.body = completeRunningToolCallBody(entry.body, "error")
		entry.toolStatus = "error"
		entry.isError = true
		entry.toolResult = message
		entry.toolExpanded = true
		entry.toolFinishedAt = m.animationNow()
		touchTranscriptEntry(entry)
		changed = true
	}
	m.lastToolProgressSecond = -1
	if changed {
		m.refreshViewport()
	}
}

func (m *appModel) renderTranscriptContent() string {
	return m.renderTranscriptContentAt(maxInt(20, m.viewport.Width), m.showThinking, m.animationNow())
}

func (m *appModel) renderTranscriptContentAt(width int, showThinking bool, at time.Time) string {
	if len(m.transcript) == 0 {
		m.transcriptRenderCache = nil
		return ""
	}
	if len(m.transcriptRenderCache) != len(m.transcript) {
		next := make([]transcriptRenderCacheEntry, len(m.transcript))
		copy(next, m.transcriptRenderCache)
		m.transcriptRenderCache = next
	}
	var renderedTranscript strings.Builder
	hasPrevious := false
	var previousKind entryKind
	for idx, entry := range m.transcript {
		if entry.kind == entryThinking && !showThinking {
			continue
		}
		if !assistantEntryIsRenderable(entry) {
			continue
		}
		key := transcriptRenderKey(entry, width, at)
		if m.transcriptRenderCache[idx].key == key {
			appendTranscriptRenderedEntry(&renderedTranscript, m.transcriptRenderCache[idx].rendered, entry.kind, previousKind, hasPrevious)
			previousKind = entry.kind
			hasPrevious = true
			continue
		}
		rendered := renderEntryAt(entry, width, at)
		if rendered == "" {
			continue
		}
		m.transcriptRenderCache[idx] = transcriptRenderCacheEntry{key: key, rendered: rendered}
		appendTranscriptRenderedEntry(&renderedTranscript, rendered, entry.kind, previousKind, hasPrevious)
		previousKind = entry.kind
		hasPrevious = true
	}
	return renderedTranscript.String()
}

func transcriptRenderKey(entry transcriptEntry, width int, at time.Time) transcriptRenderCacheKey {
	key := transcriptRenderCacheKey{
		kind:                 entry.kind,
		renderMode:           entry.renderMode,
		title:                entry.title,
		inputTokens:          inputTokenSnapshot(entry.inputTokens),
		color:                entry.color,
		isError:              entry.isError,
		toolUseID:            entry.toolUseID,
		toolName:             entry.toolName,
		toolStatus:           entry.toolStatus,
		toolTarget:           entry.toolTarget,
		toolResultLen:        len(entry.toolResult),
		toolExpanded:         entry.toolExpanded,
		toolFocused:          entry.toolFocused,
		toolHovered:          entry.toolHovered,
		toolResultOnly:       entry.toolResultOnly,
		width:                width,
		version:              entry.version,
		bodyLen:              len(entry.body),
		citationCount:        len(entry.citations),
		createdAtUnixNS:      entry.createdAt.UnixNano(),
		createdAtIsZero:      entry.createdAt.IsZero(),
		toolStartedAtUnixNS:  entry.toolStartedAt.UnixNano(),
		toolFinishedAtUnixNS: entry.toolFinishedAt.UnixNano(),
	}
	if toolEntryStatus(entry) == "running" {
		key.toolElapsedSecond = toolElapsedSeconds(entry, at)
	}
	if entry.version == 0 {
		key.body = entry.body
		key.toolResult = entry.toolResult
		key.citations = transcriptCitationSnapshot(entry.citations)
	}
	return key
}

func toolElapsedSeconds(entry transcriptEntry, at time.Time) int64 {
	if entry.toolStartedAt.IsZero() || at.Before(entry.toolStartedAt) {
		return 0
	}
	return int64(at.Sub(entry.toolStartedAt) / time.Second)
}

func inputTokenSnapshot(tokens []inputToken) string {
	if len(tokens) == 0 {
		return ""
	}
	var b strings.Builder
	for _, token := range tokens {
		b.WriteString(strconv.Itoa(int(token.Kind)))
		b.WriteByte(':')
		b.WriteString(strconv.Itoa(token.Start))
		b.WriteByte(':')
		b.WriteString(strconv.Itoa(token.End))
		b.WriteByte(':')
		b.WriteString(token.Label)
		b.WriteByte(':')
		if token.AutoSpace {
			b.WriteByte('1')
		} else {
			b.WriteByte('0')
		}
		b.WriteByte(';')
	}
	return b.String()
}

func transcriptCitationSnapshot(citations []toolCitation) string {
	if len(citations) == 0 {
		return ""
	}
	var b strings.Builder
	for _, cite := range citations {
		b.WriteString(cite.toolUseID)
		b.WriteByte('\x00')
		b.WriteString(cite.name)
		b.WriteByte('\x00')
		b.WriteString(cite.target)
		b.WriteByte('\x00')
		b.WriteString(cite.status)
		b.WriteByte('\x00')
		b.WriteString(cite.preview)
		b.WriteByte('\x00')
		if cite.isError {
			b.WriteByte('1')
		} else {
			b.WriteByte('0')
		}
		b.WriteByte('\x00')
	}
	return b.String()
}

// renderTranscript 把多条 transcript 记录渲染为 viewport 内容。
func renderTranscript(entries []transcriptEntry, width int, showThinking bool) string {
	return renderTranscriptAt(entries, width, showThinking, time.Time{})
}

func renderTranscriptAt(entries []transcriptEntry, width int, showThinking bool, at time.Time) string {
	if len(entries) == 0 {
		return ""
	}
	var renderedTranscript strings.Builder
	hasPrevious := false
	var previousKind entryKind
	for _, entry := range entries {
		if entry.kind == entryThinking && !showThinking {
			continue
		}
		if !assistantEntryIsRenderable(entry) {
			continue
		}
		rendered := renderEntryAt(entry, width, at)
		if rendered == "" {
			continue
		}
		appendTranscriptRenderedEntry(&renderedTranscript, rendered, entry.kind, previousKind, hasPrevious)
		previousKind = entry.kind
		hasPrevious = true
	}
	return renderedTranscript.String()
}

func appendTranscriptRenderedEntry(builder *strings.Builder, rendered string, currentKind, previousKind entryKind, hasPrevious bool) {
	rendered = strings.TrimRight(rendered, "\n")
	if rendered == "" {
		return
	}
	if hasPrevious {
		builder.WriteString(transcriptEntrySeparator(previousKind, currentKind))
	}
	builder.WriteString(rendered)
}

func transcriptEntrySeparator(previousKind, currentKind entryKind) string {
	if currentKind == entryTool && (previousKind == entryAssistant || previousKind == entryTool) {
		return "\n"
	}
	if previousKind == currentKind {
		return "\n"
	}
	return "\n\n"
}

// renderEntry 渲染一条带标签和统一缩进的 transcript 记录。
func renderEntry(entry transcriptEntry, width int) string {
	return renderEntryAt(entry, width, time.Time{})
}

func renderEntryAt(entry transcriptEntry, width int, at time.Time) string {
	title := displayEntryTitle(entry)
	color := sanitizeTerminalText(entry.color)
	var label string
	if color != "" {
		label = lipgloss.NewStyle().Foreground(lipgloss.Color(color)).Bold(true).Render(title)
	} else {
		label = labelStyle(entry.kind).Render(title)
	}
	bodyWidth := transcriptBodyWidth(width)
	body := renderEntryBodyAt(entry, bodyWidth, at)
	if entry.kind == entryTool {
		if body == "" {
			return ""
		}
		return indentLines(body, transcriptEntryGutter)
	}
	if body == "" {
		return transcriptEntryGutter + label
	}
	return indentLines(label+"\n"+body, transcriptEntryGutter)
}

func displayEntryTitle(entry transcriptEntry) string {
	switch entry.kind {
	case entryUser:
		return "you >"
	case entryAssistant:
		return "agent >"
	case entryThinking:
		return "thinking >"
	default:
		return sanitizeTerminalText(entry.title)
	}
}

func transcriptBodyWidth(width int) int {
	return maxInt(1, width-lipgloss.Width(transcriptEntryGutter))
}

func (m appModel) hasRenderableTranscript() bool {
	for _, entry := range m.transcript {
		if entry.kind == entryThinking && !m.showThinking {
			continue
		}
		if assistantEntryIsRenderable(entry) {
			return true
		}
	}
	return false
}

func renderEmptyState(width, height int) string {
	content := emptyTitleStyle.Render("アトリ高性能ですから!")
	return lipgloss.Place(maxInt(1, width), maxInt(1, height), lipgloss.Center, lipgloss.Center, content)
}

// renderEntryBody 按消息类型渲染正文，assistant 消息会走 Markdown 渲染。
func renderEntryBody(entry transcriptEntry, width int) string {
	return renderEntryBodyAt(entry, width, time.Time{})
}

func renderEntryBodyAt(entry transcriptEntry, width int, at time.Time) string {
	if isToolTransaction(entry) {
		return renderToolTransactionEntry(entry, width, at)
	}
	body := entry.body
	if entry.kind != entryUser {
		body = sanitizeTerminalText(body)
	}
	body = strings.TrimRight(body, "\n")
	if entry.kind == entryAssistant {
		body = sanitizeAssistantVisibleBody(body)
		if entry.renderMode != transcriptRenderFormatted {
			return bodyStyle.Width(width).Render(body)
		}
		return renderAssistantBodyWithCitations(body, width, entry.citations)
	}
	if body == "" {
		return ""
	}
	if entry.kind == entryUser && len(entry.inputTokens) > 0 {
		return renderTokenizedTranscriptBody(body, entry.inputTokens, width)
	}
	if entry.kind == entryThinking {
		return thinkingBodyStyle.Width(width).Render(body)
	}
	if entry.kind == entryTool {
		isError := entry.isError
		isResult := toolEntryHasCompletedStatus(entry)
		switch {
		case isError:
			return renderToolEntryBodyWithMarker(body, width, 1, toolErrorBorderStyle, "!")
		case isResult:
			return renderToolEntryBodyWithMarker(body, width, 1, toolResultBorderStyle, "✓")
		default:
			return renderToolEntryBody(body, width, 1)
		}
	}
	return bodyStyle.Width(width).Render(body)
}

func toolEntryHasCompletedStatus(entry transcriptEntry) bool {
	if status := toolEntryStatus(entry); status != "" {
		return status != "running" && status != "error"
	}
	line := firstToolEntryLine(entry.body)
	parts := splitToolSummaryParts(line)
	return len(parts) >= 2 && strings.EqualFold(parts[1], "ok")
}

func isToolTransaction(entry transcriptEntry) bool {
	return entry.kind == entryTool &&
		(entry.toolName != "" || entry.toolUseID != "" || entry.toolStatus != "" || entry.title == "tool")
}

func toolEntryStatus(entry transcriptEntry) string {
	status := strings.ToLower(strings.TrimSpace(entry.toolStatus))
	switch status {
	case "running", "ok", "error":
		return status
	}
	if entry.isError {
		return "error"
	}
	parts := splitToolSummaryParts(firstToolEntryLine(entry.body))
	if len(parts) >= 2 {
		switch strings.ToLower(parts[1]) {
		case "running", "ok", "error":
			return strings.ToLower(parts[1])
		}
	}
	return "ok"
}

func toolEntryDisplayName(entry transcriptEntry) string {
	name := strings.TrimSpace(entry.toolName)
	if name == "" {
		parts := splitToolSummaryParts(firstToolEntryLine(entry.body))
		if len(parts) > 0 {
			name = parts[0]
		}
	}
	if name == "" && entry.title != "tool" {
		name = strings.TrimSpace(entry.title)
	}
	if name == "" {
		name = "tool"
	}
	return name
}

func renderToolTransactionEntry(entry transcriptEntry, width int, at time.Time) string {
	status := toolEntryStatus(entry)
	borderStyle := toolResultBorderStyle
	switch status {
	case "running":
		borderStyle = toolCallBorderStyle
	case "error":
		borderStyle = toolErrorBorderStyle
	}
	if entry.toolHovered && !entry.toolFocused {
		borderStyle = borderStyle.
			BorderStyle(lipgloss.Border{Left: "┃"}).
			BorderLeft(true)
	}

	contentWidth := toolEntryContentWidth(width, borderStyle)
	innerWidth := maxInt(1, contentWidth-borderStyle.GetHorizontalFrameSize())
	summary := renderCompactToolSummary(entry, innerWidth, at)
	if entry.toolFocused {
		summary = toolFocusedStyle.Width(innerWidth).Render(summary)
	}

	if status == "running" || !entry.toolExpanded {
		return borderStyle.Width(contentWidth).Render(summary)
	}

	result := sanitizeTerminalText(entry.toolResult)
	result = strings.TrimRight(result, "\n")
	if result == "" {
		result = "(empty result)"
	}
	detailLines := strings.Split(result, "\n")
	detailWidth := maxInt(1, innerWidth-2)
	detail := renderToolDetailLines(detailLines, detailWidth)
	return borderStyle.Width(contentWidth).Render(summary + "\n" + detail)
}

func renderCompactToolSummary(entry transcriptEntry, width int, at time.Time) string {
	width = maxInt(1, width)
	status := toolEntryStatus(entry)
	icon := "✓"
	statusText := "ok"
	statusStyle := toolCitationOKStyle
	nameStyle := toolHeaderStyle
	targetStyle := toolCitationStyle
	switch status {
	case "running":
		icon = "◌"
		statusText = "running"
		statusStyle = toolHeaderStyle
	case "error":
		icon = "×"
		statusText = "error"
		statusStyle = toolCitationErrorStyle
	}
	if entry.toolHovered && !entry.toolFocused {
		statusStyle = statusStyle.Underline(true)
		nameStyle = nameStyle.Underline(true)
		targetStyle = targetStyle.Underline(true)
	}

	name := strings.Join(strings.Fields(sanitizeTerminalText(toolEntryDisplayName(entry))), " ")
	target := strings.Join(strings.Fields(sanitizeTerminalText(entry.toolTarget)), " ")
	if name == "" {
		name = "tool"
	}

	focusPrefix := ""
	if entry.toolFocused {
		focusPrefix = "› "
	}
	iconText := icon + " "
	statusSuffix := " · " + statusText
	if status == "running" {
		statusSuffix += " · " + formatRunningToolElapsed(entry.toolStartedAt, at)
	} else if !entry.toolFinishedAt.IsZero() {
		statusSuffix += " · " + formatToolDuration(entry.toolStartedAt, entry.toolFinishedAt)
	}
	nameWidth := lipgloss.Width(name)
	baseWidth := lipgloss.Width(focusPrefix) + lipgloss.Width(iconText) + nameWidth
	statusWidth := lipgloss.Width(statusSuffix)

	showStatus := true
	targetText := ""
	if target != "" {
		targetBudget := width - baseWidth - statusWidth - 1
		if targetBudget >= 1 {
			targetText = truncateDisplayWidth(target, targetBudget)
		} else {
			showStatus = false
			targetBudget = width - baseWidth - 1
			if targetBudget >= 1 {
				targetText = truncateDisplayWidth(target, targetBudget)
			}
		}
	} else if baseWidth+statusWidth > width {
		showStatus = false
	}

	fixedWidth := lipgloss.Width(focusPrefix) + lipgloss.Width(iconText)
	if targetText != "" {
		fixedWidth += 1 + lipgloss.Width(targetText)
	}
	if showStatus {
		fixedWidth += statusWidth
	}
	if fixedWidth+nameWidth > width {
		name = truncateDisplayWidth(name, maxInt(1, width-fixedWidth))
	}

	var line strings.Builder
	line.WriteString(focusPrefix)
	line.WriteString(statusStyle.Render(iconText))
	line.WriteString(nameStyle.Render(name))
	if targetText != "" {
		line.WriteString(targetStyle.Render(" " + targetText))
	}
	if showStatus {
		line.WriteString(statusStyle.Render(statusSuffix))
	}
	return truncateStyledDisplayWidth(line.String(), width)
}

func formatRunningToolElapsed(startedAt, at time.Time) string {
	if startedAt.IsZero() || at.Before(startedAt) {
		return "0s"
	}
	seconds := int(at.Sub(startedAt) / time.Second)
	return formatWholeSeconds(seconds)
}

func formatToolDuration(startedAt, finishedAt time.Time) string {
	if startedAt.IsZero() || finishedAt.Before(startedAt) {
		return "0ms"
	}
	duration := finishedAt.Sub(startedAt)
	if duration < time.Second {
		return fmt.Sprintf("%dms", duration/time.Millisecond)
	}
	if duration < 10*time.Second {
		return trimDurationTrailingZeros(fmt.Sprintf("%.2fs", duration.Seconds()))
	}
	if duration < time.Minute {
		return trimDurationTrailingZeros(fmt.Sprintf("%.1fs", duration.Seconds()))
	}
	return formatWholeSeconds(int(duration / time.Second))
}

func formatWholeSeconds(seconds int) string {
	if seconds < 60 {
		return fmt.Sprintf("%ds", seconds)
	}
	minutes := seconds / 60
	seconds %= 60
	if minutes < 60 {
		return fmt.Sprintf("%dm%ds", minutes, seconds)
	}

	hours := minutes / 60
	minutes %= 60
	if hours < 24 {
		return formatDurationParts(
			fmt.Sprintf("%dh", hours),
			fmt.Sprintf("%dm", minutes),
			fmt.Sprintf("%ds", seconds),
		)
	}

	days := hours / 24
	hours %= 24
	return formatDurationParts(
		fmt.Sprintf("%dd", days),
		fmt.Sprintf("%dh", hours),
		fmt.Sprintf("%dm", minutes),
		fmt.Sprintf("%ds", seconds),
	)
}

func formatDurationParts(parts ...string) string {
	visible := make([]string, 0, len(parts))
	for _, part := range parts {
		if strings.HasPrefix(part, "0") {
			continue
		}
		visible = append(visible, part)
	}
	return strings.Join(visible, " ")
}

func trimDurationTrailingZeros(value string) string {
	value = strings.TrimRight(value, "0")
	value = strings.TrimRight(value, ".")
	return value
}

func truncateStyledDisplayWidth(text string, width int) string {
	if lipgloss.Width(text) <= width {
		return text
	}
	return ansi.Truncate(text, maxInt(1, width), "")
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
	name := strings.TrimSpace(sanitizeTerminalText(cite.name))
	if name == "" {
		return ""
	}
	key := toolCitationKeyStyle.Render("[" + name + "]")
	status := toolCitationStatusStyle(cite).Render(toolCitationStatusText(cite))
	target := strings.TrimSpace(sanitizeTerminalText(cite.target))
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

	detailLines := limitRenderedDetailLines(strings.Split(detail, "\n"), maxRenderedToolDetailLines)
	visibleLines := maxInt(1, int(float64(len(detailLines))*progress+0.999))
	if visibleLines > len(detailLines) {
		visibleLines = len(detailLines)
	}
	detailWidth := maxInt(1, contentWidth-borderStyle.GetHorizontalFrameSize())
	content := header + "\n" + renderToolDetailLines(detailLines[:visibleLines], detailWidth)
	return borderStyle.Width(contentWidth).Render(content)
}

func toolEntryContentWidth(width int, borderStyle lipgloss.Style) int {
	return maxInt(1, width-borderStyle.GetHorizontalFrameSize()-toolEntryNoWrapSafetyCells)
}

const toolEntryNoWrapSafetyCells = transcriptPanelHorizontalFrame + 1

func limitRenderedDetailLines(lines []string, maxLines int) []string {
	if maxLines <= 0 || len(lines) <= maxLines {
		return lines
	}
	if maxLines == 1 {
		return []string{"... " + strconv.Itoa(len(lines)) + " lines hidden"}
	}
	visible := maxLines - 1
	hidden := len(lines) - visible
	out := append([]string(nil), lines[:visible]...)
	out = append(out, "... "+strconv.Itoa(hidden)+" more lines hidden")
	return out
}

func renderToolDetailLines(lines []string, width int) string {
	containsUnifiedDiffHunk := hasUnifiedDiffHunk(lines)
	diffRowWidth := diffDetailRowsWidth(lines, width)
	rendered := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		style := toolDetailStyle
		renderedLine := "  " + trimmed
		isDiffLine := false
		switch {
		case containsUnifiedDiffHunk && isUnifiedDiffMetadataLine(trimmed):
			style = toolCitationStyle
		case diffDetailLineMarker(line, containsUnifiedDiffHunk) == "+":
			style = lipgloss.NewStyle().
				Foreground(lipgloss.Color("194")).
				Background(lipgloss.Color("22")).
				Bold(true)
			renderedLine = strings.TrimRight(line, " \t\r")
			isDiffLine = true
		case diffDetailLineMarker(line, containsUnifiedDiffHunk) == "-":
			style = lipgloss.NewStyle().
				Foreground(lipgloss.Color("224")).
				Background(lipgloss.Color("52")).
				Bold(true)
			renderedLine = strings.TrimRight(line, " \t\r")
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

func diffDetailRowsWidth(_ []string, width int) int {
	return maxInt(1, width)
}

func hasUnifiedDiffHunk(lines []string) bool {
	for _, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "@@ ") {
			return true
		}
	}
	return false
}

func isUnifiedDiffMetadataLine(line string) bool {
	return strings.HasPrefix(line, "@@ ") || strings.HasPrefix(line, "--- ") || strings.HasPrefix(line, "+++ ")
}

func diffDetailLineMarker(line string, allowStandalone bool) string {
	if allowStandalone && strings.HasPrefix(line, "+") {
		return "+"
	}
	if allowStandalone && strings.HasPrefix(line, "-") {
		return "-"
	}
	fields := strings.Fields(line)
	if len(fields) < 3 || !isDecimalNumber(fields[0]) {
		return ""
	}
	switch fields[1] {
	case "+", "-":
		if fields[2] == "│" {
			return fields[1]
		}
		return ""
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
