// 本文件定义 /resume 命令的会话选择器 TUI 组件。
package bubble

import (
	"context"
	"fmt"
	tea "github.com/charmbracelet/bubbletea"
	"paw/internal/loop"
	"paw/internal/message"
	"paw/internal/session"
	"paw/internal/todo"
	"strings"
	"time"
)

type sessionStateLoader interface {
	LoadSession(ctx context.Context, sessionID string) (loop.SessionLoadResult, error)
}

// sessionPicker 保存 /resume 交互选择器的临时 UI 状态。
type sessionPicker struct {
	sessions      []sessionSummaryItem
	selectedIndex int
	loading       bool
	err           string
}

// newSessionPicker 创建一个正在加载的会话选择器。
func newSessionPicker() *sessionPicker {
	return &sessionPicker{loading: true}
}

// loadSessionsCmd 异步加载会话列表。
func loadSessionsCmd(ctx context.Context, store SessionStore) tea.Cmd {
	return func() tea.Msg {
		if store == nil {
			return sessionsLoadedMsg{err: fmt.Errorf("session store 未配置")}
		}
		summaries, err := store.ListSessions(ctx)
		if err != nil {
			return sessionsLoadedMsg{err: err}
		}
		items := make([]sessionSummaryItem, 0, len(summaries))
		for _, s := range summaries {
			items = append(items, sessionSummaryItem{
				sessionID:      s.SessionID,
				lastUsedAt:     s.LastUsedAt,
				firstMessage:   s.FirstMessage,
				transcriptSize: s.TranscriptSize,
			})
		}
		return sessionsLoadedMsg{sessions: items}
	}
}

// loadSessionHistoryCmd 异步加载指定会话的历史。
func loadSessionHistoryCmd(ctx context.Context, runner Runner, store SessionStore, sessionID string) tea.Cmd {
	return func() tea.Msg {
		if runner == nil {
			return sessionRestoredMsg{err: fmt.Errorf("runner 未初始化")}
		}
		var messages []message.Message
		var recovery *session.RecoveryState
		if loader, ok := runner.(sessionStateLoader); ok {
			result, err := loader.LoadSession(ctx, sessionID)
			if err != nil {
				return sessionRestoredMsg{err: err}
			}
			messages = result.Messages
			recovery = result.Recovery
		} else {
			var err error
			messages, err = runner.LoadHistory(ctx, sessionID)
			if err != nil {
				return sessionRestoredMsg{err: err}
			}
		}
		if toucher, ok := store.(interface {
			TouchSession(context.Context, string) error
		}); ok {
			if err := toucher.TouchSession(ctx, sessionID); err != nil {
				return sessionRestoredMsg{err: err}
			}
		}
		var currentTodo todo.Snapshot
		hasCurrentTodo := false
		todoWasCleared := false
		if todoLoader, ok := store.(TodoSnapshotLoader); ok {
			var todoErr error
			currentTodo, hasCurrentTodo, todoErr = todoLoader.LoadLatestTodoSnapshot(ctx, sessionID)
			if todoErr != nil {
				return sessionRestoredMsg{err: todoErr}
			}
			todoWasCleared = hasCurrentTodo && currentTodo.Cleared()
		}
		metadata := loadRestoreTurnMetadata(ctx, store, sessionID)
		entries := make([]transcriptEntry, 0, len(messages))
		if recordLoader, ok := store.(ResolvedRecordLoader); ok {
			if records, recordsErr := recordLoader.LoadResolvedRecords(ctx, sessionID); recordsErr == nil {
				entries = transcriptEntriesFromRecords(records, metadata, workspaceRootOf(runner))
			}
		}
		if len(entries) == 0 && len(messages) > 0 {
			createdAt := time.Now()
			for _, msg := range messages {
				entries = append(entries, transcriptEntriesFromMessage(msg, createdAt, workspaceRootOf(runner))...)
			}
			attachRestoreMetadataByAssistantOrder(entries, metadata)
		}
		if recovery != nil {
			entries = append(entries, transcriptEntry{
				kind:      entryError,
				title:     "recovery",
				body:      recoveryDisplayText(recovery),
				createdAt: time.Now(),
			})
		}
		return sessionRestoredMsg{
			sessionID:      sessionID,
			entries:        entries,
			currentTodo:    currentTodo,
			hasCurrentTodo: hasCurrentTodo && !todoWasCleared,
			todoWasCleared: todoWasCleared,
		}
	}
}

func recoveryDisplayText(recovery *session.RecoveryState) string {
	if recovery == nil {
		return ""
	}
	text := strings.TrimSpace(recovery.Error)
	if text == "" {
		text = "previous turn ended before completion"
	}
	if recovery.Interrupted {
		return "previous turn interrupted: " + text
	}
	return "previous turn failed: " + text
}

// handleSessionPickerKey 处理会话选择器中的方向键、确认键和取消键。
func (m appModel) handleSessionPickerKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.sessionPicker == nil {
		return m, nil
	}

	switch msg.String() {
	case "ctrl+c", "esc":
		m.sessionPicker = nil
		return m, m.input.Focus()
	case "up", "k":
		if m.sessionPicker.selectedIndex > 0 {
			m.sessionPicker.selectedIndex--
		}
		return m, nil
	case "down", "j":
		if m.sessionPicker.selectedIndex < len(m.sessionPicker.sessions)-1 {
			m.sessionPicker.selectedIndex++
		}
		return m, nil
	case "enter":
		if m.sessionPicker.loading || len(m.sessionPicker.sessions) == 0 {
			return m, nil
		}
		selected := m.sessionPicker.sessions[m.sessionPicker.selectedIndex]
		return m, loadSessionHistoryCmd(m.ctx, m.runner, m.sessionStore, selected.sessionID)
	}

	return m, nil
}

func loadRestoreTurnMetadata(ctx context.Context, store SessionStore, sessionID string) []session.TurnMetadata {
	metadataStore, ok := store.(session.TurnMetadataStore)
	if !ok {
		return nil
	}
	metadata, err := metadataStore.LoadTurnMetadata(ctx, sessionID)
	if err != nil {
		return nil
	}
	return metadata
}

func transcriptEntriesFromRecords(records []session.Record, metadata []session.TurnMetadata, workspaceRoot string) []transcriptEntry {
	metadataByRecordIndex := make(map[int]session.TurnMetadata, len(metadata))
	for _, item := range metadata {
		if item.Status != session.TurnStatusCompleted || item.AssistantSeq == nil || item.ResponseAt == nil {
			continue
		}
		// Forks resolve parent records and current-session records together;
		// both ranges can start at sequence zero. The last matching record is
		// the current session's own record and avoids decorating a parent entry.
		for index := len(records) - 1; index >= 0; index-- {
			if records[index].Seq == *item.AssistantSeq {
				metadataByRecordIndex[index] = item
				break
			}
		}
	}
	entries := make([]transcriptEntry, 0, len(records))
	for recordIndex, record := range records {
		createdAt := record.CreatedAt
		if createdAt.IsZero() {
			createdAt = time.Now()
		}
		recordEntries := transcriptEntriesFromMessage(record.Message, createdAt, workspaceRoot)
		if item, ok := metadataByRecordIndex[recordIndex]; ok {
			for index := len(recordEntries) - 1; index >= 0; index-- {
				if recordEntries[index].kind == entryAssistant {
					copyMetadata := item
					recordEntries[index].turnMetadata = &copyMetadata
					break
				}
			}
		}
		entries = append(entries, recordEntries...)
	}
	return entries
}

func attachRestoreMetadataByAssistantOrder(entries []transcriptEntry, metadata []session.TurnMetadata) {
	metadataIndex := 0
	for index := range entries {
		if entries[index].kind != entryAssistant {
			continue
		}
		for metadataIndex < len(metadata) && metadata[metadataIndex].Status != session.TurnStatusCompleted {
			metadataIndex++
		}
		if metadataIndex >= len(metadata) {
			return
		}
		copyMetadata := metadata[metadataIndex]
		entries[index].turnMetadata = &copyMetadata
		metadataIndex++
	}
}

// renderSessionPickerBox 渲染会话选择器面板。
func (m appModel) renderSessionPickerBox() string {
	if m.sessionPicker == nil {
		return ""
	}
	body := m.renderSessionPickerContent()
	return m.renderModalPanel(body)
}

// renderSessionPickerContent 渲染会话列表内容。
func (m appModel) renderSessionPickerContent() string {
	picker := m.sessionPicker
	lines := []string{wizardTitleStyle.Render("Resume Session")}

	if picker.err != "" {
		lines = append(lines, labelErrorStyle.Render(picker.err))
		lines = append(lines, "Press esc to cancel.")
		return strings.Join(lines, "\n")
	}

	if picker.loading {
		lines = append(lines, "Loading sessions...")
		return strings.Join(lines, "\n")
	}

	if len(picker.sessions) == 0 {
		lines = append(lines, "No sessions found.")
		lines = append(lines, "Press esc to cancel.")
		return strings.Join(lines, "\n")
	}

	// modal 的可见行数随 transcript 高度缩放；selectedIndex 始终留在窗口内。
	maxItems := clampInt(m.currentLayout().transcriptHeight-7, 1, 8)
	start := 0
	if picker.selectedIndex >= maxItems {
		start = picker.selectedIndex - maxItems + 1
	}
	end := start + maxItems
	if end > len(picker.sessions) {
		end = len(picker.sessions)
	}

	for i := start; i < end; i++ {
		item := picker.sessions[i]
		label := formatSessionLabel(item)
		if i == picker.selectedIndex {
			lines = append(lines, selectedProviderStyle.Render("> "+label))
		} else {
			lines = append(lines, unselectedProviderStyle.Render("  "+label))
		}
	}

	if len(picker.sessions) > maxItems {
		lines = append(lines, fmt.Sprintf("(%d/%d) Use up/down to scroll", picker.selectedIndex+1, len(picker.sessions)))
	}
	lines = append(lines, "Press enter to select, esc to cancel.")
	return strings.Join(lines, "\n")
}

// formatSessionLabel 格式化会话列表项的显示文本。最近使用时间在隐藏
// session ID 后提供稳定的、可读的候选项区分信息。
func formatSessionLabel(item sessionSummaryItem) string {
	lastUsedAt := item.lastUsedAt.Format("2006-01-02 15:04:05")
	size := formatFileSize(item.transcriptSize)
	msg := strings.Join(strings.Fields(sanitizeTerminalText(item.firstMessage)), " ")
	if msg != "" {
		return fmt.Sprintf("%s  %s  %s", lastUsedAt, size, truncateStyledCellLine(msg, 80))
	}
	return fmt.Sprintf("%s  %s  (empty)", lastUsedAt, size)
}

// formatFileSize 将字节数格式化为人类可读的文件大小。
func formatFileSize(bytes int64) string {
	switch {
	case bytes < 1024:
		return fmt.Sprintf("%dB", bytes)
	case bytes < 1024*1024:
		return fmt.Sprintf("%.1fKB", float64(bytes)/1024)
	default:
		return fmt.Sprintf("%.1fMB", float64(bytes)/(1024*1024))
	}
}

// formatSessionAge 将创建时间转换为人类可读的相对时间。
func formatSessionAge(t time.Time) string {
	if t.IsZero() {
		return "?"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}
