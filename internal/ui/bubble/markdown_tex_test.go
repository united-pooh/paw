package bubble

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestTeXInlineAndBlocks(t *testing.T) {
	t.Setenv("PAW_TUI_TEX", "1")
	for _, tc := range []struct{ input, want string }{
		{`空间 $\mathbb{R}^n$ 与 \(A^TA\)`, "空间 ℝⁿ 与 AᵀA"},
		{`$\frac{a+b}{c}$`, "(a + b)/c"},
		{`$$\frac{a}{b}$$`, "─"},
		{"\\[\n\\begin{bmatrix}a&b\\\\c&d\\end{bmatrix}\n\\]", "⎡"},
		{`# 空间 $\mathbb{R}^n$`, "ℝⁿ"},
		{`- 空间 $\mathbb{R}^n$`, "ℝⁿ"},
		{`> $\alpha$`, "α"},
		{`**空间 $\mathbb{R}^n$**`, "空间 ℝⁿ"},
	} {
		got := ansi.Strip(renderMarkdown(tc.input, 80))
		if !strings.Contains(got, tc.want) {
			t.Errorf("%q rendered as %q; want %q", tc.input, got, tc.want)
		}
	}
}

func TestTeXCurrencyMixedWithMath(t *testing.T) {
	t.Setenv("PAW_TUI_TEX", "1")
	got := ansi.Strip(renderInlineMarkdown(`价格 $5 USD，公式 $x^2$`))
	if got != `价格 $5 USD，公式 x²` {
		t.Fatal(got)
	}
}

func TestTeXHermitianInlineOverline(t *testing.T) {
	t.Setenv("PAW_TUI_TEX", "1")
	for _, input := range []string{
		`即每个元素满足 $a_{ij} = \overline{a_{ji}}$，就叫 Hermitian 矩阵。`,
		`- 对角线元素必须是实数（因为 $a_{ii} = \overline{a_{ii}}$）。`,
		`# $a_{ij} = \overline{a_{ji}}$`,
		`> \(a_{ij} = \overline{a_{ji}}\)`,
		"| 性质 | 公式 |\n|---|---|\n| 共轭 | $a_{ij} = \\overline{a_{ji}}$ |",
	} {
		got := ansi.Strip(renderMarkdown(input, 100))
		if strings.ContainsAny(got, `$\`) || !strings.Contains(got, "overline(a") {
			t.Errorf("inline overline remained raw: %q", got)
		}
	}
}

func TestTeXRestrictionMarkdown(t *testing.T) {
	t.Setenv("PAW_TUI_TEX", "1")
	for _, source := range []string{texRestrictionIsomorphism, texRestrictionPositive} {
		for _, width := range []int{60, 100, 140} {
			got := ansi.Strip(renderMarkdown("$$\n"+source+"\n$$", width))
			if strings.Contains(got, "$$") || strings.Contains(got, `\mathcal`) || !strings.Contains(got, "|") {
				t.Errorf("raw screenshot at width %d: %s", width, got)
			}
		}
	}
	got := ansi.Strip(renderMarkdown("| 公式 | 含义 |\n|---|---|\n| $A|_V$ | 限制 |", 60))
	if strings.Contains(got, "$A") || !strings.Contains(got, "限制") {
		t.Fatalf("restriction split table: %s", got)
	}
}

func TestTeXMalformedSpanDoesNotConsumeTableColumn(t *testing.T) {
	t.Setenv("PAW_TUI_TEX", "1")
	cells := parseMarkdownTableRow(`| $x | value |`)
	if len(cells) != 2 {
		t.Fatalf("incomplete math swallowed table: %#v", cells)
	}
}

func TestTeXProtectedAndIncomplete(t *testing.T) {
	t.Setenv("PAW_TUI_TEX", "1")
	for _, input := range []string{`$\unknown{x}$`, `\(\frac{a}{`, `$x_{a_b`, `价格 $5 和 $10`, `USD $20.00, then $30.00`, `\$5`, "`$x^2$`", "```go\n$x^2$\n```"} {
		want := input
		if strings.HasPrefix(input, "```") {
			want = "$x^2$"
		} else if strings.HasPrefix(input, "`") {
			want = strings.Trim(input, "`")
		}
		got := ansi.Strip(renderMarkdown(input, 80))
		if !strings.Contains(got, want) {
			t.Errorf("protected %q became %q", input, got)
		}
	}
	link := `[文档](https://example.com/$x_a$/path)`
	if got := renderInlineMarkdown(link); !strings.Contains(got, "https://example.com/$x_a$/path") {
		t.Fatalf("link changed: %q", got)
	}
}

func TestTeXTablePipes(t *testing.T) {
	t.Setenv("PAW_TUI_TEX", "1")
	row := parseMarkdownTableRow(`| $|x|$ | $\alpha$ |`)
	if len(row) != 2 {
		t.Fatalf("math split into columns: %#v", row)
	}
	got := ansi.Strip(renderMarkdown("| norm | value |\n|---|---|\n| $|x|$ | $\\alpha$ |", 50))
	if !strings.Contains(got, "|x|") || !strings.Contains(got, "α") {
		t.Fatalf("bad table: %s", got)
	}
}

func TestTeXOptOut(t *testing.T) {
	t.Setenv("PAW_TUI_TEX", "0")
	if got := strings.TrimSpace(ansi.Strip(renderMarkdown(`$\alpha$`, 40))); got != `$\alpha$` {
		t.Fatal(got)
	}
}

func TestTeXProtectsLongAndTildeFences(t *testing.T) {
	t.Setenv("PAW_TUI_TEX", "1")
	for _, fence := range []string{"~~~", "````"} {
		got := ansi.Strip(renderMarkdown(fence+"python\n$x^2$\n"+fence, 60))
		if !strings.Contains(got, "$x^2$") || strings.Contains(got, "x²") {
			t.Fatalf("code was converted: %s", got)
		}
	}
}

func TestTeXUnsupportedGraphPreservesSource(t *testing.T) {
	t.Setenv("PAW_TUI_TEX", "1")
	src := `\begin{tikzpicture}\node (a_b) {x_y};\end{tikzpicture}`
	got := ansi.Strip(renderMarkdown(src, 100))
	if !strings.Contains(got, src) {
		t.Fatalf("graph source changed: %s", got)
	}
}

func TestTeXPreservesIntrawordUnderscores(t *testing.T) {
	t.Setenv("PAW_TUI_TEX", "1")
	if got := ansi.Strip(renderInlineMarkdown(`foo_bar_baz $x^2$`)); got != "foo_bar_baz x²" {
		t.Fatal(got)
	}
}

func TestTeXProtectsRelativeLinkTargets(t *testing.T) {
	t.Setenv("PAW_TUI_TEX", "1")
	input := `[文档](docs/$x^2$.md) 与 $\alpha$`
	got := ansi.Strip(renderInlineMarkdown(input))
	if got != `[文档](docs/$x^2$.md) 与 α` {
		t.Fatal(got)
	}
}
