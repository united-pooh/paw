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
		if m.completion != nil && m.completion.kind == completionKindFile {
			if msg.err == nil {
				m.completion.items = msg.items
				m.completion.loading = false
			} else {
				m.completion = nil
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
		if m.completion != nil {
			return m.handleCompletionKey(msg)
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

		// 检测 / 和 @ 前缀触发命令补全或文件补全（仅在文本编辑键后）
		if isTextEditingKey(msg) {
			val := m.input.Value()
			switch {
			case strings.HasPrefix(val, "/"):
				query := strings.TrimPrefix(val, "/")
				m.completion = newCommandCompletion(query, m.commandRegistry)
				m.sessionPicker = nil
			case strings.HasPrefix(val, "@"):
				if m.completion == nil || m.completion.kind != completionKindFile {
					m.completion = newFileCompletion(val[1:])
					cmds = append(cmds, loadFileCompletionCmd())
				}
			default:
				if m.completion != nil && (m.completion.kind == completionKindCommand || m.completion.kind == completionKindFile) {
					m.completion = nil
				}
			}
		}
	}
	m.applyCursorAnimation()

	return m, tea.Batch(cmds...)
}
