// 本文件定义从右侧 Subagents 卡片选择并预览子会话 transcript 的交互。
package bubble

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"paw/internal/message"
	"paw/internal/session"
	"paw/internal/subagent"
	"paw/internal/tokentracer"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func newSubagentPicker(tasks []subagent.TaskSnapshot) *subagentPicker {
	return &subagentPicker{
		tasks: append([]subagent.TaskSnapshot(nil), tasks...),
		tab:   activityTabSubagents,
	}
}

func (m appModel) openSubagentPicker() (tea.Model, tea.Cmd) {
	m.openActivity(activityTabSubagents)
	return m, nil
}

func (m appModel) openPipelinePicker() (tea.Model, tea.Cmd) {
	m.openActivity(activityTabPipeline)
	return m, nil
}

func (m *appModel) openActivity(tab activityTab) {
	if m == nil {
		return
	}
	m.subagentPicker = newSubagentPicker(m.subagentTasks())
	m.subagentPicker.tab = tab
	m.sessionPicker = nil
	m.modelWizard = nil
	m.settingWizard = nil
	m.clearCompletionAndRelayout()
	m.relayout()
}

func (m appModel) handleSubagentPickerKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.subagentPicker == nil {
		return m, nil
	}

	switch msg.String() {
	case "ctrl+c", "ctrl+g", "esc":
		m.subagentPicker = nil
		m.relayout()
		return m, m.input.Focus()
	case "tab", "right", "l":
		m.subagentPicker.tab = activityTabPipeline
		return m, nil
	case "shift+tab", "left", "h":
		m.subagentPicker.tab = activityTabSubagents
		return m, nil
	case "up", "k":
		if m.subagentPicker.tab != activityTabSubagents {
			return m, nil
		}
		if m.subagentPicker.selectedIndex > 0 {
			m.subagentPicker.selectedIndex--
		}
		return m, nil
	case "down", "j":
		if m.subagentPicker.tab != activityTabSubagents {
			return m, nil
		}
		if m.subagentPicker.selectedIndex < len(m.subagentPicker.tasks)-1 {
			m.subagentPicker.selectedIndex++
		}
		return m, nil
	case "enter":
		if m.subagentPicker.tab != activityTabSubagents {
			return m, nil
		}
		if len(m.subagentPicker.tasks) == 0 {
			return m, nil
		}
		task := m.subagentPicker.tasks[m.subagentPicker.selectedIndex]
		return m.previewSubagentTranscript(task)
	}
	return m, nil
}

func (m *appModel) refreshActivityTasks() {
	if m == nil || m.subagentPicker == nil {
		return
	}
	m.subagentPicker.tasks = append(m.subagentPicker.tasks[:0], m.subagentTasks()...)
	if len(m.subagentPicker.tasks) == 0 {
		m.subagentPicker.selectedIndex = 0
		return
	}
	m.subagentPicker.selectedIndex = clampInt(m.subagentPicker.selectedIndex, 0, len(m.subagentPicker.tasks)-1)
}

func (m appModel) previewSubagentTranscript(task subagent.TaskSnapshot) (tea.Model, tea.Cmd) {
	sessionID := strings.TrimSpace(task.SessionID)
	if sessionID == "" {
		sessionID = strings.TrimSpace(task.ID)
	}
	if sessionID == "" {
		m.addEntry(transcriptEntry{kind: entryError, title: "subagent", body: "selected subagent has no session id"})
		return m, nil
	}

	parentSessionID := m.sessionID
	parentTranscript := copyTranscriptEntries(m.transcript)
	if m.subagentPreview != nil {
		parentSessionID = m.subagentPreview.parentSessionID
		parentTranscript = copyTranscriptEntries(m.subagentPreview.parentTranscript)
	}

	preview := &subagentTranscriptPreview{
		task:             task,
		sessionID:        sessionID,
		parentSessionID:  parentSessionID,
		parentTranscript: parentTranscript,
		liveContent:      liveSubagentContent(task),
	}
	return m, loadSubagentTranscriptPreviewCmd(m.ctx, task, sessionID, preview, m.workspaceRoot)
}

func loadSubagentTranscriptPreviewCmd(ctx context.Context, task subagent.TaskSnapshot, sessionID string, preview *subagentTranscriptPreview, workspaceRoot string) tea.Cmd {
	return func() tea.Msg {
		entries, err := loadSubagentTranscriptEntries(ctx, task, time.Now(), workspaceRoot)
		if err != nil {
			return sessionRestoredMsg{source: sessionRestoreSubagentEnter, subagentPreview: preview, err: err}
		}
		return sessionRestoredMsg{
			sessionID:       sessionID,
			entries:         entries,
			source:          sessionRestoreSubagentEnter,
			subagentPreview: preview,
		}
	}
}

func loadSubagentTranscriptEntries(ctx context.Context, task subagent.TaskSnapshot, at time.Time, workspaceRoot string) ([]transcriptEntry, error) {
	path := strings.TrimSpace(task.TranscriptPath)
	if path == "" {
		if content := strings.TrimSpace(task.Content); content != "" {
			return []transcriptEntry{{kind: entryAssistant, title: "assistant", body: content, createdAt: at}}, nil
		}
		return nil, fmt.Errorf("selected subagent has no transcript path")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var entries []transcriptEntry
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return entries, err
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var record session.Record
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			continue
		}
		createdAt := record.CreatedAt
		if createdAt.IsZero() {
			createdAt = at
		}
		entries = append(entries, transcriptEntriesFromMessage(record.Message, createdAt, workspaceRoot)...)
	}
	if err := scanner.Err(); err != nil {
		return entries, err
	}
	if len(entries) == 0 {
		if content := strings.TrimSpace(task.Content); content != "" {
			entries = append(entries, transcriptEntry{kind: entryAssistant, title: "assistant", body: content, createdAt: at})
		}
	}
	return mergeTranscriptToolEntries(entries), nil
}

func transcriptEntriesFromMessage(msg message.Message, createdAt time.Time, workspaceRoot string) []transcriptEntry {
	var entries []transcriptEntry
	if content := strings.TrimSpace(msg.Content); content != "" {
		switch msg.Role {
		case message.RoleUser:
			entries = append(entries, transcriptEntry{
				kind:        entryUser,
				title:       "you",
				body:        content,
				inputTokens: inputTokensFromMessage(msg),
				createdAt:   createdAt,
			})
		case message.RoleSystem:
			entries = append(entries, transcriptEntry{kind: entrySystem, title: "system", body: content, createdAt: createdAt})
		default:
			entries = append(entries, transcriptEntry{kind: entryAssistant, title: "assistant", body: content, createdAt: createdAt})
		}
	}
	for _, call := range appendToolCalls(msg) {
		name := strings.TrimSpace(call.Name)
		if name == "" {
			name = "tool"
		}
		target := displayToolTarget(name, call.Input, workspaceRoot)
		if selectTarget, ok := selectToolCallTarget(name, call.Input); ok {
			target = selectTarget
		}
		entries = append(entries, transcriptEntry{
			kind:       entryTool,
			title:      "tool",
			body:       formatRunningToolCallBody(name, call.Input, ""),
			toolUseID:  strings.TrimSpace(call.ID),
			toolName:   name,
			toolStatus: "running",
			toolTarget: target,
			toolInput:  append(json.RawMessage(nil), call.Input...),
			createdAt:  createdAt,
		})
	}
	for _, result := range appendToolResults(msg) {
		status := "ok"
		if result.IsError {
			status = "error"
		}
		entries = append(entries, transcriptEntry{
			kind:           entryTool,
			title:          "tool",
			body:           formatToolResultBody("tool", status, ""),
			isError:        result.IsError,
			toolUseID:      strings.TrimSpace(result.ToolUseID),
			toolName:       "tool",
			toolStatus:     status,
			toolResult:     result.Content,
			toolExpanded:   false,
			toolResultOnly: true,
			createdAt:      createdAt,
		})
	}
	return entries
}

// mergeTranscriptToolEntries 将历史中的调用和结果合并成一个仅供 Bubble 展示的事务条目。
func mergeTranscriptToolEntries(entries []transcriptEntry) []transcriptEntry {
	out := make([]transcriptEntry, 0, len(entries))
	pendingByID := make(map[string]int)
	for _, original := range entries {
		entry := original
		if !isToolTransaction(entry) {
			out = append(out, entry)
			continue
		}
		entry.toolStatus = toolEntryStatus(entry)
		entry.toolName = toolEntryDisplayName(entry)
		entry.toolFocused = false
		entry.toolHovered = false

		if entry.toolResultOnly {
			matchIndex := -1
			if entry.toolUseID != "" {
				if candidate, ok := pendingByID[entry.toolUseID]; ok {
					matchIndex = candidate
				}
			}
			if matchIndex < 0 && entry.toolUseID == "" && entry.toolName != "" && entry.toolName != "tool" {
				for index := len(out) - 1; index >= 0; index-- {
					if toolEntryStatus(out[index]) == "running" && strings.EqualFold(out[index].toolName, entry.toolName) {
						matchIndex = index
						break
					}
				}
			}
			if matchIndex >= 0 && matchIndex < len(out) {
				call := &out[matchIndex]
				call.toolStatus = entry.toolStatus
				call.toolResult = entry.toolResult
				call.isError = entry.isError
				call.toolExpanded = false
				call.toolGroupPending = false
				call.toolGroupOpen = false
				call.toolResultOnly = false
				call.body = completeToolCallBody(call.toolName, call.body, entry.toolStatus, entry.toolResult)
				if strings.EqualFold(call.toolName, "Select") && strings.EqualFold(call.toolStatus, "ok") {
					if presentation, ok := parseSelectToolPresentation(call.toolInput, call.toolResult); ok {
						call.toolTarget = presentation.target
					}
				}
				touchTranscriptEntry(call)
				delete(pendingByID, entry.toolUseID)
				continue
			}
			entry.toolExpanded = false
			entry.toolGroupPending = false
			entry.toolGroupOpen = false
			out = append(out, entry)
			continue
		}

		entry.toolExpanded = false
		entry.toolGroupPending = false
		entry.toolGroupOpen = false
		out = append(out, entry)
		if entry.toolStatus == "running" && entry.toolUseID != "" {
			pendingByID[entry.toolUseID] = len(out) - 1
		}
	}
	return out
}

func appendToolCalls(msg message.Message) []message.ToolCall {
	calls := make([]message.ToolCall, 0, len(msg.ToolUses)+1)
	if msg.ToolUse != nil {
		calls = append(calls, *msg.ToolUse)
	}
	calls = append(calls, msg.ToolUses...)
	return calls
}

func appendToolResults(msg message.Message) []message.ToolResult {
	results := make([]message.ToolResult, 0, len(msg.ToolResults)+1)
	if msg.ToolResult != nil {
		results = append(results, *msg.ToolResult)
	}
	results = append(results, msg.ToolResults...)
	return results
}

func decorateSubagentTranscript(task subagent.TaskSnapshot, entries []transcriptEntry, createdAt time.Time) []transcriptEntry {
	header := transcriptEntry{
		kind:      entrySystem,
		title:     "subagent",
		body:      fmt.Sprintf("viewing %s  session %s", taskDisplayName(task), shortTaskID(firstNonEmptyString(task.SessionID, task.ID))),
		createdAt: createdAt,
	}
	out := make([]transcriptEntry, 0, len(entries)+1)
	out = append(out, header)
	out = append(out, entries...)
	return out
}

func renderSubagentPreviewTranscript(preview *subagentTranscriptPreview, createdAt time.Time) []transcriptEntry {
	if preview == nil {
		return nil
	}
	entries := copyTranscriptEntries(preview.entries)
	if live := strings.TrimSpace(preview.liveContent); live != "" {
		entries = append(entries, transcriptEntry{
			kind:      entryAssistant,
			title:     "assistant",
			body:      live,
			createdAt: createdAt,
		})
	}
	return decorateSubagentTranscript(preview.task, entries, createdAt)
}

func (m *appModel) refreshSubagentToolEntriesFromTasks() bool {
	if m == nil || m.subagents == nil {
		return false
	}
	tasks := m.subagents.ListTasks()
	if len(tasks) == 0 {
		return false
	}
	changed := false
	for index := range m.transcript {
		entry := &m.transcript[index]
		if !isToolTransaction(*entry) || !isSubagentToolEntry(*entry) || strings.TrimSpace(entry.toolResult) == "" {
			continue
		}
		var reference struct {
			ID        string `json:"id"`
			SessionID string `json:"session_id"`
		}
		if json.Unmarshal([]byte(entry.toolResult), &reference) != nil {
			continue
		}
		for _, task := range tasks {
			if reference.ID == "" || (task.ID != reference.ID && task.SessionID != reference.SessionID) {
				continue
			}
			data, err := json.Marshal(task)
			if err != nil {
				break
			}
			status := "ok"
			if task.Status == subagent.TaskRunning {
				status = "running"
			}
			isError := strings.TrimSpace(task.Error) != "" || task.Status == subagent.TaskFailed
			if isError {
				status = "error"
			}
			if string(data) != entry.toolResult || entry.toolStatus != status || entry.isError != isError {
				entry.toolResult = string(data)
				entry.toolStatus = status
				entry.isError = isError
				entry.body = completeToolCallBody(entry.toolName, entry.body, status, entry.toolResult)
				if status != "running" {
					entry.toolFinishedAt = m.animationNow()
				}
				touchTranscriptEntry(entry)
				changed = true
			}
			break
		}
	}
	if changed {
		m.transcriptRenderCache = nil
		m.refreshViewport()
	}
	return changed
}
func (m *appModel) refreshSubagentPreviewFromTasks() bool {
	if m == nil || m.subagentPreview == nil || m.subagents == nil {
		return false
	}
	task, ok := m.findSubagentPreviewTask()
	if !ok {
		return false
	}
	live := liveSubagentContent(task)
	if task.Status == m.subagentPreview.task.Status &&
		task.Content == m.subagentPreview.task.Content &&
		task.UsedTokens == m.subagentPreview.task.UsedTokens &&
		taskUsageEqual(task.Usage, m.subagentPreview.task.Usage) &&
		live == m.subagentPreview.liveContent {
		return false
	}
	m.subagentPreview.task = task
	m.subagentPreview.liveContent = live
	m.resetToolInspect()
	m.transcript = renderSubagentPreviewTranscript(m.subagentPreview, m.animationNow())
	m.refreshViewport()
	return true
}

func (m appModel) findSubagentPreviewTask() (subagent.TaskSnapshot, bool) {
	if m.subagentPreview == nil || m.subagents == nil {
		return subagent.TaskSnapshot{}, false
	}
	sessionID := strings.TrimSpace(m.subagentPreview.sessionID)
	if sessionID == "" {
		sessionID = strings.TrimSpace(m.subagentPreview.task.SessionID)
	}
	taskID := strings.TrimSpace(m.subagentPreview.task.ID)
	for _, task := range m.subagents.ListTasks() {
		if sessionID != "" && strings.TrimSpace(task.SessionID) == sessionID {
			return task, true
		}
		if taskID != "" && strings.TrimSpace(task.ID) == taskID {
			return task, true
		}
	}
	return subagent.TaskSnapshot{}, false
}

func liveSubagentContent(task subagent.TaskSnapshot) string {
	if task.Status != subagent.TaskRunning {
		return ""
	}
	return strings.TrimSpace(task.Content)
}

func taskUsageEqual(a, b *tokentracer.Usage) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func copyTranscriptEntries(entries []transcriptEntry) []transcriptEntry {
	out := append([]transcriptEntry(nil), entries...)
	for i := range out {
		out[i].citations = append([]toolCitation(nil), out[i].citations...)
		out[i].inputTokens = cloneInputTokens(out[i].inputTokens)
		if out[i].todoSnapshot != nil {
			snapshot := out[i].todoSnapshot.Clone()
			out[i].todoSnapshot = &snapshot
		}
		out[i].toolFocused = false
		out[i].toolHovered = false
	}
	return out
}

// finalizeRestoredRunningTools 把历史恢复后仍然处于 running 的孤儿工具调用
// 收尾为 error（等价于实时流程 markRunningToolsError 的处理）。历史记录里
// 没有对应结果的调用只可能来自被中断的上一轮；保持 running 会让工具组按
// 折叠态渲染（running 组使用全局 toolGroupExpanded=false），而 toggleToolExpansion
// 拒绝切换 running 事务，于是点击\"无法展开\"。收尾成 error 后组内不再有
// running，折叠态改由首个条目的 toolExpanded 决定，点击即可正常展开。
func (m *appModel) finalizeRestoredRunningTools() {
	if m == nil {
		return
	}
	if finalizeRestoredRunningToolEntries(m.transcript, m.animationNow()) {
		m.lastToolProgressSecond = -1
	}
}

func finalizeRestoredRunningToolEntries(entries []transcriptEntry, now time.Time) bool {
	changed := false
	for index := range entries {
		entry := &entries[index]
		if !isToolTransaction(*entry) || toolEntryStatus(*entry) != "running" {
			continue
		}
		entry.body = completeRunningToolCallBody(entry.body, "error")
		entry.toolStatus = "error"
		entry.isError = true
		entry.toolResult = "interrupted: previous turn ended before completion"
		entry.toolExpanded = false
		entry.toolGroupPending = false
		entry.toolGroupOpen = false
		entry.toolFinishedAt = now
		touchTranscriptEntry(entry)
		changed = true
	}
	return changed
}

func (m *appModel) applySessionPickerRestore(msg sessionRestoredMsg) {
	m.resetToolInspect()
	m.clearNewMessageNotice()
	m.sessionID = msg.sessionID
	m.sessionPicker = nil
	m.subagentPicker = nil
	m.subagentPreview = nil
	m.todoPage = nil
	m.syncInputPlaceholder()
	m.transcript = mergeTranscriptToolEntries(copyTranscriptEntries(msg.entries))
	m.finalizeRestoredRunningTools()
	m.currentTodo = msg.currentTodo.Clone()
	m.hasCurrentTodo = msg.hasCurrentTodo
	m.todoWasCleared = msg.todoWasCleared
	m.latestTodoIndex = msg.latestTodoIndex
	if m.latestTodoIndex < 0 || m.latestTodoIndex >= len(m.transcript) || m.transcript[m.latestTodoIndex].kind != entryTodo {
		projection := todoProjectionFromEntries(m.transcript)
		m.currentTodo = projection.Current.Clone()
		m.hasCurrentTodo = projection.HasCurrent
		m.todoWasCleared = projection.WasCleared
		m.latestTodoIndex = projection.LatestIndex
	}
	m.inputHistory = inputHistoryFromTranscript(msg.entries)
	m.resetHistoryNavigation()
	m.addEntry(transcriptEntry{kind: entrySystem, title: "sessions", body: fmt.Sprintf("已切换到会话: %s", msg.sessionID)})
	m.clearNewMessageNotice()
}

func (m *appModel) applySubagentPreviewRestore(msg sessionRestoredMsg) {
	m.resetToolInspect()
	m.clearNewMessageNotice()
	m.sessionPicker = nil
	m.subagentPicker = nil
	if msg.subagentPreview != nil {
		preview := *msg.subagentPreview
		preview.parentTranscript = copyTranscriptEntries(msg.subagentPreview.parentTranscript)
		preview.entries = mergeTranscriptToolEntries(copyTranscriptEntries(msg.entries))
		finalizeRestoredRunningToolEntries(preview.entries, m.animationNow())
		m.subagentPreview = &preview
	}
	m.transcript = renderSubagentPreviewTranscript(m.subagentPreview, m.animationNow())
	m.relayout()
	m.refreshViewport()
}

func (m *appModel) restoreMainTranscriptFromSubagentPreview() {
	preview := m.subagentPreview
	if preview == nil {
		return
	}
	m.sessionPicker = nil
	m.subagentPicker = nil
	m.subagentPreview = nil
	m.resetToolInspect()
	m.clearNewMessageNotice()
	m.sessionID = preview.parentSessionID
	m.transcript = copyTranscriptEntries(preview.parentTranscript)
	m.syncInputPlaceholder()
	m.syncInputMode()
	m.relayout()
	m.refreshViewport()
}

func (m *appModel) syncInputPlaceholder() {
	m.input.Placeholder = ">"
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
