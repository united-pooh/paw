// 本文件定义从右侧 Tasks 卡片选择并预览子会话 transcript 的交互。
package bubble

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"paw/internal/message"
	"paw/internal/session"
	taskpkg "paw/internal/task"
	"paw/internal/tokentracer"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func waitTaskUpdateCmd(ctx context.Context, updates <-chan struct{}) tea.Cmd {
	return func() tea.Msg {
		if updates == nil {
			return taskUpdateMsg{closed: true}
		}
		select {
		case _, ok := <-updates:
			return taskUpdateMsg{closed: !ok}
		case <-ctx.Done():
			return taskUpdateMsg{closed: true}
		}
	}
}

func newTaskPicker(tasks []taskpkg.TaskSnapshot) *taskPicker {
	return &taskPicker{
		tasks: append([]taskpkg.TaskSnapshot(nil), tasks...),
		tab:   activityTabTasks,
	}
}
func (m appModel) openTaskPicker() (tea.Model, tea.Cmd) {
	m.openActivity(activityTabTasks)
	return m, nil
}

func (m appModel) openTodoPicker() (tea.Model, tea.Cmd) {
	m.openActivity(activityTabTodo)
	return m, nil
}

func (m *appModel) openActivity(tab activityTab) {
	if m == nil {
		return
	}
	m.taskPicker = newTaskPicker(m.taskEntries())
	m.taskPicker.tab = tab
	m.sessionPicker = nil
	m.modelWizard = nil
	m.settingWizard = nil
	m.clearCompletionAndRelayout()
	m.relayout()
}

func (m appModel) handleTaskPickerKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.taskPicker == nil {
		return m, nil
	}

	switch msg.String() {
	case "ctrl+c", "ctrl+g", "esc":
		m.taskPicker = nil
		m.relayout()
		return m, m.input.Focus()
	case "tab", "right", "l":
		m.taskPicker.tab = activityTabTodo
		return m, nil
	case "shift+tab", "left", "h":
		m.taskPicker.tab = activityTabTasks
		return m, nil
	case "up", "k":
		if m.taskPicker.tab != activityTabTasks {
			return m, nil
		}
		if m.taskPicker.selectedIndex > 0 {
			m.taskPicker.selectedIndex--
		}
		return m, nil
	case "down", "j":
		if m.taskPicker.tab != activityTabTasks {
			return m, nil
		}
		if m.taskPicker.selectedIndex < len(m.taskPicker.tasks)-1 {
			m.taskPicker.selectedIndex++
		}
		return m, nil
	case "enter":
		if m.taskPicker.tab != activityTabTasks {
			return m, nil
		}
		if len(m.taskPicker.tasks) == 0 {
			return m, nil
		}
		task := m.taskPicker.tasks[m.taskPicker.selectedIndex]
		return m.previewTaskTranscript(task)
	}
	return m, nil
}

func (m *appModel) refreshActivityTasks() {
	if m == nil || m.taskPicker == nil {
		return
	}
	m.taskPicker.tasks = append(m.taskPicker.tasks[:0], m.taskEntries()...)
	if len(m.taskPicker.tasks) == 0 {
		m.taskPicker.selectedIndex = 0
		return
	}
	m.taskPicker.selectedIndex = clampInt(m.taskPicker.selectedIndex, 0, len(m.taskPicker.tasks)-1)
}

// activityPollInterval 是 Activity 面板中 ListTasks 刷新的节流间隔。
// 仅当面板可见且存在 running 任务时才需要高频刷新；其他场景低频轮询即可。
const activityPollInterval = 500 * time.Millisecond

// refreshActivityFromTasks 由 cursorFrameMsg 每帧驱动，按频率分级调用
// ListTasks 相关刷新：高频（running 任务存在）时使用 500ms 节流，
// 否则使用更保守的 2s 节流，避免每帧都跨进程读 task registry。
func (m *appModel) refreshActivityFromTasks(now time.Time) bool {
	if m == nil || m.taskPicker == nil {
		return false
	}
	interval := activityPollInterval
	if !m.activityHasRunningTask() {
		interval = 2 * time.Second
	}
	if m.lastActivityPollAt.IsZero() || now.Sub(m.lastActivityPollAt) >= interval {
		m.lastActivityPollAt = now
		m.refreshActivityTasks()
		m.refreshTaskPreviewFromTasks()
		m.refreshTaskToolEntriesFromTasks()
		return true
	}
	return false
}

// activityHasRunningTask 报告当前 Activity 面板任务列表中是否有 running 任务。
func (m *appModel) activityHasRunningTask() bool {
	if m == nil || m.taskPicker == nil {
		return false
	}
	for _, task := range m.taskPicker.tasks {
		if string(task.Status) == "running" {
			return true
		}
	}
	return false
}

func (m appModel) previewTaskTranscript(task taskpkg.TaskSnapshot) (tea.Model, tea.Cmd) {
	sessionID := strings.TrimSpace(task.SessionID)
	if sessionID == "" {
		sessionID = strings.TrimSpace(task.ID)
	}
	if sessionID == "" {
		m.addEntry(transcriptEntry{kind: entryError, title: "task", body: "selected task has no session id"})
		return m, nil
	}

	parentSessionID := m.sessionID
	parentTranscript := copyTranscriptEntries(m.transcript)
	if m.taskPreview != nil {
		parentSessionID = m.taskPreview.parentSessionID
		parentTranscript = copyTranscriptEntries(m.taskPreview.parentTranscript)
	}

	preview := &taskTranscriptPreview{
		task:             task,
		sessionID:        sessionID,
		parentSessionID:  parentSessionID,
		parentTranscript: parentTranscript,
		liveContent:      liveTaskContent(task),
	}
	return m, loadTaskTranscriptPreviewCmd(m.ctx, m.sessionStore, task, sessionID, preview, m.workspaceRoot)
}

func loadTaskTranscriptPreviewCmd(ctx context.Context, store SessionStore, task taskpkg.TaskSnapshot, sessionID string, preview *taskTranscriptPreview, workspaceRoot string) tea.Cmd {
	return func() tea.Msg {
		entries, err := loadTaskTranscriptEntries(ctx, store, task, time.Now(), workspaceRoot)
		if err != nil {
			return sessionRestoredMsg{source: sessionRestoreTaskEnter, taskPreview: preview, err: err}
		}
		return sessionRestoredMsg{
			sessionID:   sessionID,
			entries:     entries,
			source:      sessionRestoreTaskEnter,
			taskPreview: preview,
		}
	}
}

func loadTaskTranscriptEntries(ctx context.Context, store SessionStore, task taskpkg.TaskSnapshot, at time.Time, workspaceRoot string) ([]transcriptEntry, error) {
	// 优先走与 /resume 主会话恢复一致的 store 读取：统一信封与 legacy
	// Record 双格式、fork 解析都由 store 内部处理。
	if loader, ok := store.(ResolvedRecordLoader); ok {
		sessionID := firstNonEmptyString(strings.TrimSpace(task.SessionID), strings.TrimSpace(task.ID))
		if sessionID != "" {
			if records, recordsErr := loader.LoadResolvedRecords(ctx, sessionID); recordsErr == nil {
				if entries := transcriptEntriesFromRecords(records, nil, workspaceRoot); len(entries) > 0 {
					return mergeTranscriptToolEntries(entries), nil
				}
			}
		}
	}
	path := strings.TrimSpace(task.TranscriptPath)
	if path == "" {
		if content := strings.TrimSpace(task.Content); content != "" {
			return []transcriptEntry{{kind: entryAssistant, title: "assistant", body: content, createdAt: at}}, nil
		}
		return nil, fmt.Errorf("selected task has no transcript path")
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
		record, parseErr := session.ParseTranscriptLine([]byte(line))
		if parseErr != nil {
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
				if strings.EqualFold(call.toolName, "question") && strings.EqualFold(call.toolStatus, "ok") {
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

func decorateTaskTranscript(task taskpkg.TaskSnapshot, entries []transcriptEntry, createdAt time.Time) []transcriptEntry {
	header := transcriptEntry{
		kind:      entrySystem,
		title:     "task",
		body:      fmt.Sprintf("viewing %s", taskDisplayName(task)),
		createdAt: createdAt,
	}
	out := make([]transcriptEntry, 0, len(entries)+1)
	out = append(out, header)
	out = append(out, entries...)
	return out
}

func renderTaskPreviewTranscript(preview *taskTranscriptPreview, createdAt time.Time) []transcriptEntry {
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
	return decorateTaskTranscript(preview.task, entries, createdAt)
}

func (m *appModel) refreshTaskToolEntriesFromTasks() bool {
	if m == nil || m.taskController == nil {
		return false
	}
	tasks := m.taskController.ListTasks()
	if len(tasks) == 0 {
		return false
	}
	changed := false
	for index := range m.transcript {
		entry := &m.transcript[index]
		if !isToolTransaction(*entry) || !isTaskToolEntry(*entry) || strings.TrimSpace(entry.toolResult) == "" {
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
			if task.Status == taskpkg.TaskRunning {
				status = "running"
			}
			isError := strings.TrimSpace(task.Error) != "" || task.Status == taskpkg.TaskFailed || task.Status == taskpkg.TaskInterrupted
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
func (m *appModel) refreshTaskPreviewFromTasks() bool {
	if m == nil || m.taskPreview == nil || m.taskController == nil {
		return false
	}
	task, ok := m.findTaskPreviewTask()
	if !ok {
		return false
	}
	live := liveTaskContent(task)
	if task.Status == m.taskPreview.task.Status &&
		task.Content == m.taskPreview.task.Content &&
		task.UsedTokens == m.taskPreview.task.UsedTokens &&
		taskUsageEqual(task.Usage, m.taskPreview.task.Usage) &&
		live == m.taskPreview.liveContent {
		return false
	}
	m.taskPreview.task = task
	m.taskPreview.liveContent = live
	m.resetToolInspect()
	m.transcript = renderTaskPreviewTranscript(m.taskPreview, m.animationNow())
	m.refreshViewport()
	return true
}

func (m appModel) findTaskPreviewTask() (taskpkg.TaskSnapshot, bool) {
	if m.taskPreview == nil || m.taskController == nil {
		return taskpkg.TaskSnapshot{}, false
	}
	sessionID := strings.TrimSpace(m.taskPreview.sessionID)
	if sessionID == "" {
		sessionID = strings.TrimSpace(m.taskPreview.task.SessionID)
	}
	taskID := strings.TrimSpace(m.taskPreview.task.ID)
	for _, task := range m.taskController.ListTasks() {
		if sessionID != "" && strings.TrimSpace(task.SessionID) == sessionID {
			return task, true
		}
		if taskID != "" && strings.TrimSpace(task.ID) == taskID {
			return task, true
		}
	}
	return taskpkg.TaskSnapshot{}, false
}

func liveTaskContent(task taskpkg.TaskSnapshot) string {
	if task.Status != taskpkg.TaskRunning {
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
	m.taskPicker = nil
	m.taskPreview = nil
	m.syncInputPlaceholder()
	m.transcript = mergeTranscriptToolEntries(copyTranscriptEntries(msg.entries))
	m.finalizeRestoredRunningTools()
	m.currentTodo = msg.currentTodo.Clone()
	m.hasCurrentTodo = msg.hasCurrentTodo
	m.todoWasCleared = msg.todoWasCleared
	m.inputHistory = inputHistoryFromTranscript(msg.entries)
	m.resetHistoryNavigation()
	m.addEntry(transcriptEntry{kind: entrySystem, title: "sessions", body: fmt.Sprintf("已切换到会话: %s", msg.sessionID)})
	m.clearNewMessageNotice()
}

func (m *appModel) applyTaskPreviewRestore(msg sessionRestoredMsg) {
	m.resetToolInspect()
	m.clearNewMessageNotice()
	m.sessionPicker = nil
	m.taskPicker = nil
	if msg.taskPreview != nil {
		preview := *msg.taskPreview
		preview.parentTranscript = copyTranscriptEntries(msg.taskPreview.parentTranscript)
		preview.entries = mergeTranscriptToolEntries(copyTranscriptEntries(msg.entries))
		finalizeRestoredRunningToolEntries(preview.entries, m.animationNow())
		m.taskPreview = &preview
	}
	m.transcript = renderTaskPreviewTranscript(m.taskPreview, m.animationNow())
	m.relayout()
	m.refreshViewport()
}

func (m *appModel) restoreMainTranscriptFromTaskPreview() {
	preview := m.taskPreview
	if preview == nil {
		return
	}
	m.sessionPicker = nil
	m.taskPicker = nil
	m.taskPreview = nil
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
