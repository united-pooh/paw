package bubble

import (
	"fmt"
	"math"
	"paw/internal/capability/model"
	"paw/internal/platform/settings"
	"paw/internal/runtime/loop"
	"strconv"
	"strings"
)

func contextCapacityKnown(stats loop.ContextStats) bool {
	return stats.Known && stats.LimitTokens > 0 && (stats.TotalKnown || stats.InputKnown && stats.OutputKnown)
}

// contextReadout 返回容量百分比和用量，保留未知、估算与临界值的区别。
func contextReadout(stats loop.ContextStats, decimals int) (string, string) {
	limitText := "—"
	if stats.LimitTokens > 0 {
		limitText = formatCompactTokenCount(stats.LimitTokens)
	}
	if !contextCapacityKnown(stats) {
		return "—", "— / " + limitText
	}
	used, limit := maxInt(0, stats.UsedTokens), stats.LimitTokens
	ratio := float64(used) * 100 / float64(limit)
	percent := fmt.Sprintf("%.*f%%", decimals, ratio)
	switch {
	case used > 0 && ratio < math.Pow10(-decimals):
		percent = fmt.Sprintf("<%.*f%%", decimals, math.Pow10(-decimals))
	case used < limit && ratio > 99:
		percent = fmt.Sprintf("%.1f%%", math.Min(99.9, math.Round(ratio*10)/10))
	case used > limit && ratio < 101:
		percent = fmt.Sprintf("%.1f%%", math.Max(100.1, math.Round(ratio*10)/10))
	}
	if stats.Estimated {
		percent = "≈" + percent
	}
	usedText := formatCompactTokenCount(used)
	// 两个不同的整数不能缩写成看似相等的容量。
	if used != limit && usedText == limitText {
		usedText, limitText = formatExactTokenCount(used), formatExactTokenCount(limit)
	}
	return percent, usedText + " / " + limitText
}

func formatExactTokenCount(value int) string {
	text := strconv.Itoa(maxInt(0, value))
	for at := len(text) - 3; at > 0; at -= 3 {
		text = text[:at] + "," + text[at:]
	}
	return text
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

func (m appModel) effectiveContextLimit(cfg model.Config) int {
	return model.ResolveContextLimitTokens(cfg, m.currentSettings().UI.ContextLimitTokens)
}

func (m appModel) contextStats() loop.ContextStats {
	limit := m.effectiveContextLimit(m.currentModelConfig())
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

func clampInt(value, minValue, maxValue int) int {
	if value < minValue {
		return minValue
	}
	if value > maxValue {
		return maxValue
	}
	return value
}
