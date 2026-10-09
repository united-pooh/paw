package theme

import (
	"math"
	"strings"
	"testing"
)

// TestSelectionColorsMeetContrastBudget 验证每个主题的选区配色满足双对比度约束
// （docs/mouse-selection-research.md §4）：
//   - 选区文字 vs 选区背景 ≥ 4.2:1（WCAG AA 正文，深色主题按 4.5 设计）
//   - 选区背景 vs 正文背景：深色主题 ≥ 2:1，浅色主题（TokyoNightLight）≥ 1.5:1
//
// RGB 主题选区由 palette() 的 30% 混合公式推导；Default 使用终端反色。
func TestSelectionColorsMeetContrastBudget(t *testing.T) {
	const (
		minTextContrast     = 4.2
		minDarkDistinction  = 2.0
		minLightDistinction = 1.5
	)
	for _, item := range List() {
		if item.ID == Default {
			continue
		}
		bg := item.Colors.SelectionBackground
		fg := item.Colors.SelectionForeground
		terminalBG := item.Colors.TerminalBackground
		textRatio := contrastRatio(fg, bg)
		if textRatio < minTextContrast {
			t.Fatalf("theme %q selection text contrast = %.2f:1, want >= %.1f:1 (%s on %s)",
				item.ID, textRatio, minTextContrast, fg, bg)
		}
		distinction := contrastRatio(bg, terminalBG)
		minDistinction := minDarkDistinction
		if item.Mode == ModeLight {
			// 浅色主题 1.5:1 是浅色背景的现实上限（报告 §4.2/§4.3）。
			minDistinction = minLightDistinction
		}
		if distinction < minDistinction {
			t.Fatalf("theme %q selection-vs-background distinction = %.2f:1, want >= %.1f:1 (%s vs %s)",
				item.ID, distinction, minDistinction, bg, terminalBG)
		}
	}
}

// TestSelectionBlendFormulaMatchesResearchValues 固定调研报告 §4.3 的推荐色值，
// 防止混合公式或手工特例被意外改动。
func TestSelectionBlendFormulaMatchesResearchValues(t *testing.T) {
	expected := map[ThemeID]string{
		TokyoNight:      "#4c5064", // 30% 混合
		TokyoNightStorm: "#535973",
		CatppuccinMocha: "#535569",
		Dracula:         "#66686e",
		GruvboxDark:     "#635e51",
	}
	for id, want := range expected {
		item, ok := ByID(id)
		if !ok {
			t.Fatalf("theme %q missing", id)
		}
		if got := item.Colors.SelectionBackground; got != want {
			t.Fatalf("theme %q selection background = %q, want %q", id, got, want)
		}
	}
	// 浅色主题也用混合公式（报告的手工特例 #a5a7b4 与公式结果仅差 1-2 个单位）。
	light, _ := ByID(TokyoNightLight)
	if got := light.Colors.SelectionBackground; got != "#a6a8b5" {
		t.Fatalf("tokyo-night-light selection background = %q, want blend result #a6a8b5", got)
	}
}

func relativeLuminance(hex string) float64 {
	r, g, b := parseHexColor(strings.ToLower(hex))
	linear := func(channel int) float64 {
		c := float64(channel) / 255.0
		if c <= 0.03928 {
			return c / 12.92
		}
		return math.Pow((c+0.055)/1.055, 2.4)
	}
	return 0.2126*linear(r) + 0.7152*linear(g) + 0.0722*linear(b)
}

// contrastRatio 返回两个 hex 颜色之间的 WCAG 对比度（1..21）。
func contrastRatio(a, b string) float64 {
	la := relativeLuminance(a)
	lb := relativeLuminance(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}
