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

// spinnerFrame 按 30fps 帧索引降速到 ~10fps 返回当前 spinner 字符。
// idx/3 让每 3 帧切一帧（~100ms/帧，1s/周期），既流畅又不晃眼。
func spinnerFrame(idx int) string {
	if len(spinnerFrames) == 0 {
		return ""
	}
	return spinnerFrames[(idx/3)%len(spinnerFrames)]
}

const (
	// token frontier 三段 glyph：free 最淡点状、cache 中等点状、used 实心，
	// 比细线 ━/─ 更醒目。
	tokenFreeGlyph  = "░"
	tokenCacheGlyph = "▒"
	tokenUsedGlyph  = "█"
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
// 整条线使用强调色。活动指示由底部 ⚒ 状态行承担，进度条不再叠加波纹动画。
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
	if modeHex != "" {
		usedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(modeHex)).Bold(true)
		if colorManager.palette.TerminalBackground != "" {
			cacheStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(interpolateHexColor(colorManager.Hex(colorTerminalBackground), modeHex, 0.9))).Bold(true)
			freeStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(interpolateHexColor(colorManager.Hex(colorTerminalBackground), modeHex, 0.45)))
		}
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
	if freeCells := width - usedCells; freeCells > 0 {
		front.WriteString(freeStyle.Render(strings.Repeat(tokenFreeGlyph, freeCells)))
	}
	return front.String()
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
