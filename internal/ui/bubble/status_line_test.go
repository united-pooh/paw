package bubble

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"paw/internal/loop"
)

func TestDockBorderLayoutUsesFullProgressTopAndAnchoredBottomMetadata(t *testing.T) {
	model := newTestModel(&fakeRunner{stats: loop.ContextStats{UsedTokens: 12000, LimitTokens: 128000}})
	model.cursorFrameAt = time.Unix(0, 0)

	top := ansi.Strip(model.renderDockStatusLine(80))
	if got := terminalCellWidth(top); got != 80 {
		t.Fatalf("top context bar width = %d, want 80: %q", got, top)
	}
	if strings.Trim(top, tokenFreeGlyph+tokenCacheGlyph+tokenUsedGlyph) != "" {
		t.Fatalf("top context bar = %q, want only context progress glyphs", top)
	}

	bottom := ansi.Strip(model.renderBottomDockLine(80))
	modeAt := strings.Index(bottom, "chat")
	usageAt := strings.Index(bottom, "12k / 128k")
	if modeAt < 0 || modeAt > 3 {
		t.Fatalf("bottom border = %q, want chat anchored near the left edge", bottom)
	}
	if usageAt < 80-terminalCellWidth("12k / 128k")-3 {
		t.Fatalf("bottom border = %q, want token usage anchored near the right edge", bottom)
	}
}

func TestStatusLineExactWidthAcrossWidths(t *testing.T) {
	model := newTestModel(&fakeRunner{stats: loop.ContextStats{UsedTokens: 45000, LimitTokens: 100000}})
	model.cursorFrameAt = time.Date(2026, 7, 28, 15, 42, 0, 0, time.UTC)
	for _, width := range []int{8, 12, 20, 32, 60, 80, 100} {
		line := model.renderDockStatusLine(width)
		if got := terminalCellWidth(line); got != width {
			t.Fatalf("width=%d status cell width=%d line=%q", width, got, ansi.Strip(line))
		}
	}
}

func TestBottomDockModeIndicator(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.cursorFrameAt = time.Now()
	if bottom := ansi.Strip(model.renderBottomDockLine(80)); !strings.Contains(bottom, "chat") {
		t.Fatalf("default mode = %q, want chat", bottom)
	}
	model.input.SetValue("!ls")
	if bottom := ansi.Strip(model.renderBottomDockLine(80)); !strings.Contains(bottom, "!shell") {
		t.Fatalf("bang mode = %q, want !shell", bottom)
	}
}

func TestStatusLineShowsTokenCountWithoutStatusWord(t *testing.T) {
	model := newTestModel(&fakeRunner{stats: loop.ContextStats{UsedTokens: 12000, LimitTokens: 128000}})
	model.cursorFrameAt = time.Unix(0, 0)
	bottom := ansi.Strip(model.renderBottomDockLine(100))
	if !strings.Contains(bottom, "12k / 128k") {
		t.Fatalf("bottom border = %q, want token count", bottom)
	}
	for _, unwanted := range []string{"ready", "working", "generating"} {
		if strings.Contains(bottom, unwanted) {
			t.Fatalf("bottom border = %q, should not contain %q", bottom, unwanted)
		}
	}
	if strings.Contains(bottom, "cache") || strings.Contains(bottom, "free") {
		t.Fatalf("bottom border still contains retired telemetry: %q", bottom)
	}
}

func TestGoalInputUsesModeIndicatorWithoutStatusOrPurpleBody(t *testing.T) {
	model := newTestModel(&fakeRunner{stats: loop.ContextStats{UsedTokens: 12000, LimitTokens: 128000}})
	model.cursorFrameAt = time.Unix(0, 0)
	model.goalMode = true
	model.input.SetWidth(40)
	model.input.SetHeight(1)
	model.input.SetValue("finish the migration")
	model.input.CursorEnd()

	goalContent := model.renderInputContentWithHints(40, 1)
	model.goalMode = false
	chatContent := model.renderInputContentWithHints(40, 1)
	if goalContent != chatContent {
		t.Fatalf("goal input body styling differs from chat body; goal=%q chat=%q", goalContent, chatContent)
	}

	model.goalMode = true
	bottom := ansi.Strip(model.renderBottomDockLine(100))
	if !strings.Contains(bottom, "goal") {
		t.Fatalf("goal mode indicator missing from bottom border: %q", bottom)
	}
	for _, unwanted := range []string{"ready", "working", "generating"} {
		if strings.Contains(bottom, unwanted) {
			t.Fatalf("goal bottom border contains %q: %q", unwanted, bottom)
		}
	}
	if strings.Contains(ansi.Strip(goalContent), "goal") {
		t.Fatalf("goal leaked into input body: %q", ansi.Strip(goalContent))
	}
}

func TestTokenFrontierRippleStartsAtUsedProgressAndMoves(t *testing.T) {
	model := newTestModel(&fakeRunner{stats: loop.ContextStats{UsedTokens: 40000, LimitTokens: 100000}})
	model.isGenerating = true
	model.width = 100
	model.cursorFrameAt = time.Unix(0, 0)
	first := ansi.Strip(model.renderTokenFrontier(100, 40000, 0, 100000))
	model.cursorFrameAt = time.Unix(0, int64(tokenRippleSpeed))
	second := ansi.Strip(model.renderTokenFrontier(100, 40000, 0, 100000))
	if first == second {
		t.Fatalf("ripple did not move: %q", first)
	}
	if !strings.Contains(string([]rune(first)[40:]), "█") {
		t.Fatalf("ripple lacks █ glyph in free span: %q", first)
	}
}

func TestTokenFrontierRippleSurvivesTurnCompletion(t *testing.T) {
	model := newTestModel(&fakeRunner{stats: loop.ContextStats{UsedTokens: 40000, LimitTokens: 100000}})
	model.width = 100
	if !model.queryGuard.StartModel() {
		t.Fatal("StartModel failed")
	}
	model.syncRunningFlags()
	model.isGenerating = true
	model.cursorFrameAt = time.Unix(0, 0)

	next, _ := model.Update(turnFinishedMsg{})
	model = next.(appModel)
	if model.isAgentWorking() {
		t.Fatal("model should be idle after turn completion")
	}

	// 退场进行中：波纹仍在 free 区继续右移（不立刻消失）。
	model.cursorFrameAt = time.Unix(0, int64(2*tokenRippleSpeed))
	exiting := ansi.Strip(model.renderTokenFrontier(100, 40000, 0, 100000))
	if !strings.Contains(string([]rune(exiting)[40:]), "█") {
		t.Fatalf("completed-turn ripple disappeared during exit: %q", exiting)
	}

	// 退场结束（越过右边界 + tail）：free 区无波纹。
	model.cursorFrameAt = time.Unix(0, int64((100+tokenRippleTail)*tokenRippleSpeed))
	settled := ansi.Strip(model.renderTokenFrontier(100, 40000, 0, 100000))
	if strings.Contains(string([]rune(settled)[40:]), "█") {
		t.Fatalf("completed-turn ripple remained after exit: %q", settled)
	}
}

func TestDockLinesHaveNoStatusWordDuringToolCall(t *testing.T) {
	model := newTestModel(&fakeRunner{stats: loop.ContextStats{UsedTokens: 45000, LimitTokens: 100000}})
	if !model.queryGuard.StartModel() {
		t.Fatal("StartModel failed")
	}
	model.syncRunningFlags()
	top := ansi.Strip(model.renderDockStatusLine(80))
	bottom := ansi.Strip(model.renderBottomDockLine(80))
	for _, unwanted := range []string{"ready", "working", "generating"} {
		if strings.Contains(top, unwanted) || strings.Contains(bottom, unwanted) {
			t.Fatalf("tool-call dock should not contain %q: top=%q bottom=%q", unwanted, top, bottom)
		}
	}
	if !strings.Contains(bottom, "chat") {
		t.Fatalf("tool-call bottom border = %q, want chat mode indicator", bottom)
	}
}

func TestStartTokenRippleExitRecordsStartTime(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	now := time.Unix(0, 0).Add(1250 * time.Millisecond)
	model.startTokenRippleExit(now)
	if !model.tokenRippleExitAt.Equal(now) {
		t.Fatalf("exitAt = %v, want %v", model.tokenRippleExitAt, now)
	}
}

func TestTokenRippleExitRemainsActiveUntilTailCompletes(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.width = 100
	now := time.Unix(0, 0)
	model.startTokenRippleExit(now)

	if !model.tokenRippleActive(now.Add(time.Duration(2) * tokenRippleSpeed)) {
		t.Fatal("ripple should remain active while exiting")
	}
	deadline := now.Add(time.Duration(100+tokenRippleTail) * tokenRippleSpeed)
	if model.tokenRippleActive(deadline) {
		t.Fatal("ripple should stop when the full tail has exited")
	}
}

func TestBottomDockShowsCacheHitRatioWhenCachePresent(t *testing.T) {
	model := newTestModel(&fakeRunner{stats: loop.ContextStats{UsedTokens: 12000, CacheTokens: 11000, LimitTokens: 128000}})
	model.cursorFrameAt = time.Unix(0, 0)
	bottom := ansi.Strip(model.renderBottomDockLine(100))
	if !strings.Contains(bottom, "12k / 128k") {
		t.Fatalf("bottom border = %q, want token count", bottom)
	}
	if !strings.Contains(bottom, "ⓒ91%") {
		t.Fatalf("bottom border = %q, want cache hit ratio ⓒ91%%", bottom)
	}
}

func TestBottomDockOmitsCacheHitRatioWhenZero(t *testing.T) {
	model := newTestModel(&fakeRunner{stats: loop.ContextStats{UsedTokens: 12000, CacheTokens: 0, LimitTokens: 128000}})
	model.cursorFrameAt = time.Unix(0, 0)
	bottom := ansi.Strip(model.renderBottomDockLine(100))
	if strings.Contains(bottom, "ⓒ") {
		t.Fatalf("bottom border = %q, must not show cache ratio when cache is zero", bottom)
	}
}
