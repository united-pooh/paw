package bubble

import (
	"github.com/charmbracelet/x/ansi"
	"strings"
	"testing"
)

const tikzSquare = `\begin{tikzcd}
\mathbb{R}^n \arrow[r,"A"] \arrow[d,"f"'] & \mathbb{R}^m \arrow[d,"g"] \\
V \arrow[r,"B"] & W
\end{tikzcd}`

func TestTikZSquare(t *testing.T) {
	t.Setenv("PAW_TUI_TEX", "1")
	for _, src := range []string{tikzSquare, "$$\n" + tikzSquare + "\n$$", "```tikzcd\n" + tikzSquare + "\n```"} {
		out := ansi.Strip(renderMarkdown(src, 80))
		for _, want := range []string{"ℝⁿ", "ℝᵐ", "→", "↓", "A", "B", "f", "g"} {
			if !strings.Contains(out, want) {
				t.Errorf("missing %q in\n%s", want, out)
			}
		}
		if strings.Contains(out, `\arrow`) {
			t.Fatalf("unrendered: %s", out)
		}
	}
}

func TestTikZNarrowAndRepeatedEdges(t *testing.T) {
	t.Setenv("PAW_TUI_TEX", "1")
	src := `\begin{tikzcd} A \ar[r,"f"] \ar[r,"g"] & B & C \end{tikzcd}`
	out := ansi.Strip(renderMarkdown(src, 80))
	for _, s := range []string{"A —f→ B", "A —g→ B", "C"} {
		if !strings.Contains(out, s) {
			t.Errorf("missing %q: %s", s, out)
		}
	}
	narrow := ansi.Strip(renderMarkdown(tikzSquare, 12))
	if !strings.Contains(narrow, "关系") {
		t.Fatalf("expected readable relationships: %s", narrow)
	}
	for _, line := range strings.Split(narrow, "\n") {
		if terminalCellWidth(line) > 12 {
			t.Fatalf("too wide: %q", line)
		}
	}
}

func TestTikZUnsupportedAndStreaming(t *testing.T) {
	t.Setenv("PAW_TUI_TEX", "1")
	for _, src := range []string{`\begin{tikzcd} A \ar[r,bend left] & B \end{tikzcd}`, `\begin{tikzcd} A \ar[u] \end{tikzcd}`} {
		out := ansi.Strip(renderMarkdown(src, 140))
		if !strings.Contains(out, src) || !strings.Contains(out, "tikzcd") {
			t.Fatalf("unsupported diagram lost source: %s", out)
		}
	}
	for i := len(`\begin{tikzcd}`); i < len(tikzSquare); i++ {
		prefix := tikzSquare[:i]
		if !strings.Contains(ansi.Strip(renderMarkdown(prefix, 500)), strings.TrimRight(prefix, "\n ")) {
			t.Fatalf("incomplete source changed at %d: want %q got %q", i, prefix, ansi.Strip(renderMarkdown(prefix, 500)))
		}
	}
}

func TestTikZDirectionsAndChinese(t *testing.T) {
	t.Setenv("PAW_TUI_TEX", "1")
	for _, src := range []string{
		`\begin{tikzcd} A & B \ar[l,"逆向"] \\ C \ar[ur,"h"] & D \ar[u,"上"] \end{tikzcd}`,
		`\begin{tikzcd} \text{输入} \ar[rr,"映射"] & & \text{输出} \end{tikzcd}`,
	} {
		out := ansi.Strip(renderMarkdown(src, 100))
		if strings.Contains(out, `\begin`) {
			t.Fatalf("valid diagram not rendered: %s", out)
		}
		if !strings.ContainsAny(out, "→←↑↗") {
			t.Fatalf("missing arrows: %s", out)
		}
	}
}

func TestTikZQuotedAmpersand(t *testing.T) {
	t.Setenv("PAW_TUI_TEX", "1")
	src := `\begin{tikzcd} A \ar[r,"\text{a & b}"] & B \end{tikzcd}`
	out := ansi.Strip(renderMarkdown(src, 100))
	if strings.Contains(out, `\begin`) || !strings.Contains(out, "a & b") {
		t.Fatalf("label parsing: %s", out)
	}
}

func TestTikZQuotedArrayLabel(t *testing.T) {
	t.Setenv("PAW_TUI_TEX", "1")
	src := `\begin{tikzcd} A \ar[r,"a&=b"] & B \end{tikzcd}`
	out := ansi.Strip(renderMarkdown(src, 100))
	if strings.Contains(out, `\begin`) {
		t.Fatalf("label split as grid: %s", out)
	}
}

func TestTikZDiagonalRoutesAndCollisions(t *testing.T) {
	d, r := parseTikZ(`\begin{tikzcd} A \ar[dr,"f"] & \\ & B \end{tikzcd}`)
	if r.status != texSuccess {
		t.Fatal(r)
	}
	out, ok := layoutTikZ(d, 80)
	if !ok || !strings.Contains(out, "→") || !strings.Contains(out, "┐") || !strings.Contains(out, "└") {
		t.Fatalf("missing elbow route: %q %v", out, ok)
	}
	d, r = parseTikZ(`\begin{tikzcd} A \ar[dr,"f"] & B \ar[dl,"g"] \\ C & D \end{tikzcd}`)
	if r.status != texSuccess {
		t.Fatal(r)
	}
	if _, ok = layoutTikZ(d, 80); ok {
		t.Fatal("crossing diagram was drawn ambiguously")
	}
	relations := tikzRelationships(d)
	for _, edge := range []string{"A —f→ D", "B —g→ C"} {
		if !strings.Contains(relations, edge) {
			t.Fatal(relations)
		}
	}
}
