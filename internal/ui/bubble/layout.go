// 本文件定义 Bubble Tea 主界面的固定外框、底部 dock 和终端光标锚点计算。
package bubble

import (
	"fmt"
	"strings"

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
	frameWidth        int
	frameHeight       int
	contentWidth      int
	contentHeight     int
	headerHeight      int
	transcriptHeight  int
	statusHeight      int
	worktreeHeight    int
	inputHeight       int
	queueHeight       int
	queueInlineHeight int
}

func computeTUILayout(width, height, requestedInputHeight int) tuiLayout {
	return computeTUILayoutWithInputLimit(width, height, requestedInputHeight, inputMaxVisibleLines)
}

func computeTUILayoutWithInputLimit(width, height, requestedInputHeight, inputHeightLimit int) tuiLayout {
	frameWidth := maxInt(1, width)
	frameHeight := maxInt(1, height)
	contentWidth := maxInt(1, frameWidth-mainFrameHorizontalFrame)
	contentHeight := maxInt(1, frameHeight-mainFrameVerticalFrame)

	// header 已嵌入顶边框线（renderDockedFrame），不再占用内容区行数。
	headerHeight := 0
	statusHeight := 0
	if contentHeight-headerHeight >= 2 {
		statusHeight = dockStatusHeight
	}
	// The worktree chip lives inline in the status row. Keeping this dimension
	// at zero prevents the input dock from growing a second metadata row.
	worktreeHeight := 0

	inputHeight := clampInt(requestedInputHeight, 1, maxInt(1, inputHeightLimit))
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
	if m.selectionDock != nil {
		base := computeTUILayout(m.width, m.height, inputMinVisibleLines)
		inputHeight := m.selectionDock.preferredHeight(inputDockContentWidth(base.contentWidth))
		return computeTUILayoutWithInputLimit(m.width, m.height, inputHeight, selectionDockMaxVisibleLines)
	}
	queueHeight := m.queuePanelHeight()
	textareaHeight := m.input.Height()
	if textareaHeight <= 0 {
		textareaHeight = inputMinVisibleLines
	}
	requestedInputHeight := textareaHeight + queueHeight
	inputLimit := inputMaxVisibleLines
	if queueHeight > 0 {
		inputLimit += queuePanelMaxHeight
	}
	layout := computeTUILayoutWithInputLimit(m.width, m.height, requestedInputHeight, inputLimit)
	layout.queueHeight = minInt(queueHeight, maxInt(0, layout.inputHeight-1))
	layout.queueInlineHeight = m.queueInlineSummaryHeight()
	return layout
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

	// /config 与 /setting 走全屏覆盖：清屏后使用小页面 gutter 渲染，替代旧的
	// 小居中 modal 盒。其余 modal（model 向导 / theme picker 等）仍走 overlay。
	if m.configCenter != nil || m.settingWizard != nil {
		if m.cursorAnchor != nil {
			m.cursorAnchor.clear()
		}
		return m.renderFullscreenModal()
	}

	layout := m.currentLayout()
	if m.todoPage != nil {
		inner := fitStyledRect(m.renderTodoPage(layout.contentWidth, layout.contentHeight), layout.contentWidth, layout.contentHeight)
		view := renderHairlineFrame(inner, layout.frameWidth, layout.frameHeight)
		view = paintStyledBackground(view, layout.frameWidth, layout.frameHeight, m.styles.Frame, m.theme.Colors.TerminalBackground)
		if m.cursorAnchor != nil {
			m.cursorAnchor.clear()
		}
		return view
	}
	parts := make([]string, 0, 3)
	if layout.transcriptHeight > 0 {
		parts = append(parts, m.renderTranscriptRegion(layout))
	}
	if layout.statusHeight > 0 {
		parts = append(parts, m.renderDockStatusLine(layout.contentWidth))
	}
	parts = append(parts, m.renderInputBoxForLayout(layout))
	if layout.queueHeight > 0 {
		parts = append(parts, m.renderQueuePanel(layout.contentWidth, layout.queueHeight))
	}

	inner := fitStyledRect(strings.Join(parts, "\n"), layout.contentWidth, layout.contentHeight)
	// 顶边框线嵌入 header（模型名/状态/时间），底边框线嵌入 token 用量与
	// 工作树 chip；上下边框颜色随 agentmode（plan/goal）变化。
	view := renderDockedFrame(
		inner,
		m.renderHeaderEmbedded(layout.contentWidth),
		m.renderBottomDockLine(layout.contentWidth),
		layout.frameWidth,
		layout.frameHeight,
	)
	if layout.queueInlineHeight > 0 {
		view = renderQueueInlineBottomBorder(view, layout.frameWidth, m.queuePanelContent(layout.frameWidth), m.currentModeHex())
	}
	view = paintStyledBackground(view, layout.frameWidth, layout.frameHeight, m.styles.Frame, m.theme.Colors.TerminalBackground)
	m.updateTerminalCursorAnchor(layout)
	return view
}

func paintStyledBackground(text string, width, height int, style lipgloss.Style, background string) string {
	text = fitStyledRect(text, width, height)
	foreground := ""
	if fg, ok := style.GetForeground().(lipgloss.Color); ok {
		foreground = string(fg)
	}
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		painted := style.Width(width).Render(fitStyledCellLine(line, width))
		lines[i] = restoreBackgroundAfterANSIReset(painted, background, foreground)
	}
	return strings.Join(lines, "\n")
}

// restoreBackgroundAfterANSIReset keeps the application canvas opaque and
// consistently colored when a nested style (Markdown, code, selection, etc.)
// emits an SGR reset. Without this, the reset also clears the outer frame's
// foreground and background and exposes the terminal's own defaults between
// styled spans.
func restoreBackgroundAfterANSIReset(text, background, foreground string) string {
	restore := backgroundSGR(background) + foregroundSGR(foreground)
	text = strings.ReplaceAll(text, "\x1b[0m", "\x1b[0m"+restore)
	text = strings.ReplaceAll(text, "\x1b[m", "\x1b[m"+restore)
	text = strings.ReplaceAll(text, "\x1b[49m", "\x1b[49m"+restore)
	return backgroundSGR(background) + text + "\x1b[0m"
}

func backgroundSGR(hex string) string {
	if !strings.HasPrefix(hex, "#") {
		return ""
	}
	r, g, b := parseHexColor(hex)
	return fmt.Sprintf("\x1b[48;2;%d;%d;%dm", r, g, b)
}

func foregroundSGR(hex string) string {
	if !strings.HasPrefix(hex, "#") {
		return ""
	}
	r, g, b := parseHexColor(hex)
	return fmt.Sprintf("\x1b[38;2;%d;%d;%dm", r, g, b)
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

// renderDockedFrame 渲染主界面固定外框：顶部 ─ 线嵌入 header（模型名/状态/
// 时间），底部 ─ 线嵌入 dock 元数据（token 用量/工作树）。线色由各内容自带
// （modeHex 通过 embedHairlineContent 的 lineColor 参数控制）。
func renderDockedFrame(inner, topContent, bottomContent string, width, height int) string {
	width = maxInt(1, width)
	height = maxInt(1, height)
	if height == 1 {
		return embedHairlineContent(topContent, width, "")
	}
	inner = fitStyledRect(inner, width, maxInt(1, height-2))
	return embedHairlineContent(topContent, width, "") + "\n" +
		inner + "\n" +
		embedHairlineContent(bottomContent, width, "")
}

// embedHairlineContent 把内容居中嵌入一条 ─ 线：内容两侧各留一个空格（空间
// 允许时），左右以 ─ 补齐到严格 width。lineColor 非空时整条线使用该前景色。
func embedHairlineContent(content string, width int, lineColor string) string {
	width = maxInt(1, width)
	lineStyle := lipgloss.NewStyle()
	if lineColor != "" {
		lineStyle = lineStyle.Foreground(lipgloss.Color(lineColor))
	}
	renderDash := func(n int) string {
		if n <= 0 {
			return ""
		}
		return lineStyle.Render(strings.Repeat("─", n))
	}
	content = truncateStyledCellLine(content, maxInt(1, width))
	contentWidth := terminalCellWidth(content)
	fill := maxInt(0, width-contentWidth)
	if fill >= 2 {
		left := (fill - 2) / 2
		right := fill - 2 - left
		return renderDash(left) + " " + content + " " + renderDash(right)
	}
	left := fill / 2
	return renderDash(left) + content + renderDash(fill-left)
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

	// 运行中 subagent 任务卡：贴在 transcript 右边界内侧、垂直居中。
	// modal / completion 浮层在其之后合成，必要时覆盖卡片。
	if card := m.renderSubagentTaskCard(m.animationNow()); card != "" {
		base = placeRightCenteredOverlay(base, card, layout.contentWidth, layout.transcriptHeight)
	}

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
	case m.translatePanel != nil:
		return m.renderTranslatePanel()
	case m.themePicker != nil:
		return m.renderThemePickerBox()
	case m.modelWizard != nil:
		return m.renderModelWizardBox()
	case m.configCenter != nil:
		return m.renderConfigCenterBox()
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

// renderFullscreenModal 选择当前打开的全屏覆盖层（配置中心或 /setting 退回
// 的向导），由 View 在它们打开时整页返回。
func (m appModel) renderFullscreenModal() string {
	if m.configCenter != nil {
		return m.renderConfigCenterBox()
	}
	return m.renderSettingWizardBox()
}

// renderFullscreenPanel 渲染没有固定 footer 的全屏覆盖层。
func (m appModel) renderFullscreenPanel(body string) string {
	return m.renderFullscreenPanelWithFooter(body, "")
}

// renderFullscreenPanelWithFooter 渲染终端原生的全屏覆盖层。参考界面并不是
// “窄内容列居中”，而是使用很小的页面 gutter：tab、搜索框和 footer 共用
// 页面宽度，键值列表只占左侧自然宽度，从而把留白留在值列右侧。
//
// footer 固定在底部边框上一行；body 从顶部边框下空一行开始，内容变长时只
// 裁剪 body，不会把快捷键提示顶离屏幕底部。
func (m appModel) renderFullscreenPanelWithFooter(body, footer string) string {
	layout := m.currentLayout()
	width := maxInt(1, layout.frameWidth)
	height := maxInt(1, layout.frameHeight)
	contentWidth := m.fullscreenContentWidth()
	leftMargin := m.fullscreenHorizontalMargin()

	// 顶/底各占一行 hairline，内部保留一行 top padding。
	innerHeight := maxInt(1, height-2)
	topPadding := 0
	if innerHeight >= 3 {
		topPadding = 1
	}
	bodyHeight := maxInt(1, innerHeight-topPadding)
	footerRow := -1
	if footer != "" && innerHeight >= 2 {
		footerRow = innerHeight - 1
		// footer 上方保留一行呼吸空间。
		bodyHeight = maxInt(1, footerRow-topPadding-1)
	}
	body = fitStyledRect(body, contentWidth, bodyHeight)
	bodyLines := strings.Split(body, "\n")
	lines := make([]string, innerHeight)
	copy(lines[topPadding:], bodyLines)
	if footerRow >= 0 {
		lines[footerRow] = truncateStyledCellLine(footer, contentWidth)
	}

	prefix := strings.Repeat(" ", leftMargin)
	for i, line := range lines {
		lines[i] = prefix + fitStyledCellLine(line, contentWidth)
	}
	frameLine := m.styles.Frame.Render(strings.Repeat("─", width))
	joined := frameLine + "\n" + strings.Join(lines, "\n") + "\n" + frameLine
	return paintStyledBackground(joined, width, height, m.styles.Frame, m.theme.Colors.TerminalBackground)
}

// fullscreenHorizontalMargin 返回全屏页面的小 gutter。宽终端最多留 6 列，
// 常见 100 列终端只留 2 列；避免旧实现左右各空 1/5 导致 tab 和搜索框过窄。
func (m appModel) fullscreenHorizontalMargin() int {
	width := maxInt(1, m.currentLayout().frameWidth)
	if width < 12 {
		return 0
	}
	return clampInt(width/40, 2, 6)
}

// fullscreenContentWidth 返回减去两侧小 gutter 后的页面宽度。
func (m appModel) fullscreenContentWidth() int {
	layout := m.currentLayout()
	width := maxInt(1, layout.frameWidth)
	margin := m.fullscreenHorizontalMargin()
	return maxInt(1, width-2*margin)
}

// renderModalPanel 将 modal 限制在 transcript 区域内，内容过长时进行显示层裁剪。
func (m appModel) modalPanelWidth() int {
	layout := m.currentLayout()
	availableWidth := maxInt(1, layout.contentWidth-4)
	panelWidth := minInt(80, availableWidth)
	if availableWidth >= 32 {
		panelWidth = maxInt(32, panelWidth)
	}
	return panelWidth
}

func (m appModel) modalPanelBodyWidth() int {
	styleWidth := maxInt(1, m.modalPanelWidth()-wizardPanelStyle.GetHorizontalBorderSize())
	return maxInt(1, styleWidth-wizardPanelStyle.GetHorizontalPadding())
}

func (m appModel) renderModalPanel(body string) string {
	layout := m.currentLayout()
	availableHeight := maxInt(1, layout.transcriptHeight-2)
	panelWidth := m.modalPanelWidth()
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
		// 浮层、选择器和运行态没有可供真实终端光标锚定的 textarea。
		// 此时必须隐藏光标；恢复为终端默认可见状态会让它停在整帧输出后的
		// 帧外位置（通常是底部边框下一行）。
		m.cursorAnchor.hide()
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
		m.configCenter == nil &&
		m.sessionPicker == nil &&
		m.subagentPicker == nil &&
		m.todoPage == nil
}

// inputCursorTerminalPosition 直接由固定布局矩形计算，不再依赖拼接后字符串高度。
func (m appModel) inputCursorTerminalPosition(layout tuiLayout) terminalCursorPosition {
	queueHeight := minInt(layout.queueHeight, maxInt(0, layout.inputHeight-1))
	textareaHeight := maxInt(1, layout.inputHeight-queueHeight)
	row := minInt(m.visibleInputCursorRow(), maxInt(0, textareaHeight-1))
	// queue 面板位于输入框下方；从终端底部回退时必须跨过 queue 区域。
	upFromBottom := 1 + queueHeight + layout.worktreeHeight + maxInt(0, textareaHeight-row-1)
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
	projection := m.inputTokenProjection()
	height := maxInt(1, m.input.Height())
	start := 0
	if len(projection.lines) > height && projection.cursorRow >= height {
		start = projection.cursorRow - height + 1
	}
	start = clampInt(start, 0, maxInt(0, len(projection.lines)-height))
	return clampInt(projection.cursorRow-start, 0, height-1)
}

func (m appModel) visibleInputCursorColumn() int {
	return maxInt(0, m.inputTokenProjection().cursorColumn)
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
	}
	bodyWidth := inputDockContentWidth(layout.contentWidth)
	queueHeight := minInt(layout.queueHeight, maxInt(0, layout.inputHeight-1))
	textareaHeight := maxInt(1, layout.inputHeight-queueHeight)
	inputBody := m.renderInputContentWithHints(bodyWidth, textareaHeight)
	return renderFixedStyledPanel(
		style,
		layout.contentWidth,
		textareaHeight,
		inputBody,
	)
}

func inputDockContentWidth(width int) int {
	return maxInt(1, width-inputDockStyle.GetHorizontalFrameSize()-inputDockStyle.GetHorizontalPadding())
}

func (m appModel) renderInputContentWithHints(width, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	if strings.TrimSpace(m.input.Value()) == "" && !m.isGoalInputActive() && !m.isTerminalInputActive() && !m.runningTerminal {
		if m.hasInteracted {
			return fitStyledRect("", width, height)
		}
		left := inputPromptStyle.Render("›") + " " + inputHintStyle.Render("Ask anything…")
		hint := inputHintStyle.Render("/help @file !shell")
		line := left
		if terminalCellWidth(left)+terminalCellWidth(hint)+1 <= width {
			line += strings.Repeat(" ", width-terminalCellWidth(left)-terminalCellWidth(hint)) + hint
		} else {
			line = truncateStyledCellLine(left, width)
		}
		return fitStyledRect(line, width, height)
	}
	return fitStyledRect(m.renderInputContent(), width, height)
}

// renderInputContent 渲染输入框内容。渲染、高度和光标位置统一走字符级投影
// 管线（renderTokenInputContent），textarea 仅作为文本与光标数据模型；折叠、
// token 样式、光标字符都由同一套投影处理，chat 与 multiline 不再有两条
// 渲染路径。
func (m appModel) renderInputContent() string {
	return m.renderTokenInputContent()
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
		inputPasteFoldableWithWidth(m.input.Value(), m.input.Width()) &&
		!inputCursorInPasteFoldHiddenRangeWithWidth(m.input, m.input.Width())
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
	queueHeight := m.queuePanelHeight()
	if m.selectionDock == nil {
		requestedInputHeight += queueHeight
	}
	layout := computeTUILayout(m.width, m.height, requestedInputHeight)
	if m.selectionDock != nil {
		layout = computeTUILayoutWithInputLimit(m.width, m.height, requestedInputHeight, selectionDockMaxVisibleLines)
	}
	if m.selectionDock == nil {
		actualQueueHeight := minInt(queueHeight, maxInt(0, layout.inputHeight-1))
		m.input.SetHeight(maxInt(1, layout.inputHeight-actualQueueHeight))
	} else {
		m.input.SetHeight(clampInt(m.tokenAwareInputVisibleLineCount(), 1, inputMaxVisibleLines))
	}
	m.viewport.Width = transcriptWidth
	m.viewport.Height = maxInt(1, layout.transcriptHeight)
}

func (m appModel) tokenAwareInputVisibleLineCount() int {
	// 高度基于未折叠投影行数：折叠只是显示层，行数上限始终受
	// inputMaxVisibleLines 保护。
	projection := projectInput(
		m.input.Value(),
		m.inputTokens,
		textareaAbsoluteCursor(m.input),
		m.input.Width(),
		false,
	)
	return minInt(inputMaxVisibleLines, maxInt(inputMinVisibleLines, len(projection.lines)))
}

// expandTranscriptToFillHeight 已由纯布局计算器取代，保留空实现供旧调用点平滑迁移。
func (m *appModel) expandTranscriptToFillHeight() {}
