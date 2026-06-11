package bubble

import (
	"fmt"
	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"strings"
)

func (m appModel) handleSubmit() (tea.Model, tea.Cmd) {
	if m.running {
		return m, nil
	}

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
		m.rememberInputHistory(line)
		return m.submitShellCommand(command)
	}

	if handled, cmd := m.handleCommand(line); handled {
		return m, cmd
	}

	m.rememberInputHistory(line)
	m.addEntry(transcriptEntry{
		kind:  entryUser,
		title: "you",
		body:  line,
	})
	m.running = true
	m.activeAssistant = -1
	return m, runTurnCmd(m.ctx, m.runner, line)
}

func (m *appModel) handleCommand(line string) (bool, tea.Cmd) {
	switch line {
	case "/exit", "/quit":
		return true, tea.Quit
	case "/help":
		m.addEntry(transcriptEntry{
			kind:  entrySystem,
			title: "help",
			body:  "Commands: /model opens the model switcher, /status shows session, /clear resets in-process history, /exit quits. Type ! to toggle terminal mode or !<command> to run bash once.",
		})
		return true, nil
	case "/model":
		m.modelWizard = newModelWizard(m.currentModelConfig())
		m.pending = nil
		return true, nil
	case "/status":
		sessionID := m.sessionID
		if sessionID == "" {
			sessionID = "<none>"
		}
		m.addEntry(transcriptEntry{
			kind:  entrySystem,
			title: "status",
			body:  fmt.Sprintf("session: %s", sessionID),
		})
		return true, nil
	case "/clear":
		if m.runner != nil {
			m.runner.ResetHistory()
		}
		m.transcript = nil
		m.pending = nil
		m.activeAssistant = -1
		m.syncInputMode()
		m.addEntry(transcriptEntry{
			kind:  entrySystem,
			title: "system",
			body:  "history cleared",
		})
		return true, nil
	default:
		return false, nil
	}
}

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

func (m appModel) isTerminalInputActive() bool {
	return m.terminalMode || m.terminalPreview
}

func (m appModel) hasMultilineInput() bool {
	return len(m.pending) > 0 || strings.Contains(m.input.Value(), "\n")
}

func (m *appModel) syncInputMode() {
	m.terminalPreview = hasBangPrefix(m.input.Value())
}

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

func (m appModel) updateInputWithKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	input, cmd := m.input.Update(msg)
	m.input = input
	m.syncInputMode()
	m.relayout()
	m.applyCursorAnimation()
	return m, cmd
}

func canMoveTextareaUp(input textarea.Model) bool {
	lineInfo := input.LineInfo()
	return input.Line() > 0 || lineInfo.RowOffset > 0
}

func canMoveTextareaDown(input textarea.Model) bool {
	lineInfo := input.LineInfo()
	return input.Line() < input.LineCount()-1 || lineInfo.RowOffset < lineInfo.Height-1
}

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

func (m *appModel) resetHistoryNavigation() {
	m.historyIndex = -1
	m.historyDraft = ""
	m.historyDownLock = false
}

func (m appModel) submitShellCommand(command string) (tea.Model, tea.Cmd) {
	command = strings.TrimSpace(command)
	if command == "" {
		return m, nil
	}
	m.addEntry(transcriptEntry{
		kind:  entryTool,
		title: "terminal",
		body:  "$ " + command,
	})
	m.running = true
	m.runningTerminal = true
	m.activeAssistant = -1
	return m, runShellCmd(m.ctx, command)
}
