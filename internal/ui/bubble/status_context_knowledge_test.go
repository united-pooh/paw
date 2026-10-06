package bubble

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"paw/internal/runtime/loop"
)

func TestDockContextCapacityRequiresKnownTotal(t *testing.T) {
	for _, test := range []struct {
		name  string
		stats loop.ContextStats
	}{
		{name: "no usage", stats: loop.ContextStats{LimitTokens: 1000}},
		{name: "empty snapshot", stats: loop.ContextStats{Known: true, LimitTokens: 1000}},
		{name: "only input", stats: loop.ContextStats{Known: true, InputKnown: true, UsedTokens: 100, InputTokens: 100, LimitTokens: 1000}},
		{name: "only output", stats: loop.ContextStats{Known: true, OutputKnown: true, UsedTokens: 100, OutputTokens: 100, LimitTokens: 1000}},
	} {
		t.Run(test.name, func(t *testing.T) {
			m := newTestModel(&fakeRunner{stats: test.stats})
			for _, width := range []int{1, 8, 20, 21, 60} {
				top := ansi.Strip(m.renderDockStatusLine(width))
				if terminalCellWidth(top) != width {
					t.Errorf("width %d: status width = %d", width, terminalCellWidth(top))
				}
				if strings.Contains(top, "%") || strings.Contains(top, tokenUsedGlyph) || strings.Contains(top, tokenCacheGlyph) || strings.Contains(top, tokenFreeGlyph) {
					t.Errorf("width %d: unknown capacity rendered as measured: %q", width, top)
				}
				if width >= 8 && !strings.Contains(top, "CTX —") {
					t.Errorf("width %d: unknown label absent: %q", width, top)
				}
			}
			if bottom := ansi.Strip(m.renderBottomDockUsageWithin(60)); bottom != "CTX —" {
				t.Errorf("unknown bottom capacity = %q, want CTX —", bottom)
			}
		})
	}
}

func TestDockContextCapacityAcceptsKnownZeroAndCompleteComponents(t *testing.T) {
	for _, used := range []int{0, 100} {
		m := newTestModel(&fakeRunner{stats: loop.ContextStats{Known: true, InputKnown: true, OutputKnown: true, UsedTokens: used, InputTokens: used, LimitTokens: 1000}})
		top := ansi.Strip(m.renderDockStatusLine(60))
		if !strings.Contains(top, "%") || !strings.Contains(top, tokenFreeGlyph) {
			t.Fatalf("complete input/output must retain capacity, got %q", top)
		}
	}
}
