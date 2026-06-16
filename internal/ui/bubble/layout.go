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

	input := m.renderActiveInputPanel()
	panelHeight := maxInt(1, m.height-m.headerHeight())

	leftContent := lipgloss.JoinVertical(lipgloss.Left,
		m.renderTranscriptBox(),
		input,
	)
	right := m.renderRightPanel(m.sidebarWidth, panelHeight)

	body := lipgloss.JoinHorizontal(lipgloss.Top, leftContent, right)

	var parts []string
	if header := m.renderHeader(); header != "" {
		parts = append(parts, header)
	}
	parts = append(parts, body)
	view := lipgloss.JoinVertical(lipgloss.Left, parts...)
	m.updateTerminalCursorAnchor(input)
	return view
}

func (m appModel) renderActiveInputPanel() string {
	if m.modelWizard != nil {
		return m.renderModelWizardBox()
	}
	if m.settingWizard != nil {
		return m.renderSettingWizardBox()
	}
	if m.sessionPicker != nil {
		return m.renderSessionPickerBox()
	}
	inputBox := m.renderInputBox()
	if m.completion != nil {
		// 补全弹窗悬浮在输入框上方，输入框始终可见
		return lipgloss.JoinVertical(lipgloss.Left, m.renderCompletionBox(), inputBox)
	}
	return inputBox
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
	// 补全弹窗开启时输入框仍然可见，光标仍需锚定
	return m.ready && !m.running && m.modelWizard == nil && m.settingWizard == nil && m.sessionPicker == nil
}

// inputCursorTerminalPosition 计算输入框光标相对当前帧底部的位置。
func (m appModel) inputCursorTerminalPosition(inputPanel string) terminalCursorPosition {
	inputHeight := maxInt(1, lipgloss.Height(inputPanel))
	// 补全弹窗在输入框上方时，光标行需要偏移补全框高度
	completionOffset := 0
	if m.completion != nil {
		completionOffset = lipgloss.Height(m.renderCompletionBox())
	}
	row := minInt(completionOffset+1+m.inputEmbeddedTitleHeight()+visibleTextareaCursorRow(m.input), inputHeight-1)
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

// inputEmbeddedTitleHeight 返回输入框内部 title 区域的行高。
func (m appModel) inputEmbeddedTitleHeight() int {
	if m.currentSettings().UI.ContextMeterLocation != settings.MeterLocationInputTitle {
		return 0
	}
	width := maxInt(28, m.width-2)
	title := m.contextMeterLine(maxInt(28, width-2))
	return lipgloss.Height(inputLabelStyle.Render(title))
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
	width := maxInt(28, m.width-m.sidebarWidth-2)
	title := ""
	content := m.input.View()
	style := inputPanelFocusedStyle
	titleStyle := inputLabelStyle
	if m.currentSettings().UI.ContextMeterLocation == settings.MeterLocationInputTitle {
		title = m.contextMeterLine(maxInt(28, width-2))
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
	leftColWidth := m.width - m.sidebarWidth
	width := maxInt(28, leftColWidth-2)
	return transcriptPanelStyle.Width(width).Height(maxInt(1, m.viewport.Height)).Render(m.viewport.View())
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
		return m.contextMeterLine(maxInt(28, m.width))
	}
	return ""
}

func (m appModel) renderInputAboveMeter() string {
	if m.currentSettings().UI.ContextMeterLocation != settings.MeterLocationInputAbove {
		return ""
	}
	width := maxInt(28, m.width-2)
	return inputLabelStyle.Width(width).Render(m.contextMeterLine(width))
}

// relayout 根据终端尺寸和输入高度重新计算 viewport 与 textarea 尺寸。
func (m *appModel) relayout() {
	total := maxInt(40, m.width)
	m.sidebarWidth = maxInt(rightSidebarMinWidth, total*30/100)
	leftColWidth := total - m.sidebarWidth

	m.viewport.Width = maxInt(20, leftColWidth-transcriptPanelHorizontalFrame)
	m.input.SetWidth(maxInt(20, leftColWidth-4))
	m.input.SetHeight(inputVisibleLineCount(m.input))

	headerHeight := m.headerHeight()
	inputHeight := lipgloss.Height(m.renderActiveInputPanel())
	availableTranscriptHeight := m.height - headerHeight - inputHeight - transcriptPanelVerticalFrame
	m.viewport.Height = maxInt(1, availableTranscriptHeight)
	m.expandTranscriptToFillHeight()
}

func (m *appModel) expandTranscriptToFillHeight() {
	if m.height <= 0 {
		return
	}
	input := m.renderActiveInputPanel()
	var parts []string
	if header := m.renderHeader(); header != "" {
		parts = append(parts, header)
	}
	parts = append(parts, m.renderTranscriptBox())
	parts = append(parts, input)
	full := lipgloss.JoinVertical(lipgloss.Left, parts...)
	if deficit := m.height - lipgloss.Height(full); deficit > 0 {
		m.viewport.Height += deficit
	}
}

// renderRightPanel 渲染右侧 30% 面板。Task 3-7 将填充真实内容。
func (m appModel) renderRightPanel(width, totalHeight int) string {
	inner := maxInt(4, width-2)
	return rightCardStyle.Width(inner).Height(maxInt(2, totalHeight-2)).Render("")
}
