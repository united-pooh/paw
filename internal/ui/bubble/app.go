// 本文件定义 Bubble Tea 应用模型的创建、初始化和事件更新逻辑。
package bubble

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"codex-agent-go/internal/skill"
	"github.com/charmbracelet/bubbles/cursor"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// pipelinePollCmd 异步检测 .pipeline-workspace/ 并返回 pipelineStateUpdatedMsg。
func pipelinePollCmd(activeAfter time.Time) tea.Cmd {
	return func() tea.Msg {
		cwd, err := os.Getwd()
		if err != nil {
			return pipelineStateUpdatedMsg{}
		}
		workspaceDir := filepath.Join(cwd, ".pipeline-workspace")
		return pipelineStateUpdatedMsg{state: loadPipelineState(workspaceDir, activeAfter)}
	}
}

// newModel 创建完整的 TUI 状态模型，并初始化输入框、滚动区和系统消息。
func newModel(ctx context.Context, runner Runner, sessionID string, controller ModelConfigController, settingsController SettingsController, subagentController SubagentController, sessionStore SessionStore, anchor *terminalCursorAnchor) appModel {
	now := time.Now()
	input := textarea.New()
	input.Prompt = ""
	input.Placeholder = ">"
	input.ShowLineNumbers = false
	input.EndOfBufferCharacter = ' '
	input.CharLimit = 0
	input.MaxHeight = 0
	input.SetWidth(72)
	input.SetHeight(inputMinVisibleLines)
	input.KeyMap.InsertNewline = key.NewBinding(
		key.WithKeys("ctrl+j", "alt+enter", "shift+enter"),
		key.WithHelp("ctrl+j", "newline"),
	)
	input.Cursor.SetMode(cursor.CursorStatic)
	applyTextareaPlainBackground(&input)
	input.Focus()

	vp := viewport.New(80, 20)
	skillRoot, _ := os.Getwd()
	model := appModel{
		ctx:                 ctx,
		runner:              runner,
		sessionID:           sessionID,
		modelConfig:         controller,
		settingsConfig:      settingsController,
		subagents:           subagentController,
		sessionStore:        sessionStore,
		commandRegistry:     NewCommandRegistry(),
		skillRegistry:       skill.NewRegistry(skill.DefaultRoots(skillRoot)),
		cursorAnchor:        anchor,
		input:               input,
		viewport:            vp,
		cursorFrameAt:       now,
		pipelineActiveAfter: now,
		activeAssistant:     -1,
		historyIndex:        -1,
		transcript: []transcriptEntry{
			{
				kind:  entrySystem,
				title: "system",
				body:  "Interactive mode is running on Bubble Tea. Use /help for commands.",
			},
		},
	}
	if provider, ok := runner.(tokenTracerURLProvider); ok {
		if url := strings.TrimSpace(provider.TokenTracerURL()); url != "" {
			model.transcript = append(model.transcript, transcriptEntry{
				kind:  entrySystem,
				title: "token-tracer",
				body:  "Token Tracer dashboard: " + url + "\nUse /token-tracer to show this link again.",
			})
		}
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
		m.spinnerFrameIdx++
		m.applyCursorAnimation()
		m.updateContextMeterAnimation()
		m.refreshSubagentPreviewFromTasks()
		if m.transcriptRefreshPending || m.hasActiveTranscriptAnimation() {
			if m.viewport.AtBottom() {
				m.refreshViewport()
			} else {
				m.refreshViewportPreservingOffset()
			}
		}
		return m, tea.Batch(cursorFrameTick(), pipelinePollCmd(m.pipelineActiveAfter))
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
		m.recordToolCallEntry(msg.ID, msg.Name, json.RawMessage(msg.Input), msg.OldContent)
		return m, nil
	case toolResultMsg:
		m.activeAssistant = -1
		status := "ok"
		if msg.IsError {
			status = "error"
		}
		m.recordToolResultEntry(msg.ToolUseID, msg.Name, status, msg.IsError)
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
			color: msg.Color,
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
			m.pendingToolCites = nil
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
		agentTitle := resultDisplayName(msg.result)
		agentColor := strings.TrimSpace(msg.result.AgentColor)
		if msg.err != nil {
			m.addEntry(transcriptEntry{
				kind:  entryError,
				title: agentTitle,
				color: agentColor,
				body:  msg.err.Error(),
			})
		} else {
			m.addEntry(transcriptEntry{
				kind:  entrySystem,
				title: agentTitle,
				color: agentColor,
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
			title := "sessions"
			if msg.source == sessionRestoreSubagentEnter {
				title = "subagent"
				m.subagentPicker = nil
			} else {
				m.sessionPicker = nil
			}
			m.addEntry(transcriptEntry{kind: entryError, title: title, body: msg.err.Error()})
		} else {
			switch msg.source {
			case sessionRestoreSubagentEnter:
				m.applySubagentPreviewRestore(msg)
			default:
				m.applySessionPickerRestore(msg)
			}
		}
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
			// 候选项加载完成后补全框高度变化，必须重新计算布局
			m.relayout()
		}
		return m, nil
	case pipelineStateUpdatedMsg:
		m.pipelineState = msg.state
		return m, nil
	case tea.KeyMsg:
		if isRawMouseEscapeKey(msg) {
			return m, nil
		}
		if m.settingWizard != nil {
			return m.handleSettingWizardKey(msg)
		}
		if m.modelWizard != nil {
			return m.handleModelWizardKey(msg)
		}
		if m.sessionPicker != nil {
			return m.handleSessionPickerKey(msg)
		}
		if m.subagentPicker != nil {
			return m.handleSubagentPickerKey(msg)
		}
		if msg.String() == "ctrl+g" {
			return m.openSubagentPicker()
		}
		// 补全激活时：只拦截导航键和确认键，其余按键正常透传给输入框
		if m.completion != nil {
			switch msg.String() {
			case "esc":
				m.clearCompletionAndRelayout()
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
					switch m.completion.kind {
					case completionKindFile:
						isDir := strings.HasSuffix(selected, "/")
						if isDir {
							// 目录：不加空格，候选框保持开启以继续浏览
							m = m.applyFileCompletion(selected, false)
							if syncCmd := m.syncAtCompletion(); syncCmd != nil {
								return m, syncCmd
							}
							return m, nil
						}
						// 文件：行为与 Enter 一致（加空格 + 关闭候选框）
						m = m.applyFileCompletion(selected, true)
					case completionKindSkill:
						m = m.applySkillCompletion(selected)
					default:
						m = m.applyCommandCompletion(selected)
					}
				}
				m.clearCompletionAndRelayout()
				return m, nil
			case "enter":
				visible := m.completion.visibleItems()
				if !m.completion.loading && len(visible) > 0 {
					selected := visible[m.completion.selectedIndex]
					switch m.completion.kind {
					case completionKindFile:
						// Enter：补全路径并追加空格，结束引用
						m = m.applyFileCompletion(selected, true)
					case completionKindSkill:
						m = m.applySkillCompletion(selected)
					default:
						m = m.applyCommandCompletion(selected)
					}
				}
				m.clearCompletionAndRelayout()
				return m, nil
			}
			// 其他键：不 return，继续走下面的 switch 和 textarea 更新
		}
		if m.transcriptKeyScrollActive {
			switch msg.String() {
			case "up":
				m.viewport.ScrollUp(1)
				return m, nil
			case "down":
				m.viewport.ScrollDown(1)
				if m.viewport.AtBottom() {
					m.transcriptKeyScrollActive = false
				}
				return m, nil
			case "esc":
				if m.subagentPreview != nil {
					m.restoreMainTranscriptFromSubagentPreview()
					return m, m.input.Focus()
				}
				m.transcriptKeyScrollActive = false
				return m, nil
			}
			if isTextEditingKey(msg) || msg.String() == "tab" || msg.String() == "ctrl+c" {
				m.transcriptKeyScrollActive = false
			}
		}
		switch msg.String() {
		case "esc":
			if m.subagentPreview != nil {
				m.restoreMainTranscriptFromSubagentPreview()
				return m, m.input.Focus()
			}
		case "ctrl+c":
			now := time.Now()
			if !m.lastCtrlCAt.IsZero() && now.Sub(m.lastCtrlCAt) < time.Second {
				// 1 秒内连按两次：退出
				m.queryGuard.Cancel()
				m.chatQueue.Clear()
				return m, tea.Quit
			}
			// 第一次按：清空输入框（不进入历史）、关闭补全框
			m.lastCtrlCAt = now
			m.input.SetValue("")
			m.input.CursorEnd()
			m.pending = nil
			m.inputPasteFoldActive = false
			m.clearCompletionAndRelayout()
			m.syncInputMode()
			m.relayout()
			return m, nil
		case "ctrl+o":
			m.showThinking = !m.showThinking
			m.refreshViewportPreservingOffset()
			return m, nil
		case "up":
			return m.handleInputVerticalNavigation(-1)
		case "down":
			return m.handleInputVerticalNavigation(1)
		case "tab":
			if m.isModelWorkRunning() {
				return m.handleQueueSubmit()
			}
		case "enter":
			return m.handleSubmit()
		}
	case tea.MouseMsg:
		if next, handled, cmd := m.handleTranscriptMouse(msg); handled {
			next.transcriptKeyScrollActive = true
			return next, cmd
		}
		if isMouseWheel(msg) {
			if m.isTranscriptViewportMouse(msg) {
				var viewportCmd tea.Cmd
				m.viewport, viewportCmd = m.viewport.Update(msg)
				m.transcriptKeyScrollActive = true
				return m, viewportCmd
			}
			return m, nil
		}
		if m.isSidebarMouse(msg) {
			return m, nil
		}
	}

	var viewportCmd tea.Cmd
	m.viewport, viewportCmd = m.viewport.Update(msg)
	cmds = append(cmds, viewportCmd)

	if !m.isTerminalWorkRunning() {
		var inputCmd tea.Cmd
		beforeValue := m.input.Value()
		m.input.SetHeight(inputMaxVisibleLines)
		m.input, inputCmd = m.input.Update(msg)
		textChanged := beforeValue != m.input.Value()
		if isTextEditingKey(msg) || textChanged {
			m.resetHistoryNavigation()
		}
		m.syncInputPasteFoldState(msg, beforeValue, textChanged)
		m.syncInputMode()
		m.relayout()
		cmds = append(cmds, inputCmd)

		// 每次文本变化后同步补全状态
		if isTextEditingKey(msg) || textChanged {
			// / 命令补全：整个输入以 / 开头时触发
			m.syncCommandCompletion()
			// $ skill 补全：词边界 $ 触发（只要 / 补全未激活）
			m.syncSkillCompletion()
			// @ 文件补全：词边界 @ 触发（只要 / 补全未激活）
			if cmd := m.syncAtCompletion(); cmd != nil {
				cmds = append(cmds, cmd)
			}
			// 补全状态可能已改变（如删除 @ 后清空），需要重新计算 viewport 高度
			m.relayout()
		}
	}
	m.applyCursorAnimation()

	return m, tea.Batch(cmds...)
}
