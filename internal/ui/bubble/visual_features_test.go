package bubble

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
	"paw/internal/theme"
)

func TestSyntaxLanguageInferenceUsesFileExtension(t *testing.T) {
	for _, test := range []struct {
		hint string
		want string
	}{
		{"internal/service.go", "go"},
		{"config.json", "json"},
		{"script.py", "py"},
		{"README.md", "md"},
	} {
		if got := syntaxLanguageFromText("", test.hint); got != test.want {
			t.Fatalf("syntaxLanguageFromText(%q) = %q, want %q", test.hint, got, test.want)
		}
	}
}

func TestHighlightToolDetailLinePreservesTextAndStylesTokens(t *testing.T) {
	got := highlightToolDetailLine(`func main() { return "ok" // comment }`, "go")
	if plain := ansi.Strip(got); plain != `func main() { return "ok" // comment }` {
		t.Fatalf("plain highlighted text = %q", plain)
	}
	// 关键字靠专用色相区分（参考主流编辑器主题，不再叠加粗体）。
	if syntaxKeywordStyle.GetForeground() == nil {
		t.Fatal("keyword style is not configured")
	}
	if syntaxStringStyle.GetForeground() == nil || syntaxCommentStyle.GetForeground() == nil {
		t.Fatal("string/comment styles are not configured")
	}
}

func TestHighlightToolDetailLineKeepsGraphemeClustersAtomic(t *testing.T) {
	previousProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(previousProfile) })

	base := lipgloss.NewStyle().
		Foreground(lipgloss.Color("194")).
		Background(lipgloss.Color("22")).
		Bold(true)
	got := highlightToolDetailLineWithBase("+new 👨‍👩‍👧‍👦 é हिन्दी العربية", "", base)
	if styled, plain := terminalCellWidth(got), terminalCellWidth(ansi.Strip(got)); styled != plain {
		t.Fatalf("styled width=%d plain width=%d: raw=%q plain=%q", styled, plain, got, ansi.Strip(got))
	}
}

func TestRenderToolDetailLinesHighlightsDetectedSource(t *testing.T) {
	got := renderToolDetailLines([]string{
		"internal/service.go",
		"package service",
		"func run() int { return 42 }",
	}, 80)
	if !strings.Contains(ansi.Strip(got), "func run() int { return 42 }") {
		t.Fatalf("rendered source lost text: %q", ansi.Strip(got))
	}
	if syntaxLanguageFromLines([]string{"internal/service.go", "package service"}) != "go" {
		t.Fatal("source language was not detected from the file path")
	}
}

func TestRenderToolDetailLinesWithHintHighlightsEditSource(t *testing.T) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(termenv.Ascii) })
	got := renderToolDetailLinesWithHint([]string{
		`func run() { return "changed" }`,
	}, 80, "internal/service.go")
	if plain := ansi.Strip(got); !strings.Contains(plain, `func run() { return "changed" }`) {
		t.Fatalf("Edit preview lost source text: %q", plain)
	}
	if syntaxStringStyle.GetForeground() == nil {
		t.Fatal("Edit preview string style is not configured")
	}
}

func TestHighlightToolDetailLineRainbowBrackets(t *testing.T) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(termenv.Ascii) })
	got := highlightToolDetailLine(`func run() { return foo[bar(1)] }`, "go")
	if plain := ansi.Strip(got); plain != `func run() { return foo[bar(1)] }` {
		t.Fatalf("rainbow bracket highlighting changed text: %q", plain)
	}
	if strings.Count(got, "\x1b[") < 6 {
		t.Fatalf("rainbow bracket highlighting emitted too few styles: %q", got)
	}
}
func TestUserTranscriptMessageUsesBrightOrangeForeground(t *testing.T) {
	previousProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(previousProfile) })

	got := renderEntry(transcriptEntry{kind: entryUser, body: "submitted message"}, 40)
	if !strings.Contains(got, "38;2;255;175;0") {
		t.Fatalf("user message did not use bright orange foreground: %q", got)
	}
}

func TestTokenizedUserTranscriptOrdinaryTextUsesBrightOrangeForeground(t *testing.T) {
	previousProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(previousProfile) })

	got := renderEntry(transcriptEntry{
		kind: entryUser,
		body: "ask file.go now",
		inputTokens: []inputToken{{
			Kind:  inputTokenFile,
			Start: 4,
			End:   11,
			Label: "file.go",
		}},
	}, 40)
	if !strings.Contains(got, "38;2;255;175;0") {
		t.Fatalf("tokenized user message ordinary text did not use bright orange: %q", got)
	}
	if plain := ansi.Strip(got); !strings.Contains(plain, "ask file.go now") {
		t.Fatalf("tokenized user message changed visible text: %q", plain)
	}
}

// TestToolGroupUsesTranscriptGutterAndStaysWithinWidth 验证 Tools 组折叠头行
// 使用 transcript gutter 且不再渲染外层边框：header 与 user/assistant 条目
// 同列（只缩进 gutter），展开的工具条目靠自己的边框在 header 右侧缩进，
// 避免展开工具详情后出现组边框与条目边框两条竖线。
func TestToolGroupUsesTranscriptGutterAndStaysWithinWidth(t *testing.T) {
	const width = 48
	rendered := ansi.Strip(renderToolsGroup([]transcriptEntry{{
		kind:       entryTool,
		toolName:   "Read",
		toolStatus: "ok",
		toolTarget: "internal/ui/bubble/transcript.go",
	}}, width, time.Time{}, false, false))
	lines := strings.Split(rendered, "\n")
	if len(lines) == 0 || !strings.HasPrefix(lines[0], transcriptEntryGutter) {
		t.Fatalf("tool group does not start at transcript body column: %q", rendered)
	}
	if strings.Contains(lines[0], "│") {
		t.Fatalf("tool group header carries an outer border rail: %q", lines[0])
	}
	wantTextColumn := terminalCellWidth(transcriptEntryGutter)
	if textColumn := strings.Index(lines[0], "▸"); textColumn != wantTextColumn {
		t.Fatalf("tool group header text column = %d, want %d: %q", textColumn, wantTextColumn, lines[0])
	}
	for index, line := range lines {
		if got := terminalCellWidth(line); got > width {
			t.Fatalf("tool group line %d width = %d, want <= %d: %q", index+1, got, width, line)
		}
	}
}

func TestTodoStatusStylesMatchRunningAndDimCompletedBody(t *testing.T) {
	if todoInProgressStyle.GetForeground() != contextUsedStyle.GetForeground() {
		t.Fatal("todo in-progress color is not synchronized with context-used color")
	}
	if todoCompletedStyle.GetForeground() == bodyStyle.GetForeground() {
		t.Fatal("completed todo body was not dimmed")
	}
}

func TestSelectionFocusedStyleChangesForegroundWithoutBackground(t *testing.T) {
	styles := NewStyleSet(defaultThemePaletteForTests())
	if _, hasBackground := styles.SelectionFocused.GetBackground().(lipgloss.NoColor); !hasBackground {
		t.Fatal("selection focused style unexpectedly changes background")
	}
	if styles.SelectionFocused.GetForeground() == nil {
		t.Fatal("selection focused style does not set foreground")
	}
}

// TestSelectionTokensStayVisibleInDarkAndLightThemes 防止选中态令牌退回
// “只有前景色、无背景”的隐形状态：Selected（tab 反色块）与
// SelectionSelected/SelectionFocusedSelected（行级高亮）必须同时带背景和
// 前景，且前景不能取自 provider 选中前景（浅色主题下与背景同色）。
func TestSelectionTokensStayVisibleInDarkAndLightThemes(t *testing.T) {
	for _, item := range theme.List() {
		styles := NewStyleSet(item.Colors)
		for _, token := range []struct {
			name  string
			style lipgloss.Style
		}{
			{"Selected", styles.Selected},
			{"SelectionSelected", styles.SelectionSelected},
			{"SelectionFocusedSelected", styles.SelectionFocusedSelected},
		} {
			if _, hasBackground := token.style.GetBackground().(lipgloss.NoColor); hasBackground {
				t.Fatalf("%s in theme %s lost its background highlight", token.name, item.ID)
			}
			if token.style.GetForeground() == nil {
				t.Fatalf("%s in theme %s does not set foreground", token.name, item.ID)
			}
		}
	}
}

func defaultThemePaletteForTests() (p theme.Palette) {
	item, _ := theme.ByID(theme.Default)
	return item.Colors
}
