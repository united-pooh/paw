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
	tea "github.com/charmbracelet/bubbletea"
	"paw/internal/model"
	"paw/internal/settings"
	"paw/internal/skill"
	"paw/internal/theme"
	selecttool "paw/internal/tool/select"
	"paw/internal/ui/bubble/textareax"
	"paw/internal/ui/bubble/viewportx"
)

// newModel 创建完整的 TUI 状态模型，并初始化输入框、滚动区和系统消息。
func newModel(ctx context.Context, runner Runner, sessionID string, controller ModelConfigController, settingsController SettingsController, taskController TaskController, sessionStore SessionStore, anchor *terminalCursorAnchor) appModel {
	now := time.Now()
	input := textareax.New()
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

	vp := viewportx.New(80, 20)
	vp.MouseWheelDelta = 3
	// viewport 默认 KeyMap 把空格、j/k/u/d/f/b/h/l 和 ctrl+u/ctrl+d 等绑定为
	// 滚动键。输入框常驻聚焦时，这些按键必须原样交给 textarea（打字与编辑），
	// 否则输入字符会连带滚动 transcript（ctrl+u/ctrl+d 还会同时删字和滚屏）。
	// 这里只保留不与输入冲突的 pgup/pgdn 分页键：↑/↓ 由 app 自行路由，
	// 左右无横向滚动内容，滚轮滚动也由 app 显式处理。
	vp.KeyMap = viewportx.KeyMap{
		PageDown: key.NewBinding(key.WithKeys("pgdown")),
		PageUp:   key.NewBinding(key.WithKeys("pgup")),
	}
	skillRoot, _ := os.Getwd()
	model := appModel{
		theme:                     selectedTheme,
		styles:                    styles,
		ctx:                       ctx,
		runner:                    runner,
		sessionID:                 sessionID,
		modelConfig:               controller,
		settingsConfig:            settingsController,
		taskController:            taskController,
		sessionStore:              sessionStore,
		commandRegistry:           NewCommandRegistry(),
		skillRegistry:             skill.NewRegistry(skill.DefaultRoots(skillRoot)),
		cursorAnchor:              anchor,
		input:                     input,
		viewport:                  vp,
		cursorFrameAt:             now,
		uiAnimationFrameScheduled: true,
		worktreeCWD:               skillRoot,
		worktree:                  worktreeSnapshot{name: filepath.Base(filepath.Clean(skillRoot))},
		worktreeReader:            readWorktreeStatus,
		activeAssistant:           -1,
		activeThinking:            -1,
		activeReasoning:           -1,
		activeTurnUserEntry:       -1,
		doneAssistant:             -1,
		newMessageNoticeCycle:     1,
		toolInspectIndex:          -1,
		toolHoverIndex:            -1,
		toolRuntime:               transcriptToolRuntimeIndex{initialized: true},
		historyIndex:              -1,
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
		cmds = append(cmds, waitTodoBrokerEventCmd(m.ctx, m.todoBroker, m.sessionID))
	}
	if m.taskUpdates != nil {
		cmds = append(cmds, waitTaskUpdateCmd(m.ctx, m.taskUpdates))
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
	case transcriptWheelBatchMsg:
		m = m.applyTranscriptWheelBatch(msg.lines, msg.x, msg.y)
		return m, m.renewDeferredTranscriptRefresh()
	case transcriptDeferredRefreshMsg:
		if !m.transcriptRefreshDeferred || msg.generation != m.transcriptRefreshDeferredGeneration {
			return m, nil
		}
		m.transcriptRefreshDeferred = false
		m.refreshViewport()
		return m, nil
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
	case taskUpdateMsg:
		m.refreshActivityTasks()
		previewChanged := m.refreshTaskPreviewFromTasks()
		toolEntriesChanged := m.refreshTaskToolEntriesFromTasks()
		if previewChanged || toolEntriesChanged {
			m.refreshViewport()
		}
		if m.taskUpdates != nil && !msg.closed {
			return m, waitTaskUpdateCmd(m.ctx, m.taskUpdates)
		}
		if msg.closed {
			m.taskUpdates = nil
		}
		return m, nil
	case cursorFrameMsg:
		m.uiAnimationFrameScheduled = false
		m.cursorFrameAt = time.Time(msg)
		m.spinnerFrameIdx++
		m.applyCursorAnimation()
		m.updateWaveAmp(time.Time(msg))
		m.refreshActivityFromTasks(time.Time(msg))
		m.refreshRunningToolProgress(time.Time(msg))
		animationNow := time.Time(msg)
		m.releaseStreamCharacters()
		m.flushTranscriptRefreshIfDue(animationNow)
		var frameCmd tea.Cmd
		if m.needsUIAnimationFrames(time.Time(msg)) {
			frameCmd = m.scheduleUIAnimationFrame()
		} else {
			frameCmd = m.scheduleClockTick() // 空闲：由低频率时钟链接手
		}
		if frameCmd != nil {
			return m, frameCmd
		}
	case clockTickMsg:
		m.clockTickScheduled = false
		now := time.Time(msg)
		// 工作/动画帧链已接管屏幕：时钟链退出，不再续命。
		if m.needsUIAnimationFrames(now) {
			return m, nil
		}
		// 用户刚有过键盘输入（含 IME 合成期）：跳过本次重绘，避免扰动
		// Ghostty 预编辑光标；稍后重试。
		if now.Sub(m.lastKeyEventAt) < idleClockInputGuard {
			return m, m.scheduleClockTick()
		}
		// 空闲且无近期输入：推进帧时间并重绘（Bubble Tea 自动重绘整帧），
		// header 时钟 / 状态栏时间随之刷新；继续时钟链。
		m.cursorFrameAt = now
		return m, m.scheduleClockTick()
	case assistantDeltaMsg:
		if string(msg) != "" {
			m.turnHasModelOutput = true
			m.readyPendingToolSegmentsInCurrentTurn()
			if !m.toolInspectActive {
				m.toolGroupExpanded = false
				m.toolGroupFullResult = false
			}
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
	case assistantPartMsg:
		if msg.delta != "" || msg.lifecycle == "start" || msg.lifecycle == "end" {
			m.turnHasModelOutput = true
		}
		m.isGenerating = true
		m.handleAssistantPartEvent(msg)
		return m, nil
	case toolCallMsg:
		m.turnHasModelOutput = true
		m.batchTranscriptRefresh(func() {
			m.finalizeThinkingStream()
			m.finalizeAssistantStream()
			m.isGenerating = false
			m.recordToolCallEntry(msg.ID, msg.Name, json.RawMessage(msg.Input), msg.FileMutationKnown, msg.IsFileMutation, msg.FileMutation)
		})
		return m, nil
	case toolResultMsg:
		status := "ok"
		if msg.IsError {
			status = "error"
		}
		m.batchTranscriptRefresh(func() {
			m.finalizeThinkingStream()
			m.finalizeAssistantStream()
			m.recordToolResultEntry(msg.ToolUseID, msg.Name, status, msg.Content, msg.IsError, msg.FileMutationKnown, msg.IsFileMutation, msg.FileMutation)
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
			color: msg.Color,
		})
		return m, nil
	case doneMsg:
		var deferredRefresh tea.Cmd
		deferCanceledRefresh := m.modelCancelRequested
		if deferCanceledRefresh {
			if !m.transcriptRefreshDeferred {
				m.transcriptRefreshDeferred = true
			}
			deferredRefresh = m.renewDeferredTranscriptRefresh()
		}
		m.finalizeThinkingStream()
		m.doneAssistant = m.finalizeAssistantStream()
		m.isGenerating = false
		if deferCanceledRefresh {
			return m, deferredRefresh
		}
		m.refreshViewport()
		return m, nil
	case planFinalizedMsg:
		m.planWorking = false
		m.planMode = false
		m.addEntry(transcriptEntry{kind: entrySystem, title: "plan", body: "plan 已定稿：" + msg.path + "\n切换到 chat 模式开始执行。"})
		m.inputSource = inputSourceFresh
		next, cmd := m.startChatTurn("开始执行已批准的计划：" + msg.path)
		return next, cmd
	case planStoppedMsg:
		m.planWorking = false
		m.planMode = false
		m.turnStartedAt = time.Time{}
		m.turnID = ""
		body := "plan 会话已结束"
		if strings.TrimSpace(msg.reason) != "" {
			body += "：" + msg.reason
		}
		m.addEntry(transcriptEntry{kind: entrySystem, title: "plan", body: body})
		cmds = append(cmds, m.input.Focus())
		return m, tea.Batch(cmds...)
	case goalStoppedMsg:
		wasWorking := m.isAgentWorking()
		m.goalWorking = false
		m.goalMode = false
		m.turnStartedAt = time.Time{}
		m.turnID = ""
		body := "goal 会话已结束"
		if strings.TrimSpace(msg.reason) != "" {
			body += "：" + msg.reason
		}
		m.addEntry(transcriptEntry{kind: entrySystem, title: "goal", body: body})
		if wasWorking && !m.isAgentWorking() {
			m.startTokenRippleExit(m.animationNow())
			if frameCmd := m.scheduleUIAnimationFrame(); frameCmd != nil {
				cmds = append(cmds, frameCmd)
			}
		}
		cmds = append(cmds, m.input.Focus())
		return m, tea.Batch(cmds...)
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
		if expectedCancel && hadModelOutput {
			if !m.transcriptRefreshDeferred {
				m.transcriptRefreshDeferred = true
			}
			if deferredRefresh := m.renewDeferredTranscriptRefresh(); deferredRefresh != nil {
				cmds = append(cmds, deferredRefresh)
			}
		}
		m.finalizeThinkingStream()
		assistantIndex := m.doneAssistant
		if msg.err != nil {
			m.finalizeAssistantStream()
		} else {
			if finalized := m.finalizeAssistantStream(); finalized >= 0 {
				assistantIndex = finalized
			}
			if msg.metadata != nil && assistantIndex >= 0 && assistantIndex < len(m.transcript) && m.transcript[assistantIndex].kind == entryAssistant {
				metadata := *msg.metadata
				m.transcript[assistantIndex].turnMetadata = &metadata
				m.touchTranscriptEntryAt(assistantIndex)
				m.refreshViewport()
			}
			_ = hadModelOutput
			_ = assistantIndex
		}
		m.doneAssistant = -1
		m.isGenerating = false
		m.finishModelWork()
		m.modelCancelRequested = false
		m.queryGuard.FinishModel()
		m.turnStartedAt = time.Time{}
		m.turnID = ""
		m.toolGroupExpanded = false
		m.toolGroupFullResult = false
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
	case taskFinishedMsg:
		wasWorking := m.isAgentWorking()
		expectedCancel := m.modelCancelRequested && errors.Is(msg.err, context.Canceled)
		m.finishModelWork()
		m.modelCancelRequested = false
		m.queryGuard.FinishModel()
		m.isGenerating = false
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
				body:  rendertaskResult(msg.result),
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
			if msg.source == sessionRestoreTaskEnter {
				title = "task"
				m.taskPicker = nil
			} else {
				m.sessionPicker = nil
			}
			m.addEntry(transcriptEntry{kind: entryError, title: title, body: msg.err.Error()})
		} else {
			if msg.source != sessionRestoreTaskEnter {
				if rebinder, ok := m.goalController.(SessionControllerRebinder); ok {
					if err := rebinder.Rebind(msg.sessionID); err != nil {
						msg.err = fmt.Errorf("restore goal binding: %w", err)
					}
				}
				if msg.err == nil {
					if rebinder, ok := m.planController.(SessionControllerRebinder); ok {
						if err := rebinder.Rebind(msg.sessionID); err != nil {
							msg.err = fmt.Errorf("restore plan binding: %w", err)
						}
					}
				}
				if msg.err == nil {
					if loader, ok := m.runner.(sessionModeLoader); ok {
						modes, err := loader.SessionModes(m.ctx, msg.sessionID)
						if err != nil {
							msg.err = fmt.Errorf("refresh restored modes: %w", err)
						} else {
							msg.modes = modes
						}
					}
				}
			}
			if msg.err != nil {
				m.sessionPicker = nil
				m.addEntry(transcriptEntry{kind: entryError, title: "sessions", body: msg.err.Error()})
				cmds = append(cmds, m.input.Focus())
				return m, tea.Batch(cmds...)
			}
			switch msg.source {
			case sessionRestoreTaskEnter:
				m.applyTaskPreviewRestore(msg)
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
	case transcriptClickActionMsg:
		if msg.seq != m.clickActionSeq || !m.clickActionPending {
			// 双击窗口内到达了新的按下：延迟的单击动作作废。
			return m, nil
		}
		m.clickActionPending = false
		next, handled, cmd := m.performTranscriptClick(msg.point)
		if !handled {
			m.refreshViewportPreservingOffset()
			return m, nil
		}
		return next, cmd
	case copyToastExpiredMsg:
		// 复制反馈到期：清除状态栏 toast，触发一次重绘。
		m.copyToast = ""
		return m, nil
	case configCenterSavedExpiredMsg:
		if m.configCenter == msg.state && m.configCenter != nil && m.configCenter.noticeSequence == msg.sequence {
			m.configCenter.notice = ""
		}
		return m, nil
	case translateResultMsg:
		// 过期响应（面板已切换/关闭）直接丢弃；否则按结果更新面板。
		if msg.seq != m.translateSeq || m.translatePanel == nil || m.translatePanel.word != msg.word {
			return m, nil
		}
		if msg.err != nil {
			m.translatePanel = &translatePanel{
				state: translatePanelError,
				word:  msg.word,
				err:   msg.err.Error(),
			}
			return m, nil
		}
		panel := parseTranslateResult(msg.word, msg.text)
		m.translatePanel = &panel
		return m, nil
	case tea.KeyMsg:
		var rawMouseFragment bool
		msg, rawMouseFragment = m.filterRawMouseEscapeKey(msg)
		if rawMouseFragment {
			return m, nil
		}
		// 记录最后键盘输入时刻（确认不是 raw mouse 碎片后）：空闲时钟链
		// 用它在 IME 合成/连续输入窗口内跳过重绘。
		m.lastKeyEventAt = time.Now()
		// 键盘输入意味着用户已经离开点击意图，取消挂起的延迟单击动作。
		if m.clickActionPending {
			m.clickActionPending = false
			m.clickActionSeq++
		}
		if m.translatePanel != nil {
			// 翻译面板是最轻量的浮层：esc 直接关闭，不影响其他状态。
			if msg.String() == "esc" {
				m.translatePanel = nil
				return m, nil
			}
		}
		if m.selectionDock != nil {
			return m.handleSelectionDockKey(msg)
		}
		if m.themePicker != nil {
			return m.handleThemePickerKey(msg)
		}
		if m.configCenter != nil {
			return m.handleConfigCenterKey(msg)
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
		if m.taskPicker != nil {
			return m.handleTaskPickerKey(msg)
		}
		if m.toolInspectActive {
			return m.handleToolInspectKey(msg)
		}
		// Queue interaction owns its keys before completion, transcript scrolling,
		// history navigation, and textarea handling. This prevents Down from being
		// mistaken for history navigation and keeps edit mode from submitting a
		// normal chat turn.
		if m.queueMode == queueModeEditing {
			return m.handleQueueEditKey(msg)
		}
		if m.queueMode == queueModeSelecting {
			return m.handleQueueKey(msg)
		}
		if msg.String() == "down" && m.canEnterQueueSelection() {
			return m.enterQueueSelection()
		}
		if msg.String() == "ctrl+t" {
			return m.openToolInspect()
		}

		if msg.String() == "ctrl+v" && !m.isTerminalWorkRunning() {
			return m, clipboardPasteCmd(m.ctx, textareaAbsoluteCursor(m.input))
		}
		if msg.String() == "ctrl+g" {
			// ctrl+g 是 task 面板的全局 toggle：面板打开时由
			// handleTaskPickerKey 拦截并关闭；task preview 中按下
			// 视为收起面板，直接返回主 transcript；其余状态打开面板。
			if m.taskPreview != nil {
				m.restoreMainTranscriptFromTaskPreview()
				return m, m.input.Focus()
			}
			return m.openTaskPicker()
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
				m.clearToolHover()
				m.syncNewMessageNoticeAfterScroll()
				return m, nil
			case "down":
				m.viewport.ScrollDown(1)
				m.clearToolHover()
				m.syncNewMessageNoticeAfterScroll()
				if m.viewport.AtBottom() {
					m.transcriptKeyScrollActive = false
				}
				return m, nil
			case "esc":
				if m.taskPreview != nil {
					m.restoreMainTranscriptFromTaskPreview()
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
			if m.taskPreview != nil {
				m.restoreMainTranscriptFromTaskPreview()
				return m, m.input.Focus()
			}
		case "ctrl+c":
			if m.isAgentWorking() {
				// 工作态优先取消当前 streaming tool。第一次 Ctrl+C 只停止工具，
				// 不取消整轮；再次按下或没有活动工具时才取消当前 turn。
				m.lastCtrlCAt = time.Time{}
				if !m.toolCancelRequested {
					if canceler, ok := m.runner.(ForegroundCancelRunner); ok && canceler.CancelCurrentTool() {
						m.toolCancelRequested = true
						return m, nil
					}
				}
				if canceler, ok := m.runner.(ForegroundCancelRunner); ok {
					canceler.CancelTurn()
				}
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
			if m.completion == nil && !m.isModelWorkRunning() {
				m.cycleInputMode()
				return m, nil
			}
			if m.isModelWorkRunning() {
				return m.handleQueueSubmit()
			}
		case "enter":
			return m.handleSubmit()
		}
	case tea.MouseMsg:
		m.rawMouseEscapePending = ""
		// 任何新的按下（transcript / todo / 通知 / 输入 dock / 滚轮）都取消
		// 挂起的延迟单击动作，使双击优先于单击动作。
		if msg.Action == tea.MouseActionPress && m.clickActionPending {
			m.clickActionPending = false
			m.clickActionSeq++
		}
		if isHorizontalMouseWheel(msg) {
			return m, nil
		}
		if next, handled, cmd := m.handleNewMessageNoticeMouse(msg); handled {
			return next, cmd
		}
		if next, handled, cmd := m.handleTranscriptMouse(msg); handled {
			next.syncNewMessageNoticeAfterScroll()
			// 单击/拖选是选择与打开意图，不进入键盘滚动焦点；只有滚轮滚动
			// 才把 ↑/↓ 交给 transcript（见下方 isMouseWheel 分支）。
			return next, cmd
		}
		if isMouseWheel(msg) {
			if m.isTranscriptViewportMouse(msg) {
				if lines, ok := transcriptWheelLines(m, msg); ok {
					return m.applyTranscriptWheelBatch(lines, msg.X, msg.Y), nil
				}
				var viewportCmd tea.Cmd
				beforeOffset := m.viewport.YOffset
				m.viewport, viewportCmd = m.viewport.Update(msg)
				if m.viewport.YOffset != beforeOffset {
					m.reconcileToolHoverAtMouse(msg.X, msg.Y)
				}
				m.syncNewMessageNoticeAfterScroll()
				m.transcriptKeyScrollActive = true
				return m, viewportCmd
			}
			return m, nil
		}
		if msg.Action == tea.MouseActionPress && m.isInputDockMouse(msg) {
			// 用户点回输入框准备编辑：退出 transcript 键盘滚动焦点，
			// 让 ↑/↓ 恢复为输入框内光标移动/历史导航。
			m.transcriptKeyScrollActive = false
		}
	}

	var viewportCmd tea.Cmd
	beforeViewportOffset := m.viewport.YOffset
	m.viewport, viewportCmd = m.viewport.Update(msg)
	if m.viewport.YOffset != beforeViewportOffset {
		m.clearToolHover()
	}
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
