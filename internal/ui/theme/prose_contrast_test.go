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
