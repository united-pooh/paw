// 本文件定义 Bubble Tea 主界面的固定外框、底部 dock 和终端光标锚点计算。
package bubble

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/cursor"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/lipgloss"
)

const (
	mainFrameHorizontalFrame = 0
	mainFrameVerticalFrame   = 2
	mainContentPadding       = 1
	dockStatusHeight         = 1
)

// tuiLayout 是由终端尺寸和输入高度唯一决定的纯布局结果。
// frame 的尺寸只随 WindowSizeMsg 改变，内容更新只能改变内部区域。
type tuiLayout struct {
	frameWidth       int
	frameHeight      int
	contentWidth     int
	contentHeight    int
	headerHeight     int
	transcriptHeight int
	statusHeight     int
	worktreeHeight   int
	inputHeight      int
}

func computeTUILayout(width, height, requestedInputHeight int) tuiLayout {
	frameWidth := maxInt(1, width)
	frameHeight := maxInt(1, height)
	contentWidth := maxInt(1, frameWidth-mainFrameHorizontalFrame)
	contentHeight := maxInt(1, frameHeight-mainFrameVerticalFrame)

	// header 在内容足够高时占 1 行；极小终端优先保留 transcript+input。
	headerHeight := 0
	if contentHeight >= 4 {
		headerHeight = dockStatusHeight
	}
	statusHeight := 0
	if contentHeight-headerHeight >= 2 {
		statusHeight = dockStatusHeight
	}
	// The worktree chip lives inline in the status row. Keeping this dimension
	// at zero prevents the input dock from growing a second metadata row.
	worktreeHeight := 0

	inputHeight := clampInt(requestedInputHeight, 1, inputMaxVisibleLines)
	// 正常尺寸下始终给 transcript 留至少一行；极小终端优先保留输入。
	maxInputHeight := maxInt(1, contentHeight-headerHeight-statusHeight-worktreeHeight-1)
	inputHeight = minInt(inputHeight, maxInputHeight)
	if inputHeight+headerHeight+statusHeight+worktreeHeight > contentHeight {
		inputHeight = maxInt(1, contentHeight-headerHeight-statusHeight-worktreeHeight)
	}
	transcriptHeight := maxInt(0, contentHeight-headerHeight-statusHeight-worktreeHeight-inputHeight)

	return tuiLayout{
		frameWidth:       frameWidth,
		frameHeight:      frameHeight,
		contentWidth:     contentWidth,
		contentHeight:    contentHeight,
		headerHeight:     headerHeight,
		transcriptHeight: transcriptHeight,
		statusHeight:     statusHeight,
		worktreeHeight:   worktreeHeight,
		inputHeight:      inputHeight,
	}
}

func (m appModel) currentLayout() tuiLayout {
	inputHeight := m.input.Height()
	if m.selectionDock != nil {
		base := computeTUILayout(m.width, m.height, inputMinVisibleLines)
		inputHeight = m.selectionDock.preferredHeight(inputDockContentWidth(base.contentWidth))
	}
	if inputHeight <= 0 {
		inputHeight = inputMinVisibleLines
	}
	return computeTUILayout(m.width, m.height, inputHeight)
}

// View 渲染一个尺寸严格等于当前终端的单一固定外框。
func (m appModel) View() string {
	m.activateThemeStyles()
	if !m.ready {
		if m.cursorAnchor != nil {
			m.cursorAnchor.clear()
		}
		return "Starting Bubble Tea..."
	}

	layout := m.currentLayout()
	parts := make([]string, 0, 4)
	if layout.headerHeight > 0 {
		parts = append(parts, m.renderHeaderLine(layout.contentWidth))
	}
	if layout.transcriptHeight > 0 {
		parts = append(parts, m.renderTranscriptRegion(layout))
	}
	if layout.statusHeight > 0 {
		parts = append(parts, m.renderDockStatusLine(layout.contentWidth))
	}
	parts = append(parts, m.renderInputBoxForLayout(layout))

	inner := fitStyledRect(strings.Join(parts, "\n"), layout.contentWidth, layout.contentHeight)
	view := renderHairlineFrame(inner, layout.frameWidth, layout.frameHeight)
	view = paintStyledBackground(view, layout.frameWidth, layout.frameHeight, m.styles.Frame, m.theme.Colors.TerminalBackground)
	m.updateTerminalCursorAnchor(layout)
	return view
}

func paintStyledBackground(text string, width, height int, style lipgloss.Style, background string) string {
	text = fitStyledRect(text, width, height)
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		painted := style.Width(width).Render(fitStyledCellLine(line, width))
		lines[i] = restoreBackgroundAfterANSIReset(painted, background)
	}
	return strings.Join(lines, "\n")
}

// restoreBackgroundAfterANSIReset keeps the application canvas opaque when a
// nested style (Markdown, code, selection, etc.) emits an SGR reset. Without
// this, the reset also clears the outer frame background and exposes the
// terminal's own background color between styled spans.
func restoreBackgroundAfterANSIReset(text, background string) string {
	r, g, b := parseHexColor(background)
	backgroundSGR := fmt.Sprintf("\x1b[48;2;%d;%d;%dm", r, g, b)
	text = strings.ReplaceAll(text, "\x1b[0m", "\x1b[0m"+backgroundSGR)
	text = strings.ReplaceAll(text, "\x1b[m", "\x1b[m"+backgroundSGR)
	text = strings.ReplaceAll(text, "\x1b[49m", "\x1b[49m"+backgroundSGR)
	return backgroundSGR + text + "\x1b[0m"
}

func renderHairlineFrame(inner string, width, height int) string {
	width = maxInt(1, width)
	height = maxInt(1, height)
	if height == 1 {
		return strings.Repeat("─", width)
	}
	inner = fitStyledRect(inner, width, maxInt(1, height-2))
	return strings.Repeat("─", width) + "\n" + inner + "\n" + strings.Repeat("─", width)
}

func (m appModel) renderTranscriptRegion(layout tuiLayout) string {
	if layout.transcriptHeight <= 0 {
		return ""
	}

	content := m.viewport.View()
	if !m.hasRenderableTranscript() {
		content = renderEmptyState(layout.contentWidth, layout.transcriptHeight)
	}
	base := renderFixedStyledPanel(
		transcriptContentStyle,
		layout.contentWidth,
		layout.transcriptHeight,
		content,
	)

	if modal := m.renderActiveModalBox(layout); modal != "" {
		return placeOpaqueOverlay(
			base,
			modal,
			layout.contentWidth,
			layout.transcriptHeight,
			overlayAlignCenter,
		)
	}

	if m.completion != nil {
		return placeOpaqueOverlay(
			base,
			m.renderCompletionBox(),
			layout.contentWidth,
			layout.transcriptHeight,
			overlayAlignBottom,
		)
	}
	if notice := m.renderNewMessageNotice(layout.contentWidth); notice != "" {
		base = placeOpaqueOverlay(
			base,
			notice,
			layout.contentWidth,
			layout.transcriptHeight,
			overlayAlignBottom,
		)
	}
	return fitStyledRect(base, layout.contentWidth, layout.transcriptHeight)
}

func (m appModel) renderActiveModalBox(layout tuiLayout) string {
	if layout.transcriptHeight <= 0 {
		return ""
	}
	switch {
	case m.themePicker != nil:
		return m.renderThemePickerBox()
	case m.modelWizard != nil:
		return m.renderModelWizardBox()
	case m.settingWizard != nil:
		return m.renderSettingWizardBox()
	case m.sessionPicker != nil:
		return m.renderSessionPickerBox()
	case m.subagentPicker != nil:
		return m.renderActivityBox()
	default:
		return ""
	}
}

type overlayAlignment int

const (
	overlayAlignCenter overlayAlignment = iota
	overlayAlignBottom
)

func placeOpaqueOverlay(base, overlay string, width, height int, alignment overlayAlignment) string {
	base = fitStyledRect(base, width, height)
	overlayWidth := minInt(width, maxInt(1, widestStyledLine(overlay)))
	overlayHeight := minInt(height, maxInt(1, lipgloss.Height(overlay)))
	overlay = fitStyledRect(overlay, overlayWidth, overlayHeight)

	left := maxInt(0, (width-overlayWidth)/2)
	top := maxInt(0, (height-overlayHeight)/2)
	if alignment == overlayAlignBottom {
		top = maxInt(0, height-overlayHeight)
	}

	baseLines := strings.Split(base, "\n")
	overlayLines := strings.Split(overlay, "\n")
	for row, overlayLine := range overlayLines {
		baseRow := top + row
		if baseRow < 0 || baseRow >= len(baseLines) {
			continue
		}
		baseLines[baseRow] = composeStyledCellOverlay(baseLines[baseRow], overlayLine, left, width)
	}
	return strings.Join(baseLines, "\n")
}

func widestStyledLine(text string) int {
	width := 0
	for _, line := range strings.Split(text, "\n") {
		width = maxInt(width, terminalCellWidth(line))
	}
	return width
}

// renderModalPanel 将 modal 限制在 transcript 区域内，内容过长时进行显示层裁剪。
func (m appModel) renderModalPanel(body string) string {
	layout := m.currentLayout()
	availableWidth := maxInt(1, layout.contentWidth-4)
	availableHeight := maxInt(1, layout.transcriptHeight-2)
	panelWidth := minInt(80, availableWidth)
	if availableWidth >= 32 {
		panelWidth = maxInt(32, panelWidth)
	}
	naturalHeight := lipgloss.Height(body) + modalPanelVerticalFrame
	panelHeight := minInt(availableHeight, maxInt(modalPanelVerticalFrame+1, naturalHeight))
	return renderFixedStyledPanel(wizardPanelStyle, panelWidth, panelHeight, body)
}

func (m appModel) renderCompletionPanel(body string) string {
	layout := m.currentLayout()
	panelWidth := minInt(72, maxInt(1, layout.contentWidth))
	panelHeight := minInt(
		maxInt(1, layout.transcriptHeight),
		maxInt(completionPanelVerticalFrame+1, lipgloss.Height(body)+completionPanelVerticalFrame),
	)
	return renderFixedStyledPanel(completionPanelStyle, panelWidth, panelHeight, body)
}

func renderFixedStyledPanel(style lipgloss.Style, totalWidth, totalHeight int, body string) string {
	totalWidth = maxInt(1, totalWidth)
	totalHeight = maxInt(1, totalHeight)
	horizontalBorder := style.GetHorizontalBorderSize()
	verticalBorder := style.GetVerticalBorderSize()
	horizontalPadding := style.GetHorizontalPadding()
	verticalPadding := style.GetVerticalPadding()
	// Lipgloss Width/Height include padding but are applied before borders.
	// The body must therefore fit inside the width/height left after padding,
	// while the style dimensions only subtract the border itself.
	styleWidth := maxInt(1, totalWidth-horizontalBorder)
	styleHeight := maxInt(1, totalHeight-verticalBorder)
	bodyWidth := maxInt(1, styleWidth-horizontalPadding)
	bodyHeight := maxInt(1, styleHeight-verticalPadding)
	body = fitStyledRect(body, bodyWidth, bodyHeight)
	rendered := style.
		Width(styleWidth).
		Height(styleHeight).
		Render(body)
	return fitStyledRect(rendered, totalWidth, totalHeight)
}

// fitStyledRect 对带 ANSI 样式的矩形按终端 cell 宽度裁剪并补齐。
func fitStyledRect(text string, width, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	if len(lines) > height {
		lines = lines[:height]
	}
	for len(lines) < height {
		lines = append(lines, "")
	}
	for i, line := range lines {
		lines[i] = fitStyledCellLine(line, width)
	}
	return strings.Join(lines, "\n")
}

// renderActiveInputPanel 保留为输入区兼容入口；modal 和 completion 已移入 transcript 浮层。
func (m appModel) renderActiveInputPanel() string {
	return m.renderInputBox()
}

// updateTerminalCursorAnchor 根据固定 input 矩形更新终端真实光标锚点。
func (m appModel) updateTerminalCursorAnchor(layout tuiLayout) {
	if m.cursorAnchor == nil {
		return
	}
	if !m.shouldAnchorTextInputCursor() {
		m.cursorAnchor.clear()
		return
	}
	m.cursorAnchor.set(m.inputCursorTerminalPosition(layout))
}

func (m appModel) shouldAnchorTextInputCursor() bool {
	return m.ready &&
		m.selectionDock == nil &&
		!m.isTerminalWorkRunning() &&
		!m.toolInspectActive &&
		m.themePicker == nil &&
		m.modelWizard == nil &&
		m.settingWizard == nil &&
		m.sessionPicker == nil &&
		m.subagentPicker == nil
}

// inputCursorTerminalPosition 直接由固定布局矩形计算，不再依赖拼接后字符串高度。
func (m appModel) inputCursorTerminalPosition(layout tuiLayout) terminalCursorPosition {
	row := minInt(m.visibleInputCursorRow(), maxInt(0, layout.inputHeight-1))
	upFromBottom := 1 + layout.worktreeHeight + maxInt(0, layout.inputHeight-row-1)
	column := inputDockStyle.GetPaddingLeft() + m.visibleInputCursorColumn()
	column = minInt(column, maxInt(0, layout.frameWidth-1))
	return terminalCursorPosition{
		active:       true,
		upFromBottom: upFromBottom,
		column:       maxInt(0, column),
		background:   m.theme.Colors.TerminalBackground,
	}
}

func (m appModel) inputEmbeddedTitleHeight() int {
	return 0
}

func (m appModel) visibleInputCursorRow() int {
	if len(m.inputTokens) > 0 {
		projection := m.inputTokenProjection()
		height := maxInt(1, m.input.Height())
		start := 0
		if len(projection.lines) > height && projection.cursorRow >= height {
			start = projection.cursorRow - height + 1
		}
		start = clampInt(start, 0, maxInt(0, len(projection.lines)-height))
		return clampInt(projection.cursorRow-start, 0, height-1)
	}
	if m.shouldRenderFoldedInput() {
		return foldedInputCursorRow(m.input)
	}
	return visibleTextareaCursorRow(m.input)
}

func (m appModel) visibleInputCursorColumn() int {
	if len(m.inputTokens) > 0 {
		return maxInt(0, m.inputTokenProjection().cursorColumn)
	}
	if m.shouldRenderFoldedInput() {
		return minInt(maxInt(0, m.input.LineInfo().CharOffset), maxInt(0, m.input.Width()-1))
	}
	return visibleTextareaCursorColumn(m.input)
}

func visibleTextareaCursorRow(input textarea.Model) int {
	lineInfo := input.LineInfo()
	row := input.Line() + lineInfo.RowOffset
	return minInt(maxInt(0, row), maxInt(0, input.Height()-1))
}

func visibleTextareaCursorColumn(input textarea.Model) int {
	return maxInt(0, input.LineInfo().CharOffset)
}

// renderInputBox 渲染普通输入、多行输入、终端输入和等待状态的 dock 输入区。
func (m appModel) renderInputBox() string {
	return m.renderInputBoxForLayout(m.currentLayout())
}

func (m appModel) renderInputBoxForLayout(layout tuiLayout) string {
	if m.selectionDock != nil {
		return renderFixedStyledPanel(
			inputDockStyle,
			layout.contentWidth,
			layout.inputHeight,
			m.renderSelectionDock(inputDockContentWidth(layout.contentWidth), layout.inputHeight),
		)
	}
	style := inputDockStyle
	if m.isTerminalInputActive() || m.runningTerminal {
		style = inputDockTerminalStyle
	} else if m.hasMultilineInput() {
		style = inputDockMultilineStyle
	}
	bodyWidth := inputDockContentWidth(layout.contentWidth)
	return renderFixedStyledPanel(
		style,
		layout.contentWidth,
		layout.inputHeight,
		m.renderInputContentWithHints(bodyWidth, layout.inputHeight),
	)
}

func inputDockContentWidth(width int) int {
	return maxInt(1, width-inputDockStyle.GetHorizontalFrameSize()-inputDockStyle.GetHorizontalPadding())
}

func (m appModel) renderInputContentWithHints(width, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	if strings.TrimSpace(m.input.Value()) == "" && !m.isTerminalInputActive() && !m.runningTerminal && !m.hasMultilineInput() {
		if m.hasInteracted {
			return fitStyledRect("", width, height)
		}
		left := inputPromptStyle.Render("›") + " " + inputHintStyle.Render("Ask anything…")
		hint := inputHintStyle.Render("/help @file !shell")
		line := left
		if terminalCellWidth(left)+terminalCellWidth(hint)+1 <= width {
			line += strings.Repeat(" ", width-terminalCellWidth(left)-terminalCellWidth(hint)) + hint
		} else {
			line = truncateDisplayWidth(left, width)
		}
		return fitStyledRect(line, width, height)
	}
	return fitStyledRect(m.renderInputContent(), width, height)
}

func (m appModel) renderInputContent() string {
	if len(m.inputTokens) > 0 {
		return m.renderTokenInputContent()
	}
	input := m.input
	input.Cursor.SetMode(cursor.CursorHide)
	if m.isTerminalInputActive() || m.runningTerminal {
		applyTextareaTerminalStyle(&input)
	}
	if m.shouldRenderFoldedInput() {
		return renderFoldedInputContent(input)
	}
	return input.View()
}

// applyTextareaTerminalStyle 显式覆盖 textarea 自己的子样式，避免灰色 Placeholder
// 遮蔽 terminal Dock 的父级前景色。
func applyTextareaTerminalStyle(input *textarea.Model) {
	if input == nil {
		return
	}
	input.Placeholder = "$"
	input.FocusedStyle.Text = terminalInputTextStyle
	input.BlurredStyle.Text = terminalInputTextStyle
	input.FocusedStyle.Placeholder = terminalInputLabelStyle
	input.BlurredStyle.Placeholder = terminalInputLabelStyle
}

func (m appModel) shouldRenderFoldedInput() bool {
	return m.inputPasteFoldActive &&
		inputPasteFoldable(m.input.Value()) &&
		!inputCursorInPasteFoldHiddenRange(m.input)
}

func renderFoldedInputContent(input textarea.Model) string {
	input.Cursor.SetMode(cursor.CursorHide)
	projected, _, ok := inputPasteFoldProjection(input.Value())
	if !ok {
		return input.View()
	}
	width := maxInt(1, input.Width())
	height := maxInt(1, input.Height())
	lines := make([]string, 0, height)
	for _, line := range projected {
		if len(lines) >= height {
			break
		}
		lines = append(lines, padDisplayWidth(truncateDisplayWidth(line, width), width))
	}
	for len(lines) < height {
		lines = append(lines, strings.Repeat(" ", width))
	}
	return strings.Join(lines, "\n")
}

func foldedInputCursorRow(input textarea.Model) int {
	start, end, ok := inputPasteFoldHiddenRange(input.Value())
	if !ok {
		return visibleTextareaCursorRow(input)
	}
	line := input.Line()
	if line < start {
		return minInt(maxInt(0, line), maxInt(0, input.Height()-1))
	}
	row := start + 1 + (line - end)
	return minInt(maxInt(0, row), maxInt(0, input.Height()-1))
}

// renderTranscriptBox 保留给测试和旧调用点，实际 transcript 不再拥有独立外框。
func (m appModel) renderTranscriptBox() string {
	return m.renderTranscriptRegion(m.currentLayout())
}

func (m appModel) leftPanelContentWidth(minWidth int) int {
	return maxInt(minWidth, m.viewport.Width)
}

func (m appModel) renderHeader() string { return "" }
func (m appModel) headerHeight() int    { return 0 }
func (m appModel) headerText() string   { return "" }

// relayout 只根据终端尺寸和输入视觉行数计算内部区域。
func (m *appModel) relayout() {
	if m.width <= 0 || m.height <= 0 {
		// Bubble Tea 尚未发送首个 WindowSizeMsg 时保留 textarea/viewport 的
		// 初始化尺寸，避免输入事件把宽度暂时压成 1。
		m.input.SetHeight(m.tokenAwareInputVisibleLineCount())
		return
	}
	base := computeTUILayout(m.width, m.height, inputMinVisibleLines)
	inputWidth := inputDockContentWidth(base.contentWidth)
	transcriptWidth := maxInt(1, base.contentWidth-transcriptContentStyle.GetHorizontalPadding())
	m.input.SetWidth(inputWidth)

	requestedInputHeight := m.tokenAwareInputVisibleLineCount()
	if m.selectionDock != nil {
		requestedInputHeight = m.selectionDock.preferredHeight(inputWidth)
	}
	layout := computeTUILayout(m.width, m.height, requestedInputHeight)
	if m.selectionDock == nil {
		m.input.SetHeight(layout.inputHeight)
	} else {
		m.input.SetHeight(clampInt(m.tokenAwareInputVisibleLineCount(), 1, inputMaxVisibleLines))
	}
	m.viewport.Width = transcriptWidth
	m.viewport.Height = maxInt(1, layout.transcriptHeight)
}

func (m appModel) tokenAwareInputVisibleLineCount() int {
	if len(m.inputTokens) == 0 {
		return inputVisibleLineCount(m.input)
	}
	projection := m.inputTokenProjection()
	return minInt(inputMaxVisibleLines, maxInt(inputMinVisibleLines, len(projection.lines)))
}

// expandTranscriptToFillHeight 已由纯布局计算器取代，保留空实现供旧调用点平滑迁移。
func (m *appModel) expandTranscriptToFillHeight() {}
