package bubble

import (
	"fmt"
	"gocode/internal/loop"
	"gocode/internal/settings"
	"math"
	"strings"
)

const contextMeterCells = 40

func (m appModel) contextMeterTitle() string {
	stats := m.contextStats()
	limit := maxInt(1, stats.LimitTokens)
	used := clampInt(stats.UsedTokens, 0, limit)
	cache := clampInt(stats.CacheTokens, 0, used)
	usedPercent := percent(used, limit)
	cachePercent := percent(cache, limit)
	freePercent := maxInt(0, 100-int(math.Round(usedPercent)))
	return fmt.Sprintf("%d%%(%.1f%%) %s %d%%", int(math.Round(usedPercent)), cachePercent, renderContextBar(used, cache, limit), freePercent)
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

func renderContextBar(used, cache, limit int) string {
	limit = maxInt(1, limit)
	usedCells := int(math.Round(float64(clampInt(used, 0, limit)) / float64(limit) * contextMeterCells))
	cacheCells := int(math.Round(float64(clampInt(cache, 0, used)) / float64(limit) * contextMeterCells))
	if cacheCells > usedCells {
		cacheCells = usedCells
	}
	var out strings.Builder
	for i := 0; i < contextMeterCells; i++ {
		switch {
		case i < cacheCells:
			out.WriteString(contextCacheStyle.Render("▰"))
		case i < usedCells:
			out.WriteString(contextUsedStyle.Render("▰"))
		default:
			out.WriteString(contextFreeStyle.Render("▱"))
		}
	}
	return out.String()
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

