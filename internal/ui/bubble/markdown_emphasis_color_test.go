package bubble

import (
	"strconv"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
	"paw/internal/ui/theme"
)

func TestMarkdownEmphasisColorAndResetAcrossThemes(t *testing.T) {
	setTrueColorForTest(t)
	for _, item := range theme.List() {
		t.Run(string(item.ID), func(t *testing.T) {
			m := newThemedTestModel(t, item.ID)
			for _, sample := range []struct {
				name, source, emphasized, tailColor string
				width                               int
			}{
				{"paragraph", "前文 **重点** 尾文", "重点", item.Colors.Body, 60},
				{"underscores", "前文 __重点__ 尾文", "重点", item.Colors.Body, 60},
				{"heading", "## 前文 **重点** 尾文", "重点", item.Colors.MarkdownHeading, 60},
				{"quote", "> 前文 **重点** 尾文", "重点", item.Colors.MarkdownQuoteText, 60},
				{"list", "- 前文 **重点** 尾文", "重点", item.Colors.Body, 60},
				{"table", "| 名称 | 说明 |\n|---|---|\n| **重点** | 尾文 |", "重点", item.Colors.Body, 40},
				{"wrap", "**甲乙丙丁戊己庚辛壬癸** 尾文", "甲乙丙丁戊己庚辛壬癸", item.Colors.Body, 12},
			} {
				t.Run(sample.name, func(t *testing.T) {
					out := renderMarkdown(sample.source, sample.width)
					if strings.Contains(sample.source, "前文") {
						assertEmphasisGlyphs(t, out, "前文", sample.tailColor, false)
					}
					assertEmphasisGlyphs(t, out, sample.emphasized, item.Colors.MarkdownBold, true)
					assertEmphasisGlyphs(t, out, "尾文", sample.tailColor, false)
					assertTableCellBackground(t, out, "")
				})
			}
			m.ready, m.width, m.height = true, 80, 24
			m.relayout()
			m.addEntry(transcriptEntry{kind: entryAssistant, body: "前文 **重点** 尾文"})
			assertEmphasisGlyphs(t, m.viewport.View(), "重点", item.Colors.MarkdownBold, true)
			if err := m.applyTheme(theme.TokyoNightLight); err != nil {
				t.Fatal(err)
			}
			assertEmphasisGlyphs(t, m.viewport.View(), "重点", m.theme.Colors.MarkdownBold, true)
			assertEmphasisGlyphs(t, m.viewport.View(), "尾文", m.theme.Colors.Body, false)
		})
	}
}

func TestMarkdownEmphasisRespectsNoColor(t *testing.T) {
	previous := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.Ascii)
	t.Cleanup(func() { lipgloss.SetColorProfile(previous) })
	for _, item := range theme.List() {
		newThemedTestModel(t, item.ID)
		out := renderMarkdown("**重点** 尾文\n\n> 引用 **重点**", 60)
		if out != ansi.Strip(out) || !strings.Contains(out, "重点") || strings.Contains(out, "**") {
			t.Fatalf("%s no-color emphasis = %q", item.ID, out)
		}
	}
}

func assertEmphasisGlyphs(t *testing.T, rendered, glyphs, foreground string, bold bool) {
	t.Helper()
	encodedForeground := foreground
	if strings.HasPrefix(foreground, "#") {
		// Use the terminal encoder's RGB quantization for exact channel comparison.
		expected := activityVisualANSIStyle{}
		activityVisualApplySGR(&expected, termenv.RGBColor(foreground).Sequence(false))
		encodedForeground = expected.foreground
	}
	seen := make(map[string]bool)
	for _, line := range strings.Split(rendered, "\n") {
		state := activityVisualANSIStyle{}
		for _, atom := range parseStyledCellLine(line).atoms {
			if atom.control {
				if strings.HasPrefix(atom.text, "\x1b[") && strings.HasSuffix(atom.text, "m") {
					raw := atom.text[2 : len(atom.text)-1]
					activityVisualApplySGR(&state, raw)
					params := strings.Split(raw, ";")
					for i := 0; i < len(params); i++ {
						code, _ := strconv.Atoi(params[i])
						if (code == 38 || code == 48) && i+1 < len(params) {
							if params[i+1] == "2" {
								i += 4
							} else if params[i+1] == "5" {
								i += 2
							}
						} else if code >= 30 && code <= 37 {
							state.foreground = strconv.Itoa(code - 30)
						} else if code >= 90 && code <= 97 {
							state.foreground = strconv.Itoa(code - 90 + 8)
						}
					}
				}
				continue
			}
			if atom.text != "" && strings.Contains(glyphs, atom.text) {
				seen[atom.text] = true
				if (state.foreground != foreground && state.foreground != encodedForeground) || (bold && !state.bold) {
					t.Fatalf("glyph %q foreground=%q bold=%v; want foreground=%q bold=%v", atom.text, state.foreground, state.bold, foreground, bold)
				}
			}
		}
	}
	for _, glyph := range glyphs {
		if !seen[string(glyph)] {
			t.Fatalf("missing glyph %q", glyph)
		}
	}
}
