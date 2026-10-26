// 底部 dock 渲染层。
//
// 输入区上方显示上下文进度；下方显示模式、带标签的用量和项目/分支。
// ready/working/generating 状态词只保留在 header。所有内容按 terminal cell
// 预算截断，整体严格等于目标宽度，数据内容绝不破坏布局。
package bubble

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"paw/internal/runtime/loop"
)

// statusSegmentSeparator separates compact metadata inside token usage.
const statusSegmentSeparator = "  "

// spinnerFrames 是 braille 旋转 spinner 帧序列，生成态前导。
var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// isAgentWorking 报告 agent 是否处于"工作中"态：流式生成 或 工具调用进行中。
// 区别于 isGenerating（仅流式 delta 期间为 true）：toolCallMsg 会把 isGenerating
// 置 false，但整轮未结束（queryGuard 仍 running），此时应保持 working 态。
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

	// token frontier 三段细轨 glyph：free 细线、cache 虚线、used 粗实线，
	// 替代刺眼的满高粗块 █/▒/░，波纹滑过时点亮为粗实线 ━。
	tokenFreeGlyph   = "─"
	tokenCacheGlyph  = "╍"
	tokenUsedGlyph   = "━"
	tokenRippleGlyph = "━"
)

func (m appModel) renderDockStatusLine(width int) string {
	width = maxInt(1, width)
	stats := m.contextStats()
	if !contextCapacityKnown(stats) {
		return contextFreeStyle.Render(fitStyledCellLine(copyContextLabel+" — "+strings.Repeat("·", maxInt(0, width-6)), width))
	}
	limit := maxInt(1, stats.LimitTokens)
	used := maxInt(0, stats.UsedTokens)
	cache := maxInt(0, stats.CacheTokens)
	if width <= 20 {
		return m.renderTokenFrontierWith(width, used, cache, limit, m.currentModeHex())
	}

	percent := int(float64(used)*100/float64(limit) + 0.5)
	label := fmt.Sprintf("%s %d%% ", copyContextLabel, percent)
	usage := " " + formatCompactTokenCount(used) + "/" + formatCompactTokenCount(limit)
	barWidth := width - terminalCellWidth(label)
	if barWidth-terminalCellWidth(usage) >= 12 {
		barWidth -= terminalCellWidth(usage)
	} else {
		usage = ""
	}
	return contextFreeStyle.Render(label) +
		m.renderTokenFrontierWith(barWidth, used, cache, limit, m.currentModeHex()) +
		contextFreeStyle.Render(usage)
}

func contextCapacityKnown(stats loop.ContextStats) bool {
	return stats.Known && stats.LimitTokens > 0 && (stats.TotalKnown || stats.InputKnown && stats.OutputKnown)
}

// renderBottomDockLine 渲染最下方边框：模式靠左，项目/分支靠右；中间
// 留给复制反馈（toast），无反馈时保持空白。线色随 agentmode 变化。
func (m appModel) renderBottomDockLineWithRight(width int, right string) string {
	width = maxInt(1, width)
	lineStyle := lipgloss.NewStyle()
	if modeHex := m.currentModeHex(); modeHex != "" {
		lineStyle = lineStyle.Foreground(lipgloss.Color(modeHex))
	}
	dash := func(n int) string {
		if n <= 0 {
			return ""
		}
		return lineStyle.Render(strings.Repeat(" ", n))
	}

	left := m.renderModeIndicator()
	left = truncateStyledCellLine(left, maxInt(0, width-2))
	leftWidth := terminalCellWidth(left)
	leftAt := 0
	if leftWidth > 0 && width-leftWidth >= 2 {
		leftAt = 1
	}
	leftEnd := leftAt + leftWidth

	// Keep at least one rule cell between the mode and worktree plus one at the
	// outer right edge. On narrow terminals the worktree is omitted before it can
	// overlap the mode or token usage.
	right = truncateStyledCellLine(right, maxInt(0, width-leftEnd-2))
	rightWidth := terminalCellWidth(right)
	rightAt := width
	if rightWidth > 0 {
		rightAt = maxInt(leftEnd, width-rightWidth-1)
	}

	middleLeft := minInt(width, leftEnd+1)
	middleRight := maxInt(middleLeft, width-1)
	if rightWidth > 0 {
		middleRight = maxInt(middleLeft, rightAt-1)
	}
	middle := ""
	middleAt := -1
	middleWidth := 0
	if middleRight > middleLeft {
		middle = m.renderBottomDockMiddle(middleRight - middleLeft)
		if middle != "" {
			middleWidth = terminalCellWidth(middle)
			middleAt = middleLeft + maxInt(0, (middleRight-middleLeft-middleWidth)/2)
		}
	}

	// 分段直接拼接：上游截断保证 left/middle/right 区间互不重叠且落在
	// [0,width) 内，无需对整条 200-cell 基线做 cell 级切割合成
	//（旧实现每帧 3 次全行 parse+cut 是 View 最大的分配源）。
	var b strings.Builder
	b.WriteString(dash(leftAt))
	b.WriteString(left)
	if middleAt >= 0 {
		b.WriteString(dash(middleAt - leftEnd))
		b.WriteString(middle)
		b.WriteString(dash(rightAt - middleAt - middleWidth))
	} else {
		b.WriteString(dash(rightAt - leftEnd))
	}
	b.WriteString(right)
	b.WriteString(dash(width - rightAt - rightWidth))
	return fitStyledCellLine(b.String(), width)
}

func (m appModel) renderBottomDockLine(width int) string {
	return m.renderBottomDockLineWithRight(width, m.renderBottomDockWorktree(width))
}

func (m appModel) renderBottomWorkspaceLine(width int) string {
	return m.renderBottomDockLineWithRight(width, "")
}

func (m appModel) renderActivityBottomLine(width int) string {
	return embedHairlineContent(m.renderBottomDockWorktree(maxInt(width, worktreeInlineMinimumWidth)), width, m.currentModeHex())
}

func (m appModel) renderBottomDockMiddle(width int) string {
	if width <= 0 {
		return ""
	}
	if toast := m.renderStatusLeftSegment(); toast != "" {
		return truncateStyledCellLine(toast, width)
	}
	// CTX 读数唯一化到底部上下文条（renderDockStatusLine），底栏不再重复；
	// 缓存占比数字随 footer 一同退役（▒ 段仍保留缓存可视信息）。
	return ""
}

func (m appModel) renderBottomDockWorktree(totalWidth int) string {
	if totalWidth < worktreeInlineMinimumWidth {
		return ""
	}
	return m.renderWorktreeChip(minInt(28, maxInt(1, totalWidth/3)))
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
// 整条线使用强调色。波纹为固定速度细轨流光：循环态首尾相接，退场态向右越界，
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
		if colorManager.palette.TerminalBackground != "" {
			cacheStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(interpolateHexColor(colorManager.Hex(colorTerminalBackground), modeHex, 0.9))).Bold(true)
			freeStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(interpolateHexColor(colorManager.Hex(colorTerminalBackground), modeHex, 0.45)))
		}
		rippleHex = modeHex
	}

	// 按段批量渲染：同一连续段只做一次 lipgloss Render，而不是逐 cell
	//（200 列终端下旧实现每帧 200 次 Render 是固定大头）。
	var front strings.Builder
	if cacheCells > 0 {
		front.WriteString(cacheStyle.Render(strings.Repeat(tokenCacheGlyph, cacheCells)))
	}
	if usedCells > cacheCells {
		front.WriteString(usedStyle.Render(strings.Repeat(tokenUsedGlyph, usedCells-cacheCells)))
	}
	freeCells := width - usedCells
	if freeCells <= 0 {
		return front.String()
	}

	now := m.animationNow()
	if !m.tokenRippleActive(now) {
		front.WriteString(freeStyle.Render(strings.Repeat(tokenFreeGlyph, freeCells)))
		return front.String()
	}

	background := colorManager.Hex(colorTerminalBackground)
	looping := m.isAgentWorking()
	head := m.tokenRippleHead(usedCells, freeCells, now)

	// free 区内逐 cell 布局：空串表示普通 free glyph（最后按连续段批量
	// 渲染），非空为已染色的波纹 cell。
	free := make([]string, freeCells)
	if looping && freeCells < tokenRippleTail {
		// 窄 free 区：波纹块比 free 区长，整个 free 区都是波纹。
		for offset := 0; offset < freeCells; offset++ {
			distance := positiveModulo(head-usedCells-offset, tokenRippleTail)
			free[offset] = renderTokenRippleCell(distance, background, rippleHex)
		}
		front.WriteString(joinStyledCellRuns(free, freeStyle))
		return front.String()
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
		free[cell-usedCells] = renderTokenRippleCell(head-i, background, rippleHex)
	}
	front.WriteString(joinStyledCellRuns(free, freeStyle))
	return front.String()
}

// joinStyledCellRuns 把逐 cell 布局拼成一行：空串 cell 归并为一次批量
// Render，非空 cell（已带样式）原样输出。每个 glyph 固定 1 cell 宽。
func joinStyledCellRuns(cells []string, plainStyle lipgloss.Style) string {
	var b strings.Builder
	runStart := -1
	for i, cell := range cells {
		if cell == "" {
			if runStart < 0 {
				runStart = i
			}
			continue
		}
		if runStart >= 0 {
			b.WriteString(plainStyle.Render(strings.Repeat(tokenFreeGlyph, i-runStart)))
			runStart = -1
		}
		b.WriteString(cell)
	}
	if runStart >= 0 {
		b.WriteString(plainStyle.Render(strings.Repeat(tokenFreeGlyph, len(cells)-runStart)))
	}
	return b.String()
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
	if background == "" {
		return lipgloss.NewStyle().Foreground(lipgloss.Color(rippleHex)).Faint(alpha < 0.5).Render(tokenRippleGlyph)
	}
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
		return modePlanStyle.Render(copyModePlan)
	case m.goalMode:
		return modeGoalStyle.Render(copyModeGoal)
	case m.isTerminalInputActive() || m.runningTerminal:
		return modeTerminalStyle.Render(copyModeTerminal)
	case hasBangPrefix(m.input.Value()):
		return modeShellStyle.Render("!shell")
	default:
		return modeChatStyle.Render(copyModeChat)
	}
}
