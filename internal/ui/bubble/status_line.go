// 底部 status 行渲染层（方案 B  细线分隔）。
//
// 布局：[状态]  [模式]  [token ripple]  [token count]  [工作树]
// 生成态前导 braille 旋转 spinner；均衡器 idle=进度填充、generating=波浪起伏。
// 遵循 UI/数据隔离：各段从 appModel accessor 读数据，按 cell 预算截断，整体
// 严格等于 width 个 cell，数据内容绝不破坏布局。
package bubble

import (
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// statusSegmentSeparator 是 B 布局各段之间的点号分隔符（轻盈）。
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
	return m.isGenerating || m.isModelWorkRunning()
}

// startTokenRippleExit 保留当前 Ripple 周期的剩余部分，让回答结束时波纹
// 继续向右移动并淡出，而不是随 working 状态切换瞬间消失。
func (m *appModel) startTokenRippleExit(now time.Time) {
	if m == nil || now.IsZero() {
		return
	}
	m.tokenRippleHideAt = now.Add(tokenRippleRemainingUntilExit(now))
}

func (m appModel) tokenRippleActive(now time.Time) bool {
	if m.isAgentWorking() {
		return true
	}
	return !m.tokenRippleHideAt.IsZero() && now.Before(m.tokenRippleHideAt)
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
	tokenRippleTravel = 3 * time.Second
	tokenRippleExit   = 600 * time.Millisecond
	tokenRippleCycle  = tokenRippleTravel + tokenRippleExit
	tokenRippleTail   = 14
)

// renderDockStatusLine 渲染输入框上方的单行状态条。
func (m appModel) renderDockStatusLine(width int) string {
	width = maxInt(1, width)

	left := m.renderStatusLeftSegment()
	stats := m.contextStats()
	limit := maxInt(1, stats.LimitTokens)
	used := clampInt(stats.UsedTokens, 0, limit)
	count := contextUsedStyle.Render(formatCompactTokenCount(used) + " / " + formatCompactTokenCount(limit))
	mode := m.renderModeIndicator()
	sepW := terminalCellWidth(statusSegmentSeparator)
	left = truncateStyledCellLine(left, minInt(18, width))

	worktree := ""
	if width >= worktreeInlineMinimumWidth {
		worktree = m.renderWorktreeChip(minInt(28, maxInt(1, width/3)))
	}

	// Keep the requested information order. When space gets tight, hide the
	// worktree first, then mode, then token count, while retaining a frontier.
	includeMode := true
	includeCount := true
	worktreeGap := 2
	statusWidth := func(includeMode, includeCount bool, worktree string) int {
		fixed := terminalCellWidth(left)
		// The frontier is always present, so it is already one of the parts.
		parts := 2
		if includeMode {
			fixed += terminalCellWidth(mode)
			parts++
		}
		if includeCount {
			fixed += terminalCellWidth(count)
			parts++
		}
		fixed += (parts - 1) * sepW
		if worktree != "" {
			fixed += worktreeGap + terminalCellWidth(worktree)
		}
		return fixed
	}
	fixed := statusWidth(includeMode, includeCount, worktree)
	if fixed >= width {
		worktree = ""
		fixed = statusWidth(includeMode, includeCount, worktree)
	}
	if fixed >= width {
		includeMode = false
		fixed = statusWidth(includeMode, includeCount, worktree)
	}
	if fixed >= width {
		includeCount = false
		fixed = statusWidth(includeMode, includeCount, worktree)
	}
	barBudget := maxInt(1, width-fixed)
	bar := m.renderTokenFrontier(barBudget, used, limit)
	statusParts := []string{left}
	if includeMode {
		statusParts = append(statusParts, mode)
	}
	statusParts = append(statusParts, bar)
	if includeCount {
		statusParts = append(statusParts, count)
	}
	line := strings.Join(statusParts, statusSegmentSeparator)
	if worktree != "" {
		line += strings.Repeat(" ", worktreeGap) + worktree
	}
	return fitStyledCellLine(line, width)
}

// renderStatusLeftSegment 返回左段：优先显示复制反馈 toast，其次工作中态
// spinner + 状态词，空闲显示 ready。
func (m appModel) renderStatusLeftSegment() string {
	if toast := m.copyToastAt(m.animationNow()); toast != "" {
		return copyToastStyle.Render(toast)
	}
	if m.isGenerating {
		return generatingStatusStyle.Render(spinnerFrame(m.spinnerFrameIdx) + " generating")
	}
	if m.isModelWorkRunning() {
		return generatingStatusStyle.Render(spinnerFrame(m.spinnerFrameIdx) + " working")
	}
	return idleStatusStyle.Render("ready")
}

// copyToastAt 返回当前仍有效的复制反馈文本（未过期），过期则返回空串。
func (m appModel) copyToastAt(now time.Time) string {
	if m.copyToast != "" && now.Before(m.copyToastUntil) {
		return m.copyToast
	}
	return ""
}

func (m appModel) renderTokenFrontier(width, used, limit int) string {
	width = maxInt(1, width)
	limit = maxInt(1, limit)
	used = clampInt(used, 0, limit)
	usedCells := int(float64(width) * float64(used) / float64(limit))
	usedCells = clampInt(usedCells, 0, width)
	cells := make([]string, width)
	for i := range cells {
		if i < usedCells {
			cells[i] = contextUsedStyle.Render("━")
		} else {
			cells[i] = contextFreeStyle.Render("─")
		}
	}
	if usedCells >= width {
		return strings.Join(cells, "")
	}

	now := m.animationNow()
	if !m.tokenRippleActive(now) {
		return strings.Join(cells, "")
	}
	phase := tokenRipplePhase(now)
	fade := tokenRippleFade(phase)
	background := colorManager.Hex(colorTerminalBackground)
	freeCells := width - usedCells
	if freeCells < tokenRippleTail {
		// A narrow free span is a window into one uncompressed cyclic ripple.
		// The next head waits for the previous tail to pass instead of crowding it.
		for offset, distance := range tokenRippleNarrowDistances(freeCells, phase) {
			cells[usedCells+offset] = renderTokenRippleCell(distance, fade, background)
		}
		return strings.Join(cells, "")
	}

	head := tokenRippleHead(usedCells, width, phase)
	for i := maxInt(usedCells, head-tokenRippleTail+1); i <= head && i < width; i++ {
		cells[i] = renderTokenRippleCell(head-i, fade, background)
	}
	return strings.Join(cells, "")
}

func tokenRipplePhase(now time.Time) time.Duration {
	phase := now.Sub(time.Unix(0, 0)) % tokenRippleCycle
	if phase < 0 {
		phase += tokenRippleCycle
	}
	return phase
}

// tokenRippleRemainingUntilExit returns the time until the active cycle's head
// and complete tail have passed the context meter's right edge.
func tokenRippleRemainingUntilExit(now time.Time) time.Duration {
	remaining := tokenRippleCycle - tokenRipplePhase(now)
	if remaining <= 0 {
		return tokenRippleCycle
	}
	return remaining
}

func tokenRippleHead(usedCells, width int, phase time.Duration) int {
	lastVisible := maxInt(usedCells, width-1)
	if phase <= tokenRippleTravel {
		progress := clamp01(float64(phase) / float64(tokenRippleTravel))
		return usedCells + int(float64(lastVisible-usedCells)*progress+0.5)
	}
	exitProgress := clamp01(float64(phase-tokenRippleTravel) / float64(tokenRippleExit))
	return lastVisible + int(float64(tokenRippleTail)*exitProgress+0.5)
}

func tokenRippleFade(phase time.Duration) float64 {
	if phase <= tokenRippleTravel {
		return 1
	}
	return 1 - clamp01(float64(phase-tokenRippleTravel)/float64(tokenRippleExit))
}

// tokenRippleNarrowDistances returns a contiguous window into the full ripple
// sequence. Distances wrap only after the complete tail has passed, so a new
// head never overwrites or compresses the previous tail in a narrow free span.
func tokenRippleNarrowDistances(freeCells int, phase time.Duration) []int {
	return tokenRippleNarrowDistancesAtHead(freeCells, tokenRippleHead(0, freeCells, phase))
}

func tokenRippleNarrowDistancesAtHead(freeCells, virtualHead int) []int {
	if freeCells <= 0 {
		return nil
	}
	distances := make([]int, freeCells)
	for offset := range distances {
		distances[offset] = positiveModulo(virtualHead-offset, tokenRippleTail)
	}
	return distances
}

func renderTokenRippleCell(distance int, fade float64, background string) string {
	alpha := float64(tokenRippleTail-distance) / float64(tokenRippleTail)
	alpha = clamp01(alpha) * fade
	glyph := "░"
	switch distance {
	case 0:
		glyph = "█"
	case 1:
		glyph = "▓"
	case 2:
		glyph = "▒"
	}
	color := interpolateHexColor(background, colorManager.Hex(colorSignal), alpha)
	return lipgloss.NewStyle().Foreground(lipgloss.Color(color)).Render(glyph)
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

// renderModeIndicator 返回右侧模式标记。优先级：terminal > !bang > multiline > chat。
func (m appModel) renderModeIndicator() string {
	switch {
	case m.isTerminalInputActive() || m.runningTerminal:
		return modeTerminalStyle.Render("terminal")
	case hasBangPrefix(m.input.Value()):
		return modeShellStyle.Render("!shell")
	case m.hasMultilineInput():
		return modeMultilineStyle.Render("multiline")
	default:
		return modeChatStyle.Render("chat")
	}
}

// equalizerActiveStyle 是均衡器+百分比样式（cyan，不加粗避免块字符过重）。
var equalizerActiveStyle = contextUsedStyle.Copy().UnsetBold()

// equalizerDimStyle 是辅助信息（cache%/free%）样式，低饱和。
var equalizerDimStyle = contextFreeStyle.Copy()
