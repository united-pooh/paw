package bubble

import (
	"fmt"
	"github.com/charmbracelet/lipgloss"
	"gocode/internal/loop"
	"gocode/internal/settings"
	"math"
	"strconv"
	"strings"
	"time"
)

func (m appModel) contextMeterTitle() string {
	width := m.width
	if width <= 0 {
		width = contextMeterDefaultWidth
	}
	return m.contextMeterLine(maxInt(28, width-2))
}

func (m appModel) contextMeterLine(width int) string {
	stats := m.contextStats()
	limit := maxInt(1, stats.LimitTokens)
	rawUsed := maxInt(0, stats.UsedTokens)
	rawCache := clampInt(stats.CacheTokens, 0, rawUsed)
	// 进度条：easeOutBack（有轻微超出感，720ms）
	animatedUsed, animatedCache, pulse := m.animatedContextTokens(limit)
	// 标签数字：easeOutCubic（无超出，400ms），呈现快速计数跳动效果
	labelUsed, labelCache := m.animatedLabelTokens(rawUsed, rawCache, limit)
	usedLabel := formatContextUsageLabel(labelUsed, labelCache, limit, m.isGenerating)
	freeLabel := formatContextFreeLabel(labelUsed, limit)
	return renderContextMeterLine(width, usedLabel, freeLabel, animatedUsed, animatedCache, limit, m.thinkingLabel(), pulse)
}

func formatContextUsageLabel(used, cache, limit int, isGenerating bool) string {
	arrow := "↑"
	if isGenerating {
		arrow = "↓"
	}
	parts := []string{formatCompactTokenCount(used) + arrow}
	parts = append(parts, fmt.Sprintf("%s(%s)", formatContextPercent(used, limit), formatContextPercent(cache, limit)))
	return strings.Join(parts, " ")
}

func formatContextFreeLabel(used, limit int) string {
	free := maxInt(0, limit-used)
	return fmt.Sprintf("free(%s)", formatContextPercent(free, limit))
}

func formatContextPercent(value, total int) string {
	return fmt.Sprintf("%.0f%%", percent(clampInt(value, 0, maxInt(0, total)), total))
}

func formatCompactTokenCount(value int) string {
	value = maxInt(0, value)
	if value <= 999 {
		return strconv.Itoa(value)
	}
	scaled := float64(value) / 1000
	unit := "k"
	if value > 999000 {
		scaled = float64(value) / 1000000
		unit = "M"
	}
	text := formatThreeDigitNumber(scaled)
	if unit == "k" && strings.HasPrefix(text, "1000") {
		text = formatThreeDigitNumber(float64(value) / 1000000)
		unit = "M"
	}
	return text + unit
}

func formatThreeDigitNumber(value float64) string {
	switch {
	case value >= 100:
		return fmt.Sprintf("%.0f", value)
	case value >= 10:
		return trimTrailingDecimalZeros(fmt.Sprintf("%.1f", value))
	default:
		return trimTrailingDecimalZeros(fmt.Sprintf("%.2f", value))
	}
}

func trimTrailingDecimalZeros(text string) string {
	text = strings.TrimRight(text, "0")
	return strings.TrimRight(text, ".")
}

func (m appModel) contextStats() loop.ContextStats {
	cfg := m.currentSettings()
	limit := cfg.UI.ContextLimitTokens
	if provider, ok := m.runner.(contextStatsProvider); ok {
		return provider.ContextStats(limit, m.input.Value())
	}
	return loop.ContextStats{LimitTokens: limit}
}

func (m appModel) currentSettings() settings.Config {
	if m.settingsConfig == nil {
		return settings.DefaultConfig()
	}
	return settings.Normalize(m.settingsConfig.CurrentSettings())
}

func (m appModel) thinkingLabel() string {
	if !m.isModelWorkRunning() || m.turnStartedAt.IsZero() {
		return ""
	}
	now := m.cursorFrameAt
	if now.IsZero() {
		now = time.Now()
	}
	elapsed := now.Sub(m.turnStartedAt)
	if elapsed < 0 {
		elapsed = 0
	}
	return fmt.Sprintf("<thinking %ds>", int(elapsed.Seconds()))
}

func contextBarWidth(width int, usedLabel, freeLabel string) int {
	width = maxInt(1, width)
	barWidth := width - lipgloss.Width(usedLabel) - lipgloss.Width(freeLabel) - 2
	if barWidth >= 8 {
		return barWidth
	}
	return maxInt(contextMeterMinimumBarCells, width-lipgloss.Width(compactContextLabel(usedLabel))-lipgloss.Width(compactContextLabel(freeLabel))-2)
}

func compactContextLabel(label string) string {
	if strings.HasPrefix(label, "free(") {
		return "free"
	}
	fields := strings.Fields(label)
	if len(fields) == 0 {
		return label
	}
	if len(fields) <= 2 {
		return fields[0]
	}
	return strings.Join(fields[:2], " ")
}

func renderContextMeterLine(width int, usedLabel, freeLabel string, used, cache, limit int, overlay string, pulse float64) string {
	width = maxInt(1, width)
	barWidth := contextBarWidth(width, usedLabel, freeLabel)
	if barWidth+lipgloss.Width(usedLabel)+lipgloss.Width(freeLabel)+2 > width {
		usedLabel = compactContextLabel(usedLabel)
		freeLabel = compactContextLabel(freeLabel)
	}
	usedText := contextUsedStyle.Render(usedLabel)
	freeText := contextFreeStyle.Render(freeLabel)
	barWidth = maxInt(contextMeterMinimumBarCells, width-lipgloss.Width(usedLabel)-lipgloss.Width(freeLabel)-2)
	barOffset := lipgloss.Width(usedLabel) + 1
	overlayStart := centeredOverlayStart(width, barOffset, barWidth, overlay)
	bar := renderContextBarWithOverlayStart(used, cache, limit, barWidth, overlay, overlayStart, pulse)
	line := usedText + " " + bar + " " + freeText
	if visible := lipgloss.Width(line); visible < width {
		line += strings.Repeat(" ", width-visible)
	}
	return line
}

func renderContextBar(used, cache, limit, width int, overlay string) string {
	overlayStart := -1
	if overlayWidth := lipgloss.Width(overlay); overlayWidth > 0 && overlayWidth <= width {
		overlayStart = (width - overlayWidth) / 2
	}
	return renderContextBarWithOverlayStart(used, cache, limit, width, overlay, overlayStart, 0)
}

func renderContextBarWithOverlayStart(used, cache, limit, width int, overlay string, overlayStart int, pulse float64) string {
	limit = maxInt(1, limit)
	width = maxInt(contextMeterMinimumBarCells, width)
	usedCells := int(math.Round(float64(clampInt(used, 0, limit)) / float64(limit) * float64(width)))
	cacheCells := int(math.Round(float64(clampInt(cache, 0, used)) / float64(limit) * float64(width)))
	if cacheCells > usedCells {
		cacheCells = usedCells
	}
	cells := make([]string, width)
	for i := 0; i < width; i++ {
		switch {
		case i < cacheCells:
			cells[i] = contextCacheStyle.Render("▰")
		case i < usedCells:
			cells[i] = contextUsedPulseStyle(pulse, i, usedCells).Render("▰")
		default:
			cells[i] = contextFreeStyle.Render("▱")
		}
	}
	overlayRunes := []rune(overlay)
	if len(overlayRunes) > 0 && overlayStart >= 0 && overlayStart+len(overlayRunes) <= width {
		for i, r := range overlayRunes {
			cells[overlayStart+i] = contextThinkingStyle.Render(string(r))
		}
	}
	return strings.Join(cells, "")
}

func (m *appModel) updateContextMeterAnimation() {
	now := m.animationNow()
	stats := m.contextStats()
	limit := maxInt(1, stats.LimitTokens)
	used := clampInt(maxInt(0, stats.UsedTokens), 0, limit)
	cache := clampInt(stats.CacheTokens, 0, used)
	if !m.contextMeter.initialized {
		m.contextMeter = contextMeterAnimation{
			initialized: true,
			startedAt:   now,
			fromUsed:    float64(used),
			fromCache:   float64(cache),
			targetUsed:  used,
			targetCache: cache,
		}
		return
	}
	if m.contextMeter.targetUsed == used && m.contextMeter.targetCache == cache {
		return
	}
	currentUsed, currentCache, _ := m.animatedContextTokens(limit)
	m.contextMeter.startedAt = now
	m.contextMeter.fromUsed = float64(currentUsed)
	m.contextMeter.fromCache = float64(currentCache)
	m.contextMeter.targetUsed = used
	m.contextMeter.targetCache = cache
}

func (m appModel) animatedContextTokens(limit int) (int, int, float64) {
	if !m.contextMeter.initialized {
		return m.contextMeter.targetUsed, m.contextMeter.targetCache, 0
	}
	now := m.animationNow()
	elapsed := now.Sub(m.contextMeter.startedAt)
	if elapsed < 0 {
		elapsed = 0
	}
	const duration = 720 * time.Millisecond
	progress := clamp01(float64(elapsed) / float64(duration))
	eased := easeOutBack(progress)
	animatedUsed := int(math.Round(lerp(m.contextMeter.fromUsed, float64(m.contextMeter.targetUsed), eased)))
	animatedCache := int(math.Round(lerp(m.contextMeter.fromCache, float64(m.contextMeter.targetCache), eased)))
	animatedUsed = clampInt(animatedUsed, 0, limit)
	animatedCache = clampInt(animatedCache, 0, animatedUsed)
	return animatedUsed, animatedCache, contextPulse(progress)
}

// animatedLabelTokens 用 easeOutCubic + 400ms 为数字标签计算动画值。
// 动画未初始化时回退到 rawUsed/rawCache（立即显示真实值）。
func (m appModel) animatedLabelTokens(rawUsed, rawCache, limit int) (used, cache int) {
	if !m.contextMeter.initialized {
		return clampInt(rawUsed, 0, limit), clampInt(rawCache, 0, clampInt(rawUsed, 0, limit))
	}
	now := m.animationNow()
	elapsed := now.Sub(m.contextMeter.startedAt)
	if elapsed < 0 {
		elapsed = 0
	}
	const labelDuration = 400 * time.Millisecond
	progress := clamp01(float64(elapsed) / float64(labelDuration))
	eased := easeOutCubic(progress)
	animUsed := int(math.Round(lerp(m.contextMeter.fromUsed, float64(m.contextMeter.targetUsed), eased)))
	animCache := int(math.Round(lerp(m.contextMeter.fromCache, float64(m.contextMeter.targetCache), eased)))
	animUsed = clampInt(animUsed, 0, limit)
	animCache = clampInt(animCache, 0, animUsed)
	return animUsed, animCache
}

func contextPulse(progress float64) float64 {
	if progress <= 0 || progress >= 1 {
		return 0
	}
	return math.Sin(progress * math.Pi)
}

func contextUsedPulseStyle(pulse float64, index, usedCells int) lipgloss.Style {
	if pulse <= 0 || usedCells <= 0 || index < usedCells-2 {
		return contextUsedStyle
	}
	return contextUsedStyle.Copy().Foreground(colorManager.LipglossColor(colorMarkdownHeading)).Bold(true)
}

func lerp(from, to, t float64) float64 {
	return from + (to-from)*t
}

func easeOutBack(t float64) float64 {
	t = clamp01(t)
	const c1 = 1.70158
	const c3 = c1 + 1
	x := t - 1
	return 1 + c3*x*x*x + c1*x*x
}

func centeredOverlayStart(lineWidth, barOffset, barWidth int, overlay string) int {
	overlayWidth := lipgloss.Width(overlay)
	if overlayWidth == 0 || overlayWidth > barWidth {
		return -1
	}
	start := (lineWidth-overlayWidth)/2 - barOffset
	if start < 0 || start+overlayWidth > barWidth {
		return (barWidth - overlayWidth) / 2
	}
	return start
}

func percent(value, total int) float64 {
	if total <= 0 {
		return 0
	}
	return float64(value) / float64(total) * 100
}

func clampInt(value, minValue, maxValue int) int {
	if value < minValue {
		return minValue
	}
	if value > maxValue {
		return maxValue
	}
	return value
}

// renderContextCardContent 为右侧 Context 卡片渲染多行内容。
// 输出四行：token数+箭头/free%, 进度条, used%/cache%/free%, turns
func (m appModel) renderContextCardContent(innerWidth int) string {
	stats := m.contextStats()
	limit := maxInt(1, stats.LimitTokens)
	rawUsed := maxInt(0, stats.UsedTokens)
	rawCache := clampInt(stats.CacheTokens, 0, rawUsed)
	labelUsed, labelCache := m.animatedLabelTokens(rawUsed, rawCache, limit)

	arrow := "↑"
	if m.isGenerating {
		arrow = "↓"
	}
	tokenStr := contextUsedStyle.Render(formatCompactTokenCount(labelUsed) + arrow)
	freeStr := contextFreeStyle.Render(formatContextFreeLabel(labelUsed, limit))

	// Line 1: token + free
	gap := maxInt(1, innerWidth-lipgloss.Width(tokenStr)-lipgloss.Width(freeStr))
	topLine := tokenStr + strings.Repeat(" ", gap) + freeStr

	// Line 2: progress bar
	animatedUsed, animatedCache, _ := m.animatedContextTokens(limit)
	bar := renderContextBar(animatedUsed, animatedCache, limit, innerWidth, "")

	// Line 3: percentages
	usedPct := contextUsedStyle.Render(formatContextPercent(labelUsed, limit))
	cachePct := contextCacheStyle.Render("cache " + formatContextPercent(labelCache, limit))
	freePct := contextFreeStyle.Render("free " + formatContextPercent(maxInt(0, limit-labelUsed), limit))
	pctLine := usedPct + " " + cachePct + " " + freePct

	// Line 4: turns (right-aligned)
	turnsStr := fmt.Sprintf("turns %d", m.turnsCount())
	turnsLine := lipgloss.NewStyle().
		Width(innerWidth).
		Foreground(lipgloss.Color("236")).
		Align(lipgloss.Right).
		Render(turnsStr)

	return strings.Join([]string{topLine, bar, pctLine, turnsLine}, "\n")
}
