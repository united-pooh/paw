// 本文件定义 Bubble Tea 主界面的布局、输入区渲染和终端光标锚点计算。
package bubble

import (
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/lipgloss"
	"gocode/internal/settings"
)

// View 根据当前状态渲染 header、聊天历史面板和输入面板。
func (m appModel) View() string {
	if !m.ready {
		if m.cursorAnchor != nil {
			m.cursorAnchor.clear()
		}
		return "Starting Bubble Tea..."
	}

	input := m.renderInputBox()
	if m.modelWizard != nil {
		input = m.renderModelWizardBox()
	}
	if m.settingWizard != nil {
		input = m.renderSettingWizardBox()
	}

	var parts []string
	if header := m.renderHeader(); header != "" {
		parts = append(parts, header)
	}
	parts = append(parts, m.renderTranscriptBox())
	if meter := m.renderInputAboveMeter(); meter != "" {
		parts = append(parts, meter)
	}
	parts = append(parts, input)
	view := lipgloss.JoinVertical(lipgloss.Left, parts...)
	m.updateTerminalCursorAnchor(input)
	return view
}

// updateTerminalCursorAnchor 根据输入框在画面中的位置更新终端真实光标锚点。
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

// shouldAnchorTextInputCursor 判断当前是否应该把终端真实光标移动到输入单元格。
func (m appModel) shouldAnchorTextInputCursor() bool {
	return m.ready && !m.running && m.modelWizard == nil && m.settingWizard == nil
}

// inputCursorTerminalPosition 计算输入框光标相对当前帧底部的位置。
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

// visibleTextareaCursorRow 返回 textarea 光标在当前可视输入区域中的行号。
func visibleTextareaCursorRow(input textarea.Model) int {
	lineInfo := input.LineInfo()
	row := input.Line() + lineInfo.RowOffset
	return minInt(maxInt(0, row), maxInt(0, inputVisibleLineCount(input)-1))
}

// visibleTextareaCursorColumn 返回 textarea 光标在当前可视输入区域中的列号。
func visibleTextareaCursorColumn(input textarea.Model) int {
	return maxInt(0, input.LineInfo().CharOffset)
}

// renderInputBox 渲染普通输入、多行输入、终端输入和等待状态的输入面板。
func (m appModel) renderInputBox() string {
	width := maxInt(28, m.width-2)
	title := ""
	content := m.input.View()
	style := inputPanelFocusedStyle
	titleStyle := inputLabelStyle
	if m.currentSettings().UI.ContextMeterLocation == settings.MeterLocationInputTitle {
		title = m.contextMeterTitle()
	}

	if m.isTerminalInputActive() {
		style = inputPanelTerminalStyle
	}
	if m.hasMultilineInput() {
		style = inputPanelMultilineStyle
		if m.isTerminalInputActive() {
			style = inputPanelTerminalStyle
		}
	}
	if m.running {
		content = inputWaitingStyle.Render("waiting for assistant...")
		style = inputPanelWaitingStyle
		if m.runningTerminal {
			content = inputWaitingStyle.Render("running shell command...")
			style = inputPanelTerminalStyle
		}
	}

	body := content
	if title != "" {
		body = lipgloss.JoinVertical(lipgloss.Left, titleStyle.Render(title), content)
	}
	return style.Width(width).Render(body)
}

// renderTranscriptBox 渲染带边框的聊天历史滚动面板。
func (m appModel) renderTranscriptBox() string {
	width := maxInt(28, m.width-2)
	return transcriptPanelStyle.Width(width).Render(m.viewport.View())
}

// renderHeader 渲染顶部状态栏。空 header 文本表示当前不展示状态栏。
func (m appModel) renderHeader() string {
	text := m.headerText()
	if text == "" {
		return ""
	}
	return headerStyle.Width(m.width).Render(text)
}

// headerHeight 返回当前布局中 header 实际占用的高度。
func (m appModel) headerHeight() int {
	return lipgloss.Height(m.renderHeader())
}

// headerText 生成顶部状态栏文本。
// TODO: 后期希望改成显示时间或者用标签页方式展示 agent 的状态。
func (m appModel) headerText() string {
	if m.currentSettings().UI.ContextMeterLocation == settings.MeterLocationHeader {
		return m.contextMeterTitle()
	}
	return ""
}

func (m appModel) renderInputAboveMeter() string {
	if m.currentSettings().UI.ContextMeterLocation != settings.MeterLocationInputAbove {
		return ""
	}
	return inputLabelStyle.Width(maxInt(28, m.width-2)).Render(m.contextMeterTitle())
}

// relayout 根据终端尺寸和输入高度重新计算 viewport 与 textarea 尺寸。
func (m *appModel) relayout() {
	width := maxInt(20, m.width)
	m.viewport.Width = maxInt(20, width-transcriptPanelHorizontalFrame)
	m.input.SetWidth(maxInt(20, width-4))
	m.input.SetHeight(inputVisibleLineCount(m.input))
	headerHeight := m.headerHeight()
	inputHeight := lipgloss.Height(m.renderInputBox())
	inputAboveHeight := lipgloss.Height(m.renderInputAboveMeter())
	availableTranscriptHeight := m.height - headerHeight - inputAboveHeight - inputHeight - transcriptPanelVerticalFrame
	m.viewport.Height = maxInt(1, availableTranscriptHeight)
}
