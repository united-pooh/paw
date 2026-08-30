// 本文件定义 Bubble Tea 主界面的固定外框、底部 dock 和终端光标锚点计算。
package bubble

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"paw/internal/ui/bubble/textareax"
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
	frameWidth             int
	frameHeight            int
	contentWidth           int
	contentHeight          int
	workspaceWidth         int
	activityWidth          int
	activitySeparatorWidth int
	activityMode           activityLayoutMode
	headerHeight           int
	transcriptHeight       int
	statusHeight           int
	worktreeHeight         int
	inputHeight            int
	queueHeight            int
	queueInlineHeight      int
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
		workspaceWidth:   contentWidth,
		activityMode:     activityLayoutHidden,
		headerHeight:     headerHeight,
		transcriptHeight: transcriptHeight,
		statusHeight:     statusHeight,
		worktreeHeight:   worktreeHeight,
		inputHeight:      inputHeight,
	}
}

func (m appModel) currentLayout() tuiLayout {
	if m.selectionDock != nil {
		base := applyActivityGeometry(computeTUILayout(m.width, m.height, inputMinVisibleLines), m.activity.visible, m.activity.widthColumns)
		selectionWidth := base.workspaceWidth
		if base.activityMode == activityLayoutFullscreen {
			selectionWidth = base.contentWidth
		}
		inputHeight := m.selectionDock.preferredHeight(inputDockContentWidth(selectionWidth))
		layout := computeTUILayoutWithInputLimit(m.width, m.height, inputHeight, selectionDockMaxVisibleLines)
		return applyActivityGeometry(layout, m.activity.visible, m.activity.widthColumns)
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
	return applyActivityGeometry(layout, m.activity.visible, m.activity.widthColumns)
}

func (m appModel) renderWorkspaceBody(layout tuiLayout) string {
	workspaceLayout := layout
	workspaceLayout.contentWidth = layout.workspaceWidth
	parts := make([]string, 0, 4)
	if workspaceLayout.transcriptHeight > 0 {
		parts = append(parts, m.renderTranscriptRegion(workspaceLayout))
	}
	if workspaceLayout.statusHeight > 0 {
		parts = append(parts, m.renderDockStatusLine(workspaceLayout.contentWidth))
	}
	parts = append(parts, m.renderInputBoxForLayout(workspaceLayout))
	if workspaceLayout.queueHeight > 0 {
		parts = append(parts, m.renderQueuePanel(workspaceLayout.contentWidth, workspaceLayout.queueHeight))
	}
	return fitStyledRect(strings.Join(parts, "\n"), workspaceLayout.contentWidth, layout.contentHeight)
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
			// 全屏覆盖层没有可锚定的 textarea（编辑页的光标由渲染层的反色块
			// 表达）。必须隐藏真实终端光标：clear 只会把它恢复为可见，帧输出
			// 结束后它就停在左下角闪烁，形成"两个光标"。
			m.cursorAnchor.hide()
		}
		return m.renderFullscreenModal()
	}

	layout := m.currentLayout()
	var view string
	switch layout.activityMode {
	case activityLayoutFullscreen:
		inner := m.renderActivityPane(layout.activityWidth, layout.contentHeight)
		view = renderDockedFrame(
			inner,
			m.renderActivityHeader(layout.activityWidth),
			m.renderActivityFullscreenBottomContent(layout.activityWidth),
			layout.frameWidth,
			layout.frameHeight,
		)
	case activityLayoutDocked:
		workspace := m.renderWorkspaceBody(layout)
		activity := m.renderActivityPane(layout.activityWidth, layout.contentHeight)
		separatorColor := colorManager.Hex(colorMarkdownQuoteBorder)
		if m.activity.focus == activityFocusPanel {
			separatorColor = m.currentModeHex()
			if separatorColor == "" {
				separatorColor = colorManager.Hex(colorSignal)
			}
		}
		separatorStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(separatorColor))
		inner := joinActivityColumns(
			workspace,
			activity,
			layout.workspaceWidth,
			layout.activityWidth,
			layout.contentHeight,
			separatorStyle.Render("│"),
		)
		top := renderSplitHairline(
			m.renderHeaderEmbedded(layout.workspaceWidth),
			m.renderActivityHeader(layout.activityWidth),
			layout.workspaceWidth,
			layout.activityWidth,
			"┬",
			separatorColor,
		)
		bottom := renderSplitHairline(
			m.renderBottomWorkspaceLine(layout.workspaceWidth),
			m.renderActivityBottomLine(layout.activityWidth),
			layout.workspaceWidth,
			layout.activityWidth,
			"┴",
			m.currentModeHex(),
		)
		view = top + "\n" + inner + "\n" + bottom
	default:
		inner := m.renderWorkspaceBody(layout)
		top := renderHairlineWithRightHint(
			m.renderHeaderEmbedded(layout.contentWidth),
			m.renderHeaderActivityHint(),
			layout.frameWidth,
			"",
		)
		bottom := m.renderBottomDockLine(layout.contentWidth)
		view = top + "\n" + inner + "\n" + bottom
	}
	if layout.queueInlineHeight > 0 && layout.activityMode != activityLayoutFullscreen {
		rightInset := terminalCellWidth(m.renderBottomDockWorktree(layout.frameWidth)) + 2
		if layout.activityMode == activityLayoutDocked {
			rightInset += layout.activitySeparatorWidth + layout.activityWidth
		}
		view = renderQueueInlineBottomBorder(
			view,
			layout.frameWidth,
			m.queuePanelContent(layout.workspaceWidth),
			m.currentModeHex(),
			terminalCellWidth(m.renderModeIndicator())+2,
			rightInset,
		)
	}
	view = paintStyledBackgroundBody(view, layout.frameWidth, layout.frameHeight, m.styles.Frame, m.theme.Colors.TerminalBackground, true)
	m.updateTerminalCursorAnchor(layout)
	return view
}

func paintStyledBackground(text string, width, height int, style lipgloss.Style, background string) string {
	return paintStyledBackgroundBody(text, width, height, style, background, false)
}

// paintStyledBackgroundBody 背景喷涂。preFitted=true 时调用方保证 text 已是
// 精确的 width×height 矩形（renderDockedFrame 输出），跳过整体重测的
// fit pass；逐行 fitStyledCellLine 仍保留作为宽度兼容保底。
func paintStyledBackgroundBody(text string, width, height int, style lipgloss.Style, background string, preFitted bool) string {
	if !preFitted {
		text = fitStyledRect(text, width, height)
	}
	foreground := ""
	if fg, ok := style.GetForeground().(lipgloss.Color); ok {
		foreground = string(fg)
	}
	lines := strings.Split(text, "\n")
	if paintStyleIsFlat(style) {
		// 纯 fg/bg 样式 + 已精确到 width 的行：lipgloss Render 只会做一次
		// 全行对齐/重新测宽后包一层 SGR。直接拼接等价序列，省掉逐行
		// lipgloss 渲染（grapheme 迭代是整帧 View 的最大热点）。
		bgSGR := backgroundSGR(background)
		fgSGR := foregroundSGR(foreground)
		for i, line := range lines {
			painted := fgSGR + bgSGR + fitStyledCellLine(line, width) + "\x1b[0m"
			lines[i] = restoreBackgroundAfterANSIReset(painted, background, foreground)
		}
		return strings.Join(lines, "\n")
	}
	for i, line := range lines {
		painted := style.Width(width).Render(fitStyledCellLine(line, width))
		lines[i] = restoreBackgroundAfterANSIReset(painted, background, foreground)
	}
	return strings.Join(lines, "\n")
}

// paintStyleIsFlat 报告样式是否只携带颜色（无 padding/margin/border/尺寸），
// 此时对精确宽度的行，Style.Width(w).Render(line) 等价于
// fgSGR+bgSGR+line+reset。
func paintStyleIsFlat(style lipgloss.Style) bool {
	return style.GetWidth() == 0 &&
		style.GetHeight() == 0 &&
		style.GetHorizontalFrameSize() == 0 &&
		style.GetVerticalFrameSize() == 0
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

// renderDockedFrame 渲染主界面固定外框：顶部 ─ 线嵌入 header，底部 ─ 线
// 接收已完成左右锚定的 dock 内容。各段自行携带颜色与样式。
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
	contentFromViewport := true
	if !m.selectionActive && !m.viewportShowsSelection {
		if unit, ok := m.currentTranscriptHoverPatchUnit(); ok && m.transcriptHoverPatch.valid && m.transcriptHoverPatch.unit == unit {
			content = overlayTranscriptViewport(content, m.transcriptHoverPatch.lines, true, unit, m.viewport.YOffset, m.viewport.Width)
		}
	}
	if !m.hasRenderableTranscript() {
		content = renderEmptyState(layout.contentWidth, layout.transcriptHeight)
		contentFromViewport = false
	}
	var base string
	if contentFromViewport {
		// viewport.View() 输出恒为 viewport.Width×viewport.Height 的精确矩形
		//（逐行 pad/cut）；尺寸与面板 body 一致时跳过逐行重测的 fit pass。
		bodyWidth := maxInt(1, layout.contentWidth-transcriptContentStyle.GetHorizontalPadding())
		preFitted := m.viewport.Width == bodyWidth && m.viewport.Height == layout.transcriptHeight
		base = renderFixedStyledPanelBody(
			transcriptContentStyle,
			layout.contentWidth,
			layout.transcriptHeight,
			content,
			preFitted,
		)
	} else {
		base = renderFixedStyledPanel(
			transcriptContentStyle,
			layout.contentWidth,
			layout.transcriptHeight,
			content,
		)
	}

	// Activity 和运行任务状态不再作为 transcript overlay；其余局部浮层仍只
	// 在 workspace transcript 区内合成。
	overlaid := false

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
		overlaid = true
	}
	if !overlaid {
		// 无任何浮层时 renderFixedStyledPanel 已输出精确的
		// contentWidth×transcriptHeight 矩形，末尾 fit 只会重测每一行。
		return base
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
	default:
		return ""
	}
}

type overlayAlignment int

const (
	overlayAlignCenter overlayAlignment = iota
	overlayAlignBottom
	overlayAlignRight
)

func placeOpaqueOverlay(base, overlay string, width, height int, alignment overlayAlignment) string {
	base = fitStyledRect(base, width, height)
	overlayWidth := minInt(width, maxInt(1, widestStyledLine(overlay)))
	overlayHeight := minInt(height, maxInt(1, lipgloss.Height(overlay)))
	overlay = fitStyledRect(overlay, overlayWidth, overlayHeight)

	left := maxInt(0, (width-overlayWidth)/2)
	top := maxInt(0, (height-overlayHeight)/2)
	switch alignment {
	case overlayAlignBottom:
		top = maxInt(0, height-overlayHeight)
	case overlayAlignRight:
		left = maxInt(0, width-overlayWidth-1)
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
	return renderFixedStyledPanelBody(style, totalWidth, totalHeight, body, false)
}

// renderFixedStyledPanelBody 渲染固定尺寸面板。bodyPreFitted=true 时调用方
// 保证 body 已经是精确的 bodyWidth×bodyHeight（如 viewport.View() 输出），
// 跳过逐行重测的 fit pass。尺寸不匹配时仍回退到 fit 保底。
func renderFixedStyledPanelBody(style lipgloss.Style, totalWidth, totalHeight int, body string, bodyPreFitted bool) string {
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
	if !bodyPreFitted {
		body = fitStyledRect(body, bodyWidth, bodyHeight)
	}
	if horizontalBorder == 0 && verticalBorder == 0 && verticalPadding == 0 &&
		style.GetHorizontalMargins() == 0 && style.GetVerticalMargins() == 0 &&
		style.GetAlignHorizontal() == lipgloss.Left && style.GetAlignVertical() == lipgloss.Top {
		// 无边框、无纵向 padding、无 margin 的「水平 padding + 颜色」面板
		//（transcript / input dock 等）可直接构造：body 行已精确到 bodyWidth，
		// lipgloss Render 的对齐/换行/边距 pass 全是固定开销。
		return renderSimpleStyledPanel(style, styleWidth, styleHeight, horizontalPadding, body, bodyHeight)
	}
	rendered := style.
		Width(styleWidth).
		Height(styleHeight).
		Render(body)
	if horizontalBorder == 0 && verticalBorder == 0 {
		// 无边框时 Width/Height 已把渲染结果锁定在 styleWidth×styleHeight
		//（= totalWidth×totalHeight），末尾的 fitStyledRect 只会重测每一行。
		return rendered
	}
	return fitStyledRect(rendered, totalWidth, totalHeight)
}

// renderSimpleStyledPanel 直接构造「水平 padding + 颜色」面板：
// 每行 = SGR + 左 padding + body 行 + 右 padding + reset；补齐行整行空格。
// body 行必须已精确到 (styleWidth - 左右 padding 之和) 个 cell。
func renderSimpleStyledPanel(style lipgloss.Style, styleWidth, styleHeight, horizontalPadding int, body string, bodyHeight int) string {
	foreground := ""
	if fg, ok := style.GetForeground().(lipgloss.Color); ok {
		foreground = string(fg)
	}
	background := ""
	if bg, ok := style.GetBackground().(lipgloss.Color); ok {
		background = string(bg)
	}
	sgr := foregroundSGR(foreground) + backgroundSGR(background)
	reset := ""
	if sgr != "" {
		reset = "\x1b[0m"
	}
	leftPad := strings.Repeat(" ", style.GetPaddingLeft())
	rightPad := strings.Repeat(" ", style.GetPaddingRight())
	lines := strings.Split(body, "\n")
	if len(lines) > bodyHeight {
		lines = lines[:bodyHeight]
	}
	out := make([]string, styleHeight)
	fullRow := sgr + strings.Repeat(" ", styleWidth) + reset
	for i := range out {
		if i < len(lines) {
			out[i] = sgr + leftPad + lines[i] + rightPad + reset
		} else {
			out[i] = fullRow
		}
	}
	return strings.Join(out, "\n")
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
		(!m.activity.visible || (m.activity.focus == activityFocusWorkspace && m.currentLayout().activityMode != activityLayoutFullscreen)) &&
		true
}

// inputCursorTerminalPosition 直接由固定布局矩形计算，不再依赖拼接后字符串高度。
func (m appModel) inputCursorTerminalPosition(layout tuiLayout) terminalCursorPosition {
	queueHeight := minInt(layout.queueHeight, maxInt(0, layout.inputHeight-1))
	textareaHeight := maxInt(1, layout.inputHeight-queueHeight)
	row := minInt(m.visibleInputCursorRow(), maxInt(0, textareaHeight-1))
	// queue 面板位于输入框下方；从终端底部回退时必须跨过 queue 区域。
	upFromBottom := 1 + queueHeight + layout.worktreeHeight + maxInt(0, textareaHeight-row-1)
	column := inputDockStyle.GetPaddingLeft() + m.visibleInputCursorColumn()
	column = minInt(column, maxInt(0, layout.workspaceWidth-1))
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

func visibleTextareaCursorRow(input textareax.Model) int {
	lineInfo := input.LineInfo()
	row := input.Line() + lineInfo.RowOffset
	return minInt(maxInt(0, row), maxInt(0, input.Height()-1))
}

func visibleTextareaCursorColumn(input textareax.Model) int {
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
func applyTextareaTerminalStyle(input *textareax.Model) {
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
		m.input.SetHeight(m.tokenAwareInputVisibleLineCount())
		return
	}
	base := applyActivityGeometry(computeTUILayout(m.width, m.height, inputMinVisibleLines), m.activity.visible, m.activity.widthColumns)
	workspaceModelWidth := base.workspaceWidth
	if base.activityMode == activityLayoutFullscreen {
		workspaceModelWidth = base.contentWidth
	}
	inputWidth := inputDockContentWidth(workspaceModelWidth)
	transcriptWidth := maxInt(1, workspaceModelWidth-transcriptContentStyle.GetHorizontalPadding())
	m.input.SetWidth(inputWidth)

	requestedInputHeight := m.tokenAwareInputVisibleLineCount()
	if m.selectionDock != nil {
		requestedInputHeight = m.selectionDock.preferredHeight(inputWidth)
	}
	queueHeight := m.queuePanelHeight()
	if m.selectionDock == nil {
		requestedInputHeight += queueHeight
	}
	layout := applyActivityGeometry(computeTUILayout(m.width, m.height, requestedInputHeight), m.activity.visible, m.activity.widthColumns)
	if m.selectionDock != nil {
		layout = applyActivityGeometry(computeTUILayoutWithInputLimit(m.width, m.height, requestedInputHeight, selectionDockMaxVisibleLines), m.activity.visible, m.activity.widthColumns)
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
