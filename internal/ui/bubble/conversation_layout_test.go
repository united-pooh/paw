package bubble

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"paw/internal/runtime/loop"
	"paw/internal/ui/bubble/viewportx"
	"paw/internal/ui/theme"
)

func TestToolRowLeadsWithStatusGlyph(t *testing.T) {
	started := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		status string
		want   string
	}{
		{"ok", glyphSuccess},
		{"error", glyphFailure},
		{"running", spinnerFrames[0]},
	} {
		entry := transcriptEntry{kind: entryTool, toolName: "Read", toolStatus: tc.status, toolTarget: "a.go",
			toolStartedAt: started, toolFinishedAt: started.Add(200 * time.Millisecond)}
		got := ansi.Strip(renderCompactToolSummary(entry, 60, started))
		if !strings.HasPrefix(got, tc.want+" ") {
			t.Errorf("%s row = %q, want leading %q", tc.status, got, tc.want)
		}
	}
}

func TestRunningToolSpinnerAdvancesEveryHundredMilliseconds(t *testing.T) {
	started := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	entry := transcriptEntry{kind: entryTool, toolName: "Bash", toolStatus: "running", toolTarget: "go test", toolStartedAt: started}
	for i := 0; i < len(spinnerFrames)+1; i++ {
		at := started.Add(time.Duration(i) * 100 * time.Millisecond)
		got := ansi.Strip(renderCompactToolSummary(entry, 60, at))
		want := spinnerFrames[i%len(spinnerFrames)]
		if !strings.HasPrefix(got, want+" ") {
			t.Fatalf("frame %d row = %q, want spinner %q", i, got, want)
		}
	}
	a := transcriptRenderKey(entry, 60, started, false)
	b := transcriptRenderKey(entry, 60, started.Add(100*time.Millisecond), false)
	if a == b {
		t.Fatal("render cache key must change with each spinner frame")
	}
}

func TestToolRowRightAlignsStatusAndDuration(t *testing.T) {
	started := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		status, suffix string
	}{
		{"ok", "200ms"},
		{"error", copyToolStatusError + " 200ms"},
		{"running", copyToolStatusRunning + " 3s"},
	} {
		entry := transcriptEntry{kind: entryTool, toolName: "Read", toolStatus: tc.status, toolTarget: "a.go",
			toolStartedAt: started, toolFinishedAt: started.Add(200 * time.Millisecond)}
		got := ansi.Strip(renderCompactToolSummary(entry, 50, started.Add(3*time.Second)))
		if terminalCellWidth(got) != 50 || !strings.HasSuffix(got, tc.suffix) {
			t.Errorf("%s row = %q (width %d), want width 50 ending %q", tc.status, got, terminalCellWidth(got), tc.suffix)
		}
	}
}

func TestRunningToolRefreshesOnSpinnerTicks(t *testing.T) {
	vp := viewportx.New(100, 20)
	vp.Width = 100
	base := time.Now()
	m := appModel{viewport: vp, transcript: []transcriptEntry{{
		kind: entryTool, title: "tool", toolName: "bash", toolStatus: "running", body: "bash running", toolStartedAt: base,
	}}}
	m.cursorFrameAt = base
	_ = m.renderTranscriptContent()
	m.refreshRunningToolProgress(base.Add(150 * time.Millisecond))
	versionBefore := m.transcript[0].version
	m.refreshRunningToolProgress(base.Add(250 * time.Millisecond))
	if m.transcript[0].version == versionBefore {
		t.Fatal("next spinner tick must touch the running tool entry")
	}
	versionBefore = m.transcript[0].version
	m.refreshRunningToolProgress(base.Add(290 * time.Millisecond))
	if m.transcript[0].version != versionBefore {
		t.Fatal("same spinner tick must not touch the entry again")
	}
}

func TestUserBlockShowsAccentBarOnSurface(t *testing.T) {
	newRGBTestModel(t)
	setTrueColorForTest(t)
	got := renderEntry(transcriptEntry{kind: entryUser, body: "第一行\n第二行"}, 40)
	for i, line := range strings.Split(got, "\n") {
		if plain := ansi.Strip(line); !strings.HasPrefix(plain, glyphUserBar+" ") {
			t.Fatalf("line %d = %q, want leading user bar", i, plain)
		}
	}
	if !strings.Contains(got, backgroundSGR(colorManager.Hex(colorSurfaceBackground))) {
		t.Fatalf("user block must paint the surface tier: %q", got)
	}
}

func TestUserBarIsNotCopied(t *testing.T) {
	lines := buildTranscriptLineSnapshotsFromLines([]string{glyphUserBar + " 你好"})
	if lines[0].plain != transcriptEntryGutter+"你好" {
		t.Fatalf("plain = %q, want bar replaced by gutter", lines[0].plain)
	}
}

func TestHeaderBarPaintsSurfaceWithModelMark(t *testing.T) {
	model := newRGBTestModel(t)
	setTrueColorForTest(t)
	next, _ := model.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	model = next.(appModel)
	top := model.renderTopLine(model.currentLayout())
	plain := ansi.Strip(top)
	if !strings.HasPrefix(plain, " "+glyphModel+" ") || strings.Contains(plain, "─") {
		t.Fatalf("top line = %q, want surface status bar led by model mark", plain)
	}
	if terminalCellWidth(plain) != model.currentLayout().frameWidth {
		t.Fatalf("top line width = %d, want %d", terminalCellWidth(plain), model.currentLayout().frameWidth)
	}
	if !strings.Contains(top, backgroundSGR(colorManager.Hex(colorSurfaceBackground))) {
		t.Fatalf("top line must paint the surface tier: %q", top)
	}
}

func TestTerminalThemeKeepsHairlineHeader(t *testing.T) {
	model := newThemedTestModel(t, theme.Default)
	next, _ := model.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	model = next.(appModel)
	if plain := ansi.Strip(model.renderTopLine(model.currentLayout())); !strings.Contains(plain, "─") {
		t.Fatalf("terminal theme top line = %q, want hairline without surface", plain)
	}
}

func TestEditRowShowsDiffTotals(t *testing.T) {
	started := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	entry := transcriptEntry{kind: entryTool, title: "tool", toolName: "Edit", toolStatus: "ok", toolTarget: "a.go",
		body: "Edit  ok  +12 -3", toolStartedAt: started, toolFinishedAt: started.Add(time.Second)}
	if got := ansi.Strip(renderCompactToolSummary(entry, 60, started)); !strings.Contains(got, "a.go +12 −3") {
		t.Fatalf("edit row = %q, want diff totals after target", got)
	}
	if got := ansi.Strip(renderCompactToolSummary(entry, 16, started)); strings.Contains(got, "+12") {
		t.Fatalf("narrow edit row = %q, diff totals must yield to target", got)
	}
}

func TestContextReadoutShowsCacheHitRate(t *testing.T) {
	model := newTestModel(&fakeRunner{stats: loop.ContextStats{Known: true, TotalKnown: true, CacheKnown: true, InputKnown: true,
		UsedTokens: 45000, InputTokens: 40000, CacheTokens: 30000, LimitTokens: 100000}})
	if wide := ansi.Strip(model.renderBottomDockLine(100)); !strings.Contains(wide, "45k / 100k · 缓存 75%") {
		t.Fatalf("context readout = %q, want cache hit rate", wide)
	}
}
