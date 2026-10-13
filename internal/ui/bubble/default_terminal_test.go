package bubble

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	"paw/internal/ui/theme"
)

func assertTerminalPaletteOnly(t *testing.T, rendered string) {
	t.Helper()
	for _, sgr := range regexp.MustCompile(`\x1b\[([0-9;]*)m`).FindAllStringSubmatch(rendered, -1) {
		for _, raw := range strings.Split(sgr[1], ";") {
			n, _ := strconv.Atoi(raw)
			if n == 38 || n == 48 || n >= 41 && n <= 47 || n >= 100 && n <= 107 {
				t.Fatalf("fixed color or background in default theme: %q", sgr[0])
			}
		}
	}
}

func TestDefaultTerminalFrameAndSwitch(t *testing.T) {
	previous := lipgloss.ColorProfile()
	t.Cleanup(func() { lipgloss.SetColorProfile(previous) })
	for _, profile := range []termenv.Profile{termenv.TrueColor, termenv.ANSI, termenv.Ascii} {
		lipgloss.SetColorProfile(profile)
		m := newThemedTestModel(t, theme.Default)
		m.ready, m.hasInteracted = true, true
		m.width, m.height = 100, 32
		m.transcript = []transcriptEntry{
			{kind: entryUser, body: "使用终端配色"},
			{kind: entryAssistant, body: "# 标题\n\n普通正文 **加粗**、==重点== 与 `code`。\n\n> 引用\n\n```go\nvar n = 42 // comment\nfmt.Println(\"hello\")\n```"},
		}
		m.input.SetValue("继续 /help")
		m.relayout()
		m.refreshViewport()
		for _, id := range []theme.ThemeID{theme.Default, theme.TokyoNight, theme.Default} {
			if err := m.applyTheme(id); err != nil {
				t.Fatal(err)
			}
			if id != theme.Default {
				continue
			}
			view := m.View()
			assertFixedFrame(t, view, 100, 32)
			assertTerminalPaletteOnly(t, view)
			if profile == termenv.Ascii && regexp.MustCompile(`\x1b\[(?:[0-9]+;)*(?:3[0-7]|9[0-7])(?:;[0-9]+)*m`).MatchString(view) {
				t.Fatal("ASCII profile emits ANSI palette colors")
			}
			if dir := os.Getenv("PAW_TERMINAL_VISUAL_DIR"); dir != "" && profile == termenv.TrueColor {
				if err := os.MkdirAll(dir, 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "default.ansi"), []byte(view), 0644); err != nil {
					t.Fatal(err)
				}
			}

			if !strings.Contains(view, "普通正文") {
				t.Fatal("missing prose")
			}
			assertTerminalPaletteOnly(t, m.input.View())
			if profile != termenv.Ascii && !m.styles.SelectionSelected.GetReverse() {
				t.Fatal("selection must use reverse video")
			}
			if _, active := m.cursorAnchor.currentAnimation(); active {
				t.Fatal("default theme must release RGB cursor animation")
			}
		}
	}
}

func TestDefaultTerminalStylesAndANSIColors(t *testing.T) {
	previous := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(previous) })
	m := newThemedTestModel(t, theme.Default)
	if got := m.styles.Body.Render("plain"); got != "plain" {
		t.Fatalf("body imposes a color: %q", got)
	}
	if got := m.styles.LabelError.Render("error"); !strings.Contains(got, "31") {
		t.Fatalf("error does not use ANSI red: %q", got)
	}
	for _, s := range []lipgloss.Style{m.styles.MarkdownCode, m.styles.MarkdownCodeBlockLabel, userTranscriptRowStyle, modePlanStyle, toolStatusOKStyle, markdownHighlightStyle} {
		assertTerminalPaletteOnly(t, s.Render("sample"))
	}
	if !selectedTranscriptLineStyle.GetReverse() {
		t.Fatal("transcript selection must reverse terminal colors")
	}
}

func TestDefaultTerminalCursorResetAndAnimations(t *testing.T) {
	m := newThemedTestModel(t, theme.TokyoNight)
	if _, active := m.cursorAnchor.currentAnimation(); !active {
		t.Fatal("RGB theme must animate cursor")
	}
	if err := m.applyTheme(theme.Default); err != nil {
		t.Fatal(err)
	}
	if _, active := m.cursorAnchor.currentAnimation(); active {
		t.Fatal("old animation retained")
	}
	if got := terminalCursorVisualSequence(terminalCursorVisual{color: "#abcdef"}, terminalCursorVisual{}); got != resetTerminalCursorColorSequence() {
		t.Fatalf("cursor reset = %q", got)
	}
	previous := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(previous) })
}

func TestDefaultTerminalRestoresDefaultsAfterNestedStyles(t *testing.T) {
	previous := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(previous) })
	got := restoreBackgroundAfterANSIReset("plain\x1b[0mnext\x1b[49mtail", "", "")
	if !strings.Contains(got, "\x1b[49m") || !strings.Contains(got, "\x1b[39m") {
		t.Fatalf("missing terminal default reset: %q", got)
	}
	assertTerminalPaletteOnly(t, got)
	if got := foregroundSGR("6"); got != "\x1b[36m" {
		t.Fatalf("ANSI cyan restore = %q", got)
	}
}

// RGB regressions use an explicit RGB theme instead of relying on Default.
func newRGBTestModel(t *testing.T) appModel {
	t.Helper()
	previous, active := colorManager, activePalette
	t.Cleanup(func() { colorManager = previous; activePalette = active; rebuildLegacyStyles() })
	return newThemedTestModel(t, theme.TokyoNight)
}

func TestDefaultTerminalSelectionSurvivesStyledText(t *testing.T) {
	previous := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(previous) })
	m := newThemedTestModel(t, theme.Default)
	styled := renderInlineMarkdown("**bold** and `code` with [link](https://example.com)")
	selected := renderSelectedLineFragment(styled, 0, terminalCellWidth(styled))
	assertTerminalPaletteOnly(t, selected)
	state := activityVisualANSIStyle{}
	for _, atom := range parseStyledCellLine(selected).atoms {
		if atom.control {
			if strings.HasPrefix(atom.text, "\x1b[") && strings.HasSuffix(atom.text, "m") {
				activityVisualApplySGR(&state, atom.text[2:len(atom.text)-1])
			}
		} else if !state.reverse {
			t.Fatalf("selection lost reverse on %q", atom.text)
		}
	}
	if dir := os.Getenv("PAW_TERMINAL_VISUAL_DIR"); dir != "" {
		menu := m.styles.SelectionSelected.Render("› Default · terminal colors")
		if err := os.WriteFile(filepath.Join(dir, "selection.ansi"), []byte(menu+"\n"+selected), 0644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestDefaultTerminalReadableChrome(t *testing.T) {
	previous := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(previous) })
	m := newThemedTestModel(t, theme.Default)
	m.ready, m.hasInteracted = true, true
	m.width, m.height = 100, 25
	m.transcript = []transcriptEntry{
		{kind: entryUser, body: "绘制一个表格看看"},
		{kind: entryAssistant, body: "普通正文与辅助信息都应清楚可见。\n\n| 序号 | 项目 | 语言 | 状态 | 备注 |\n|---|---|---|---|---|\n| 1 | 核心运行时 | Go | 完成 | `internal/runtime` |\n| 2 | 终端 UI | Go | 进行中 | `internal/ui` |\n| 3 | 文档整理 | Markdown | 待办 | `docs/` |\n\n> 引用与说明文字\n\n```go\nvar n = 42 // comment\n```"},
	}
	m.input.SetValue("")
	m.relayout()
	m.refreshViewport()
	view := m.View()
	assertFixedFrame(t, view, 100, 25)
	assertTerminalPaletteOnly(t, view)
	if regexp.MustCompile(`\x1b\[(?:[0-9]+;)*90(?:;[0-9]+)*m`).MatchString(view) {
		t.Error("low-contrast ANSI 8 remains in frame")
	}
	if dir := os.Getenv("PAW_TERMINAL_VISUAL_DIR"); dir != "" {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "readability.ansi"), []byte(view), 0644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestDefaultGoldEmphasisAndStickyBackground(t *testing.T) {
	previous := lipgloss.ColorProfile()
	t.Cleanup(func() { lipgloss.SetColorProfile(previous) })
	for _, profile := range []termenv.Profile{termenv.TrueColor, termenv.ANSI, termenv.Ascii} {
		lipgloss.SetColorProfile(profile)
		m := newThemedTestModel(t, theme.Default)
		if profile != termenv.Ascii {
			assertEmphasisGlyphs(t, renderMarkdown("正文 **重点** 尾文", 40), "重点", "3", true)
		}
		m.ready, m.hasInteracted = true, true
		m.transcript = []transcriptEntry{
			{kind: entryUser, body: "检查 file.go\n保留源文件", inputTokens: []inputToken{{Kind: inputTokenFile, Start: len([]rune("检查 ")), End: len([]rune("检查 file.go")), Label: "file.go"}}},
			{kind: entryAssistant, body: strings.Repeat("普通正文 **浅金重点**\n\n", 20)},
		}
		for _, width := range []int{40, 70} {
			m.width, m.height = width, 14
			m.relayout()
			m.refreshViewport()
			m.viewport.GotoBottom()
			before := m.viewport.View()
			if strings.Contains(before, "\x1b[40m") {
				t.Fatal("sticky background leaked into transcript canvas")
			}
			after := m.overlayStickyUser(before)
			row, tail, _ := strings.Cut(after, "\n")
			_, originalTail, _ := strings.Cut(before, "\n")
			if tail != originalTail {
				t.Fatal("sticky background changed following rows")
			}
			if profile == termenv.Ascii {
				if strings.Contains(after, "\x1b[") {
					t.Fatal("no-color sticky row emits ANSI")
				}
				continue
			}
			background := false
			for _, atom := range parseStyledCellLine(row).atoms {
				if atom.control {
					if strings.HasPrefix(atom.text, "\x1b[") && strings.HasSuffix(atom.text, "m") {
						for _, param := range strings.Split(atom.text[2:len(atom.text)-1], ";") {
							switch param {
							case "", "0", "49":
								background = false
							case "40":
								background = true
							}
						}
					}
				} else if !background {
					t.Fatalf("sticky cell %d %q has no terminal background", atom.cellStart, atom.text)
				}
			}
			assertFixedFrame(t, m.View(), width, 14)
			if dir := os.Getenv("PAW_TERMINAL_VISUAL_DIR"); dir != "" && profile == termenv.TrueColor && width == 70 {
				if err := os.WriteFile(filepath.Join(dir, "gold-sticky.ansi"), []byte(m.View()), 0644); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
}
