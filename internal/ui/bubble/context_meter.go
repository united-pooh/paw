package bubble

import (
	"fmt"
	"github.com/charmbracelet/lipgloss"
	"math"
	"paw/internal/loop"
	"paw/internal/model"
	"paw/internal/settings"
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
	// 进度条：匀速线性（720ms）
	animatedUsed, animatedCache, phase := m.animatedContextTokens(limit)
	// 标签数字：easeOutCubic（无超出，400ms），呈现快速计数跳动效果
	labelUsed, labelCache := m.animatedLabelTokens(rawUsed, rawCache, limit)
	usedLabel := formatContextUsageLabel(labelUsed, labelCache, limit, m.isGenerating)
	freeLabel := formatContextFreeLabel(labelUsed, limit)
	return renderContextMeterLine(width, usedLabel, freeLabel, animatedUsed, animatedCache, limit, m.thinkingLabel(), phase)
}

func formatContextUsageLabel(used, cache, limit int, isGenerating bool) string {
	arrow := contextDirectionArrow(isGenerating)
	parts := []string{formatCompactTokenCount(used) + arrow}
	parts = append(parts, fmt.Sprintf("%s(%s)", formatContextPercent(used, limit), formatContextPercent(cache, limit)))
	return strings.Join(parts, " ")
}

func formatContextFreeLabel(used, limit int) string {
	free := maxInt(0, limit-used)
	return fmt.Sprintf("free(%s)", formatContextPercent(free, limit))
}

// contextDirectionArrow returns "↓" during generation, "↑" otherwise.
func contextDirectionArrow(isGenerating bool) string {
	if isGenerating {
		return "↓"
	}
	return "↑"
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
	limit := model.EffectiveContextLimitTokens(m.currentModelConfig())
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
	barWidth := width - terminalCellWidth(usedLabel) - terminalCellWidth(freeLabel) - 2
	if barWidth >= 8 {
		return barWidth
	}
	return maxInt(contextMeterMinimumBarCells, width-terminalCellWidth(compactContextLabel(usedLabel))-terminalCellWidth(compactContextLabel(freeLabel))-2)
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

func renderContextMeterLine(width int, usedLabel, freeLabel string, used, cache, limit int, overlay string, phase int) string {
	width = maxInt(1, width)
	barWidth := contextBarWidth(width, usedLabel, freeLabel)
	if barWidth+terminalCellWidth(usedLabel)+terminalCellWidth(freeLabel)+2 > width {
		usedLabel = compactContextLabel(usedLabel)
		freeLabel = compactContextLabel(freeLabel)
	}
	usedText := contextUsedStyle.Render(usedLabel)
	freeText := contextFreeStyle.Render(freeLabel)
	barWidth = maxInt(contextMeterMinimumBarCells, width-terminalCellWidth(usedLabel)-terminalCellWidth(freeLabel)-2)
	barOffset := terminalCellWidth(usedLabel) + 1
	overlayStart := centeredOverlayStart(width, barOffset, barWidth, overlay)
	bar := renderContextBarWithOverlayStart(used, cache, limit, barWidth, overlay, overlayStart, phase)
	line := usedText + " " + bar + " " + freeText
	if visible := terminalCellWidth(line); visible < width {
		line += strings.Repeat(" ", width-visible)
	}
	return line
}

func renderContextBar(used, cache, limit, width int, overlay string) string {
	overlayStart := -1
	if overlayWidth := terminalCellWidth(overlay); overlayWidth > 0 && overlayWidth <= width {
		overlayStart = (width - overlayWidth) / 2
	}
	return renderContextBarWithOverlayStart(used, cache, limit, width, overlay, overlayStart, -1)
}

func renderContextBarWithOverlayStart(used, cache, limit, width int, overlay string, overlayStart int, phase int) string {
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
			cells[i] = contextUsedFlowStyle(phase, i, usedCells).Render("▰")
		default:
			cells[i] = contextFreeStyle.Render("▱")
		}
	}
	overlayWidth := terminalCellWidth(overlay)
	if overlayWidth > 0 && overlayStart >= 0 && overlayStart+overlayWidth <= width {
		cells[overlayStart] = contextThinkingStyle.Render(overlay)
		for i := 1; i < overlayWidth; i++ {
			cells[overlayStart+i] = ""
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

func (m appModel) animatedContextTokens(limit int) (int, int, int) {
	if !m.contextMeter.initialized {
		return m.contextMeter.targetUsed, m.contextMeter.targetCache, -1
	}
	now := m.animationNow()
	elapsed := now.Sub(m.contextMeter.startedAt)
	if elapsed < 0 {
		elapsed = 0
	}
	const duration = 720 * time.Millisecond
	progress := clamp01(float64(elapsed) / float64(duration))
	eased := progress // 匀速线性（原 easeOutBack）
	animatedUsed := int(math.Round(lerp(m.contextMeter.fromUsed, float64(m.contextMeter.targetUsed), eased)))
	animatedCache := int(math.Round(lerp(m.contextMeter.fromCache, float64(m.contextMeter.targetCache), eased)))
	animatedUsed = clampInt(animatedUsed, 0, limit)
	animatedCache = clampInt(animatedCache, 0, animatedUsed)
	return animatedUsed, animatedCache, contextFlowPhase(elapsed, progress)
}

// contextFlowPhase 从时间推导流动相位（无状态）：动画期间返回随 elapsed
// 递增的整数相位（每 flowInterval 加 1，渲染端对 usedCells 取模形成循环），
// 动画结束或未进行时返回 -1（不流动）。
func contextFlowPhase(elapsed time.Duration, progress float64) int {
	if progress <= 0 || progress >= 1 {
		return -1
	}
	const flowInterval = 100 * time.Millisecond // 约 3 帧/格，可调
	return int(elapsed / flowInterval)
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

// contextFlowLength 是流动光带的固定长度（格）：前沿 1 格最亮，向后逐级
// 递减。used 区不足时自动截断为 usedCells。
const contextFlowLength = 14

// contextFlowTailAmount 是光带尾部颜色的插值位置（0 = heading 最亮，
// 1 = used 最暗）。尾部停在 0.65 而不是 1.0：让整条光带（含尾）肉眼可见、
// 与光带外的普通 used 格区分开——环形流动跨边界时"头"在右端、"尾"绕到
// 左端，头尾同时存在。
const contextFlowTailAmount = 0.65

// contextFlowBrightness 返回格子亮度级别 1..contextFlowLength（越大越亮，
// 前沿 = contextFlowLength），0 = 不在光带。光带绕 used 区环形右移：
// phase 每 +1 光带整体右移一格，形成传送带式流动感。
func contextFlowBrightness(phase, index, usedCells int) int {
	if phase < 0 || usedCells <= 0 || index < 0 || index >= usedCells {
		return 0
	}
	flowLen := minInt(contextFlowLength, usedCells)
	front := ((usedCells-1-phase)%usedCells + usedCells) % usedCells
	d := ((front-index)%usedCells + usedCells) % usedCells
	if d >= flowLen {
		return 0
	}
	return flowLen - d
}

// contextUsedFlowStyle 为流动光带内的格子生成纯颜色渐变的样式：
// 前沿用 markdown.heading 色（最亮），向后线性渐变到 contextFlowTailAmount
// 处的浅青色（尾部保持可见，与光带外普通 used 格有明确色差）；不使用加粗
// 等块状效果。
func contextUsedFlowStyle(phase, index, usedCells int) lipgloss.Style {
	level := contextFlowBrightness(phase, index, usedCells)
	if level <= 0 {
		return contextUsedStyle
	}
	flowLen := minInt(contextFlowLength, usedCells)
	amount := float64(flowLen-level) / float64(maxInt(flowLen-1, 1)) * contextFlowTailAmount
	hex := interpolateHexColor(colorManager.Hex(colorMarkdownHeading), colorManager.Hex(colorContextUsed), amount)
	return contextUsedStyle.Copy().Foreground(lipgloss.Color(hex))
}

func lerp(from, to, t float64) float64 {
	return from + (to-from)*t
}

func centeredOverlayStart(lineWidth, barOffset, barWidth int, overlay string) int {
	overlayWidth := terminalCellWidth(overlay)
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
// 输出 token 数、进度条，以及压缩后的 cache/free/turns 状态。
func (m appModel) renderContextCardContent(innerWidth int) string {
	stats := m.contextStats()
	limit := maxInt(1, stats.LimitTokens)
	rawUsed := maxInt(0, stats.UsedTokens)
	rawCache := clampInt(stats.CacheTokens, 0, rawUsed)
	labelUsed, labelCache := m.animatedLabelTokens(rawUsed, rawCache, limit)

	// Line 1: cache% left (hit rate = cache / used), token centre-left, free right.
	cacheLabel := "cache " + formatContextPercent(labelCache, labelUsed)
	rawToken := formatCompactTokenCount(labelUsed) + contextDirectionArrow(m.isGenerating)
	rawFree := formatContextFreeLabel(labelUsed, limit)
	cacheVW := terminalCellWidth(cacheLabel)
	tokenVW := terminalCellWidth(rawToken)
	freeVW := terminalCellWidth(rawFree)
	gap := maxInt(1, innerWidth-cacheVW-1-tokenVW-freeVW)
	topLine := contextCacheStyle.Render(cacheLabel) + " " + contextUsedStyle.Render(rawToken) + strings.Repeat(" ", gap) + contextFreeStyle.Render(rawFree)

	// Line 2: progress bar
	animatedUsed, animatedCache, _ := m.animatedContextTokens(limit)
	bar := renderContextBar(animatedUsed, animatedCache, limit, innerWidth, "")

	lines := []string{topLine, bar}
	if supplementLine := m.contextWorkStatusLine(innerWidth); supplementLine != "" {
		lines = append(lines, supplementLine)
	}

	return strings.Join(lines, "\n")
}

func centeredContextStatusLine(width int, parts ...string) string {
	return compactContextStatusLine(width, nonEmptyStrings(parts...))
}

func compactContextStatusLine(width int, options ...[]string) string {
	if width <= 0 {
		return ""
	}
	for _, parts := range options {
		visible := strings.Join(nonEmptyStrings(parts...), "  ")
		if terminalCellWidth(visible) <= width {
			return centeredContextStatusText(width, visible)
		}
	}
	if len(options) == 0 {
		return centeredContextStatusText(width, "")
	}
	fallback := strings.Join(nonEmptyStrings(options[len(options)-1]...), " ")
	return centeredContextStatusText(width, truncateStyledCells(fallback, width, ""))
}

func centeredContextStatusText(width int, visible string) string {
	return lipgloss.NewStyle().
		Width(width).
		Align(lipgloss.Center).
		Foreground(colorManager.LipglossColor(colorContextFree)).
		Render(visible)
}

func (m appModel) contextPercentStatusLine(width int, cachePct, freePct string) string {
	turns := m.turnsCount()
	return compactContextStatusLine(
		width,
		[]string{"cache " + cachePct, "free " + freePct, fmt.Sprintf("turns %d", turns)},
		[]string{"cache" + cachePct, "free" + freePct, fmt.Sprintf("turns%d", turns)},
		[]string{"c" + cachePct, "f" + freePct, fmt.Sprintf("t%d", turns)},
	)
}

func (m appModel) contextWorkStatusLine(width int) string {
	var full []string
	var compact []string
	var tiny []string
	if supplements := m.pendingSupplementCount(); supplements > 0 {
		full = append(full, fmt.Sprintf("supplements %d", supplements))
		compact = append(compact, fmt.Sprintf("supp %d", supplements))
		tiny = append(tiny, fmt.Sprintf("s%d", supplements))
	}
	if queued := m.chatQueue.Len(); queued > 0 {
		full = append(full, fmt.Sprintf("queued %d", queued))
		compact = append(compact, fmt.Sprintf("queue %d", queued))
		tiny = append(tiny, fmt.Sprintf("q%d", queued))
	}
	if len(full) == 0 {
		return ""
	}
	return compactContextStatusLine(width, full, compact, tiny)
}

func (m appModel) pendingSupplementCount() int {
	provider, ok := m.runner.(SupplementStatsProvider)
	if !ok {
		return 0
	}
	return provider.PendingSupplementCount()
}
