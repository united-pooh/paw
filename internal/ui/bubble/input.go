// 本文件定义输入框提交、斜杠命令、终端模式和历史输入导航行为。
package bubble

import (
	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"strings"
	"time"
)

// handleSubmit 处理 Enter 提交，根据输入内容分派到聊天、命令或终端执行路径。
func (m appModel) handleSubmit() (tea.Model, tea.Cmd) {
	m.reconcileLegacyRunningState()
	var line string
	var ok bool
	m, line, ok = m.consumeSubmittedInput()
	if !ok {
		return m, nil
	}
	m.restoreMainTranscriptFromSubagentPreview()

	if isExitCommandInput(line) {
		m.rememberInputHistory(line)
		_, cmd := m.handleCommand(line)
		return m, cmd
	}
	if line == "!" {
		m.toggleTerminalMode()
		return m, nil
	}
	if m.terminalMode {
		m.rememberInputHistory(line)
		return m.submitShellCommand(line)
	}
	if command, ok := shellCommandFromBang(line); ok {
		if m.isWorkRunning() {
			m.addEntry(transcriptEntry{kind: entrySystem, title: "busy", body: "terminal commands are unavailable while a turn is running"})
			return m, nil
		}
		m.rememberInputHistory(line)
		return m.submitShellCommand(command)
	}

	if rewritten, ok := m.rewriteSlashSkillInput(line); ok {
		line = rewritten
	} else {
		if handled, cmd := m.handleCommand(line); handled {
			return m, cmd
		}
	}

	if m.isModelWorkRunning() {
		if len(imageTokensInDraft(m.submittedDraft)) > 0 {
			return m.queueChatInput(line), nil
		}
		return m.submitSupplement(line), nil
	}
	if m.isTerminalWorkRunning() {
		m.addEntry(transcriptEntry{kind: entrySystem, title: "busy", body: "chat is unavailable while a terminal command is running"})
		return m, nil
	}

	m.rememberInputHistory(line)
	return m.startChatTurn(line)
}

func (m appModel) rewriteSlashSkillInput(line string) (string, bool) {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "/") {
		return "", false
	}
	token := commandToken(trimmed)
	if token == "" || token == "/" {
		return "", false
	}
	if m.commandRegistry != nil {
		if _, ok := m.commandRegistry.Lookup(token); ok {
			return "", false
		}
	}
	ref, ok := m.slashSkillReference(token)
	if !ok {
		return "", false
	}
	args := strings.TrimSpace(strings.TrimPrefix(trimmed, token))
	if args == "" {
		return ref, true
	}
	return ref + " " + args, true
}

func (m appModel) handleQueueSubmit() (tea.Model, tea.Cmd) {
	var line string
	var ok bool
	m, line, ok = m.consumeSubmittedInput()
	if !ok {
		return m, nil
	}
	if m.isModelWorkRunning() {
		return m.queueChatInput(line), nil
	}
	draft := m.submittedDraft
	if strings.TrimSpace(draft.Text) != line {
		draft = inputDraft{Text: line}
	}
	m.setInputDraft(draft)
	m.inputPasteFoldActive = false
	m.syncInputMode()
	m.relayout()
	return m, nil
}

func (m appModel) consumeSubmittedInput() (appModel, string, bool) {
	draft := trimInputDraft(m.currentInputDraft())
	if draft.Text == "" {
		return m, "", false
	}
	// The landing hint is a first-use affordance. Once the user submits
	// anything, keep the dock quiet while it waits for the next input.
	m.hasInteracted = true
	m.input.Reset()
	m.inputTokens = nil
	m.inputPasteFoldActive = false
	m.resetHistoryNavigation()
	m.syncInputMode()
	m.relayout()

	if continued, text := stripContinuationDraft(draft); continued {
		m.submittedDraft = cloneInputDraft(draft)
		m.rememberInputHistory(draft.Text)
		m.pending = append(m.pending, text)
		m.refreshViewport()
		return m, "", false
	}

	if len(m.pending) > 0 {
		m.pending = append(m.pending, draft)
		draft = joinInputDrafts(m.pending, "\n")
		m.pending = nil
	}
	m.submittedDraft = cloneInputDraft(draft)
	return m, draft.Text, true
}

// handleCommand 处理内置 TUI 命令。除 exit/quit 外，命令仍要求以斜杠开头。
func (m *appModel) handleCommand(line string) (bool, tea.Cmd) {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "/") && !isBareExitCommand(trimmed) {
		return false, nil
	}
	if m.commandRegistry == nil {
		m.commandRegistry = NewCommandRegistry()
	}
	return m.commandRegistry.Dispatch(m, line)
}

func isExitCommandInput(line string) bool {
	trimmed := strings.TrimSpace(line)
	return trimmed == "/exit" || trimmed == "/quit" || isBareExitCommand(trimmed)
}

func isBareExitCommand(line string) bool {
	return line == "exit" || line == "quit"
}

// toggleTerminalMode 切换持续终端模式，并在 transcript 中写入状态提示。
func (m *appModel) toggleTerminalMode() {
	m.terminalMode = !m.terminalMode
	m.pending = nil
	m.activeAssistant = -1
	m.applyCursorAnimation()
	state := "terminal mode disabled"
	if m.terminalMode {
		state = "terminal mode enabled"
	}
	m.addEntry(transcriptEntry{
		kind:  entrySystem,
		title: "terminal",
		body:  state,
	})
}

// isTerminalInputActive 判断当前输入框是否需要展示终端模式视觉状态。
func (m appModel) isTerminalInputActive() bool {
	return m.terminalMode || m.terminalPreview
}

// hasMultilineInput 判断当前输入是否处于多行输入或续行状态。
func (m appModel) hasMultilineInput() bool {
	return len(m.pending) > 0 || strings.Contains(m.input.Value(), "\n")
}

// syncInputMode 根据当前文本同步一次性终端预览状态。
func (m *appModel) syncInputMode() {
	m.terminalPreview = hasBangPrefix(m.input.Value())
}

// handleInputVerticalNavigation 让上下键先在多行文本内移动，抵达边界后再切换历史输入。
func (m appModel) handleInputVerticalNavigation(direction int) (tea.Model, tea.Cmd) {
	if m.isTerminalWorkRunning() {
		return m, nil
	}

	if direction < 0 {
		if canMoveTextareaUp(m.input) {
			return m.updateInputWithKey(tea.KeyMsg{Type: tea.KeyUp})
		}
		if !textareaCursorAtStart(m.input) {
			m.input.CursorStart()
			m.syncInputMode()
			m.applyCursorAnimation()
			return m, nil
		}
		return m.handleHistoryNavigation(-1)
	}

	if canMoveTextareaDown(m.input) {
		return m.updateInputWithKey(tea.KeyMsg{Type: tea.KeyDown})
	}
	if !textareaCursorAtEnd(m.input) {
		m.input.CursorEnd()
		m.historyDownLock = false
		m.syncInputMode()
		m.applyCursorAnimation()
		return m, nil
	}
	if m.historyDownLock && m.historyIndex != -1 {
		m.historyDownLock = false
		return m, nil
	}
	return m.handleHistoryNavigation(1)
}

func (m appModel) submitSupplement(line string) appModel {
	line = strings.TrimSpace(line)
	if line == "" {
		return m
	}
	m.rememberInputHistory(line)
	submitter, ok := m.runner.(SupplementSubmitter)
	if !ok || !submitter.SubmitSupplement(line) {
		m.addEntry(transcriptEntry{
			kind:  entrySystem,
			title: "supplement",
			body:  "runner does not support running-turn supplements",
		})
		return m
	}
	m.addEntry(transcriptEntry{
		kind:        entryUser,
		title:       "you (supplement)",
		body:        line,
		inputTokens: m.submittedTokensForLine(line),
	})
	return m
}

func (m appModel) submittedTokensForLine(line string) []inputToken {
	draft := trimInputDraft(m.submittedDraft)
	if draft.Text != strings.TrimSpace(line) {
		return nil
	}
	return cloneInputTokens(draft.Tokens)
}

func (m appModel) userTranscriptEntry(title, line string) transcriptEntry {
	return transcriptEntry{
		kind:        entryUser,
		title:       title,
		body:        strings.TrimSpace(line),
		inputTokens: m.submittedTokensForLine(line),
	}
}

// startChatTurn records and starts a model turn when the guard is idle.
func (m appModel) startChatTurn(line string) (appModel, tea.Cmd) {
	line = strings.TrimSpace(line)
	if line == "" {
		return m, nil
	}
	draft := trimInputDraft(m.submittedDraft)
	if draft.Text != line {
		draft = inputDraft{Text: line}
	}
	if !m.queryGuard.StartModel() {
		m.addEntry(transcriptEntry{kind: entrySystem, title: "busy", body: "assistant is already running"})
		return m, nil
	}
	m.resetStreamingBuffers()
	m.toolGroupExpanded = false
	m.toolGroupFullResult = false
	m.addEntry(m.userTranscriptEntry("you", line))
	m.activeTurnUserEntry = len(m.transcript) - 1
	m.turnStartedAt = time.Now()
	m.turnID = newTurnID(m.turnStartedAt)
	m.syncRunningFlags()
	workCmd := runTurnCmd(m.beginModelWorkContext(), m.runner, draft, m.turnID, m.turnStartedAt)
	frameCmd := m.scheduleUIAnimationFrame()
	return m, tea.Batch(workCmd, frameCmd)
}

// queueChatInput records a chat input for FIFO execution after the active turn.
func (m appModel) queueChatInput(line string) appModel {
	line = strings.TrimSpace(line)
	if line == "" {
		return m
	}
	draft := trimInputDraft(m.submittedDraft)
	if draft.Text != line {
		draft = inputDraft{Text: line}
	}
	m.rememberInputHistory(line)
	if _, ok := m.chatQueue.EnqueueDraft(draft); ok {
		m.addEntry(m.userTranscriptEntry("you (queued)", line))
		m.addEntry(transcriptEntry{
			kind:  entrySystem,
			title: "queued",
			body:  "queued for next turn",
		})
	}
	return m
}

// updateInputWithKey 将按键交给 textarea 处理，并同步输入模式、布局和光标动画。
func (m appModel) updateInputWithKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	cmd := m.updateTokenAwareInput(msg)
	m.inputSource = inputSourceFresh
	m.syncInputMode()
	m.relayout()
	m.applyCursorAnimation()
	return m, cmd
}

func (m *appModel) syncInputPasteFoldState(msg tea.Msg, beforeValue string, textChanged bool) {
	value := m.input.Value()
	if !inputPasteFoldable(value) {
		m.inputPasteFoldActive = false
		return
	}
	if m.inputPasteFoldActive {
		return
	}
	if textChanged && inputTextMutationLooksLikeMultilinePaste(msg, beforeValue, value) {
		m.inputPasteFoldActive = true
	}
}

// canMoveTextareaUp 判断 textarea 光标是否还能向上移动到上一行或上方可视行。
func canMoveTextareaUp(input textarea.Model) bool {
	lineInfo := input.LineInfo()
	return input.Line() > 0 || lineInfo.RowOffset > 0
}

// canMoveTextareaDown 判断 textarea 光标是否还能向下移动到下一行或下方可视行。
func canMoveTextareaDown(input textarea.Model) bool {
	lineInfo := input.LineInfo()
	return input.Line() < input.LineCount()-1 || lineInfo.RowOffset < lineInfo.Height-1
}

// textareaCursorAtStart 判断 textarea 光标是否位于当前输入的逻辑开头。
func textareaCursorAtStart(input textarea.Model) bool {
	if canMoveTextareaUp(input) {
		return false
	}
	lineInfo := input.LineInfo()
	return input.Line() == 0 &&
		lineInfo.RowOffset == 0 &&
		lineInfo.CharOffset == 0
}

// textareaCursorAtEnd 判断 textarea 光标是否位于当前输入的逻辑末尾。
func textareaCursorAtEnd(input textarea.Model) bool {
	if canMoveTextareaDown(input) {
		return false
	}
	lineInfo := input.LineInfo()
	cursorLine := input.Line()
	copyInput := input
	copyInput.CursorEnd()
	endInfo := copyInput.LineInfo()
	return cursorLine == copyInput.Line() &&
		lineInfo.RowOffset == endInfo.RowOffset &&
		lineInfo.ColumnOffset == endInfo.ColumnOffset
}

// handleHistoryNavigation 在输入边界处切换历史记录，并保留进入历史前的草稿。
func (m appModel) handleHistoryNavigation(direction int) (tea.Model, tea.Cmd) {
	if m.running || len(m.inputHistory) == 0 {
		return m, nil
	}

	if direction < 0 {
		m.historyDownLock = false
		if m.historyIndex == -1 {
			m.historyDraft = m.currentInputDraft()
			m.historyIndex = len(m.inputHistory) - 1
		} else if m.historyIndex > 0 {
			m.historyIndex--
		}
		m.setInputDraft(m.inputHistory[m.historyIndex])
		m.inputSource = inputSourceHistory
		m.inputPasteFoldActive = false
		m.syncInputMode()
		m.relayout()
		m.applyCursorAnimation()
		return m, nil
	}

	if m.historyIndex == -1 {
		return m, nil
	}
	if m.historyIndex < len(m.inputHistory)-1 {
		m.historyIndex++
		m.setInputDraft(m.inputHistory[m.historyIndex])
		m.inputSource = inputSourceHistory
		m.historyDownLock = true
	} else {
		m.historyIndex = -1
		m.setInputDraft(m.historyDraft)
		m.inputSource = inputSourceFresh
		m.historyDraft = inputDraft{}
		m.historyDownLock = false
	}
	m.input.CursorEnd()
	m.inputPasteFoldActive = false
	m.syncInputMode()
	m.relayout()
	m.applyCursorAnimation()
	return m, nil
}

// rememberInputHistory 记录一条非空且不重复的用户输入历史。
func (m *appModel) rememberInputHistory(line string) {
	line = strings.TrimSpace(line)
	if line == "" {
		return
	}
	draft := inputDraft{Text: line}
	if strings.TrimSpace(m.submittedDraft.Text) == line {
		draft = trimInputDraft(m.submittedDraft)
	}
	if len(m.inputHistory) > 0 && m.inputHistory[len(m.inputHistory)-1].Text == line {
		if !inputDraftEqual(m.inputHistory[len(m.inputHistory)-1], draft) {
			m.inputHistory[len(m.inputHistory)-1] = cloneInputDraft(draft)
		}
		return
	}
	m.inputHistory = append(m.inputHistory, cloneInputDraft(draft))
}

func inputHistoryFromTranscript(entries []transcriptEntry) []inputDraft {
	history := make([]inputDraft, 0)
	for _, entry := range entries {
		if entry.kind != entryUser {
			continue
		}
		draft := trimInputDraft(inputDraft{
			Text:   entry.body,
			Tokens: cloneInputTokens(entry.inputTokens),
		})
		if draft.Text == "" {
			continue
		}
		if len(history) > 0 && history[len(history)-1].Text == draft.Text {
			if !inputDraftEqual(history[len(history)-1], draft) {
				history[len(history)-1] = cloneInputDraft(draft)
			}
			continue
		}
		history = append(history, cloneInputDraft(draft))
	}
	return history
}

// resetHistoryNavigation 清除当前历史浏览状态，回到普通编辑模式。
func (m *appModel) resetHistoryNavigation() {
	m.historyIndex = -1
	m.historyDraft = inputDraft{}
	m.historyDownLock = false
}

// submitShellCommand 提交终端命令，先写入 transcript，再异步执行 bash。
func (m appModel) submitShellCommand(command string) (tea.Model, tea.Cmd) {
	command = strings.TrimSpace(command)
	if command == "" {
		return m, nil
	}
	if !m.queryGuard.StartTerminal() {
		m.addEntry(transcriptEntry{kind: entrySystem, title: "busy", body: "terminal commands are unavailable while work is running"})
		return m, nil
	}
	m.addEntry(transcriptEntry{
		kind:  entryTool,
		title: "terminal",
		body:  "$ " + command,
	})
	m.syncRunningFlags()
	m.activeAssistant = -1
	return m, runShellCmd(m.ctx, command)
}
