// 本文件定义聊天历史 transcript 的追加、刷新和逐条渲染逻辑。
package bubble

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"paw/internal/session"
	"paw/internal/ui"
)

const transcriptStreamingRefreshInterval = cursorFrameInterval
const maxRenderedToolDetailLines = 12
const transcriptEntryGutter = "  "

type transcriptRenderCacheEntry struct {
	key               transcriptRenderCacheKey
	rendered          string
	renderable        bool
	renderableVersion int
	// sourceSignature 折叠渲染该条目时的输入版本（entry version + 关键字段长度）。
	// 增量渲染用它做轻量变化检测：组条目折叠组内全部条目的签名。
	sourceSignature uint64
}

// transcriptEntrySpan 记录一条 transcript 条目在渲染行缓存中的行区间。
// startRow<0 表示该条目未渲染（被隐藏/跳过/渲染为空）。
type transcriptEntrySpan struct {
	startRow int
	height   int
	kind     entryKind // 实际渲染 kind（工具组统一为 entryTool）
}

type transcriptRenderCacheKey struct {
	kind                 entryKind
	title                string
	body                 string
	inputTokens          string
	color                string
	isError              bool
	toolUseID            string
	toolName             string
	toolStatus           string
	toolTarget           string
	toolInput            string
	toolResult           string
	toolResultLen        int
	toolExpanded         bool
	toolGroupPending     bool
	toolGroupOpen        bool
	toolFocused          bool
	toolHovered          bool
	toolResultOnly       bool
	todoSnapshot         string
	todoExpanded         bool
	todoLatest           bool
	todoCompletedFold    bool
	todoCleared          bool
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
	turnMetadata         string
}

func (m *appModel) ensureAssistantStreamEntry() {
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
}

// appendAssistantDelta 将经过流隔离器确认的稳定文本逐行追加到当前 assistant 消息。
// 刷新统一交给帧窗口合并（refreshViewportForStreaming，33ms 节流）：
//   - 没有任何提交内容（纯未完成尾部，streamLineBuffer 已隐藏）时完全不刷新；
//   - 增量渲染使单次重建成本与变化段成正比，33ms 窗口内的多次 delta 合并为
//     一次刷新即可，完整行不再单独插队全量重建（旧实现每个换行都全量刷新，
//     在大会话上是滚动/输入延迟的主要来源）。
func (m *appModel) appendAssistantDelta(delta string) {
	if delta == "" {
		return
	}
	for _, line := range strings.SplitAfter(delta, "\n") {
		m.ensureAssistantStreamEntry()
		m.transcript[m.activeAssistant].body += line
		touchTranscriptEntry(&m.transcript[m.activeAssistant])
		m.recordAssistantActivity(m.activeAssistant)
	}
	m.refreshViewportForStreaming()
}

func (m *appModel) ensureThinkingStreamEntry() {
	m.activeAssistant = -1
	if m.activeThinking < 0 || m.activeThinking >= len(m.transcript) || m.transcript[m.activeThinking].kind != entryThinking {
		m.transcript = append(m.transcript, transcriptEntry{
			kind:      entryThinking,
			title:     "thinking",
			createdAt: m.animationNow(),
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
		m.finalizeAssistantStream()
	}
	committed := m.thinkingStream.Push(delta, m.streamingBodyWidth())
	if m.thinkingStream.HasContent() {
		m.ensureThinkingStreamEntry()
	}
	m.appendThinkingDelta(committed)
}

func (m *appModel) finalizeAssistantStream() int {
	hadContent := m.assistantStream.HasContent()
	committed := m.assistantStream.Flush(m.streamingBodyWidth())
	if hadContent {
		m.ensureAssistantStreamEntry()
	}
	m.appendAssistantDelta(committed)
	finalized := m.activeAssistant
	m.activeAssistant = -1
	return finalized
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
	m.activeTurnUserEntry = -1
	m.doneAssistant = -1
	m.turnHasModelOutput = false
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
	cite := newToolCallCitation(toolUseID, name, input, m.workspaceRoot)
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

// isSubagentWaitTool 报告工具名是否为 SubagentWait。SubagentWait 在 TUI 中
// 不渲染为工具调用块：运行期间显示一行 "子智能体 <名字> 正在运行 Ns"，
// 工具完成后该行直接消失（错误时改为错误行），不进入 Tools 折叠组。
func isSubagentWaitTool(name string) bool {
	return strings.EqualFold(strings.TrimSpace(name), "SubagentWait")
}

// parseSubagentWaitTaskIDs 从 SubagentWait 的 input JSON 中解析 task_ids。
func parseSubagentWaitTaskIDs(input json.RawMessage) []string {
	if len(input) == 0 {
		return nil
	}
	var payload struct {
		TaskIDs []string `json:"task_ids"`
	}
	if err := json.Unmarshal(input, &payload); err != nil {
		return nil
	}
	ids := make([]string, 0, len(payload.TaskIDs))
	for _, id := range payload.TaskIDs {
		if id = strings.TrimSpace(id); id != "" {
			ids = append(ids, id)
		}
	}
	return ids
}

// subagentWaitNames 通过 subagent controller 查询 task_ids 对应的子智能体名字。
// 只返回能匹配到名字的任务；找不到名字时显示兜底文案 "子智能体"。
func (m *appModel) subagentWaitNames(ids []string) []string {
	if len(ids) == 0 || m.subagents == nil {
		return nil
	}
	byID := make(map[string]string, len(ids))
	for _, task := range m.subagents.ListTasks() {
		if name := strings.TrimSpace(task.Name); name != "" {
			byID[task.ID] = name
		}
	}
	seen := make(map[string]bool, len(ids))
	names := make([]string, 0, len(ids))
	for _, id := range ids {
		name, ok := byID[id]
		if !ok || seen[name] {
			continue
		}
		seen[name] = true
		names = append(names, name)
	}
	return names
}

// subagentWaitRunningBody 生成 SubagentWait 状态行的正文，如
// "子智能体 高松灯 正在运行 13s"。
func subagentWaitRunningBody(names []string, elapsed int64) string {
	label := "子智能体"
	if len(names) > 0 {
		label += " " + strings.Join(names, "、")
	}
	return label + " 正在运行 " + formatWholeSeconds(int(elapsed))
}

func (m *appModel) recordToolCallEntry(toolUseID, name string, input json.RawMessage, mutationKnown, isMutation bool, mutation *ui.FileMutationSnapshot) {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "tool"
	}
	if isSubagentWaitTool(name) {
		names := m.subagentWaitNames(parseSubagentWaitTaskIDs(input))
		m.addEntry(transcriptEntry{
			kind:                entrySystem,
			title:               "",
			body:                subagentWaitRunningBody(names, 0),
			toolUseID:           strings.TrimSpace(toolUseID),
			toolName:            name,
			toolStatus:          "running",
			subagentWaitRunning: true,
			subagentWaitNames:   names,
			createdAt:           m.animationNow(),
			toolStartedAt:       m.animationNow(),
		})
		return
	}
	segmentPending := appendedToolEntryStartsPendingSegment(m.transcript)
	target := displayToolTarget(name, input, m.workspaceRoot)
	if selectTarget, ok := selectToolCallTarget(name, input); ok {
		target = selectTarget
	}
	m.addEntry(transcriptEntry{
		kind:              entryTool,
		title:             "tool",
		body:              formatRunningToolCallBodyWithSnapshot(name, input, "", mutationKnown, isMutation, mutation),
		toolUseID:         strings.TrimSpace(toolUseID),
		toolName:          name,
		toolStatus:        "running",
		toolTarget:        target,
		toolInput:         append(json.RawMessage(nil), input...),
		fileMutationKnown: mutationKnown,
		isFileMutation:    isMutation,
		toolGroupPending:  segmentPending,
		createdAt:         m.animationNow(),
		toolStartedAt:     m.animationNow(),
	})
	m.toolGroupExpanded = true
	m.transcriptRenderCache = nil
	m.refreshViewport()
}

func (m *appModel) recordToolResultEntry(toolUseID, name, status, content string, isError, mutationKnown, isMutation bool, mutation *ui.FileMutationSnapshot) {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "tool"
	}
	if isSubagentWaitTool(name) {
		if m.removeSubagentWaitEntry(strings.TrimSpace(toolUseID), name) {
			m.lastToolProgressSecond = -1
			if isError {
				body := strings.TrimSpace(content)
				if body == "" {
					body = "子智能体等待失败"
				}
				m.addEntry(transcriptEntry{
					kind:  entryError,
					title: "subagent",
					body:  body,
				})
			} else {
				m.refreshViewport()
			}
		}
		return
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
		effectiveKnown := mutationKnown || entry.fileMutationKnown
		effectiveMutation := entry.isFileMutation
		if mutationKnown {
			effectiveMutation = isMutation
		}
		switch {
		case effectiveKnown && effectiveMutation && isError:
			entry.body = completeFileMutationToolCallBody(name, entry.toolInput, "", status, &ui.FileMutationSnapshot{BeforeExists: true, AfterExists: true})
		case effectiveKnown && effectiveMutation && mutation != nil:
			entry.body = completeFileMutationToolCallBody(name, entry.toolInput, "", status, mutation)
		case effectiveKnown && effectiveMutation:
			entry.body = completeRunningToolCallBody(entry.body, status)
		case effectiveKnown:
			entry.body = completeToolCallBody(name, entry.body, status, content)
		case isFileMutationTool(name) && isError:
			entry.body = completeFileMutationToolCallBody(name, entry.toolInput, "", status, &ui.FileMutationSnapshot{BeforeExists: true, AfterExists: true})
		case isFileMutationTool(name):
			entry.body = completeFileMutationToolCallBody(name, entry.toolInput, "", status, mutation)
		default:
			entry.body = completeToolCallBody(name, entry.body, status, content)
		}
		entry.fileMutationKnown = effectiveKnown
		entry.isFileMutation = effectiveMutation
		entry.isError = isError
		entry.toolStatus = status
		entry.toolResult = content
		if strings.EqualFold(name, "Select") && strings.EqualFold(status, "ok") {
			if presentation, ok := parseSelectToolPresentation(entry.toolInput, content); ok {
				entry.toolTarget = presentation.target
			}
		}
		if strings.EqualFold(name, "update_todo") && strings.EqualFold(status, "ok") && !isError {
			entry.toolTarget = compactUpdateTodoResult(content)
		}
		entry.toolExpanded = false
		entry.toolGroupOpen = false
		if toolSegmentIsPending(m.transcript, idx) && status == "ok" && toolEntryShowsMutationDetail(*entry, name, effectiveKnown, effectiveMutation) {
			m.collapsePendingToolDetailsInCurrentTurn(idx)
			entry.toolExpanded = true
		}
		entry.toolResultOnly = false
		entry.toolFinishedAt = m.animationNow()
		touchTranscriptEntry(entry)
		m.recordTranscriptEntryActivity(idx, true)
		m.refreshViewport()
		return
	}
	target := ""
	if strings.EqualFold(name, "update_todo") && strings.EqualFold(status, "ok") && !isError {
		target = compactUpdateTodoResult(content)
	}
	segmentPending := appendedToolEntryStartsPendingSegment(m.transcript)
	m.addEntry(transcriptEntry{
		kind:             entryTool,
		title:            "tool",
		body:             formatToolResultBody(name, status, ""),
		isError:          isError,
		toolUseID:        strings.TrimSpace(toolUseID),
		toolName:         name,
		toolStatus:       status,
		toolTarget:       target,
		toolResult:       content,
		toolExpanded:     false,
		toolGroupPending: segmentPending,
		toolResultOnly:   true,
		createdAt:        m.animationNow(),
	})
	if status == "ok" {
		if last := len(m.transcript) - 1; last >= 0 && toolSegmentIsPending(m.transcript, last) && toolEntryShowsMutationDetail(m.transcript[last], name, mutationKnown, isMutation) {
			m.collapsePendingToolDetailsInCurrentTurn(last)
			m.transcript[last].toolExpanded = true
			touchTranscriptEntry(&m.transcript[last])
			m.refreshViewport()
		}
	}
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

// removeSubagentWaitEntry 从 transcript 中移除匹配的 SubagentWait 状态行，
// 使等待结束（成功或失败）后该行直接消失而不折叠为工具块。
func (m *appModel) removeSubagentWaitEntry(toolUseID, name string) bool {
	for index := len(m.transcript) - 1; index >= 0; index-- {
		entry := &m.transcript[index]
		if !entry.subagentWaitRunning {
			continue
		}
		if toolUseID != "" && entry.toolUseID != "" {
			if entry.toolUseID != toolUseID {
				continue
			}
		} else if !strings.EqualFold(entry.toolName, name) {
			continue
		}
		m.transcript = append(m.transcript[:index], m.transcript[index+1:]...)
		m.transcriptRenderCache = nil
		m.transcriptLinesValid = false
		return true
	}
	return false
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

func toolEntryShowsMutationDetail(entry transcriptEntry, name string, mutationKnown, isMutation bool) bool {
	effectiveKnown := mutationKnown || entry.fileMutationKnown
	effectiveMutation := entry.isFileMutation
	if mutationKnown {
		effectiveMutation = isMutation
	}
	return (effectiveKnown && effectiveMutation) || (!effectiveKnown && isFileMutationTool(name))
}

func appendedToolEntryStartsPendingSegment(entries []transcriptEntry) bool {
	if len(entries) == 0 {
		return true
	}
	return !isToolTransaction(entries[len(entries)-1])
}

func (m *appModel) collapsePendingToolDetailsInCurrentTurn(skipIndex int) {
	start, end := currentTurnTranscriptBounds(m.transcript)
	for index := start; index <= end && index < len(m.transcript); {
		if !isToolTransaction(m.transcript[index]) {
			index++
			continue
		}
		segmentStart, segmentEnd, ok := toolSegmentRange(m.transcript, index)
		if !ok {
			index++
			continue
		}
		if !toolSegmentIsPending(m.transcript, segmentStart) {
			index = segmentEnd + 1
			continue
		}
		for segmentIndex := segmentStart; segmentIndex <= segmentEnd; segmentIndex++ {
			if segmentIndex == skipIndex {
				continue
			}
			entry := &m.transcript[segmentIndex]
			if entry.toolExpanded || entry.toolGroupOpen {
				entry.toolExpanded = false
				entry.toolGroupOpen = false
				touchTranscriptEntry(entry)
			}
		}
		index = segmentEnd + 1
	}
}

func (m *appModel) readyPendingToolSegmentsInCurrentTurn() bool {
	start, end := currentTurnTranscriptBounds(m.transcript)
	if start < 0 || end < start {
		return false
	}
	changed := false
	for index := start; index <= end && index < len(m.transcript); {
		if !isToolTransaction(m.transcript[index]) {
			index++
			continue
		}
		segmentStart, segmentEnd, ok := toolSegmentRange(m.transcript, index)
		if !ok {
			index++
			continue
		}
		if !toolSegmentIsPending(m.transcript, segmentStart) {
			index = segmentEnd + 1
			continue
		}
		for segmentIndex := segmentStart; segmentIndex <= segmentEnd; segmentIndex++ {
			entry := &m.transcript[segmentIndex]
			if !entry.toolGroupPending && !entry.toolExpanded && !entry.toolGroupOpen {
				continue
			}
			entry.toolGroupPending = false
			entry.toolExpanded = false
			entry.toolGroupOpen = false
			touchTranscriptEntry(entry)
			changed = true
		}
		index = segmentEnd + 1
	}
	return changed
}

func currentTurnTranscriptBounds(entries []transcriptEntry) (int, int) {
	if len(entries) == 0 {
		return -1, -1
	}
	start := 0
	for index := len(entries) - 1; index >= 0; index-- {
		if entries[index].kind == entryUser {
			start = index + 1
			break
		}
	}
	return start, len(entries) - 1
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
// 如果刷新前 viewport 位于底部，则继续跟随新内容；否则保留手动滚动位置。
// 文字选区只影响渲染，不影响用户已经回到底部后的自动跟随。
func (m *appModel) refreshViewport() {
	m.refreshViewportWithBottomState(m.viewport.AtBottom())
}

func (m *appModel) refreshViewportWithBottomState(wasAtBottom bool) {
	offset := m.viewport.YOffset
	m.applyTranscriptToViewport(maxInt(20, m.viewport.Width), m.showThinking, m.animationNow())
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
	m.applyTranscriptToViewport(maxInt(20, m.viewport.Width), m.showThinking, m.animationNow())
	m.viewport.SetYOffset(offset)
	m.markTranscriptRefreshed()
}

// applyTranscriptToViewport 把 transcript 增量渲染到 viewport。
// 非选择状态走增量行替换；选择激活时（选择高亮会改写选区行的 SGR）退化为
// 全量 content 重绘，仅在选择期间付出该成本。
func (m *appModel) applyTranscriptToViewport(width int, showThinking bool, at time.Time) {
	changed, startRow, newLines := m.ensureTranscriptLinesAt(width, showThinking, at)
	if m.selectionActive {
		// 拖拽/双击/三击只改选区状态（selectionStart/End/Mode），不触发
		// transcript 内容签名变化；必须把选区签名纳入重绘判断，否则拖拽
		// 中途视口高亮停留在第一次超出容差时的选区，与最终复制内容不符。
		sig := m.selectionRenderSignature()
		if changed || !m.viewportShowsSelection || sig != m.lastSelectionRenderSig {
			content := strings.Join(m.transcriptLines, "\n")
			m.viewport.SetContent(m.renderTranscriptSelection(content))
			m.viewportShowsSelection = true
			m.lastSelectionRenderSig = sig
		}
		return
	}
	switch {
	case changed && startRow < 0:
		m.viewport.SetLines(m.transcriptLines)
	case changed:
		m.viewport.ReplaceLines(startRow, newLines)
	case m.viewportShowsSelection:
		m.viewport.SetLines(m.transcriptLines)
	}
	m.viewportShowsSelection = false
	m.lastSelectionRenderSig = 0
}

// ensureTranscriptLinesAt 保证 m.transcriptLines / m.transcriptEntrySpans 与
// 当前 transcript 内容同步，并返回如何更新 viewport：
//   - changed=false：无内容变化，viewport 无需更新；
//   - changed=true 且 startRow<0：需要全量 SetLines；
//   - changed=true 且 startRow>=0：只需 ReplaceLines(startRow, newLines)。
//
// 行缓存失效（首次渲染、条目增删、宽度/可见性变化）时走全量重建；其余
// 变化只重渲染「第一个变化条目到末尾」的段并增量替换行区间。渲染成本与
// 变化段大小成正比，流式追加/工具计时等尾部更新因此是 O(变化段) 而非
// O(全部条目)。
func (m *appModel) ensureTranscriptLinesAt(width int, showThinking bool, at time.Time) (changed bool, startRow int, newLines []string) {
	if m == nil {
		return false, -1, nil
	}
	if len(m.transcriptRenderCache) != len(m.transcript) {
		next := make([]transcriptRenderCacheEntry, len(m.transcript))
		copy(next, m.transcriptRenderCache)
		m.transcriptRenderCache = next
	}
	if m.transcriptLinesValid && m.transcriptContentCached {
		sig := transcriptRenderSignature(m.transcript, width, showThinking, m.toolGroupExpanded, m.toolGroupFullResult)
		if sig == m.transcriptRenderSignature {
			return false, -1, nil
		}
	}
	// 内容确实变化：交互缓存（行快照/条目位置）全部失效。
	m.transcriptLineCache = nil
	m.transcriptLineCacheReady = false
	m.transcriptLocationCache = nil
	m.transcriptLocationsReady = false

	if len(m.transcript) == 0 {
		m.transcriptLines = nil
		m.transcriptEntrySpans = nil
		m.transcriptLinesValid = true
		m.transcriptContentCached = true
		m.transcriptRenderSignature = transcriptRenderSignature(m.transcript, width, showThinking, m.toolGroupExpanded, m.toolGroupFullResult)
		return true, -1, nil
	}

	if !m.transcriptLinesValid {
		segment, spans := m.renderTranscriptEntriesFrom(0, width, showThinking, at)
		m.transcriptLines = transcriptSegmentLines(segment)
		m.transcriptEntrySpans = spans
		m.transcriptLinesValid = true
		m.transcriptContentCached = true
		m.transcriptRenderSignature = transcriptRenderSignature(m.transcript, width, showThinking, m.toolGroupExpanded, m.toolGroupFullResult)
		return true, -1, nil
	}
	// 条目追加后对齐 spans 长度（新条目的 span 无效，firstChanged 会从新条目
	// 开始增量渲染）。条目删除/重排的路径必须显式置 transcriptLinesValid=false
	// 走全量重建，否则后续 span 的行号会错位。注意新 span 必须显式置
	// startRow=-1：零值 startRow=0 会被误认为有效渲染条目，把替换起点
	// 错算到内容顶部。
	if len(m.transcriptEntrySpans) < len(m.transcript) {
		for len(m.transcriptEntrySpans) < len(m.transcript) {
			m.transcriptEntrySpans = append(m.transcriptEntrySpans, transcriptEntrySpan{startRow: -1})
		}
	} else if len(m.transcriptEntrySpans) > len(m.transcript) {
		m.transcriptEntrySpans = m.transcriptEntrySpans[:len(m.transcript)]
	}

	startIdx := m.firstChangedTranscriptIndex(width, showThinking, at)
	if startIdx < 0 {
		// 签名不同但条目级签名全命中：兜底全量重建（正常不会发生）。
		segment, spans := m.renderTranscriptEntriesFrom(0, width, showThinking, at)
		m.transcriptLines = transcriptSegmentLines(segment)
		m.transcriptEntrySpans = spans
		m.transcriptContentCached = true
		m.transcriptRenderSignature = transcriptRenderSignature(m.transcript, width, showThinking, m.toolGroupExpanded, m.toolGroupFullResult)
		return true, -1, nil
	}
	startRow = 0
	if startIdx > 0 {
		for i := startIdx - 1; i >= 0; i-- {
			if span := m.transcriptEntrySpans[i]; span.startRow >= 0 && span.height > 0 {
				startRow = span.startRow + span.height
				break
			}
		}
	}
	segment, spans := m.renderTranscriptEntriesFrom(startIdx, width, showThinking, at)
	for i := startIdx; i < len(m.transcript); i++ {
		m.transcriptEntrySpans[i] = spans[i-startIdx]
	}
	// 替换起点 = 段内第一个渲染条目的 startRow：条目间的分隔符空行属于
	// 前缀（保留在 lines 中），替换只从条目本体开始。段内无渲染条目时
	// 回退到上一渲染条目的结束行（截断尾部）。
	replaceStart := startRow
	for _, span := range spans {
		if span.startRow >= 0 {
			replaceStart = span.startRow
			break
		}
	}
	newLines = transcriptSegmentLines(segment)
	if replaceStart >= len(m.transcriptLines) {
		m.transcriptLines = append(m.transcriptLines, newLines...)
	} else {
		m.transcriptLines = append(m.transcriptLines[:replaceStart], newLines...)
	}
	m.transcriptContentCached = true
	m.transcriptRenderSignature = transcriptRenderSignature(m.transcript, width, showThinking, m.toolGroupExpanded, m.toolGroupFullResult)
	return true, replaceStart, newLines
}

// transcriptSegmentLines 把渲染段字符串拆成行，并保留内容底部的一行空隙
// （与旧 renderTranscriptContentAt 尾部追加 "\n" 的行为一致）。
func transcriptSegmentLines(segment string) []string {
	if segment == "" {
		return nil
	}
	lines := strings.Split(segment, "\n")
	lines = append(lines, "")
	return lines
}

// transcriptEntrySignature 折叠渲染单条 entry 所需的输入版本。所有会改变
// 渲染结果的字段变更路径都会 touchTranscriptEntry（version++），因此
// version + 关键长度足以作为轻量变化检测；完整校验由 cache key 承担。
func transcriptEntrySignature(entry transcriptEntry) uint64 {
	var h uint64 = 1469598103934665603 // FNV-1a offset
	mix := func(v uint64) {
		h ^= v
		h *= 1099511628211
	}
	mix(uint64(entry.kind))
	mix(uint64(entry.version))
	mix(uint64(len(entry.body)))
	mix(uint64(len(entry.toolResult)))
	mix(uint64(len(entry.citations)))
	return h
}

// transcriptGroupSourceSignature 折叠工具组内全部条目的版本签名：组渲染
// 依赖组内任意条目，任一变化都必须使组缓存失效。
func transcriptGroupSourceSignature(entries []transcriptEntry, first, last int) uint64 {
	var h uint64 = 1469598103934665603
	mix := func(v uint64) {
		h ^= v
		h *= 1099511628211
	}
	mix(1) // 标记组
	for i := first; i <= last; i++ {
		h ^= transcriptEntrySignature(entries[i])
		h *= 1099511628211
	}
	return h
}

// transcriptEntryRenderable 返回条目 idx 是否参与渲染，结果缓存在渲染缓存
// 里：assistant 条目的可渲染判断会清洗整段 body（O(body)），不能在每次
// 刷新的变化检测里重算。version 为 0（未 touch 的直构条目）时缓存零值
// 不可信，总是重算。
func (m *appModel) transcriptEntryRenderable(idx int, showThinking bool) bool {
	entry := m.transcript[idx]
	if entry.kind == entryThinking && !showThinking {
		return false
	}
	cache := &m.transcriptRenderCache[idx]
	if cache.renderableVersion != entry.version || entry.version == 0 {
		cache.renderable = assistantEntryIsRenderable(entry)
		cache.renderableVersion = entry.version
	}
	return cache.renderable
}

// firstChangedTranscriptIndex 返回第一个渲染输入发生变化的条目索引；没有
// 变化返回 -1。判断基于条目级轻量签名（渲染循环的完整 cache key 是权威）。
func (m *appModel) firstChangedTranscriptIndex(width int, showThinking bool, at time.Time) int {
	for idx := range m.transcript {
		if !m.transcriptEntryRenderable(idx, showThinking) {
			continue
		}
		if toolEntryUsesReadyGroup(m.transcript, idx) {
			first, last := toolGroupRange(m.transcript, idx)
			if first != idx {
				continue
			}
			if m.transcriptRenderCache[idx].sourceSignature != transcriptGroupSourceSignature(m.transcript, first, last) {
				return first
			}
			continue
		}
		if m.transcriptRenderCache[idx].sourceSignature != transcriptEntrySignature(m.transcript[idx]) {
			return idx
		}
	}
	return -1
}

// renderTranscriptEntriesFrom 渲染 [startIdx, len(transcript)) 的条目段，
// 复用 per-entry 渲染缓存，并同步输出每个条目的渲染行区间（spans）。
// 返回段字符串（不含底部空隙行）与 spans。调用方保证 startIdx 之前的
// 条目已渲染且 m.transcriptEntrySpans 有效（用于分隔符与前缀行数）。
func (m *appModel) renderTranscriptEntriesFrom(startIdx int, width int, showThinking bool, at time.Time) (string, []transcriptEntrySpan) {
	var rendered strings.Builder
	hasPrevious := false
	var previousKind entryKind
	wroteAny := false
	totalRows := 0
	spans := make([]transcriptEntrySpan, len(m.transcript)-startIdx)
	if startIdx > 0 {
		for i := startIdx - 1; i >= 0; i-- {
			if span := m.transcriptEntrySpans[i]; span.startRow >= 0 && span.height > 0 {
				hasPrevious = true
				previousKind = span.kind
				totalRows = span.startRow + span.height
				break
			}
		}
	}
	for idx := startIdx; idx < len(m.transcript); idx++ {
		entry := m.transcript[idx]
		span := &spans[idx-startIdx]
		span.startRow = -1
		if !m.transcriptEntryRenderable(idx, showThinking) {
			continue
		}
		var renderedEntry string
		kind := entry.kind
		if toolEntryUsesReadyGroup(m.transcript, idx) {
			first, last := toolGroupRange(m.transcript, idx)
			if first != idx {
				continue
			}
			key := transcriptRenderKey(entry, width, at)
			groupEntries := toolEntriesForGroup(m.transcript, idx)
			groupExpanded := m.toolGroupExpanded
			if !toolGroupHasRunning(m.transcript, first, last) {
				groupExpanded = m.transcript[first].toolExpanded
			}
			key.body = toolGroupRenderSnapshot(groupEntries, width, at)
			key.version = 0
			if groupExpanded {
				key.toolExpanded = true
			}
			key.toolGroupPending = false
			key.toolFocused = key.toolFocused || m.toolGroupFullResult
			if m.transcriptRenderCache[idx].key == key {
				renderedEntry = m.transcriptRenderCache[idx].rendered
			} else {
				renderedEntry = renderToolsGroup(groupEntries, width, at, groupExpanded, m.toolGroupFullResult)
				if renderedEntry == "" {
					continue
				}
				m.storeTranscriptRenderCacheEntry(idx, key, renderedEntry, transcriptGroupSourceSignature(m.transcript, first, last), entry.version)
			}
			kind = entryTool
		} else {
			key := transcriptRenderKey(entry, width, at)
			if m.transcriptRenderCache[idx].key == key {
				renderedEntry = m.transcriptRenderCache[idx].rendered
			} else {
				renderedEntry = renderEntryAt(entry, width, at)
				if renderedEntry == "" {
					continue
				}
				m.storeTranscriptRenderCacheEntry(idx, key, renderedEntry, transcriptEntrySignature(entry), entry.version)
			}
		}
		renderedEntry = strings.TrimRight(renderedEntry, "\n")
		if renderedEntry == "" {
			continue
		}
		sep := ""
		if hasPrevious {
			sep = transcriptEntrySeparator(previousKind, kind)
		}
		startRow := totalRows
		if hasPrevious {
			startRow = totalRows + strings.Count(sep, "\n") - 1
		}
		// 分隔符的换行计入前缀行数（startRow），但段内第一个条目不再把
		// 分隔符写进段字符串：前缀行已包含到 startRow，再写会把空行多算
		// 一次，导致增量替换后内容整体下移一行。
		if wroteAny {
			rendered.WriteString(sep)
		}
		rendered.WriteString(renderedEntry)
		height := maxInt(1, lipgloss.Height(renderedEntry))
		span.startRow = startRow
		span.height = height
		span.kind = kind
		totalRows = startRow + height
		hasPrevious = true
		previousKind = kind
		wroteAny = true
	}
	return rendered.String(), spans
}

// storeTranscriptRenderCacheEntry 写入条目渲染缓存，保留可渲染性缓存的
// 现值（渲染循环调用方已经判断过该条目可渲染）。
func (m *appModel) storeTranscriptRenderCacheEntry(idx int, key transcriptRenderCacheKey, rendered string, sourceSignature uint64, version int) {
	entry := transcriptRenderCacheEntry{
		key:               key,
		rendered:          rendered,
		renderable:        true,
		renderableVersion: version,
		sourceSignature:   sourceSignature,
	}
	if version == 0 {
		entry.renderable = assistantEntryIsRenderable(m.transcript[idx])
	}
	m.transcriptRenderCache[idx] = entry
}

func (m *appModel) refreshViewportForStreaming() {
	if m == nil {
		return
	}
	now := time.Now()
	if m.transcriptRefreshPending {
		// 已有合并窗口在等待：窗口到期后由 cursorFrame flush；若动画帧链
		// 未运行，后续 delta 到达时直接补刷，保证内容不会滞留。
		if now.Sub(m.transcriptRefreshPendingAt) >= transcriptStreamingRefreshInterval {
			m.flushTranscriptRefreshIfDue(now)
		}
		return
	}
	if m.lastTranscriptRefreshAt.IsZero() || now.Sub(m.lastTranscriptRefreshAt) >= transcriptStreamingRefreshInterval {
		m.refreshViewport()
		return
	}
	m.transcriptRefreshPending = true
	m.transcriptRefreshPendingAt = now
}

// flushTranscriptRefreshIfDue 由 cursorFrameMsg 帧驱动调用：pending 置位后至少
// 等待一个流式刷新窗口再真正刷新，避免每个 cursor tick 都重建 transcript。
func (m *appModel) flushTranscriptRefreshIfDue(now time.Time) {
	if m == nil || !m.transcriptRefreshPending {
		return
	}
	if m.transcriptRefreshPendingAt.IsZero() {
		m.transcriptRefreshPending = false
		return
	}
	if now.Sub(m.transcriptRefreshPendingAt) >= transcriptStreamingRefreshInterval {
		if m.viewport.AtBottom() {
			m.refreshViewport()
		} else {
			m.refreshViewportPreservingOffset()
		}
	}
}

func (m *appModel) markTranscriptRefreshed() {
	if m == nil {
		return
	}
	m.transcriptRefreshPending = false
	m.transcriptRefreshPendingAt = time.Time{}
	m.lastTranscriptRefreshAt = time.Now()
}

func (m *appModel) refreshRunningToolProgress(now time.Time) {
	if m == nil {
		return
	}
	maxElapsed := int64(-1)
	for index := range m.transcript {
		entry := &m.transcript[index]
		switch {
		case entry.subagentWaitRunning:
			elapsed := toolElapsedSeconds(*entry, now)
			if elapsed > maxElapsed {
				maxElapsed = elapsed
			}
			body := subagentWaitRunningBody(entry.subagentWaitNames, elapsed)
			if entry.body != body {
				entry.body = body
				touchTranscriptEntry(entry)
			}
		case isToolTransaction(*entry) && toolEntryStatus(*entry) == "running":
			elapsed := toolElapsedSeconds(*entry, now)
			if elapsed > maxElapsed {
				maxElapsed = elapsed
			}
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
	// running tool 的 elapsed 是时间相关渲染输入，而严格 revision cache 的
	// 签名只折叠 entry 版本与 body 长度，不含时间；不 touch 的话每秒的
	// refreshViewport 都会命中缓存，计时永远冻结。这里让所有仍在运行的
	// 工具条目版本递增，使缓存失效后重渲染刷新计时。
	for index := range m.transcript {
		entry := &m.transcript[index]
		if entry.subagentWaitRunning || (isToolTransaction(*entry) && toolEntryStatus(*entry) == "running") {
			touchTranscriptEntry(entry)
		}
	}
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
	for index := len(m.transcript) - 1; index >= 0; index-- {
		entry := &m.transcript[index]
		if entry.subagentWaitRunning {
			// turn 意外失败时 SubagentWait 不会再收到结果：直接移除状态行，
			// 由后续 entryError 呈现失败原因，不保留悬挂的工具块。
			m.transcript = append(m.transcript[:index], m.transcript[index+1:]...)
			m.transcriptRenderCache = nil
			m.transcriptLinesValid = false
			changed = true
			continue
		}
		if !isToolTransaction(*entry) || toolEntryStatus(*entry) != "running" {
			continue
		}
		entry.body = completeRunningToolCallBody(entry.body, "error")
		entry.toolStatus = "error"
		entry.isError = true
		entry.toolResult = message
		entry.toolExpanded = false
		entry.toolGroupOpen = false
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

// transcriptRenderSignature folds every input that can change the rendered
// transcript into a cheap scalar. Per-entry body/tool/status fields are
// captured through the version counter (touchTranscriptEntry bumps it on every
// mutation); the remaining inputs are explicit parameters.
func transcriptRenderSignature(entries []transcriptEntry, width int, showThinking, groupExpanded, fullResult bool) uint64 {
	var h uint64 = 1469598103934665603 // FNV-1a offset
	mix := func(v uint64) {
		h ^= v
		h *= 1099511628211
	}
	mix(uint64(width))
	if showThinking {
		mix(1)
	} else {
		mix(0)
	}
	if groupExpanded {
		mix(2)
	}
	if fullResult {
		mix(4)
	}
	for _, entry := range entries {
		mix(uint64(entry.kind))
		mix(uint64(entry.version))
		mix(uint64(len(entry.body)))
		mix(uint64(len(entry.toolResult)))
		mix(uint64(len(entry.citations)))
	}
	return h
}

// renderTranscriptContentAt 渲染完整 transcript 内容（兼容入口，供测试与
// 外部调用）。实际 viewport 路径使用 applyTranscriptToViewport 的增量渲染；
// 这里复用同一份行缓存，内容未变化时只付出一次 join 的代价。
func (m *appModel) renderTranscriptContentAt(width int, showThinking bool, at time.Time) string {
	m.ensureTranscriptLinesAt(width, showThinking, at)
	return strings.Join(m.transcriptLines, "\n")
}

func transcriptRenderKey(entry transcriptEntry, width int, at time.Time) transcriptRenderCacheKey {
	key := transcriptRenderCacheKey{
		kind:                 entry.kind,
		title:                entry.title,
		inputTokens:          inputTokenSnapshot(entry.inputTokens),
		color:                entry.color,
		isError:              entry.isError,
		toolUseID:            entry.toolUseID,
		toolName:             entry.toolName,
		toolStatus:           entry.toolStatus,
		toolTarget:           entry.toolTarget,
		toolInput:            string(entry.toolInput),
		toolResult:           entry.toolResult,
		toolResultLen:        len(entry.toolResult),
		toolExpanded:         entry.toolExpanded,
		toolGroupPending:     entry.toolGroupPending,
		toolGroupOpen:        entry.toolGroupOpen,
		toolFocused:          entry.toolFocused,
		toolHovered:          entry.toolHovered,
		toolResultOnly:       entry.toolResultOnly,
		todoSnapshot:         transcriptTodoSnapshot(entry.todoSnapshot),
		todoExpanded:         entry.todoExpanded,
		todoLatest:           entry.todoLatest,
		todoCompletedFold:    entry.todoCompletedFold,
		todoCleared:          entry.todoCleared,
		width:                width,
		version:              entry.version,
		bodyLen:              len(entry.body),
		citationCount:        len(entry.citations),
		createdAtUnixNS:      entry.createdAt.UnixNano(),
		createdAtIsZero:      entry.createdAt.IsZero(),
		toolStartedAtUnixNS:  entry.toolStartedAt.UnixNano(),
		toolFinishedAtUnixNS: entry.toolFinishedAt.UnixNano(),
		turnMetadata:         transcriptTurnMetadataSnapshot(entry.turnMetadata),
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

func transcriptTodoSnapshot(snapshot any) string {
	if snapshot == nil {
		return ""
	}
	data, err := json.Marshal(snapshot)
	if err != nil {
		return ""
	}
	return string(data)
}

func transcriptTurnMetadataSnapshot(metadata *session.TurnMetadata) string {
	if metadata == nil {
		return ""
	}
	data, err := json.Marshal(metadata)
	if err != nil {
		return ""
	}
	return string(data)
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

func toolGroupRenderSnapshot(entries []transcriptEntry, width int, at time.Time) string {
	var snapshot strings.Builder
	for _, entry := range entries {
		// Include every entry because one cached frame represents the whole group.
		snapshot.WriteString(fmt.Sprintf("%#v;", transcriptRenderKey(entry, width, at)))
	}
	return snapshot.String()
}

// renderTranscript 把多条 transcript 记录渲染为 viewport 内容。
func renderTranscript(entries []transcriptEntry, width int, showThinking bool) string {
	return renderTranscriptAt(entries, width, showThinking, time.Time{})
}

// renderTranscriptAt 渲染历史记录；appModel 的实际 viewport 使用
// renderTranscriptContentAt，以便把同一轮工具调用合并为一个 Tools 组。
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

// renderTranscriptWithToolGroup 是实际聊天 viewport 使用的渲染路径。
// 每个 user entry 开始一个 turn；该 turn 中的 direct tool transaction 在
// 第一个工具的位置合并成一个 Tools 组，原始 transcript entry 仍保留用于
// 键盘检查、鼠标选择和结果状态更新。
func renderTranscriptWithToolGroup(entries []transcriptEntry, width int, showThinking bool, at time.Time, groupExpanded, fullResult bool) string {
	if len(entries) == 0 {
		return ""
	}
	var renderedTranscript strings.Builder
	hasPrevious := false
	var previousKind entryKind
	for index := 0; index < len(entries); index++ {
		entry := entries[index]
		if entry.kind == entryThinking && !showThinking {
			continue
		}
		if !assistantEntryIsRenderable(entry) {
			continue
		}
		if toolEntryUsesReadyGroup(entries, index) {
			first, _ := toolGroupRange(entries, index)
			if first == index {
				groupEntries := toolEntriesForGroup(entries, index)
				group := renderToolsGroup(groupEntries, width, at, groupExpanded, fullResult)
				appendTranscriptRenderedEntry(&renderedTranscript, group, entryTool, previousKind, hasPrevious)
				if group != "" {
					previousKind = entryTool
					hasPrevious = true
				}
			}
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

func toolGroupRange(entries []transcriptEntry, start int) (int, int) {
	first, last, ok := toolSegmentRange(entries, start)
	if !ok || toolSegmentIsPending(entries, start) {
		return start, start
	}
	return first, last
}

// toolEntriesForGroup returns the tool transaction entries belonging to the
// contiguous group that starts at start. It deliberately stops at the first
// non-tool entry (agent text, thinking, etc.) so interleaved model replies
// split one user turn into separate Tools groups.
func toolEntriesForGroup(entries []transcriptEntry, start int) []transcriptEntry {
	first, last, ok := toolSegmentRange(entries, start)
	if !ok || toolSegmentIsPending(entries, start) {
		return nil
	}
	tools := make([]transcriptEntry, 0, last-first+1)
	for index := first; index <= last; index++ {
		tools = append(tools, entries[index])
	}
	return tools
}

func toolEntriesForTurn(entries []transcriptEntry, start int) []transcriptEntry {
	if start < 0 || start >= len(entries) {
		return nil
	}
	first := start
	for first > 0 && entries[first-1].kind != entryUser {
		first--
	}
	tools := make([]transcriptEntry, 0)
	for index := first; index < len(entries) && entries[index].kind != entryUser; index++ {
		if isToolTransaction(entries[index]) {
			tools = append(tools, entries[index])
		}
	}
	return tools
}

// toolGroupHasRunning reports whether any tool transaction in [first, last]
// is still running. It is used to keep completed tool groups collapsed even
// while a later group in the same turn is active.
func toolGroupHasRunning(entries []transcriptEntry, first, last int) bool {
	if first < 0 || last < first || last >= len(entries) {
		return false
	}
	for index := first; index <= last; index++ {
		if isToolTransaction(entries[index]) && toolEntryStatus(entries[index]) == "running" {
			return true
		}
	}
	return false
}

// toolSegmentRange returns the raw contiguous tool segment containing index.
// Segments stay within a single user turn and break on any non-tool entry.
func toolSegmentRange(entries []transcriptEntry, index int) (int, int, bool) {
	if index < 0 || index >= len(entries) || !isToolTransaction(entries[index]) {
		return index, index, false
	}
	first := index
	for first > 0 && entries[first-1].kind != entryUser && isToolTransaction(entries[first-1]) {
		first--
	}
	last := index
	for last+1 < len(entries) && entries[last+1].kind != entryUser && isToolTransaction(entries[last+1]) {
		last++
	}
	return first, last, true
}

func toolSegmentIsPending(entries []transcriptEntry, index int) bool {
	first, _, ok := toolSegmentRange(entries, index)
	return ok && entries[first].toolGroupPending
}

func toolEntryUsesReadyGroup(entries []transcriptEntry, index int) bool {
	first, _, ok := toolSegmentRange(entries, index)
	return ok && !entries[first].toolGroupPending
}

func toolGroupBorderStyle(entries []transcriptEntry) lipgloss.Style {
	for _, entry := range entries {
		if toolEntryStatus(entry) == "running" {
			return toolCallBorderStyle
		}
	}
	return toolResultBorderStyle
}

func groupedToolBorderStyle(entry transcriptEntry) lipgloss.Style {
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
	return borderStyle
}

func renderToolsGroup(entries []transcriptEntry, width int, at time.Time, expanded, fullResult bool) string {
	if len(entries) == 0 {
		return ""
	}
	status := "ok"
	for _, entry := range entries {
		if toolEntryStatus(entry) == "running" {
			status = "running"
		}
	}
	style := toolGroupBorderStyle(entries)
	marker := "▸"
	if expanded || anyToolGroupOpen(entries) {
		marker = "▾"
	}
	started, finished := time.Time{}, time.Time{}
	for _, entry := range entries {
		if started.IsZero() || (!entry.toolStartedAt.IsZero() && entry.toolStartedAt.Before(started)) {
			started = entry.toolStartedAt
		}
		end := entry.toolFinishedAt
		if toolEntryStatus(entry) == "running" {
			end = at
		}
		if !end.IsZero() && (finished.IsZero() || end.After(finished)) {
			finished = end
		}
	}
	duration := formatToolDuration(started, finished)
	if status == "running" {
		duration = formatToolDuration(started, at)
	}
	label := fmt.Sprintf("%s Tools  %d calls  %s", marker, len(entries), duration)
	// Tool groups share the same two-cell external gutter as user and assistant
	// entries, so the header sits at the same column as every other transcript
	// row. The group itself carries no outer border: expanding the group and
	// then a tool detail would otherwise stack the group rail outside the tool's
	// own rail into two green lines. Only the individual tool entries keep their
	// border rail, which indents them one frame + one padding cell past the
	// header — the same nested shape as todo/assistant content under a header.
	groupWidth := transcriptBodyWidth(width)
	contentWidth := toolEntryContentWidth(groupWidth, style)
	innerWidth := maxInt(1, contentWidth-style.GetHorizontalFrameSize())
	header := toolHeaderStyle.Render(truncateStyledCellLine(label, innerWidth))
	lines := []string{indentLines(header, transcriptEntryGutter)}
	if expanded {
		for _, entry := range entries {
			lines = append(lines, indentLines(renderGroupedToolEntry(entry, innerWidth, at, fullResult), transcriptEntryGutter))
		}
	}
	return strings.Join(lines, "\n")
}

func renderGroupedToolEntry(entry transcriptEntry, width int, at time.Time, fullResult bool) string {
	borderStyle := groupedToolBorderStyle(entry)
	contentWidth := toolEntryContentWidth(width, borderStyle)
	innerWidth := maxInt(1, contentWidth-borderStyle.GetHorizontalFrameSize())
	summary := renderCompactToolSummary(entry, innerWidth, at)
	if entry.toolFocused {
		summary = toolFocusedStyle.Width(innerWidth).Render(summary)
	}
	if toolEntryStatus(entry) == "running" {
		if !isSubagentToolEntry(entry) {
			return borderStyle.Width(contentWidth).Render(summary)
		}
		detail := renderToolInputForDisplay(entry, maxInt(1, innerWidth-2))
		if detail == "" {
			return borderStyle.Width(contentWidth).Render(summary)
		}
		return borderStyle.Width(contentWidth).Render(summary + "\n" + detail)
	}
	if !entry.toolGroupOpen {
		return borderStyle.Width(contentWidth).Render(summary)
	}
	result := toolResultForDisplay(entry)
	if isSubagentToolEntry(entry) {
		result = renderSubagentToolDetail(entry)
	}
	if result == "" {
		result = "(empty result)"
	}
	resultLines := strings.Split(result, "\n")
	if !(fullResult && entry.toolFocused) {
		resultLines = limitRenderedDetailLines(resultLines, maxRenderedToolDetailLines)
	}
	detail := renderToolDetailLinesWithHint(resultLines, maxInt(1, innerWidth-2), entry.toolTarget)
	return borderStyle.Width(contentWidth).Render(summary + "\n" + detail)
}

func renderToolInputForDisplay(entry transcriptEntry, width int) string {
	if len(entry.toolInput) == 0 {
		return ""
	}
	var input struct {
		Prompt      string `json:"prompt"`
		Description string `json:"description,omitempty"`
		ContextMode string `json:"context_mode,omitempty"`
		RunMode     string `json:"run_mode,omitempty"`
	}
	if json.Unmarshal(entry.toolInput, &input) == nil && strings.TrimSpace(input.Prompt) != "" {
		lines := []string{"prompt: " + strings.TrimSpace(input.Prompt)}
		if value := strings.TrimSpace(input.Description); value != "" {
			lines = append(lines, "description: "+value)
		}
		if value := strings.TrimSpace(input.ContextMode); value != "" {
			lines = append(lines, "context: "+value)
		}
		if value := strings.TrimSpace(input.RunMode); value != "" {
			lines = append(lines, "mode: "+value)
		}
		return renderToolDetailLines(lines, width)
	}
	return renderToolDetailLines(strings.Split(strings.TrimSpace(string(entry.toolInput)), "\n"), width)
}

func isSubagentToolEntry(entry transcriptEntry) bool {
	return strings.EqualFold(strings.TrimSpace(toolEntryDisplayName(entry)), "subagent")
}

func renderSubagentToolDetail(entry transcriptEntry) string {
	var payload struct {
		Status         string `json:"status"`
		ID             string `json:"id"`
		Name           string `json:"name"`
		Prompt         string `json:"prompt"`
		Description    string `json:"description"`
		SessionID      string `json:"session_id"`
		TranscriptPath string `json:"transcript_path"`
		OutputPath     string `json:"output_path"`
		Content        string `json:"content"`
		Error          string `json:"error"`
	}
	if err := json.Unmarshal([]byte(entry.toolResult), &payload); err != nil {
		return toolResultForDisplay(entry)
	}
	lines := make([]string, 0, 8)
	if value := strings.TrimSpace(payload.Status); value != "" {
		lines = append(lines, "status: "+value)
	}
	if value := strings.TrimSpace(payload.Name); value != "" {
		lines = append(lines, "agent: "+value)
	}
	if value := strings.TrimSpace(payload.Prompt); value != "" {
		lines = append(lines, "prompt: "+value)
	}
	if value := strings.TrimSpace(payload.Description); value != "" {
		lines = append(lines, "description: "+value)
	}
	if value := strings.TrimSpace(payload.Content); value != "" {
		lines = append(lines, "result: "+value)
	}
	if value := strings.TrimSpace(payload.Error); value != "" {
		lines = append(lines, "error: "+value)
	}
	if value := strings.TrimSpace(payload.TranscriptPath); value != "" {
		lines = append(lines, "transcript: "+value)
	}
	if value := strings.TrimSpace(payload.OutputPath); value != "" {
		lines = append(lines, "output: "+value)
	}
	return strings.Join(lines, "\n")
}

func toolResultForDisplay(entry transcriptEntry) string {
	result := entry.toolResult
	name := toolEntryDisplayName(entry)
	if entry.fileMutationKnown && entry.isFileMutation || (!entry.fileMutationKnown && isFileMutationTool(name)) {
		if _, detail := splitToolSummary(entry.body); strings.TrimSpace(detail) != "" {
			result = detail
		}
	} else if strings.EqualFold(name, "Select") && toolEntryStatus(entry) == "ok" {
		if presentation, ok := parseSelectToolPresentation(entry.toolInput, entry.toolResult); ok {
			result = presentation.detail
		}
	}
	return strings.TrimRight(renderTerminalLinks(sanitizeTerminalText(result)), "\n")
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
	if entry.kind == entryTodo {
		if width <= terminalCellWidth(transcriptEntryGutter) {
			return fitStyledCellLine(renderTodoEntry(entry, width), width)
		}
		return indentLines(renderTodoEntry(entry, transcriptBodyWidth(width)), transcriptEntryGutter)
	}
	// SubagentWait 状态行：渲染为单行状态文字（如
	// "子智能体 高松灯 正在运行 13s"），没有工具块边框、不可折叠。
	if entry.subagentWaitRunning {
		body := sanitizeTerminalText(entry.body)
		if strings.TrimSpace(body) == "" {
			return ""
		}
		return indentLines(subagentWaitStyle.Render(body), transcriptEntryGutter)
	}
	bodyWidth := transcriptBodyWidth(width)
	// 结构化 <task> 完成块：整块渲染为状态色框线卡片，不显示 "subagent" 标签
	// （卡片标题行自带 ✓/✗/■ 状态标记与名称）。
	if entry.kind == entrySystem && isTaskCompletionBlock(entry.body) {
		card := renderTaskCompletionCard(entry.body, bodyWidth)
		if card == "" {
			return ""
		}
		return indentLines(card, transcriptEntryGutter)
	}
	body := renderEntryBodyAt(entry, bodyWidth, at)
	if entry.kind == entryAssistant && entry.turnMetadata != nil {
		footer := contextFreeStyle.Render(formatTurnFooter(*entry.turnMetadata))
		if body == "" {
			body = footer
		} else {
			body += "\n" + footer
		}
	}
	if entry.kind == entryTool {
		if body == "" {
			return ""
		}
		return indentLines(body, transcriptEntryGutter)
	}
	if body == "" {
		return ""
	}
	if entry.kind == entryUser {
		// 用户消息与 assistant 共用两列外部 gutter：assistant 首行显示 ✦，
		// 用户消息保留空 gutter，正文从同一列开始，避免两类消息视觉错位。
		return userTranscriptRowStyle.Width(width).Render(indentLines(body, transcriptEntryGutter))
	}
	if entry.kind == entryAssistant {
		// ✦ 位于 transcript 左侧外部 gutter。首行用 marker 占据 gutter，
		// 后续行用等宽空格延续同一正文列，避免模型回答失去原有缩进。
		return indentAssistantGutter(body)
	}
	title := displayEntryTitle(entry)
	color := sanitizeTerminalText(entry.color)
	var label string
	if color != "" {
		label = lipgloss.NewStyle().Foreground(lipgloss.Color(color)).Bold(true).Render(title)
	} else {
		label = labelStyle(entry.kind).Render(title)
	}
	if body == "" {
		return transcriptEntryGutter + label
	}
	return indentLines(label+"\n"+body, transcriptEntryGutter)
}

func displayEntryTitle(entry transcriptEntry) string {
	switch entry.kind {
	case entryUser, entryAssistant:
		return ""
	case entryThinking:
		return "thinking >"
	default:
		return sanitizeTerminalText(entry.title)
	}
}

func transcriptBodyWidth(width int) int {
	return maxInt(1, width-terminalCellWidth(transcriptEntryGutter))
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

// ---- <task> 结构化完成块渲染 ----

// taskBlockInfo 是从父会话注入/系统事件推送的 <task> 块解析出的展示信息。
type taskBlockInfo struct {
	ID         string
	State      string
	Name       string
	DurationMS int64
	OutputSize int64
	Summary    string
	Error      string
	Transcript string
	Output     string
}

// taskBlockAttrPattern 匹配 <task ...> 首行中的 XML 属性。
var taskBlockAttrPattern = regexp.MustCompile(`([a-z_]+)="([^"]*)"`)

func isTaskCompletionBlock(body string) bool {
	trimmed := strings.TrimSpace(body)
	return strings.HasPrefix(trimmed, "<task ") && strings.HasSuffix(trimmed, "</task>")
}

func parseTaskCompletionBlock(body string) (taskBlockInfo, bool) {
	trimmed := strings.TrimSpace(body)
	if !isTaskCompletionBlock(trimmed) {
		return taskBlockInfo{}, false
	}
	headerEnd := strings.IndexByte(trimmed, '>')
	if headerEnd < 0 {
		return taskBlockInfo{}, false
	}
	info := taskBlockInfo{}
	for _, match := range taskBlockAttrPattern.FindAllStringSubmatch(trimmed[:headerEnd], -1) {
		value := unescapeTaskBlockAttr(match[2])
		switch match[1] {
		case "id":
			info.ID = value
		case "state":
			info.State = value
		case "name":
			info.Name = value
		case "duration_ms":
			info.DurationMS, _ = strconv.ParseInt(value, 10, 64)
		case "output_size":
			info.OutputSize, _ = strconv.ParseInt(value, 10, 64)
		}
	}
	for _, line := range strings.Split(strings.TrimSuffix(trimmed[headerEnd+1:], "</task>"), "\n") {
		line = strings.TrimRight(line, "\r")
		switch {
		case strings.HasPrefix(line, "summary: "):
			info.Summary = strings.TrimPrefix(line, "summary: ")
		case strings.HasPrefix(line, "error: "):
			info.Error = strings.TrimPrefix(line, "error: ")
		case strings.HasPrefix(line, "transcript: "):
			info.Transcript = strings.TrimPrefix(line, "transcript: ")
		case strings.HasPrefix(line, "output: "):
			info.Output = strings.TrimPrefix(line, "output: ")
		}
	}
	return info, true
}

func unescapeTaskBlockAttr(value string) string {
	value = strings.ReplaceAll(value, "&quot;", `"`)
	value = strings.ReplaceAll(value, "&lt;", "<")
	value = strings.ReplaceAll(value, "&gt;", ">")
	value = strings.ReplaceAll(value, "&amp;", "&")
	return value
}

// renderTaskCompletionCard 把 <task> 完成块渲染为状态色框线卡片：
//
//	┌ ✓ 名称 完成 ───────┐
//	│ 短id · 42s · 12.4KB │
//	│ 摘要: ...           │
//	└─────────────────────┘
func renderTaskCompletionCard(body string, width int) string {
	info, ok := parseTaskCompletionBlock(body)
	if !ok {
		return bodyStyle.Width(width).Render(sanitizeTerminalText(body))
	}
	state := info.State
	if state != "completed" && state != "failed" && state != "stopped" && state != "interrupted" {
		state = "completed"
	}
	borderColor := colorManager.LipglossColor(colorWorktreeClean)
	marker := "✓"
	stateText := "完成"
	switch state {
	case "failed":
		borderColor = colorManager.LipglossColor(colorLabelError)
		marker = "✗"
		stateText = "失败"
	case "stopped":
		borderColor = colorManager.LipglossColor(colorContextCache)
		marker = "■"
		stateText = "已停止"
	case "interrupted":
		borderColor = colorManager.LipglossColor(colorWorktreeDirty)
		marker = "!"
		stateText = "已中断"
	}
	name := strings.TrimSpace(info.Name)
	if name == "" {
		name = "subagent"
	}
	title := lipgloss.NewStyle().Foreground(borderColor).Bold(true).Render(marker + " " + name + " " + stateText)

	lines := []string{title}
	if meta := taskBlockMetaLine(info); meta != "" {
		lines = append(lines, meta)
	}
	if value := strings.TrimSpace(info.Error); value != "" {
		lines = append(lines, "错误: "+value)
	}
	if value := strings.TrimSpace(info.Summary); value != "" {
		lines = append(lines, "摘要: "+value)
	}
	if value := strings.TrimSpace(info.Output); value != "" {
		lines = append(lines, "输出: "+value)
	}

	cardStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(borderColor).
		Padding(0, 1)
	// lipgloss 的 Width 含 padding 但不含 border：styleWidth 只减边框，
	// body 再 fit 到减去 padding 后的宽度，最终总宽精确等于 width。
	horizontalBorder := cardStyle.GetHorizontalBorderSize()
	styleWidth := maxInt(1, width-horizontalBorder)
	bodyWidth := maxInt(1, styleWidth-cardStyle.GetHorizontalPadding())
	content := make([]string, 0, len(lines))
	for _, line := range lines {
		content = append(content, fitStyledCellLine(truncateStyledCellLine(line, bodyWidth), bodyWidth))
	}
	return cardStyle.Width(styleWidth).Render(strings.Join(content, "\n"))
}

// taskBlockMetaLine 渲染完成块卡片的 meta 行（时长 · 输出大小）。
// 刻意不显示任务 id / session id。
func taskBlockMetaLine(info taskBlockInfo) string {
	parts := make([]string, 0, 2)
	if info.DurationMS > 0 {
		parts = append(parts, formatTaskBlockDuration(info.DurationMS))
	}
	if info.OutputSize > 0 {
		parts = append(parts, formatTaskBlockSize(info.OutputSize))
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, " · ")
}

func formatTaskBlockDuration(ms int64) string {
	if ms <= 0 {
		return ""
	}
	duration := time.Duration(ms) * time.Millisecond
	if duration < time.Second {
		return fmt.Sprintf("%dms", ms)
	}
	return formatWholeSeconds(int(duration / time.Second))
}

func formatTaskBlockSize(bytes int64) string {
	if bytes < 1024 {
		return fmt.Sprintf("%dB", bytes)
	}
	if bytes < 1024*1024 {
		return trimDurationTrailingZeros(fmt.Sprintf("%.1fKB", float64(bytes)/1024))
	}
	return trimDurationTrailingZeros(fmt.Sprintf("%.1fMB", float64(bytes)/(1024*1024)))
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
		// Always render as Markdown; unclosed markers stay literal during streaming.
		return renderAssistantBodyWithCitations(body, width, entry.citations)
	}
	if body == "" {
		return ""
	}
	if entry.kind == entryUser && len(entry.inputTokens) > 0 {
		return renderTokenizedTranscriptBody(body, entry.inputTokens, width)
	}
	if entry.kind == entryUser {
		// Apply the user foreground at the innermost text layer. The outer row
		// style supplies background and width, but cannot override an ANSI
		// foreground already emitted by a nested style.
		return userTranscriptRowStyle.UnsetBackground().Width(width).Render(body)
	}
	if entry.kind != entryUser {
		body = renderTerminalLinks(body)
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

	result := entry.toolResult
	name := toolEntryDisplayName(entry)
	if status == "ok" && ((entry.fileMutationKnown && entry.isFileMutation) || (!entry.fileMutationKnown && isFileMutationTool(name))) {
		if _, detail := splitToolSummary(entry.body); strings.TrimSpace(detail) != "" {
			result = detail
		}
	} else if strings.EqualFold(name, "Select") && status == "ok" {
		if presentation, ok := parseSelectToolPresentation(entry.toolInput, entry.toolResult); ok {
			result = presentation.detail
		}
	}
	result = renderTerminalLinks(sanitizeTerminalText(result))
	result = strings.TrimRight(result, "\n")
	if result == "" {
		result = "(empty result)"
	}
	detailLines := strings.Split(result, "\n")
	detailWidth := maxInt(1, innerWidth-2)
	detail := renderToolDetailLinesWithHint(detailLines, detailWidth, entry.toolTarget)
	return borderStyle.Width(contentWidth).Render(summary + "\n" + detail)
}

func renderCompactToolSummary(entry transcriptEntry, width int, at time.Time) string {
	width = maxInt(1, width)
	status := toolEntryStatus(entry)
	icon := "✓"
	iconStyle := toolCitationOKStyle
	nameStyle := toolHeaderStyle
	targetStyle := toolCitationStyle
	switch status {
	case "running":
		icon = "◌"
		iconStyle = toolHeaderStyle
	case "error":
		icon = "×"
		iconStyle = toolCitationErrorStyle
	}
	if entry.toolHovered && !entry.toolFocused {
		iconStyle = iconStyle.Underline(true)
		nameStyle = nameStyle.Underline(true)
		targetStyle = targetStyle.Underline(true)
	}

	name := strings.Join(strings.Fields(sanitizeTerminalText(displayToolName(toolEntryDisplayName(entry)))), " ")
	target := strings.Join(strings.Fields(sanitizeTerminalText(entry.toolTarget)), " ")
	if name == "" {
		name = "tool"
	}

	focusPrefix := ""
	if entry.toolFocused {
		focusPrefix = "› "
	}
	iconText := icon + " "
	duration := ""
	if status == "running" {
		duration = formatRunningToolElapsed(entry.toolStartedAt, at)
	} else if !entry.toolFinishedAt.IsZero() {
		duration = formatToolDuration(entry.toolStartedAt, entry.toolFinishedAt)
	}
	statusStyle := toolStatusStyle(status)
	if entry.toolHovered && !entry.toolFocused {
		statusStyle = statusStyle.Underline(true)
	}
	statusSeparator := "  "
	statusAvailable := width - terminalCellWidth(focusPrefix) - terminalCellWidth(iconText) - terminalCellWidth(statusSeparator)
	if statusAvailable < terminalCellWidth(toolStatusLabel(status)) {
		iconText = icon
		statusSeparator = "  "
		statusAvailable = width - terminalCellWidth(focusPrefix) - terminalCellWidth(iconText) - terminalCellWidth(statusSeparator)
	}
	statusChip := toolStatusChipWithinWidth(status, duration, maxInt(1, statusAvailable), statusStyle)

	nameWidth := terminalCellWidth(name)
	fixedWithoutTarget := terminalCellWidth(focusPrefix) + terminalCellWidth(iconText) + terminalCellWidth(statusSeparator) + terminalCellWidth(statusChip)
	if fixedWithoutTarget+nameWidth > width {
		nameBudget := width - fixedWithoutTarget
		if nameBudget >= 1 {
			name = truncateStyledCellLine(name, nameBudget)
		} else {
			name = ""
		}
		nameWidth = terminalCellWidth(name)
	}
	targetText := ""
	if target != "" {
		targetBudget := width - fixedWithoutTarget - nameWidth - 1
		if targetBudget >= 1 {
			targetText = truncateStyledCellLine(target, targetBudget)
		}
	}

	var line strings.Builder
	line.WriteString(focusPrefix)
	line.WriteString(iconStyle.Render(iconText))
	line.WriteString(nameStyle.Render(name))
	if targetText != "" {
		separator := "  "
		if strings.HasSuffix(name, ":") {
			separator = " "
		}
		line.WriteString(targetStyle.Render(separator + targetText))
	}
	line.WriteString(statusSeparator)
	line.WriteString(statusChip)
	return truncateStyledCells(line.String(), width, "")
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

func newToolCallCitation(toolUseID, name string, input json.RawMessage, workspaceRoot string) toolCitation {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "tool"
	}
	display := buildToolDisplay(name, input, workspaceRoot)
	return toolCitation{
		toolUseID: strings.TrimSpace(toolUseID),
		name:      display.name,
		target:    display.target,
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
		name:      displayToolName(name),
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
	lineWidth := terminalCellWidth("["+name+"] ") + terminalCellWidth(toolCitationStatusText(cite))
	if target == "" {
		return key + " " + status
	}
	targetWidth := maxInt(1, width-lineWidth-terminalCellWidth("  "))
	return key + "  " + status + toolCitationStyle.Render("  "+truncateStyledCellLine(target, targetWidth))
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
	return renderToolDetailLinesWithHint(lines, width, "")
}

func omitLeadingToolTargetLine(lines []string, hint string) []string {
	if len(lines) == 0 || strings.TrimSpace(hint) == "" {
		return lines
	}
	target := strings.TrimSpace(sanitizeTerminalText(hint))
	if target == "" || isNumberedDiffLine(lines[0]) || strings.TrimSpace(lines[0]) != target {
		return lines
	}
	return lines[1:]
}

func renderToolDetailLinesWithHint(lines []string, width int, hint string) string {
	lines = omitLeadingToolTargetLine(lines, hint)
	containsUnifiedDiffHunk := hasUnifiedDiffHunk(lines)
	diffRowWidth := diffDetailRowsWidth(lines, width)
	language := syntaxLanguageFromText(strings.Join(lines, "\n"), hint)
	if language == "" {
		language = syntaxLanguageFromLines(lines)
	}
	rendered := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		style := toolDetailStyle
		// Keep the detail gutter separate from the tool output itself. Leading
		// whitespace is meaningful in file contents and command output (for
		// example Read, sed, and go test), so use trimmed text only for line
		// classification and remove trailing whitespace only for display.
		renderedLine := "  " + strings.TrimRight(line, " \t\r")
		isDiffLine := false
		switch {
		case containsUnifiedDiffHunk && isUnifiedDiffMetadataLine(trimmed):
			style = toolCitationStyle
		case diffDetailLineMarker(line, containsUnifiedDiffHunk) == "+":
			style = lipgloss.NewStyle().
				Foreground(colorManager.LipglossColor(colorDiffAddedForeground)).
				Background(colorManager.LipglossColor(colorDiffAddedBackground)).
				Bold(true)
			renderedLine = strings.TrimRight(line, " \t\r")
			isDiffLine = true
		case diffDetailLineMarker(line, containsUnifiedDiffHunk) == "-":
			style = lipgloss.NewStyle().
				Foreground(colorManager.LipglossColor(colorDiffDeletedForeground)).
				Background(colorManager.LipglossColor(colorDiffDeletedBackground)).
				Bold(true)
			renderedLine = strings.TrimRight(line, " \t\r")
			isDiffLine = true
		case isNumberedDiffLine(line):
			// Diff preview context lines already contain their alignment prefix.
			// Do not add the normal detail indentation to them, otherwise their
			// line number and separator drift relative to +/- lines.
			renderedLine = strings.TrimRight(line, " \t\r")
		case containsUnifiedDiffHunk && strings.HasPrefix(line, " "):
			// A leading space in a unified diff is the context marker, not source
			// indentation. Replace that marker with the normal detail gutter while
			// preserving any whitespace that follows it.
			renderedLine = "  " + strings.TrimRight(strings.TrimPrefix(line, " "), " \t\r")
		}
		if isDiffLine || isNumberedDiffLine(line) {
			renderedLine = fitStyledCellLine(truncateStyledCellLine(renderedLine, diffRowWidth), diffRowWidth)
			rendered = append(rendered, style.Render(highlightToolDetailLineWithBase(renderedLine, language, style)))
			continue
		}
		renderedLine = truncateStyledCellLine(renderedLine, width)
		rendered = append(rendered, style.Width(width).Render(highlightToolDetailLineWithBase(renderedLine, language, style)))
	}
	return strings.Join(rendered, "\n")
}

func diffDetailRowsWidth(_ []string, width int) int {
	return maxInt(1, width)
}

func isNumberedDiffLine(line string) bool {
	fields := strings.Fields(line)
	if len(fields) < 2 || !isDecimalNumber(fields[0]) {
		return false
	}
	if fields[1] == "│" {
		return true
	}
	return len(fields) >= 3 && (fields[1] == "+" || fields[1] == "-") && fields[2] == "│"
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

// indentAssistantGutter 将 assistant 的首行 marker 放在外部 gutter，并让后续
// 行使用等宽 gutter 延续正文列。marker 和分隔空格共占两列，正文不会因为
// 去掉 agent 标签而左移；body 内部的 ANSI/Markdown 样式保持不变。
func indentAssistantGutter(body string) string {
	lines := strings.Split(body, "\n")
	if len(lines) == 0 {
		return ""
	}
	lines[0] = assistantMarkerStyle.Render("✦") + " " + lines[0]
	for i := 1; i < len(lines); i++ {
		lines[i] = transcriptEntryGutter + lines[i]
	}
	return strings.Join(lines, "\n")
}

// indentLines 给多行文本逐行添加固定前缀。
func indentLines(text, prefix string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = prefix + line
	}
	return strings.Join(lines, "\n")
}
