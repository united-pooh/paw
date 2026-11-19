package theme

import "testing"

func TestProseEmphasisAndQuotesRemainReadable(t *testing.T) {
	for _, item := range List() {
		if item.Mode == ModeTerminal {
			continue
		}
		for role, foreground := range map[string]string{
			"emphasis": item.Colors.MarkdownBold,
			"quote":    item.Colors.MarkdownQuoteText,
		} {
			if ratio := contrastRatio(foreground, item.Colors.TerminalBackground); ratio < 4.5 {
				t.Errorf("%s %s contrast %.2f:1, want >= 4.5:1", item.ID, role, ratio)
			}
		}
	}
}

func TestSurfaceTiersStepAwayFromBackground(t *testing.T) {
	for _, item := range List() {
		if item.Mode == ModeTerminal {
			continue
		}
		p := item.Colors
		bg := p.TerminalBackground
		tiers := []struct{ name, color string }{
			{"surface", p.Surface}, {"raised", p.Raised}, {"overlay", p.Overlay},
		}
		prev := 1.0
		for _, tier := range tiers {
			ratio := contrastRatio(tier.color, bg)
			if ratio <= prev {
				t.Errorf("%s %s contrast vs bg %.3f:1, want > previous tier %.3f:1", item.ID, tier.name, ratio, prev)
			}
			prev = ratio
		}
		if contrastRatio(p.BorderStrong, bg) <= contrastRatio(p.BorderSubtle, bg) {
			t.Errorf("%s border.strong must stand out more than border.subtle", item.ID)
		}
		if p.HeaderBackground != p.Surface || p.ToolDetailBackground != p.Raised || p.MarkdownCodeBackground != p.Raised || p.PanelBorder != p.BorderSubtle {
			t.Errorf("%s legacy roles must draw from surface tiers", item.ID)
		}
	}
}

func TestTextTiersRemainReadableOnSurfaces(t *testing.T) {
	for _, item := range List() {
		if item.Mode == ModeTerminal {
			continue
		}
		p := item.Colors
		for _, text := range []struct {
			name, color string
			min         float64
		}{
			{"text.primary", p.Body, 4.5},
			{"text.secondary", p.TextSecondary, 4.5},
			{"text.muted", p.TextMuted, 3},
		} {
			for surfaceName, surface := range map[string]string{"bg": p.TerminalBackground, "surface": p.Surface} {
				if ratio := contrastRatio(text.color, surface); ratio < text.min {
					t.Errorf("%s %s on %s contrast %.2f:1, want >= %.1f:1", item.ID, text.name, surfaceName, ratio, text.min)
				}
			}
		}
		if contrastRatio(p.TextDisabled, p.TerminalBackground) >= contrastRatio(p.TextMuted, p.TerminalBackground) {
			t.Errorf("%s text.disabled must be dimmer than text.muted", item.ID)
		}
	}
}
