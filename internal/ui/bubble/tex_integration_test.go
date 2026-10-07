package bubble

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
	"paw/internal/ui/theme"
)

const texVisualFixture = "# 数学公式与映射图\n\n" +
	`行内：$\mathbb{R}^n$、$A^TA$、$\alpha+\beta$、$x_1^2$。` + "\n\n" +
	`Hermitian：$a_{ij}=\overline{a_{ji}}$。` + "\n" +
	`- 对角元为实数：$a_{ii}=\overline{a_{ii}}$；单字符：$\overline{x}$。` + "\n\n" +
	`$$x=\frac{-b\pm\sqrt{b^2-4ac}}{2a}$$` + "\n\n" +
	`$$\begin{bmatrix}a&b\\c&d\end{bmatrix}\begin{pmatrix}x\\y\end{pmatrix}$$` + "\n\n" +
	tikzSquare + "\n\n" +
	`价格 $5 与 $10；代码 ` + "`$x^2$`" + " 保留源码。\n" +
	`未支持：$\custom{x}$` + "\n\n" +
	`$$\frac{a+b+c+d+e+f+g+h+i+j+k+l+m+n+o+p+q+r+s+t}{x+y}$$`

func TestTeXStreamRestoreResizeAndCopy(t *testing.T) {
	t.Setenv("PAW_TUI_TEX", "1")
	body := `空间 $\mathbb{R}^n$，$a_{ij}=\overline{a_{ji}}$` + "\n\n" + `$$\frac{x+1}{2}$$` + "\n\n" + tikzSquare +
		"\n\n$$" + texRestrictionIsomorphism + "$$\n\n$$" + texRestrictionPositive + "$$"
	for _, chunkSize := range []int{1, 7, 31, len(body)} {
		m := newTestModel(&fakeRunner{})
		m.ready = true
		m.hasInteracted = true
		m.width = 100
		m.height = 40
		m.relayout()
		for at := 0; at < len(body); at += chunkSize {
			end := minInt(len(body), at+chunkSize)
			next, _ := m.Update(assistantDeltaMsg(body[at:end]))
			m = next.(appModel)
		}
		next, _ := m.Update(doneMsg{})
		m = next.(appModel)
		entry := lastEntryOfKind(t, m.transcript, entryAssistant)
		if entry.body != body {
			t.Fatalf("chunk %d mutated original: %q", chunkSize, entry.body)
		}
		got := ansi.Strip(renderEntryBody(entry, 80))
		want := ansi.Strip(renderMarkdown(body, 80))
		if got != want {
			t.Fatalf("chunk %d mismatched final rendering", chunkSize)
		}
		next, _ = m.Update(sessionRestoredMsg{sessionID: "tex-restore", entries: []transcriptEntry{entry}})
		m = next.(appModel)
		for _, width := range []int{60, 140, 100} {
			next, _ = m.Update(tea.WindowSizeMsg{Width: width, Height: 40})
			m = next.(appModel)
			m.viewport.GotoTop()
			assertFixedFrame(t, m.View(), width, 40)
			if m.transcript[0].body != body {
				t.Fatal("resize changed source")
			}
		}
		snapshots := m.transcriptLineSnapshots()
		m.selectionActive = true
		m.selectionStart = selectionPoint{row: 0, col: 0}
		m.selectionEnd = selectionPoint{row: len(snapshots) - 1, col: 100}
		copied := m.selectedTranscriptText()
		for _, s := range []string{"ℝⁿ", "→", "↓", "aᵢⱼ = overline(aⱼᵢ)"} {
			if !strings.Contains(copied, s) {
				t.Fatalf("copy missing %q: %s", s, copied)
			}
		}
		if strings.Contains(copied, `\arrow`) || strings.Contains(copied, "\x1b") {
			t.Fatalf("copy retained invisible syntax: %q", copied)
		}
	}
}

func TestTeXBlockDoesNotTruncate(t *testing.T) {
	t.Setenv("PAW_TUI_TEX", "1")
	var rows []string
	for i := 0; i < 40; i++ {
		rows = append(rows, fmt.Sprintf("x_{%d}", i))
	}
	body := `$$\begin{matrix}` + strings.Join(rows, `\\`) + `\end{matrix}$$`
	got := ansi.Strip(renderMarkdown(body, 80))
	if !strings.Contains(got, "₃₉") || strings.Contains(got, "more lines") {
		t.Fatalf("truncated math: %s", got)
	}
}

func TestTeXCancelKeepsIncompleteSource(t *testing.T) {
	t.Setenv("PAW_TUI_TEX", "1")
	body := `\[\frac{x_1}{`
	m := newTestModel(&fakeRunner{})
	m.ready = true
	m.width = 80
	m.height = 24
	m.relayout()
	next, _ := m.Update(assistantDeltaMsg(body))
	m = next.(appModel)
	next, _ = m.Update(turnFinishedMsg{err: context.Canceled})
	m = next.(appModel)
	entry := lastEntryOfKind(t, m.transcript, entryAssistant)
	if entry.body != body {
		t.Fatalf("cancellation lost original: %q", entry.body)
	}
	if out := ansi.Strip(renderEntryBody(entry, 80)); !strings.Contains(out, body) {
		t.Fatalf("canceled formula changed: %s", out)
	}
}

func TestTeXVisualFrames(t *testing.T) {
	testTeXVisualFrames(t, texVisualFixture, "", []string{"ℝⁿ", "α", "→", "↓", "$5", "custom", "overline(aⱼᵢ)", "overline(aᵢᵢ)", "x̅"})
}

func TestTeXRestrictionVisualFrames(t *testing.T) {
	body := "# 限制映射与正定性\n\n## A 把行空间同构映到列空间\n\n$$\n" + texRestrictionIsomorphism + "\n$$\n\n" +
		"## AᵀA 在行空间上正定可逆\n\n$$\n" + texRestrictionPositive + "\n$$\n\n" +
		`行内带标签箭头：$V\xrightarrow{\sim}W$。` + "\n\n" + `绝对值与范数：$|x|^2$，$\|Ax\|^2$。`
	testTeXVisualFrames(t, body, "restriction-", []string{"是同构", "正定可逆", "→", "∼", "‖", "ᵀ", "|"})
}

func testTeXVisualFrames(t *testing.T, body, prefix string, tokens []string) {
	t.Helper()
	t.Setenv("PAW_TUI_TEX", "1")
	setTrueColorForTest(t)
	for _, themeID := range []string{"dracula", "tokyo-night-light"} {
		for _, width := range []int{60, 100, 140} {
			t.Run(fmt.Sprintf("%s-%d", themeID, width), func(t *testing.T) {
				m := newThemedTestModel(t, theme.ThemeID(themeID))
				m.ready = true
				m.hasInteracted = true
				m.width = width
				m.height = 54
				m.transcript = []transcriptEntry{{kind: entryUser, body: "展示公式与数学映射图"}, {kind: entryAssistant, body: body}}
				m.relayout()
				m.refreshViewport()
				m.viewport.GotoTop()
				view := m.View()
				assertFixedFrame(t, view, width, m.height)
				plain := ansi.Strip(view)
				for _, s := range tokens {
					if !strings.Contains(strings.Join(strings.Fields(plain), ""), strings.Join(strings.Fields(s), "")) {
						t.Fatalf("fixture missing %q:\n%s", s, plain)
					}
				}
				for _, line := range strings.Split(view, "\n") {
					assertTerminalSequencesComplete(t, line)
				}
				if dir := os.Getenv("PAW_TEX_VISUAL_DIR"); dir != "" {
					if err := os.MkdirAll(dir, 0755); err != nil {
						t.Fatal(err)
					}
					stem := filepath.Join(dir, fmt.Sprintf("%s%s-%d", prefix, themeID, width))
					page := fmt.Sprintf(`<!doctype html><html lang="zh"><meta charset="utf-8"><title>Paw TUI TeX</title><style>body{margin:0;padding:16px;background:%s;color:%s}pre{margin:0;font:16px/1.35 Menlo,monospace;white-space:pre}</style><pre>%s</pre></html>`, m.styles.Frame.GetBackground(), m.theme.Colors.Body, hierarchyVisualANSIToHTML(view))
					for ext, s := range map[string]string{".html": page, ".ansi": view, ".txt": plain} {
						if err := os.WriteFile(stem+ext, []byte(s), 0644); err != nil {
							t.Fatal(err)
						}
					}
				}
			})
		}
	}
	lipgloss.SetColorProfile(termenv.Ascii)
	rebuildLegacyStyles()
	plain := renderMarkdown(body, 60)
	if strings.Contains(plain, "\x1b") {
		t.Fatal("ANSI in no-color math")
	}
}

func TestTeXBlocksUseBodyForeground(t *testing.T) {
	setTrueColorForTest(t)
	m := newThemedTestModel(t, theme.Dracula)
	_ = m
	for _, src := range []string{`$$\frac{a}{b}$$`, tikzSquare, "```tikzcd\n" + tikzSquare + "\n```"} {
		got := renderMarkdown(src, 80)
		if !strings.Contains(got, foregroundSGR(colorManager.Hex(colorBody))) {
			t.Fatalf("block missing body foreground: %q", got)
		}
	}
}

func BenchmarkTeX(b *testing.B) {
	for _, enabled := range []string{"0", "1"} {
		for name, body := range map[string]string{
			"prose":   strings.Repeat("普通正文包含 **加粗** 与 `code`。\n", 20),
			"math":    strings.Repeat(`公式 $\alpha+x_i^2$，$\frac{a+b}{c}$。`+"\n", 20),
			"diagram": tikzSquare,
		} {
			b.Run(enabled+"/"+name, func(b *testing.B) {
				b.Setenv("PAW_TUI_TEX", enabled)
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					renderMarkdown(body, 100)
				}
			})
		}
	}
}

func FuzzTeXRendering(f *testing.F) {
	for _, s := range []string{`\frac{a}{b}`, `\begin{matrix}a&b\\c&d\end{matrix}`, `\text{中文}`, `\sqrt{x}`, `x^{y^2}`, `\left(x\right)`, tikzSquare} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		if len(s) > 2048 {
			t.Skip()
		}
		renderTeXMath(s, false, 60)
		renderMarkdown("$"+s+"$", 60)
		renderTikZBlock(s, s, 60)
	})
}
