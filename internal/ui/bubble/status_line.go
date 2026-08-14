// 底部 dock 渲染层。
//
// 布局：输入区上方整行是 context progress bar；最下方边框左侧显示输入模式，
// 中间按空间显示复制反馈或工作树，右侧显示 token usage。
// ready/working/generating 状态词只保留在 header。所有内容按 terminal cell
// 预算截断，整体严格等于目标宽度，数据内容绝不破坏布局。
package bubble

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// statusSegmentSeparator separates compact metadata inside token usage.
const statusSegmentSeparator = "  "

// spinnerFrames 是 braille 旋转 spinner 帧序列，生成态前导。
var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// equalizerAmpDuration 是 idle↔working 振幅过渡时长。
const equalizerAmpDuration = 400 * time.Millisecond

// isAgentWorking 报告 agent 是否处于"工作中"态：流式生成 或 工具调用进行中。
// 区别于 isGenerating（仅流式 delta 期间为 true）：toolCallMsg 会把 isGenerating
// 置 false，但整轮未结束（queryGuard 仍 running），此时应保持 working 态——
// 均衡器继续起伏、状态词显示 working，而非退回 idle。
func (m appModel) isAgentWorking() bool {
	return m.isGenerating || m.isModelWorkRunning() || m.goalWorking || m.planWorking
}

// startTokenRippleExit 记录任务结束时刻，让波纹继续保持固定速度向右越界
// 退出（不再循环），而不是随 working 状态切换瞬间消失。
func (m *appModel) startTokenRippleExit(now time.Time) {
	if m == nil || now.IsZero() {
		return
	}
	m.tokenRippleExitAt = now
}

func (m appModel) tokenRippleActive(now time.Time) bool {
	if m.isAgentWorking() {
		return true
	}
	if m.tokenRippleExitAt.IsZero() {
		return false
	}
	// 退场：波纹头从当前位置走到完全越过右边界（+tail）的最长耗时。
	// 用 m.width 作保守上限（真实 bar 宽 ≤ m.width），渲染层在 head 越界后
	// 自然渲染为空，多续几帧无害。
	maxExit := time.Duration(maxInt(0, m.width+tokenRippleTail)) * tokenRippleSpeed
	return now.Sub(m.tokenRippleExitAt) < maxExit
}

// updateWaveAmp 在 cursorFrameMsg 每帧推进振幅过渡（指针接收者，写持久模型）。
// 目标态由 isAgentWorking 派生（流式 + 工具调用期间均满振幅），翻转时记录起点
// amp 与时刻，用 easeOutCubic 在 400ms 内缓动。结果写入 waveAmpCurrent 供渲染只读。
func (m *appModel) updateWaveAmp(now time.Time) {
	target := m.isAgentWorking()
	if target != m.waveAmpTarget {
		m.waveAmpFrom = m.waveAmpCurrent
		m.waveAmpTarget = target
		m.waveAmpStartedAt = now
	}
	if m.waveAmpStartedAt.IsZero() {
		if target {
			m.waveAmpCurrent = 1
		} else {
			m.waveAmpCurrent = 0
		}
		return
	}
	p := clamp01(float64(now.Sub(m.waveAmpStartedAt)) / float64(equalizerAmpDuration))
	eased := easeOutCubic(p)
	if target {
		m.waveAmpCurrent = m.waveAmpFrom + (1-m.waveAmpFrom)*eased
	} else {
		m.waveAmpCurrent = m.waveAmpFrom * (1 - eased)
	}
}

// spinnerFrame 按 30fps 帧索引降速到 ~10fps 返回当前 spinner 字符。
// idx/3 让每 3 帧切一帧（~100ms/帧，1s/周期），既流畅又不晃眼。
func spinnerFrame(idx int) string {
	if len(spinnerFrames) == 0 {
		return ""
	}
	return spinnerFrames[(idx/3)%len(spinnerFrames)]
}

const (
	// tokenRippleSpeed 是波纹固定速度：每格耗时。循环态按此速度首尾相接
	// 循环；退场态按此速度向右越界退出。
	tokenRippleSpeed = 40 * time.Millisecond
	tokenRippleTail  = 14

	// token frontier 三段 glyph：free 最淡点状、cache 中等点状、used 实心，
	// 比细线 ━/─ 更醒目。波纹块用 used 段同款实心 glyph。
	tokenFreeGlyph   = "░"
	tokenCacheGlyph  = "▒"
	tokenUsedGlyph   = "█"
	tokenRippleGlyph = "█"
)

// renderDockStatusLine 渲染输入区上方完整的 context progress bar。
// 模式、token 数值和工作树都位于最下方边框，避免切断进度条。
func (m appModel) renderDockStatusLine(width int) string {
	width = maxInt(1, width)
	stats := m.contextStats()
	limit := maxInt(1, stats.LimitTokens)
	used := clampInt(stats.UsedTokens, 0, limit)
	cache := clampInt(stats.CacheTokens, 0, used)
	return m.renderTokenFrontierWith(width, used, cache, limit, m.currentModeHex())
}

// renderBottomDockLine 渲染最下方边框：模式靠左，token usage 靠右；中间剩余
// 空间优先显示复制反馈，其次显示工作树 chip。线色随 agentmode 变化。
func (m appModel) renderBottomDockLine(width int) string {
	width = maxInt(1, width)
	lineStyle := lipgloss.NewStyle()
	if modeHex := m.currentModeHex(); modeHex != "" {
		lineStyle = lineStyle.Foreground(lipgloss.Color(modeHex))
	}
	line := lineStyle.Render(strings.Repeat("─", width))

	left := m.renderModeIndicator()
	right := m.renderBottomDockUsage()
	left = truncateStyledCellLine(left, maxInt(0, width-2))
	leftWidth := terminalCellWidth(left)
	leftAt := 0
	if leftWidth > 0 && width-leftWidth >= 2 {
		leftAt = 1
	}
	leftEnd := leftAt + leftWidth

	// Keep at least one rule cell between the mode and usage plus one at the
	// outer right edge. On very narrow terminals, usage truncates before it can
	// overlap the mode.
	right = truncateStyledCellLine(right, maxInt(0, width-leftEnd-2))
	rightWidth := terminalCellWidth(right)
	rightAt := width
	if rightWidth > 0 {
		rightAt = maxInt(leftEnd, width-rightWidth-1)
	}

	if leftWidth > 0 {
		line = composeStyledCellOverlay(line, left, leftAt, width)
	}
	if rightWidth > 0 {
		line = composeStyledCellOverlay(line, right, rightAt, width)
	}

	middleLeft := minInt(width, leftEnd+1)
	middleRight := maxInt(middleLeft, width-1)
	if rightWidth > 0 {
		middleRight = maxInt(middleLeft, rightAt-1)
	}
	if middleRight > middleLeft {
		middle := m.renderBottomDockMiddle(middleRight-middleLeft, width)
		if middle != "" {
			middleWidth := terminalCellWidth(middle)
			middleAt := middleLeft + maxInt(0, (middleRight-middleLeft-middleWidth)/2)
			line = composeStyledCellOverlay(line, middle, middleAt, width)
		}
	}
	return fitStyledCellLine(line, width)
}

func (m appModel) renderBottomDockUsage() string {
	stats := m.contextStats()
	limit := maxInt(1, stats.LimitTokens)
	used := clampInt(stats.UsedTokens, 0, limit)
	cache := clampInt(stats.CacheTokens, 0, used)
	countStyle := contextUsedStyle
	if modeHex := m.currentModeHex(); modeHex != "" {
		countStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(modeHex)).Bold(true)
	}
	usage := countStyle.Render(formatCompactTokenCount(used) + " / " + formatCompactTokenCount(limit))
	if cache > 0 {
		ratio := int(float64(cache) * 100 / float64(maxInt(1, used)))
		if ratio >= 1 {
			usage += statusSegmentSeparator + countStyle.Render(fmt.Sprintf("ⓒ%d%%", ratio))
		}
	}
	return usage
}

func (m appModel) renderBottomDockMiddle(width, totalWidth int) string {
	if width <= 0 {
		return ""
	}
	if toast := m.renderStatusLeftSegment(); toast != "" {
		return truncateStyledCellLine(toast, width)
	}
	if totalWidth < worktreeInlineMinimumWidth {
		return ""
	}
	return m.renderWorktreeChip(width)
}

// currentModeHex 返回 agentmode（plan/goal）的强调色 hex；chat/shell/
// terminal 等非 agentmode 返回空串，表示保持默认配色。
func (m appModel) currentModeHex() string {
	switch {
	case m.planMode:
		return colorManager.Hex(colorSignal)
	case m.goalMode:
		return colorManager.Hex(colorInputTokenCommand)
	default:
		return ""
	}
}

// renderStatusLeftSegment 返回短暂的复制反馈。状态词
// （ready/working/generating）已移入 header，输入 dock 不再显示。
func (m appModel) renderStatusLeftSegment() string {
	if m.isGoalInputActive() {
		// Goal 状态通过边框和 mode=goal 表示；输入区不显示 toast。
		return ""
	}
	if toast := m.copyToastAt(m.animationNow()); toast != "" {
		return copyToastStyle.Render(toast)
	}
	return ""
}

// copyToastAt 返回当前仍有效的复制反馈文本（未过期），过期则返回空串。
func (m appModel) copyToastAt(now time.Time) string {
	if m.copyToast != "" && now.Before(m.copyToastUntil) {
		return m.copyToast
	}
	return ""
}

func (m appModel) renderTokenFrontier(width, used, cache, limit int) string {
	return m.renderTokenFrontierWith(width, used, cache, limit, "")
}

// renderTokenFrontierWith 渲染 token 进度条，三段：cache 段（最左，主题
// cache 色加深）+ used 段 + free 段。modeHex 非空时（plan/goal agentmode）
// 整条线使用强调色。波纹为固定速度实心块：循环态首尾相接，退场态向右越界，
// 波纹头颜色与 used（非 cache）段一致。
func (m appModel) renderTokenFrontierWith(width, used, cache, limit int, modeHex string) string {
	width = maxInt(1, width)
	limit = maxInt(1, limit)
	used = clampInt(used, 0, limit)
	cache = clampInt(cache, 0, used)
	usedCells := int(float64(width) * float64(used) / float64(limit))
	usedCells = clampInt(usedCells, 0, width)
	cacheCells := int(float64(width) * float64(cache) / float64(limit))
	cacheCells = clampInt(cacheCells, 0, usedCells)

	usedStyle := contextUsedStyle
	cacheStyle := contextCacheStyle
	freeStyle := contextFreeStyle
	rippleHex := colorManager.Hex(colorContextUsed)
	if modeHex != "" {
		usedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(modeHex)).Bold(true)
		cacheStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(interpolateHexColor(colorManager.Hex(colorTerminalBackground), modeHex, 0.9))).Bold(true)
		freeStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(interpolateHexColor(colorManager.Hex(colorTerminalBackground), modeHex, 0.45)))
		rippleHex = modeHex
	}
	cells := make([]string, width)
	for i := range cells {
		switch {
		case i < cacheCells:
			cells[i] = cacheStyle.Render(tokenCacheGlyph)
		case i < usedCells:
			cells[i] = usedStyle.Render(tokenUsedGlyph)
		default:
			cells[i] = freeStyle.Render(tokenFreeGlyph)
		}
	}
	if usedCells >= width {
		return strings.Join(cells, "")
	}

	now := m.animationNow()
	if !m.tokenRippleActive(now) {
		return strings.Join(cells, "")
	}
	background := colorManager.Hex(colorTerminalBackground)
	freeCells := width - usedCells
	looping := m.isAgentWorking()
	head := m.tokenRippleHead(usedCells, freeCells, now)

	if looping && freeCells < tokenRippleTail {
		// 窄 free 区：波纹块比 free 区长，整个 free 区都是波纹。
		for offset := 0; offset < freeCells; offset++ {
			distance := positiveModulo(head-usedCells-offset, tokenRippleTail)
			cells[usedCells+offset] = renderTokenRippleCell(distance, background, rippleHex)
		}
		return strings.Join(cells, "")
	}

	// 宽 free 区（或退场态）：波纹块 = head 及之前 tail-1 格。
	// 循环态跨边界时绕回 free 区首部；退场态单调越界、不绕回。
	for i := head - tokenRippleTail + 1; i <= head; i++ {
		cell := i
		if looping && cell < usedCells {
			cell += freeCells
		}
		if cell < usedCells || cell >= width {
			continue
		}
		cells[cell] = renderTokenRippleCell(head-i, background, rippleHex)
	}
	return strings.Join(cells, "")
}

// tokenRippleHead 返回当前波纹头 cell 索引。循环态在 free 区内首尾相接；
// 退场态从退场开始时刻的循环头位置起，按固定速度向右越界。
func (m appModel) tokenRippleHead(usedCells, freeCells int, now time.Time) int {
	if freeCells <= 0 {
		return usedCells
	}
	loopOffset := func(at time.Time) int {
		return int(time.Duration(at.UnixNano())/tokenRippleSpeed) % freeCells
	}
	if m.isAgentWorking() {
		return usedCells + loopOffset(now)
	}
	if m.tokenRippleExitAt.IsZero() {
		return usedCells
	}
	exitHead := usedCells + loopOffset(m.tokenRippleExitAt)
	return exitHead + int(now.Sub(m.tokenRippleExitAt)/tokenRippleSpeed)
}

func renderTokenRippleCell(distance int, background, rippleHex string) string {
	alpha := float64(tokenRippleTail-distance) / float64(tokenRippleTail)
	alpha = clamp01(alpha)
	color := interpolateHexColor(background, rippleHex, alpha)
	return lipgloss.NewStyle().Foreground(lipgloss.Color(color)).Render(tokenRippleGlyph)
}

func positiveModulo(value, modulus int) int {
	if modulus <= 0 {
		return 0
	}
	result := value % modulus
	if result < 0 {
		result += modulus
	}
	return result
}

// renderModeIndicator 返回当前输入模式标记；chat/多行输入共用 chat 模式。
func (m appModel) renderModeIndicator() string {
	switch {
	case m.planMode:
		return modePlanStyle.Render("plan")
	case m.goalMode:
		return modeGoalStyle.Render("goal")
	case m.isTerminalInputActive() || m.runningTerminal:
		return modeTerminalStyle.Render("terminal")
	case hasBangPrefix(m.input.Value()):
		return modeShellStyle.Render("!shell")
	default:
		return modeChatStyle.Render("chat")
	}
}

// equalizerActiveStyle 是均衡器+百分比样式（cyan，不加粗避免块字符过重）。
var equalizerActiveStyle = contextUsedStyle.Copy().UnsetBold()

// equalizerDimStyle 是辅助信息（cache%/free%）样式，低饱和。
var equalizerDimStyle = contextFreeStyle.Copy()
