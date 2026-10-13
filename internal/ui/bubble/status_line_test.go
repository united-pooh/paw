package bubble

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"paw/internal/runtime/loop"
)

func TestDockLayoutUsesContextProgressAndLabeledBottomMetadata(t *testing.T) {
	model := newTestModel(&fakeRunner{stats: loop.ContextStats{Known: true, TotalKnown: true, CacheKnown: true, UsedTokens: 12000, LimitTokens: 128000}})
	model.cursorFrameAt = time.Unix(0, 0)
	model.worktree = worktreeSnapshot{name: "paw", ref: "dev", state: worktreeDirty, isGit: true}

	top := ansi.Strip(model.renderDockStatusLine(80))
	if got := terminalCellWidth(top); got != 80 {
		t.Fatalf("top context bar width = %d, want 80: %q", got, top)
	}
	if !strings.HasPrefix(top, copyContextLabel+" 9% ") || !strings.HasSuffix(top, " 12k/128k") || !strings.Contains(top, tokenUsedGlyph) || !strings.Contains(top, tokenFreeGlyph) {
		t.Fatalf("top context bar = %q, want labeled context progress and usage", top)
	}

	bottom := ansi.Strip(model.renderBottomDockLine(80))
	modeAt := strings.Index(bottom, copyModeChat)
	worktreeAt := strings.Index(bottom, "paw  dev")
	if modeAt < 0 || modeAt > 3 {
		t.Fatalf("bottom border = %q, want chat anchored near the left edge", bottom)
	}
	// CTX 读数唯一化：底部边框不再携带 token 用量，模式与工作区之间留白。
	if strings.Contains(bottom, "12k") || worktreeAt < 0 || modeAt >= worktreeAt {
		t.Fatalf("bottom border = %q, want mode then project/branch with no token usage", bottom)
	}
	if worktreeAt < 80-terminalCellWidth("paw  dev")-3 {
		t.Fatalf("bottom border = %q, want project/branch anchored near the right edge", bottom)
	}
}

func TestBottomDockExactWidthAndWorktreeVisibilityBoundary(t *testing.T) {
	model := newTestModel(&fakeRunner{stats: loop.ContextStats{Known: true, TotalKnown: true, CacheKnown: true, UsedTokens: 12000, LimitTokens: 128000}})
	model.worktree = worktreeSnapshot{name: "paw", ref: "dev", state: worktreeDirty, isGit: true}

	for _, width := range []int{8, 12, 20, 32, 60, 71, 72, 80, 100} {
		bottom := model.renderBottomDockLine(width)
		if got := terminalCellWidth(bottom); got != width {
			t.Fatalf("width=%d bottom border cell width=%d line=%q", width, got, ansi.Strip(bottom))
		}
	}
	if bottom := ansi.Strip(model.renderBottomDockLine(71)); strings.Contains(bottom, "paw") {
		t.Fatalf("width=71 bottom border = %q, worktree should be hidden", bottom)
	}
	if bottom := ansi.Strip(model.renderBottomDockLine(72)); !strings.Contains(bottom, "paw") {
		t.Fatalf("width=72 bottom border = %q, worktree should be visible", bottom)
	}
}

func TestBottomDockLongCJKWorktreeKeepsRegionOrder(t *testing.T) {
	model := newTestModel(&fakeRunner{stats: loop.ContextStats{Known: true, TotalKnown: true, CacheKnown: true, UsedTokens: 12000, LimitTokens: 128000}})
	model.worktree = worktreeSnapshot{name: "项目中文名称很长", ref: "功能/长分支名称", state: worktreeDirty, isGit: true}
	bottom := ansi.Strip(model.renderBottomDockLine(100))
	if got := terminalCellWidth(bottom); got != 100 {
		t.Fatalf("bottom border cell width=%d, want 100: %q", got, bottom)
	}
	modeAt := strings.Index(bottom, copyModeChat)
	worktreeAt := strings.Index(bottom, "项目")
	if modeAt < 0 || worktreeAt < 0 || modeAt >= worktreeAt {
		t.Fatalf("bottom border = %q, want mode then CJK project/branch", bottom)
	}
}

func TestStatusLineExactWidthAcrossWidths(t *testing.T) {
	model := newTestModel(&fakeRunner{stats: loop.ContextStats{Known: true, TotalKnown: true, CacheKnown: true, UsedTokens: 45000, LimitTokens: 100000}})
	model.cursorFrameAt = time.Date(2026, 7, 28, 15, 42, 0, 0, time.UTC)
	for _, width := range []int{1, 2, 8, 12, 20, 21, 24, 31, 32, 60, 80, 100} {
		line := model.renderDockStatusLine(width)
		if got := terminalCellWidth(line); got != width {
			t.Fatalf("width=%d status cell width=%d line=%q", width, got, ansi.Strip(line))
		}
	}
}

func TestDockContextProgressPreservesSegmentsAndDropsMetadataOnNarrowWidths(t *testing.T) {
	model := newTestModel(&fakeRunner{stats: loop.ContextStats{Known: true, TotalKnown: true, CacheKnown: true, UsedTokens: 45000, CacheTokens: 20000, LimitTokens: 100000}})
	for _, width := range []int{1, 2, 8, 12, 20} {
		line := ansi.Strip(model.renderDockStatusLine(width))
		if strings.Trim(line, tokenCacheGlyph+tokenUsedGlyph+tokenFreeGlyph) != "" {
			t.Fatalf("width=%d contains metadata: %q", width, line)
		}
	}
	if got, want := ansi.Strip(model.renderDockStatusLine(20)), strings.Repeat(tokenCacheGlyph, 4)+strings.Repeat(tokenUsedGlyph, 5)+strings.Repeat(tokenFreeGlyph, 11); got != want {
		t.Fatalf("compact context progress = %q, want %q", got, want)
	}
	compact := ansi.Strip(model.renderDockStatusLine(24))
	if !strings.HasPrefix(compact, copyContextLabel+" 45% ") || strings.Contains(compact, "/") {
		t.Fatalf("compact labeled progress = %q, want percentage without token counts", compact)
	}
	wide := ansi.Strip(model.renderDockStatusLine(80))
	if !strings.HasPrefix(wide, copyContextLabel+" 45% ") || !strings.HasSuffix(wide, " 45k/100k") {
		t.Fatalf("wide context progress = %q, want percentage and token counts", wide)
	}
}

func TestDockContextProgressStaysStaticWhileGenerating(t *testing.T) {
	model := newTestModel(&fakeRunner{stats: loop.ContextStats{Known: true, TotalKnown: true, CacheKnown: true, UsedTokens: 40000, LimitTokens: 100000}})
	model.width = 100
	model.isGenerating = true
	model.cursorFrameAt = time.Unix(0, 0)
	first := ansi.Strip(model.renderDockStatusLine(100))
	model.cursorFrameAt = time.Unix(0, int64(40*time.Millisecond))
	second := ansi.Strip(model.renderDockStatusLine(100))
	if first != second {
		t.Fatal("dock context progress should stay static while generating（活动指示已收敛到 ⚒ 状态行）")
	}
	for _, line := range []string{first, second} {
		if terminalCellWidth(line) != 100 || !strings.HasPrefix(line, copyContextLabel+" 40% ") || !strings.HasSuffix(line, " 40k/100k") {
			t.Fatalf("context metadata moved: %q", line)
		}
	}
}

func TestBottomDockModeIndicator(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.cursorFrameAt = time.Now()
	if bottom := ansi.Strip(model.renderBottomDockLine(80)); !strings.Contains(bottom, copyModeChat) {
		t.Fatalf("default mode = %q, want chat", bottom)
	}
	model.input.SetValue("!ls")
	if bottom := ansi.Strip(model.renderBottomDockLine(80)); !strings.Contains(bottom, "!shell") {
		t.Fatalf("bang mode = %q, want !shell", bottom)
	}
}

func TestStatusLineShowsTokenCountWithoutStatusWord(t *testing.T) {
	model := newTestModel(&fakeRunner{stats: loop.ContextStats{Known: true, TotalKnown: true, CacheKnown: true, UsedTokens: 12000, LimitTokens: 128000}})
	model.cursorFrameAt = time.Unix(0, 0)
	top := ansi.Strip(model.renderDockStatusLine(100))
	if !strings.Contains(top, "12k/128k") {
		t.Fatalf("context bar = %q, want token count", top)
	}
	for _, unwanted := range []string{"ready", "working", "generating"} {
		if strings.Contains(top, unwanted) {
			t.Fatalf("context bar = %q, should not contain %q", top, unwanted)
		}
	}
	if strings.Contains(top, "cache") || strings.Contains(top, "free") {
		t.Fatalf("context bar still contains retired telemetry: %q", top)
	}
	// 读数唯一化：底部边框不再重复 token 计数。
	if bottom := ansi.Strip(model.renderBottomDockLine(100)); strings.Contains(bottom, "12k") {
		t.Fatalf("bottom border duplicated token count: %q", bottom)
	}
}

func TestGoalInputUsesModeIndicatorWithoutStatusOrPurpleBody(t *testing.T) {
	model := newTestModel(&fakeRunner{stats: loop.ContextStats{Known: true, TotalKnown: true, CacheKnown: true, UsedTokens: 12000, LimitTokens: 128000}})
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
	if !strings.Contains(bottom, copyModeGoal) {
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

func TestDockLinesHaveNoStatusWordDuringToolCall(t *testing.T) {
	model := newTestModel(&fakeRunner{stats: loop.ContextStats{Known: true, TotalKnown: true, CacheKnown: true, UsedTokens: 45000, LimitTokens: 100000}})
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
	if !strings.Contains(bottom, copyModeChat) {
		t.Fatalf("tool-call bottom border = %q, want chat mode indicator", bottom)
	}
}

func TestBottomDockOmitsUsageAndCacheRatio(t *testing.T) {
	// CTX 读数唯一化到顶部上下文条：底部边框只剩模式与工作区；缓存可视
	// 信息由上下文条 ▒ 段承担，不再重复数字。
	model := newTestModel(&fakeRunner{stats: loop.ContextStats{Known: true, TotalKnown: true, CacheKnown: true, UsedTokens: 12000, CacheTokens: 11000, LimitTokens: 128000}})
	model.cursorFrameAt = time.Unix(0, 0)
	bottom := ansi.Strip(model.renderBottomDockLine(100))
	for _, unwanted := range []string{"12k", "128k", copyContextLabel, "缓存占比"} {
		if strings.Contains(bottom, unwanted) {
			t.Fatalf("bottom border = %q, must not duplicate %q (context bar owns the readout)", bottom, unwanted)
		}
	}
	top := ansi.Strip(model.renderDockStatusLine(100))
	for _, want := range []string{copyContextLabel + " 9%", "12k/128k", tokenCacheGlyph} {
		if !strings.Contains(top, want) {
			t.Fatalf("context bar = %q, want %q", top, want)
		}
	}
}

func TestBottomDockOmitsCacheHitRatioWhenZero(t *testing.T) {
	model := newTestModel(&fakeRunner{stats: loop.ContextStats{Known: true, TotalKnown: true, CacheKnown: true, UsedTokens: 12000, CacheTokens: 0, LimitTokens: 128000}})
	model.cursorFrameAt = time.Unix(0, 0)
	bottom := ansi.Strip(model.renderBottomDockLine(100))
	if strings.Contains(bottom, "缓存占比") {
		t.Fatalf("bottom border = %q, must not show cache ratio when cache is zero", bottom)
	}
	if top := ansi.Strip(model.renderDockStatusLine(100)); strings.Contains(top, tokenCacheGlyph) {
		t.Fatalf("context bar = %q, must not render cache segment when cache is zero", top)
	}
}
