// 本文件定义从右侧 Subagents 卡片选择并预览子会话 transcript 的交互。
package bubble

import (
	"bufio"
	"codex-agent-go/internal/message"
	"codex-agent-go/internal/session"
	"codex-agent-go/internal/subagent"
	"codex-agent-go/internal/tokentracer"
	"context"
	"encoding/json"
	"fmt"
	"os"
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
	return m, loadSubagentTranscriptPreviewCmd(m.ctx, task, sessionID, preview)
}

func loadSubagentTranscriptPreviewCmd(ctx context.Context, task subagent.TaskSnapshot, sessionID string, preview *subagentTranscriptPreview) tea.Cmd {
	return func() tea.Msg {
		entries, err := loadSubagentTranscriptEntries(ctx, task, time.Now())
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

func loadSubagentTranscriptEntries(ctx context.Context, task subagent.TaskSnapshot, at time.Time) ([]transcriptEntry, error) {
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
		entries = append(entries, transcriptEntriesFromMessage(record.Message, createdAt)...)
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

func transcriptEntriesFromMessage(msg message.Message, createdAt time.Time) []transcriptEntry {
	var entries []transcriptEntry
	if content := strings.TrimSpace(msg.Content); content != "" {
		switch msg.Role {
		case message.RoleUser:
			entries = append(entries, transcriptEntry{kind: entryUser, title: "you", body: content, createdAt: createdAt})
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
		entries = append(entries, transcriptEntry{
			kind:       entryTool,
			title:      "tool",
			body:       formatRunningToolCallBody(name, call.Input, ""),
			toolUseID:  strings.TrimSpace(call.ID),
			toolName:   name,
			toolStatus: "running",
			toolTarget: toolSummaryTarget(name, call.Input),
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
			toolExpanded:   result.IsError,
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
				call.toolExpanded = entry.isError
				call.toolResultOnly = false
				call.body = completeRunningToolCallBody(call.body, entry.toolStatus)
				touchTranscriptEntry(call)
				delete(pendingByID, entry.toolUseID)
				continue
			}
			entry.toolExpanded = entry.isError
			out = append(out, entry)
			continue
		}

		entry.toolExpanded = entry.isError
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
		body:      fmt.Sprintf("viewing %s · session %s", taskDisplayName(task), shortTaskID(firstNonEmptyString(task.SessionID, task.ID))),
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
		out[i].toolFocused = false
		out[i].toolHovered = false
	}
	return out
}

func (m *appModel) applySessionPickerRestore(msg sessionRestoredMsg) {
	m.resetToolInspect()
	m.sessionID = msg.sessionID
	m.sessionPicker = nil
	m.subagentPicker = nil
	m.subagentPreview = nil
	m.syncInputPlaceholder()
	if len(msg.entries) > 0 {
		m.transcript = mergeTranscriptToolEntries(copyTranscriptEntries(msg.entries))
		m.refreshViewport()
	}
	m.addEntry(transcriptEntry{kind: entrySystem, title: "sessions", body: fmt.Sprintf("已切换到会话: %s", msg.sessionID)})
}

func (m *appModel) applySubagentPreviewRestore(msg sessionRestoredMsg) {
	m.resetToolInspect()
	m.sessionPicker = nil
	m.subagentPicker = nil
	if msg.subagentPreview != nil {
		preview := *msg.subagentPreview
		preview.parentTranscript = copyTranscriptEntries(msg.subagentPreview.parentTranscript)
		preview.entries = mergeTranscriptToolEntries(copyTranscriptEntries(msg.entries))
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
