package bubble

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"paw/internal/runtime/loop"
)

func TestContextDockReadoutSharesModeRowAndCentersAcrossFullTrack(t *testing.T) {
	m := newTestModel(&fakeRunner{stats: loop.ContextStats{Known: true, TotalKnown: true, UsedTokens: 244000, LimitTokens: 1048576}})
	m.worktree = worktreeSnapshot{name: "paw", ref: "dev", isGit: true}
	for _, width := range []int{80, 120, 200} {
		line := ansi.Strip(m.renderBottomDockLine(width))
		readout := "23% · 244k / 1.05M"
		at := strings.Index(line, readout)
		if at < 0 || !strings.Contains(line, copyModeChat+" · "+copyContextLabel) {
			t.Fatalf("width %d missing inline context: %q", width, line)
		}
		left := terminalCellWidth(line[:at])
		if delta := 2*left + terminalCellWidth(readout) - width; delta < -1 || delta > 1 {
			t.Fatalf("width %d readout not centered: %q", width, line)
		}
		if !strings.Contains(line, "  "+readout+"  ") || strings.Count(line, tokenUsedGlyph)+strings.Count(line, tokenFreeGlyph) < width-50 {
			t.Fatalf("width %d track does not fill available space or touches text: %q", width, line)
		}
		if terminalCellWidth(line) != width {
			t.Fatalf("width %d line overflows: %q", width, line)
		}
	}
	if top := ansi.Strip(m.renderDockStatusLine(80)); strings.Contains(top, copyContextLabel) || strings.Contains(top, "%") {
		t.Fatalf("input separator duplicates context: %q", top)
	}
}

func TestContextDockKeepsCopyFeedbackAtNarrowWidth(t *testing.T) {
	m := newTestModel(&fakeRunner{})
	m.cursorFrameAt = time.Unix(1, 0)
	m.copyToast, m.copyToastUntil = "已复制", time.Unix(2, 0)
	if line := ansi.Strip(m.renderBottomDockLine(40)); !strings.Contains(line, "已复制") || terminalCellWidth(line) != 40 {
		t.Fatalf("copy feedback lost or overlaps at narrow width: %q", line)
	}
	m.cursorFrameAt = time.Unix(3, 0)
	if line := ansi.Strip(m.renderBottomDockLine(40)); strings.Contains(line, "已复制") || !strings.Contains(line, "—") {
		t.Fatalf("copy feedback did not restore context: %q", line)
	}
}

func TestContextDockNearLimitNeverRoundsToFullOrZero(t *testing.T) {
	for _, tc := range []struct {
		used            int
		percent, counts string
	}{
		{0, "0%", "0 / 1.05M"},
		{1, "<1%", "1 / 1.05M"},
		{1047500, "99.9%", "1,047,500 / 1,048,576"},
		{1048575, "99.9%", "1,048,575 / 1,048,576"},
		{1048576, "100%", "1.05M / 1.05M"},
		{1048577, "100.1%", "1,048,577 / 1,048,576"},
		{1174405, "112%", "1.17M / 1.05M"},
	} {
		m := newTestModel(&fakeRunner{stats: loop.ContextStats{Known: true, TotalKnown: true, UsedTokens: tc.used, LimitTokens: 1048576}})
		for _, width := range []int{40, 80, 120} {
			line := ansi.Strip(m.renderBottomDockLine(width))
			if terminalCellWidth(line) != width || !strings.Contains(line, tc.percent) || width >= 80 && !strings.Contains(line, tc.counts) {
				t.Errorf("used %d width %d: %q; want %s, %s", tc.used, width, line, tc.percent, tc.counts)
			}
		}
	}
}

func TestContextDockEstimatedAndUnknownUsageRemainDistinct(t *testing.T) {
	stats := loop.ContextStats{Known: true, TotalKnown: true, Estimated: true, UsedTokens: 244000, LimitTokens: 1048576}
	runner := &fakeRunner{stats: stats}
	m := newTestModel(runner)
	if line := ansi.Strip(m.renderBottomDockLine(80)); !strings.Contains(line, "≈23%") {
		t.Fatalf("estimated usage is presented as measured: %q", line)
	}
	monitor := ansi.Strip(strings.Join(tokenMonitorContext(tokenMonitorView{Context: stats}, 48, 6), "\n"))
	if !strings.Contains(monitor, "≈23.3%") || !strings.Contains(monitor, "Estimated from active history") {
		t.Fatalf("monitor lost estimate source: %q", monitor)
	}
	runner.stats = loop.ContextStats{LimitTokens: 1048576}
	if line := ansi.Strip(m.renderBottomDockLine(80)); !strings.Contains(line, "—") || strings.Contains(line, "%") || strings.Contains(line, tokenUsedGlyph) {
		t.Fatalf("unknown usage is presented as measured: %q", line)
	}
}

func TestContextDockCellBudgetAcrossModesAndCapacityStates(t *testing.T) {
	for _, mode := range []string{"chat", "goal", "plan", "shell"} {
		for _, used := range []int{0, 1, 244000, 1048575, 1048576, 1174405} {
			m := newTestModel(&fakeRunner{stats: loop.ContextStats{Known: true, TotalKnown: true, UsedTokens: used, LimitTokens: 1048576}})
			m.goalMode, m.planMode = mode == "goal", mode == "plan"
			if mode == "shell" {
				m.input.SetValue("!pwd")
			}
			m.worktree = worktreeSnapshot{name: "项目中文名称很长", ref: "功能/长分支名称", isGit: true}
			for width := 1; width <= 200; width++ {
				line := m.renderBottomDockLine(width)
				if terminalCellWidth(line) != width || strings.Contains(line, "\n") {
					t.Fatalf("%s used %d width %d overflows: %q", mode, used, width, ansi.Strip(line))
				}
			}
		}
	}
}
