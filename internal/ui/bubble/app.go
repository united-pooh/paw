// 本文件定义 Bubble Tea 应用模型的创建、初始化和事件更新逻辑。
package bubble

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/cursor"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"paw/internal/model"
	"paw/internal/settings"
	"paw/internal/skill"
	"paw/internal/theme"
	selecttool "paw/internal/tool/select"
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
	input.Placeholder = "Ask anything…"
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

	cfg := settings.DefaultConfig()
	if settingsController != nil {
		cfg = settings.Normalize(settingsController.CurrentSettings())
	}
	if setter, ok := runner.(interface{ SetContextLimitTokens(int) }); ok {
		modelCfg := model.Config{}
		if controller != nil {
			modelCfg = controller.CurrentModelConfig()
		}
		setter.SetContextLimitTokens(model.EffectiveContextLimitTokens(modelCfg))
	}
	selectedTheme, ok := theme.ByID(cfg.UI.Theme)
	if !ok {
		selectedTheme, _ = theme.ByID(theme.Default)
	}
	styles := NewStyleSet(selectedTheme.Colors)

	vp := viewport.New(80, 20)
	vp.MouseWheelDelta = 1
	skillRoot, _ := os.Getwd()
	model := appModel{
		theme:                     selectedTheme,
		styles:                    styles,
		ctx:                       ctx,
		runner:                    runner,
		sessionID:                 sessionID,
		modelConfig:               controller,
		settingsConfig:            settingsController,
		subagents:                 subagentController,
		sessionStore:              sessionStore,
		commandRegistry:           NewCommandRegistry(),
		skillRegistry:             skill.NewRegistry(skill.DefaultRoots(skillRoot)),
		cursorAnchor:              anchor,
		input:                     input,
		viewport:                  vp,
		cursorFrameAt:             now,
		uiAnimationFrameScheduled: true,
		pipelineActiveAfter:       now,
		worktreeCWD:               skillRoot,
		worktree:                  worktreeSnapshot{name: filepath.Base(filepath.Clean(skillRoot))},
		worktreeReader:            readWorktreeStatus,
		activeAssistant:           -1,
		activeThinking:            -1,
		activeTurnUserEntry:       -1,
		doneAssistant:             -1,
		newMessageNoticeCycle:     1,
		toolInspectIndex:          -1,
		toolHoverIndex:            -1,
		historyIndex:              -1,
		latestTodoIndex:           -1,
		transcript:                nil,
	}
	model.activateThemeStyles()
	model.applyTextareaTheme()
	// Focus only after the themed styles are installed. textarea keeps an
	// internal pointer to the active style; focusing earlier would leave the
	// first render bound to its default black CursorLine style.
	model.input.Focus()
	model.applyCursorAnimation()
	model.refreshViewport()
	return model
}

// Init 返回 Bubble Tea 启动时需要执行的初始命令。
func (m appModel) Init() tea.Cmd {
	cmds := []tea.Cmd{m.input.Focus(), cursorFrameTick()}
	if m.selectionBroker != nil {
		cmds = append(cmds, waitSelectionBrokerEventCmd(m.ctx, m.selectionBroker))
	}
	if m.todoBroker != nil {
		cmds = append(cmds, waitTodoBrokerEventCmd(m.ctx, m.todoBroker))
	}
	if m.worktreeCWD != "" {
		cmds = append(cmds, worktreeRefreshCmd(m.ctx, m.worktreeCWD, m.worktreeReader))
	}
	return tea.Batch(cmds...)
}

// Update 是 Bubble Tea 的核心状态机，负责把输入事件、模型事件和工具事件规约为新状态。
func (m appModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case todoBrokerEventMsg:
		next, cmd := todoBrokerEventCommand(m, msg)
		return next, cmd
	case selectionBrokerEventMsg:
		if errors.Is(msg.err, selecttool.ErrBrokerClosed) || errors.Is(msg.err, context.Canceled) || msg.event.Kind == selecttool.EventClosed {
			m.selectionDock = nil
			m.relayout()
			return m, m.input.Focus()
		}
		if msg.err != nil {
			return m, waitSelectionBrokerEventCmd(m.ctx, m.selectionBroker)
		}
		switch msg.event.Kind {
		case selecttool.EventRequest:
			if m.selectionDock == nil || m.selectionDock.request.ID == msg.event.Request.ID {
				m.selectionDock = newSelectionDock(msg.event.Request)
				m.relayout()
			}
		case selecttool.EventInvalidated:
			if m.selectionDock != nil && m.selectionDock.request.ID == msg.event.RequestID {
				m.selectionDock = nil
				m.relayout()
			}
		}
		return m, waitSelectionBrokerEventCmd(m.ctx, m.selectionBroker)
	case tea.WindowSizeMsg:
		wasAtBottom := m.viewport.AtBottom()
		m.ready = true
		m.width = msg.Width
		m.height = msg.Height
		m.relayout()
		m.resizeStreamingBuffers()
		m.refreshViewportWithBottomState(wasAtBottom)
		return m, nil
	case cursorFrameMsg:
		m.uiAnimationFrameScheduled = false
		m.cursorFrameAt = time.Time(msg)
		m.spinnerFrameIdx++
		m.applyCursorAnimation()
		m.updateContextMeterAnimation()
		m.updateWaveAmp(time.Time(msg))
		m.refreshActivityTasks()
		m.refreshSubagentPreviewFromTasks()
		m.refreshRunningToolProgress(time.Time(msg))
		if m.transcriptRefreshPending {
			if m.viewport.AtBottom() {
				m.refreshViewport()
			} else {
				m.refreshViewportPreservingOffset()
			}
		}
		var frameCmd tea.Cmd
		if m.needsUIAnimationFrames(time.Time(msg)) {
			frameCmd = m.scheduleUIAnimationFrame()
		}
		pollCmd := pipelinePollCmd(m.pipelineActiveAfter)
		if frameCmd == nil {
			return m, pollCmd
		}
		return m, tea.Batch(frameCmd, pollCmd)
	case assistantDeltaMsg:
		if string(msg) != "" {
			m.turnHasModelOutput = true
		}
		m.isGenerating = true
		m.consumeAssistantStreamDelta(string(msg))
		return m, nil
	case thinkingDeltaMsg:
		if string(msg) != "" {
			m.turnHasModelOutput = true
		}
		m.isGenerating = true
		m.consumeThinkingStreamDelta(string(msg))
		return m, nil
	case toolCallMsg:
		m.turnHasModelOutput = true
		m.finalizeThinkingStream()
		m.finalizeAssistantStream(transcriptRenderFormatted)
		m.isGenerating = false
		m.recordToolCallEntry(msg.ID, msg.Name, json.RawMessage(msg.Input), msg.FileMutationKnown, msg.IsFileMutation, msg.FileMutation)
		return m, nil
	case toolResultMsg:
		m.finalizeThinkingStream()
		m.finalizeAssistantStream(transcriptRenderFormatted)
		status := "ok"
		if msg.IsError {
			status = "error"
		}
		m.recordToolResultEntry(msg.ToolUseID, msg.Name, status, msg.Content, msg.IsError, msg.FileMutationKnown, msg.IsFileMutation, msg.FileMutation)
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
		m.finalizeThinkingStream()
		m.doneAssistant = m.finalizeAssistantStream(transcriptRenderFormatted)
		m.isGenerating = false
		m.refreshViewport()
		return m, nil
	case contextCompactionFinishedMsg:
		m.finishModelWork()
		m.modelCancelRequested = false
		m.queryGuard.FinishModel()
		m.syncRunningFlags()
		if msg.err != nil {
			m.addEntry(transcriptEntry{kind: entryError, title: "compact", body: msg.err.Error()})
		} else if msg.result.FoldedMessages == 0 {
			m.addEntry(transcriptEntry{kind: entrySystem, title: "compact", body: fmt.Sprintf("nothing to compact: %d messages", msg.result.BeforeMessages)})
		} else {
			lines := []string{fmt.Sprintf("compacted %d messages: %d → %d; full journal preserved", msg.result.FoldedMessages, msg.result.BeforeMessages, msg.result.AfterMessages)}
			if len(msg.result.ArchivePaths) > 0 {
				lines = append(lines, "archive: "+msg.result.ArchivePaths[0])
			}
			if msg.result.Mechanical {
				lines = append(lines, "summary unavailable; folded mechanically")
			}
			m.addEntry(transcriptEntry{
				kind:  entrySystem,
				title: "compact",
				body:  strings.Join(lines, "\n"),
			})
		}
		cmds = append(cmds, m.input.Focus())
		return m, tea.Batch(cmds...)
	case turnFinishedMsg:
		wasWorking := m.isAgentWorking()
		hadModelOutput := m.turnHasModelOutput
		expectedCancel := m.modelCancelRequested && errors.Is(msg.err, context.Canceled)
		m.finalizeThinkingStream()
		assistantIndex := m.doneAssistant
		if msg.err != nil {
			m.finalizeAssistantStream(transcriptRenderPlain)
			m.setAssistantRenderMode(m.doneAssistant, transcriptRenderPlain)
		} else {
			if finalized := m.finalizeAssistantStream(transcriptRenderFormatted); finalized >= 0 {
				assistantIndex = finalized
			}
			if msg.metadata != nil && assistantIndex >= 0 && assistantIndex < len(m.transcript) && m.transcript[assistantIndex].kind == entryAssistant {
				metadata := *msg.metadata
				m.transcript[assistantIndex].turnMetadata = &metadata
				touchTranscriptEntry(&m.transcript[assistantIndex])
				m.refreshViewport()
			}
			if hadModelOutput && m.assistantFinalAnswerVisible(assistantIndex) {
				m.foldCompletedTodoAfterFinalAnswer()
			}
		}
		m.doneAssistant = -1
		m.isGenerating = false
		m.finishModelWork()
		m.modelCancelRequested = false
		m.queryGuard.FinishModel()
		m.turnStartedAt = time.Time{}
		m.turnID = ""
		m.syncRunningFlags()
		if wasWorking && !m.isAgentWorking() {
			m.startTokenRippleExit(m.animationNow())
			if frameCmd := m.scheduleUIAnimationFrame(); frameCmd != nil {
				cmds = append(cmds, frameCmd)
			}
		}
		if expectedCancel && !hadModelOutput {
			m.removeInterruptedTurnUserEntry()
			if msg.interruptedDraft != nil && m.input.Value() == "" {
				m.setInputDraft(*msg.interruptedDraft)
				m.inputPasteFoldActive = false
				m.syncInputMode()
				m.relayout()
			}
		} else if msg.err != nil && !expectedCancel {
			m.markRunningToolsError(msg.err)
			m.pendingToolCites = nil
			m.addEntry(transcriptEntry{
				kind:  entryError,
				title: "error",
				body:  msg.err.Error(),
			})
			if msg.restoreDraft != nil && m.input.Value() == "" {
				m.setInputDraft(*msg.restoreDraft)
				m.inputPasteFoldActive = false
				m.syncInputMode()
				m.relayout()
			}
		} else if msg.metadataErr != nil {
			m.addEntry(transcriptEntry{
				kind:  entryError,
				title: "turn metadata",
				body:  msg.metadataErr.Error(),
			})
		}
		m.activeTurnUserEntry = -1
		m.turnHasModelOutput = false
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
		wasWorking := m.isAgentWorking()
		expectedCancel := m.modelCancelRequested && errors.Is(msg.err, context.Canceled)
		m.finishModelWork()
		m.modelCancelRequested = false
		m.queryGuard.FinishModel()
		m.turnStartedAt = time.Time{}
		m.syncRunningFlags()
		if wasWorking && !m.isAgentWorking() {
			m.startTokenRippleExit(m.animationNow())
			if frameCmd := m.scheduleUIAnimationFrame(); frameCmd != nil {
				cmds = append(cmds, frameCmd)
			}
		}
		agentTitle := resultDisplayName(msg.result)
		agentColor := strings.TrimSpace(msg.result.AgentColor)
		if msg.err != nil && !expectedCancel {
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
		m.resetStreamingBuffers()
		m.resetToolInspect()
		m.clearInputTokens()
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
	case clipboardPasteMsg:
		if msg.err != nil {
			// A normal text clipboard is handled by clipboardPasteCmd; only
			// surface an error when neither image nor text could be read.
			if msg.err != errClipboardNoImage {
				m.addEntry(transcriptEntry{kind: entryError, title: "clipboard", body: msg.err.Error()})
			}
			return m, nil
		}
		beforeValue := m.input.Value()
		if msg.image != nil {
			m.insertClipboardImage(msg.cursor, *msg.image)
		} else {
			m.insertClipboardText(msg.text)
		}
		if beforeValue != m.input.Value() {
			m.resetHistoryNavigation()
			m.syncInputPasteFoldState(tea.KeyMsg{Type: tea.KeyRunes, Paste: true}, beforeValue, true)
			m.syncInputMode()
			m.relayout()
			m.syncCommandCompletion()
			m.syncSkillCompletion()
		}
		m.applyCursorAnimation()
		return m, nil
	case pipelineStateUpdatedMsg:
		m.pipelineState = msg.state
		return m, nil
	case worktreeRefreshMsg:
		if msg.err == nil && msg.snapshot.visible() {
			m.worktree = msg.snapshot
		}
		// Do not keep an idle polling loop alive. Any periodic Bubble Tea message
		// redraws the frame and would pull Ghostty's IME cursor back to the
		// textarea's committed position while preedit text is advancing it.
		return m, nil
	case worktreeRefreshTickMsg:
		return m, nil
	case tea.KeyMsg:
		var rawMouseFragment bool
		msg, rawMouseFragment = m.filterRawMouseEscapeKey(msg)
		if rawMouseFragment {
			return m, nil
		}
		if m.selectionDock != nil {
			return m.handleSelectionDockKey(msg)
		}
		if m.themePicker != nil {
			return m.handleThemePickerKey(msg)
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
		if m.toolInspectActive {
			return m.handleToolInspectKey(msg)
		}
		if msg.String() == "ctrl+t" {
			return m.openToolInspect()
		}
		if msg.String() == "ctrl+v" && !m.isTerminalWorkRunning() {
			return m, clipboardPasteCmd(m.ctx, textareaAbsoluteCursor(m.input))
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
				m.syncNewMessageNoticeAfterScroll()
				return m, nil
			case "down":
				m.viewport.ScrollDown(1)
				m.syncNewMessageNoticeAfterScroll()
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
			if m.isAgentWorking() {
				// 工作态优先取消当前模型操作，不清空用户输入，也不开始双击退出计时。
				m.lastCtrlCAt = time.Time{}
				m.cancelModelWork()
				return m, nil
			}
			if m.isWorkRunning() {
				// 其他前台工作结束前也不允许双击退出；避免一次工作态按键
				// 与一次 ready 态按键拼成退出手势。
				m.lastCtrlCAt = time.Time{}
				return m, nil
			}
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
			m.clearInputTokens()
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
		m.rawMouseEscapePending = ""
		if isHorizontalMouseWheel(msg) {
			return m, nil
		}
		if next, handled, cmd := m.handleNewMessageNoticeMouse(msg); handled {
			return next, cmd
		}
		if next, handled, cmd := m.handleTranscriptMouse(msg); handled {
			next.syncNewMessageNoticeAfterScroll()
			if msg.Action != tea.MouseActionMotion || next.selecting {
				next.transcriptKeyScrollActive = true
			}
			return next, cmd
		}
		if isMouseWheel(msg) {
			if m.isTranscriptViewportMouse(msg) {
				var viewportCmd tea.Cmd
				m.viewport, viewportCmd = m.viewport.Update(msg)
				m.syncNewMessageNoticeAfterScroll()
				m.transcriptKeyScrollActive = true
				return m, viewportCmd
			}
			return m, nil
		}
	}

	var viewportCmd tea.Cmd
	m.viewport, viewportCmd = m.viewport.Update(msg)
	m.syncNewMessageNoticeAfterScroll()
	cmds = append(cmds, viewportCmd)

	if !m.isTerminalWorkRunning() {
		var inputCmd tea.Cmd
		beforeValue := m.input.Value()
		m.input.SetHeight(inputMaxVisibleLines)
		inputCmd = m.updateTokenAwareInput(msg)
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
