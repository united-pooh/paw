package bubble

import (
	"fmt"
	"paw/internal/loop"
	"paw/internal/model"
	"paw/internal/settings"
	"strconv"
	"strings"
)

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

func clampInt(value, minValue, maxValue int) int {
	if value < minValue {
		return minValue
	}
	if value > maxValue {
		return maxValue
	}
	return value
}
