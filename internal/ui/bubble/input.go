// 本文件定义输入框提交、斜杠命令、终端模式和历史输入导航行为。
package bubble

import (
	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"strings"
)

// handleSubmit 处理 Enter 提交，根据输入内容分派到聊天、命令或终端执行路径。
func (m appModel) handleSubmit() (tea.Model, tea.Cmd) {
	m.reconcileLegacyRunningState()
	line := strings.TrimSpace(m.input.Value())
	if line == "" {
		return m, nil
	}
	m.input.Reset()
	m.resetHistoryNavigation()
	m.syncInputMode()
	m.relayout()

	if continued, text := splitContinuation(line); continued {
		m.rememberInputHistory(line)
		m.pending = append(m.pending, text)
		m.refreshViewport()
		return m, nil
	}

	if len(m.pending) > 0 {
		m.pending = append(m.pending, line)
		line = strings.Join(m.pending, "\n")
		m.pending = nil
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

	if handled, cmd := m.handleCommand(line); handled {
		return m, cmd
	}

	if m.isModelWorkRunning() {
		return m.queueChatInput(line), nil
	}
	if m.isTerminalWorkRunning() {
		m.addEntry(transcriptEntry{kind: entrySystem, title: "busy", body: "chat is unavailable while a terminal command is running"})
		return m, nil
	}

	m.rememberInputHistory(line)
	return m.startChatTurn(line)
}

// handleCommand 处理以斜杠开头的内置 TUI 命令。
func (m *appModel) handleCommand(line string) (bool, tea.Cmd) {
	if !strings.HasPrefix(strings.TrimSpace(line), "/") {
		return false, nil
	}
	if m.commandRegistry == nil {
		m.commandRegistry = NewCommandRegistry()
	}
	return m.commandRegistry.Dispatch(m, line)
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
	if m.running {
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

// startChatTurn records and starts a model turn when the guard is idle.
func (m appModel) startChatTurn(line string) (appModel, tea.Cmd) {
	line = strings.TrimSpace(line)
	if line == "" {
		return m, nil
	}
	if !m.queryGuard.StartModel() {
		m.addEntry(transcriptEntry{kind: entrySystem, title: "busy", body: "assistant is already running"})
		return m, nil
	}
	m.addEntry(transcriptEntry{
		kind:  entryUser,
		title: "you",
		body:  line,
	})
	m.syncRunningFlags()
	m.activeAssistant = -1
	return m, runTurnCmd(m.ctx, m.runner, line)
}

// queueChatInput records a chat input for FIFO execution after the active turn.
func (m appModel) queueChatInput(line string) appModel {
	line = strings.TrimSpace(line)
	if line == "" {
		return m
	}
	m.rememberInputHistory(line)
	if m.chatQueue.Enqueue(line) {
		m.addEntry(transcriptEntry{
			kind:  entryUser,
			title: "you (queued)",
			body:  line,
		})
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
	input, cmd := m.input.Update(msg)
	m.input = input
	m.syncInputMode()
	m.relayout()
	m.applyCursorAnimation()
	return m, cmd
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
	cursorLine := input.Line()
	copyInput := input
	copyInput.CursorStart()
	startInfo := copyInput.LineInfo()
	return cursorLine == copyInput.Line() &&
		lineInfo.RowOffset == startInfo.RowOffset &&
		lineInfo.ColumnOffset == startInfo.ColumnOffset
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
			m.historyDraft = m.input.Value()
			m.historyIndex = len(m.inputHistory) - 1
		} else if m.historyIndex > 0 {
			m.historyIndex--
		}
		m.input.SetValue(m.inputHistory[m.historyIndex])
		m.input.CursorEnd()
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
		m.input.SetValue(m.inputHistory[m.historyIndex])
		m.historyDownLock = true
	} else {
		m.historyIndex = -1
		m.input.SetValue(m.historyDraft)
		m.historyDraft = ""
		m.historyDownLock = false
	}
	m.input.CursorEnd()
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
	if len(m.inputHistory) > 0 && m.inputHistory[len(m.inputHistory)-1] == line {
		return
	}
	m.inputHistory = append(m.inputHistory, line)
}

// resetHistoryNavigation 清除当前历史浏览状态，回到普通编辑模式。
func (m *appModel) resetHistoryNavigation() {
	m.historyIndex = -1
	m.historyDraft = ""
	m.historyDownLock = false
}

// submitShellCommand 提交终端命令，先写入 transcript，再异步执行 bash。
func (m appModel) submitShellCommand(command string) (tea.Model, tea.Cmd) {
	command = strings.TrimSpace(command)
	if command == "" {
		return m, nil
	}
	if !m.queryGuard.StartTerminal() {
		m.addEntry(transcriptEntry{kind: entrySystem, title: "busy", body: "terminal command is already running"})
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
