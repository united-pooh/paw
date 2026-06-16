// 本文件定义 /sessions 命令的会话选择器 TUI 组件。
package bubble

import (
	"context"
	"fmt"
	tea "github.com/charmbracelet/bubbletea"
	"strings"
	"time"
)

// sessionPicker 保存 /sessions 交互选择器的临时 UI 状态。
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
				createdAt:      s.CreatedAt,
				firstMessage:   s.FirstMessage,
				transcriptSize: s.TranscriptSize,
			})
		}
		return sessionsLoadedMsg{sessions: items}
	}
}

// loadSessionHistoryCmd 异步加载指定会话的历史。
func loadSessionHistoryCmd(ctx context.Context, runner Runner, sessionID string) tea.Cmd {
	return func() tea.Msg {
		if runner == nil {
			return sessionRestoredMsg{err: fmt.Errorf("runner 未初始化")}
		}
		if err := runner.LoadHistory(ctx, sessionID); err != nil {
			return sessionRestoredMsg{err: err}
		}
		return sessionRestoredMsg{sessionID: sessionID}
	}
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
		return m, loadSessionHistoryCmd(m.ctx, m.runner, selected.sessionID)
	}

	return m, nil
}

// renderSessionPickerBox 渲染会话选择器面板。
func (m appModel) renderSessionPickerBox() string {
	if m.sessionPicker == nil {
		return ""
	}
	width := maxInt(32, m.width-2)
	body := m.renderSessionPickerContent()
	return wizardPanelStyle.Width(width).Render(body)
}

// renderSessionPickerContent 渲染会话列表内容。
func (m appModel) renderSessionPickerContent() string {
	picker := m.sessionPicker
	lines := []string{wizardTitleStyle.Render("Sessions")}

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

	maxItems := 8
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

// formatSessionLabel 格式化会话列表项的显示文本。
// 格式：ID[:8]  YYYY-MM-DD  <size>  <firstMessage>
func formatSessionLabel(item sessionSummaryItem) string {
	id := item.sessionID
	if len(id) > 8 {
		id = id[:8]
	}
	date := item.createdAt.Format("2006-01-02")
	size := formatFileSize(item.transcriptSize)
	if item.firstMessage != "" {
		msg := item.firstMessage
		if runes := []rune(msg); len(runes) > 80 {
			msg = string(runes[:80])
		}
		return fmt.Sprintf("%s  %s  %s  %s", id, date, size, msg)
	}
	return fmt.Sprintf("%s  %s  %s  (empty)", id, date, size)
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
