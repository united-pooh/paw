package bubble

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"paw/internal/runtime/loop"
)

// renderContextDock 用满可用宽度绘制容量轨；文字与轨道使用互斥的 cell 区间。
func (m appModel) renderContextDock(width int, right string) string {
	stats := m.contextStats()
	percent, amount := contextReadout(stats, 0)
	percentStyle := contextFreeStyle.Bold(true)
	if hex := m.contextDockHex(stats); hex != "" {
		percentStyle = percentStyle.Foreground(lipgloss.Color(hex))
	}
	reading := percentStyle.Render(percent)
	left := m.renderModeIndicator() + contextFreeStyle.Render(" · "+copyContextLabel)
	toast := m.renderBottomDockMiddle(width / 3)
	if width >= 60 {
		middle := reading + contextFreeStyle.Render(" · "+amount)
		if toast != "" {
			middle = toast
		}
		if line, ok := m.renderCenteredContextDock(width, left, middle, right, stats); ok {
			return line
		}
	}
	// 窄屏先让工作区和完整计数退出，保留模式、上下文与右侧百分比。
	if toast != "" {
		reading = toast
	}
	padding := minInt(1, width/2)
	inner := width - 2*padding
	reading = truncateStyledCellLine(reading, inner)
	leftBudget := inner - terminalCellWidth(reading) - 5
	if leftBudget <= 0 {
		return fitStyledCellLine(strings.Repeat(" ", padding)+reading, width)
	}
	if terminalCellWidth(left) > leftBudget {
		left = m.renderModeIndicator()
	}
	left = truncateStyledCellLine(left, leftBudget)
	trackWidth := inner - terminalCellWidth(left) - terminalCellWidth(reading) - 4
	return fitStyledCellLine(" "+left+"  "+m.renderContextDockTrack(trackWidth, stats)+"  "+reading+" ", width)
}

// renderCenteredContextDock 把读数对齐整行中心，两侧留两格不画线。
func (m appModel) renderCenteredContextDock(width int, left, middle, right string, stats loop.ContextStats) (string, bool) {
	middleWidth := terminalCellWidth(middle)
	middleAt := (width - middleWidth) / 2
	leftTrackWidth := middleAt - 2 - (1 + terminalCellWidth(left) + 1)
	if leftTrackWidth < 1 {
		return "", false
	}
	// 长项目名按右侧实际预算截断，至少保留一格轨道和文字间隔。
	rightBudget := width - 1 - (middleAt + middleWidth + 2) - 2
	if rightBudget < 3 {
		right = ""
	} else {
		right = truncateStyledCellLine(right, rightBudget)
	}
	rightTrackWidth := width - 1 - (middleAt + middleWidth + 2)
	rightGap := ""
	if right != "" {
		rightTrackWidth -= terminalCellWidth(right) + 1
		rightGap = " "
	}
	if rightTrackWidth < 1 {
		return "", false
	}
	// 先生成一根连续容量轨，再分到数字两侧，避免左右各算一次占用比例。
	trackWidth := leftTrackWidth + rightTrackWidth
	track := m.renderContextDockTrack(trackWidth, stats)
	line := " " + left + " " + cutStyledCellsExact(track, 0, leftTrackWidth) +
		"  " + middle + "  " + cutStyledCellsExact(track, leftTrackWidth, trackWidth) + rightGap + right + " "
	return fitStyledCellLine(line, width), true
}

func (m appModel) renderContextDockTrack(width int, stats loop.ContextStats) string {
	if width <= 0 {
		return ""
	}
	if !contextCapacityKnown(stats) {
		return contextFreeStyle.Render(strings.Repeat("·", width))
	}
	cache := 0
	if stats.CacheKnown {
		cache = stats.CacheTokens
	}
	return m.renderTokenFrontierStatic(width, stats.UsedTokens, cache, stats.LimitTokens, m.contextDockHex(stats))
}

func (m appModel) contextDockHex(stats loop.ContextStats) string {
	if contextCapacityKnown(stats) {
		if stats.UsedTokens > stats.LimitTokens {
			return colorManager.Hex(colorStatusError)
		}
		if float64(stats.UsedTokens)/float64(stats.LimitTokens) >= 0.99 {
			return colorManager.Hex(colorStatusWarning)
		}
	}
	return m.currentModeHex()
}
