package bubble

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
)

// setTrueColorForTest 切换到真彩色 profile 并重建样式，测试结束后恢复。
func setTrueColorForTest(t *testing.T) {
	previousProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() {
		lipgloss.SetColorProfile(previousProfile)
		rebuildLegacyStyles()
	})
	rebuildLegacyStyles()
}

// TestSelectionBackgroundSurvivesMarkdownResets 验证 markdown 富文本内部的
// SGR reset（加粗/行内代码自带的 \x1b[0m）不会抹掉选区背景：选区样式应在
// 每个内部 reset 之后重新断言，且 markdown 自带背景（行内代码）会被选区
// 背景覆盖。
func TestSelectionBackgroundSurvivesMarkdownResets(t *testing.T) {
	setTrueColorForTest(t)

	styled := renderInlineMarkdown("**bold** and `code` text")
	width := terminalCellWidth(styled)
	if width == 0 {
		t.Fatal("markdown line rendered empty")
	}
	got := renderSelectedLineFragment(styled, 0, width)

	fullSGR, bgSGR := selectionSGRPrefixes()
	if fullSGR == "" {
		t.Fatal("selection SGR prefix is empty in truecolor")
	}
	if bgSGR == "" {
		t.Fatal("selection background SGR prefix is empty in truecolor")
	}

	// 纯文本内容必须完整保留（与原始渲染行一致）。
	if plain := ansi.Strip(got); plain != ansi.Strip(styled) {
		t.Fatalf("selected text = %q, want %q", plain, ansi.Strip(styled))
	}

	// 选区样式必须出现在开头、以及每个内部 reset 之后（每个 \x1b[0m 后重新断言）。
	expectedCount := 1 + strings.Count(styled, "\x1b[0m")
	if gotCount := strings.Count(got, fullSGR); gotCount != expectedCount {
		t.Fatalf("selection SGR count = %d, want %d\nstyled=%q\nselected=%q", gotCount, expectedCount, styled, got)
	}

	// 行内代码的自带背景必须被选区背景覆盖（bgSGR 紧随 48;... 之后）。
	if !strings.Contains(got, "\x1b[48;2;48;48;48m"+bgSGR) {
		t.Fatalf("markdown code background not overridden by selection background:\n%q\nbgSGR=%q", got, bgSGR)
	}
}

// TestSelectionBackgroundOverMarkdownBlock 验证选区覆盖 markdown 加粗片段时
// 既保留加粗前景色，又持续显示选区背景。
func TestSelectionBackgroundOverMarkdownBlock(t *testing.T) {
	setTrueColorForTest(t)

	styled := renderInlineMarkdown("**bold** text")
	width := terminalCellWidth(styled)
	got := renderSelectedLineFragment(styled, 0, width)

	fullSGR, _ := selectionSGRPrefixes()
	// 加粗片段自身的 reset（\x1b[0m）之后，必须重新断言选区样式。
	if !strings.Contains(got, "\x1b[0m"+fullSGR) {
		t.Fatalf("selection SGR not re-asserted after markdown reset:\n%q", got)
	}
	// 加粗的前景色（38;2;255;255;175 由默认主题 markdown bold 提供）保留。
	if !strings.Contains(got, "38;2;255;255;175") {
		t.Fatalf("bold foreground lost in selection:\n%q", got)
	}
}

// TestSelectionKeepsAssistantMarkerStyledLine 验证 assistant 首行（✦ marker
// 行）在选区渲染中保留原始 markdown 富文本样式，而不是退化成纯文本。
func TestSelectionKeepsAssistantMarkerStyledLine(t *testing.T) {
	setTrueColorForTest(t)

	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = 80
	model.height = 12
	model.relayout()
	model.transcript = []transcriptEntry{{
		kind:  entryAssistant,
		title: "assistant",
		body:  "**bold** first line\nplain second line",
	}}
	model.refreshViewport()
	model.viewport.GotoTop()

	snapshots := model.transcriptLineSnapshots()
	if len(snapshots) < 2 {
		t.Fatalf("snapshots = %d, want >= 2", len(snapshots))
	}
	if !snapshots[0].assistantMarker {
		t.Fatalf("first line = %q, want assistant marker", snapshots[0].plain)
	}
	// marker 行的 styled 必须保留 markdown 样式（加粗前景色）而不是纯文本。
	if !strings.Contains(snapshots[0].styled, "38;2;255;255;175") {
		t.Fatalf("marker line styled lost markdown styling: %q", snapshots[0].styled)
	}
	if !strings.Contains(snapshots[0].styled, "✦") {
		t.Fatalf("marker line styled lost marker glyph: %q", snapshots[0].styled)
	}
	// 选中整个 marker 行时，渲染结果同样保留样式且选区背景连续。
	width := snapshots[0].width
	selected := renderSelectedLineFragment(snapshots[0].styled, 0, width)
	fullSGR, _ := selectionSGRPrefixes()
	if !strings.Contains(selected, "38;2;255;255;175") {
		t.Fatalf("selected marker line lost markdown styling: %q", selected)
	}
	if !strings.Contains(selected, fullSGR) {
		t.Fatalf("selected marker line missing selection SGR: %q", selected)
	}
}

// TestSelectionPlainTextStillWrappedOnce 验证纯文本行的选区渲染不回归：
// 选区样式只出现一次（开头），无内部 reset。
func TestSelectionPlainTextStillWrappedOnce(t *testing.T) {
	setTrueColorForTest(t)

	line := "plain text line"
	got := renderSelectedLineFragment(line, 0, len(line))
	fullSGR, _ := selectionSGRPrefixes()
	if strings.Count(got, fullSGR) != 1 {
		t.Fatalf("plain selection SGR count = %d, want 1: %q", strings.Count(got, fullSGR), got)
	}
	if plain := ansi.Strip(got); plain != line {
		t.Fatalf("plain text = %q, want %q", plain, line)
	}
}
