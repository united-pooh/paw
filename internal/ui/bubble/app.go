// 本文件定义 Bubble Tea 应用模型的创建、初始化和事件更新逻辑。
package bubble

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/charmbracelet/bubbles/cursor"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"strings"
	"time"
)

// newModel 创建完整的 TUI 状态模型，并初始化输入框、滚动区和系统消息。
func newModel(ctx context.Context, runner Runner, sessionID string, controller ModelConfigController, settingsController SettingsController, subagentController SubagentController, sessionStore SessionStore, anchor *terminalCursorAnchor) appModel {
	input := textarea.New()
	input.Prompt = ""
	input.Placeholder = ">"
	input.ShowLineNumbers = false
	input.EndOfBufferCharacter = ' '
	input.CharLimit = 0
	input.MaxHeight = inputMaxVisibleLines
	input.SetWidth(72)
	input.SetHeight(1)
	input.KeyMap.InsertNewline = key.NewBinding(
		key.WithKeys("ctrl+j", "alt+enter", "shift+enter"),
		key.WithHelp("ctrl+j", "newline"),
	)
	input.Cursor.SetMode(cursor.CursorStatic)
	applyTextareaPlainBackground(&input)
	input.Focus()

	vp := viewport.New(80, 20)
	model := appModel{
		ctx:             ctx,
		runner:          runner,
		sessionID:       sessionID,
		modelConfig:     controller,
		settingsConfig:  settingsController,
		subagents:       subagentController,
		sessionStore:    sessionStore,
		commandRegistry: NewCommandRegistry(),
		cursorAnchor:    anchor,
		input:           input,
		viewport:        vp,
		cursorFrameAt:   time.Now(),
		activeAssistant: -1,
		historyIndex:    -1,
		transcript: []transcriptEntry{
			{
				kind:  entrySystem,
				title: "system",
				body:  "Interactive mode is running on Bubble Tea. Use /help for commands.",
			},
		},
	}
	model.applyCursorAnimation()
	model.refreshViewport()
	return model
}

// applyTextareaPlainBackground 移除 textarea 默认的当前行背景色，让输入文字保持透明背景。
func applyTextareaPlainBackground(input *textarea.Model) {
	plain := lipgloss.NewStyle()
	input.FocusedStyle.Base = plain
	input.FocusedStyle.CursorLine = plain
	input.FocusedStyle.Text = plain
	input.FocusedStyle.Placeholder = lipgloss.NewStyle().Foreground(colorManager.LipglossColor(colorMarkdownRule))
	input.BlurredStyle.Base = plain
	input.BlurredStyle.CursorLine = plain
	input.BlurredStyle.Text = plain
	input.BlurredStyle.Placeholder = lipgloss.NewStyle().Foreground(colorManager.LipglossColor(colorMarkdownRule))
}

// Init 返回 Bubble Tea 启动时需要执行的初始命令。
func (m appModel) Init() tea.Cmd {
	return tea.Batch(m.input.Focus(), cursorFrameTick())
}

// Update 是 Bubble Tea 的核心状态机，负责把输入事件、模型事件和工具事件规约为新状态。
func (m appModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.ready = true
		m.width = msg.Width
		m.height = msg.Height
		m.relayout()
		m.refreshViewport()
		return m, nil
	case cursorFrameMsg:
		m.cursorFrameAt = time.Time(msg)
		m.applyCursorAnimation()
		m.updateContextMeterAnimation()
		if m.hasActiveTranscriptAnimation() {
			if m.viewport.AtBottom() {
				m.refreshViewport()
			} else {
				m.refreshViewportPreservingOffset()
			}
		}
		return m, cursorFrameTick()
	case assistantDeltaMsg:
		m.isGenerating = true
		m.appendAssistantDelta(string(msg))
		return m, nil
	case thinkingDeltaMsg:
		m.isGenerating = true
		m.appendThinkingDelta(string(msg))
		return m, nil
	case toolCallMsg:
		m.isGenerating = false
		m.activeAssistant = -1
		m.addEntry(transcriptEntry{
			kind:  entryTool,
			title: "tool",
			body:  fmt.Sprintf("%s %s", msg.Name, prettyJSON(json.RawMessage(msg.Input))),
		})
		return m, nil
	case toolResultMsg:
		m.activeAssistant = -1
		status := "ok"
		if msg.IsError {
			status = "error"
		}
		body := fmt.Sprintf("%s %s", msg.Name, status)
		if preview := summarizeToolContent(msg.Content); preview != "" {
			body = fmt.Sprintf("%s: %s", body, preview)
		}
		m.addEntry(transcriptEntry{
			kind:  entryTool,
			title: "result",
			body:  body,
		})
		return m, nil
	case systemEventMsg:
		title := strings.TrimSpace(msg.Title)
		if title == "" {
			title = "system"
		}
		m.addEntry(transcriptEntry{
			kind:  entrySystem,
			title: title,
			body:  strings.TrimSpace(msg.Body),
		})
		return m, nil
	case doneMsg:
		m.isGenerating = false
		m.activeAssistant = -1
		m.refreshViewport()
		return m, nil
	case turnFinishedMsg:
		m.isGenerating = false
		m.queryGuard.FinishModel()
		m.turnStartedAt = time.Time{}
		m.syncRunningFlags()
		if msg.err != nil {
			m.addEntry(transcriptEntry{
				kind:  entryError,
				title: "error",
				body:  msg.err.Error(),
			})
		}
		cmds = append(cmds, m.input.Focus())
		if queuedCmd := m.startNextQueuedTurn(); queuedCmd != nil {
			cmds = append(cmds, queuedCmd)
		}
	case shellFinishedMsg:
		m.queryGuard.FinishTerminal()
		m.syncRunningFlags()
		kind := entryTool
		if msg.err != nil {
			kind = entryError
		}
		m.addEntry(transcriptEntry{
			kind:  kind,
			title: "terminal",
			body:  shellResultBody(msg),
		})
		cmds = append(cmds, m.input.Focus())
	case subagentFinishedMsg:
		m.queryGuard.FinishModel()
		m.turnStartedAt = time.Time{}
		m.syncRunningFlags()
		if msg.err != nil {
			m.addEntry(transcriptEntry{
				kind:  entryError,
				title: "subagent",
				body:  msg.err.Error(),
			})
		} else {
			m.addEntry(transcriptEntry{
				kind:  entrySystem,
				title: "subagent",
				body:  renderSubagentResult(msg.result),
			})
		}
		cmds = append(cmds, m.input.Focus())
		if queuedCmd := m.startNextQueuedTurn(); queuedCmd != nil {
			cmds = append(cmds, queuedCmd)
		}
	case sessionsLoadedMsg:
		if m.sessionPicker != nil {
			if msg.err != nil {
				m.sessionPicker.err = msg.err.Error()
			} else {
				m.sessionPicker.sessions = msg.sessions
				m.sessionPicker.loading = false
			}
		}
		return m, nil
	case sessionRestoredMsg:
		if msg.err != nil {
			m.addEntry(transcriptEntry{kind: entryError, title: "sessions", body: msg.err.Error()})
		} else {
			m.sessionID = msg.sessionID
			m.addEntry(transcriptEntry{kind: entrySystem, title: "sessions", body: fmt.Sprintf("已切换到会话: %s", msg.sessionID)})
		}
		m.sessionPicker = nil
		cmds = append(cmds, m.input.Focus())
		return m, tea.Batch(cmds...)
	case fileCompletionLoadedMsg:
		// 丢弃与当前 searchDir 不匹配的过期结果
		if m.completion != nil && m.completion.kind == completionKindFile &&
			m.completion.searchDir == msg.searchDir {
			m.completion.allItems = msg.items
			m.completion.filteredItems = msg.filtered
			m.completion.loading = false
			// 用当前 prefix 再过滤一次（加载期间前缀可能已改变）
			if m.completion.prefix != "" {
				m.completion.filteredItems = filterByPrefix(m.completion.allItems, m.completion.prefix)
			}
			if m.completion.selectedIndex >= len(m.completion.filteredItems) {
				m.completion.selectedIndex = 0
			}
		}
		return m, nil
	case tea.KeyMsg:
		if m.settingWizard != nil {
			return m.handleSettingWizardKey(msg)
		}
		if m.modelWizard != nil {
			return m.handleModelWizardKey(msg)
		}
		if m.sessionPicker != nil {
			return m.handleSessionPickerKey(msg)
		}
		// 补全激活时：只拦截导航键和确认键，其余按键正常透传给输入框
		if m.completion != nil {
			switch msg.String() {
			case "esc":
				m.completion = nil
				return m, nil
			case "up", "ctrl+p":
				m.completion.navigateUp()
				return m, nil
			case "down", "ctrl+n":
				m.completion.navigateDown()
				return m, nil
			case "tab":
				visible := m.completion.visibleItems()
				if !m.completion.loading && len(visible) > 0 {
					selected := visible[m.completion.selectedIndex]
					if m.completion.kind == completionKindFile {
						// Tab：补全路径，不加尾部空格，光标停在路径末尾
						m = m.applyFileCompletion(selected, false)
					} else {
						m = m.applyCommandCompletion(selected)
					}
				}
				m.completion = nil
				return m, nil
			case "enter":
				visible := m.completion.visibleItems()
				if !m.completion.loading && len(visible) > 0 {
					selected := visible[m.completion.selectedIndex]
					if m.completion.kind == completionKindFile {
						// Enter：补全路径并追加空格，结束引用
						m = m.applyFileCompletion(selected, true)
					} else {
						m = m.applyCommandCompletion(selected)
					}
				}
				m.completion = nil
				return m, nil
			}
			// 其他键：不 return，继续走下面的 switch 和 textarea 更新
		}
		switch msg.String() {
		case "ctrl+c", "esc":
			return m, tea.Quit
		case "ctrl+o":
			m.showThinking = !m.showThinking
			m.refreshViewportPreservingOffset()
			return m, nil
		case "up":
			return m.handleInputVerticalNavigation(-1)
		case "down":
			return m.handleInputVerticalNavigation(1)
		case "enter":
			return m.handleSubmit()
		}
	case tea.MouseMsg:
		if next, handled, cmd := m.handleTranscriptMouse(msg); handled {
			return next, cmd
		}
	}

	var viewportCmd tea.Cmd
	m.viewport, viewportCmd = m.viewport.Update(msg)
	cmds = append(cmds, viewportCmd)

	if !m.isTerminalWorkRunning() {
		var inputCmd tea.Cmd
		m.input, inputCmd = m.input.Update(msg)
		if isTextEditingKey(msg) {
			m.resetHistoryNavigation()
		}
		m.syncInputMode()
		m.relayout()
		cmds = append(cmds, inputCmd)

		// 每次文本变化后同步补全状态
		if isTextEditingKey(msg) {
			// / 命令补全：整个输入以 / 开头时触发
			m.syncCommandCompletion()
			// @ 文件补全：词边界 @ 触发（只要 / 补全未激活）
			if cmd := m.syncAtCompletion(); cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
	}
	m.applyCursorAnimation()

	return m, tea.Batch(cmds...)
}
