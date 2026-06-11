package bubble

import (
	"fmt"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/lipgloss"
)

func (m appModel) View() string {
	if !m.ready {
		if m.cursorAnchor != nil {
			m.cursorAnchor.clear()
		}
		return "Starting Bubble Tea..."
	}

	header := headerStyle.Width(m.width).Render(m.headerText())
	input := m.renderInputBox()
	if m.modelWizard != nil {
		input = m.renderModelWizardBox()
	}

	view := lipgloss.JoinVertical(lipgloss.Left, header, m.renderTranscriptBox(), input)
	m.updateTerminalCursorAnchor(input)
	return view
}

func (m appModel) updateTerminalCursorAnchor(inputPanel string) {
	if m.cursorAnchor == nil {
		return
	}
	if !m.shouldAnchorTextInputCursor() {
		m.cursorAnchor.clear()
		return
	}
	m.cursorAnchor.set(m.inputCursorTerminalPosition(inputPanel))
}

func (m appModel) shouldAnchorTextInputCursor() bool {
	return m.ready && !m.running && m.modelWizard == nil
}

func (m appModel) inputCursorTerminalPosition(inputPanel string) terminalCursorPosition {
	inputHeight := maxInt(1, lipgloss.Height(inputPanel))
	row := minInt(2+visibleTextareaCursorRow(m.input), inputHeight-1)
	upFromBottom := maxInt(0, inputHeight-row-1)
	column := 2 + visibleTextareaCursorColumn(m.input)
	if m.width > 0 {
		column = minInt(column, maxInt(0, m.width-1))
	}
	return terminalCursorPosition{
		active:       true,
		upFromBottom: upFromBottom,
		column:       maxInt(0, column),
	}
}

func visibleTextareaCursorRow(input textarea.Model) int {
	lineInfo := input.LineInfo()
	row := input.Line() + lineInfo.RowOffset
	return minInt(maxInt(0, row), maxInt(0, inputVisibleLineCount(input)-1))
}

func visibleTextareaCursorColumn(input textarea.Model) int {
	return maxInt(0, input.LineInfo().CharOffset)
}

func (m appModel) renderInputBox() string {
	width := maxInt(28, m.width-2)
	title := "Input"
	content := m.input.View()
	style := inputPanelFocusedStyle
	titleStyle := inputLabelStyle

	if m.isTerminalInputActive() {
		title = "Terminal"
		style = inputPanelTerminalStyle
		titleStyle = terminalInputLabelStyle
	}
	if m.hasMultilineInput() {
		title = "Multiline"
		style = inputPanelMultilineStyle
		titleStyle = inputLabelStyle
		if m.isTerminalInputActive() {
			title = "Terminal multiline"
			style = inputPanelTerminalStyle
			titleStyle = terminalInputLabelStyle
		}
	}
	if m.running {
		title = "Waiting"
		content = inputWaitingStyle.Render("waiting for assistant...")
		style = inputPanelWaitingStyle
		titleStyle = inputLabelStyle
		if m.runningTerminal {
			title = "Running"
			content = inputWaitingStyle.Render("running shell command...")
			style = inputPanelTerminalStyle
			titleStyle = terminalInputLabelStyle
		}
	}

	body := lipgloss.JoinVertical(lipgloss.Left, titleStyle.Render(title), content)
	return style.Width(width).Render(body)
}

func (m appModel) renderTranscriptBox() string {
	width := maxInt(28, m.width-2)
	return transcriptPanelStyle.Width(width).Render(m.viewport.View())
}

func (m appModel) headerText() string {
	state := "ready"
	if m.running {
		state = "running"
	}
	if m.terminalMode {
		state = "terminal"
		if m.running {
			state = "terminal running"
		}
	}
	if m.hasMultilineInput() {
		state = fmt.Sprintf("%s, multiline", state)
	}
	if m.modelWizard != nil {
		state = "model"
	}
	if m.sessionID == "" {
		return fmt.Sprintf("go-code Bubble Tea | %s | session: <none>", state)
	}
	return fmt.Sprintf("go-code Bubble Tea | %s | session: %s", state, m.sessionID)
}

func (m *appModel) relayout() {
	width := maxInt(20, m.width)
	m.viewport.Width = maxInt(20, width-transcriptPanelHorizontalFrame)
	m.input.SetWidth(maxInt(20, width-4))
	m.input.SetHeight(inputVisibleLineCount(m.input))
	headerHeight := lipgloss.Height(headerStyle.Width(width).Render(m.headerText()))
	inputHeight := lipgloss.Height(m.renderInputBox())
	m.viewport.Height = maxInt(3, m.height-headerHeight-inputHeight-transcriptPanelVerticalFrame)
}
