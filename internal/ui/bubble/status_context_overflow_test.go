package bubble

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"paw/internal/runtime/loop"
)

func TestDockContextOverflowPreservesReportedCounts(t *testing.T) {
	stats := loop.ContextStats{Known: true, TotalKnown: true, InputKnown: true, OutputKnown: true, CacheKnown: true,
		UsedTokens: 370796, InputTokens: 370619, OutputTokens: 177, CacheTokens: 369792, LimitTokens: 256000}
	m := newTestModel(&fakeRunner{stats: stats})
	for _, width := range []int{21, 32, 60, 80} {
		top := ansi.Strip(m.renderDockStatusLine(width))
		if terminalCellWidth(top) != width || !strings.HasPrefix(top, "CTX 145% ") {
			t.Errorf("width %d clipped overflow: %q", width, top)
		}
		if width >= 60 && !strings.Contains(top, "371k / 256k") {
			t.Errorf("width %d lost raw usage: %q", width, top)
		}
	}
	bottom := ansi.Strip(m.renderBottomDockUsageWithin(80))
	for _, want := range []string{"上下文 145%", "371k/256k", "缓存占比 99%"} {
		if !strings.Contains(bottom, want) {
			t.Errorf("overflow bottom missing %q: %s", want, bottom)
		}
	}
	context := ansi.Strip(strings.Join(tokenMonitorContext(tokenMonitorView{Context: stats}, 32, 6), "\n"))
	if !strings.Contains(context, "144.8%") || !strings.Contains(context, "371k / 256k") {
		t.Errorf("monitor clipped raw capacity: %s", context)
	}
}
