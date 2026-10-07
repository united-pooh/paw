package bubble

import (
	"strings"
	"testing"
)

const texRestrictionIsomorphism = `A|_{\mathcal{R}(A^T)} : \mathcal{R}(A^T) \;\xrightarrow{\sim}\; \mathcal{R}(A) \quad \text{是同构}`
const texRestrictionPositive = `x \in \mathcal{R}(A^T): \quad x^T A^T A x = \|Ax\|^2 > 0 \quad (x \neq 0)
\;\Rightarrow\; A^T A|_{\mathcal{R}(A^T)} \text{ 正定可逆}`

func TestTeXRestrictionScreenshot(t *testing.T) {
	for _, source := range []string{texRestrictionIsomorphism, texRestrictionPositive} {
		for _, width := range []int{8, 60, 100, 140} {
			got := renderTeXMath(source, false, width)
			if got.status != texSuccess && got.status != texOverflow {
				t.Errorf("width %d: %+v", width, got)
			}
			for _, token := range []string{"|", "ᵀ"} {
				if !strings.Contains(got.text, token) {
					t.Errorf("lost %q at width %d: %+v", token, width, got)
				}
			}
			tokens := []string{"∼", "→", "是同构"}
			if source == texRestrictionPositive {
				tokens = []string{"‖Ax‖²", "⇒", "≠", "正定可逆"}
			}
			for _, token := range tokens {
				if !strings.Contains(strings.Join(strings.Fields(got.text), ""), token) {
					t.Errorf("lost %q at width %d: %+v", token, width, got)
				}
			}
		}
		got := renderTeXMath(source, true, 0)
		if got.status != texSuccess || strings.Contains(got.text, "\n") {
			t.Errorf("inline screenshot: %+v", got)
		}
	}
}

func TestTeXScriptedBarsProtectDelimitersAndText(t *testing.T) {
	for _, source := range []string{`|x|^2`, `|x|_n`, `\left|\frac{a}{b}\right|^2`, `\|Ax\|^2`, `\text{A|_V}`, `\texttt{A|_V}`} {
		if got := texScriptedBars(source); got != source {
			t.Errorf("rewrote protected %q => %q", source, got)
		}
	}
	for _, source := range []string{`A|_V`, `A| _{V}`, `A|_V+B|_W`, `|x|^2+A|_V`, `\frac{A|_V}{2}`, `\mathrm{A|_V}`} {
		got := renderTeXMath(source, true, 0)
		if got.status != texSuccess || !strings.Contains(got.text, "|") {
			t.Errorf("scripted bar: %s => %+v", source, got)
		}
	}
}

func TestTeXLabeledArrowLinearFallback(t *testing.T) {
	for _, tc := range []struct{ source, want string }{
		{`A\xrightarrow{\sim}B`, `A →[∼] B`},
		{`A\xleftarrow[ g ]{f}B`, `A ←[above: f; below: g] B`},
		{`A\xrightarrow[\text{]}]{\frac{x}{y}}B`, `A →[above: (x)/(y); below: ]] B`},
	} {
		got := renderTeXMath(tc.source, true, 0)
		if got.status != texSuccess || got.text != tc.want {
			t.Errorf("arrow %s => %+v; want %s", tc.source, got, tc.want)
		}
	}
}

func TestTeXLinearFallbackPreservesStructure(t *testing.T) {
	for _, tc := range []struct {
		source string
		tokens []string
	}{
		{`\frac{a+b}{c+d}`, []string{"(a + b)", "(c + d)", "/"}},
		{`\frac{1}{\frac{x+y}{z}}`, []string{"x + y", "z", "/"}},
		{`\begin{bmatrix}a&b\\c&d\end{bmatrix}`, []string{"[a, b; c, d]"}},
		{`\begin{cases}x^2 & x>0\\-x & x\leq 0\end{cases}`, []string{"cases", "x²", "x > 0", "x ≤ 0", ";"}},
		{`\begin{aligned}a&=b+c\\d&=e\end{aligned}`, []string{"a", "= b + c", ";", "= e"}},
		{`e^{x^2}`, []string{"^{x²}"}},
		{`\begin{bmatrix}\frac{a}{b}&\begin{pmatrix}x\\y\end{pmatrix}\end{bmatrix}`, []string{"/", "x; y"}},
	} {
		got, ok := linearTeX(tc.source)
		if !ok {
			t.Errorf("cannot linearize %q: %q", tc.source, got)
			continue
		}
		for _, s := range tc.tokens {
			if !strings.Contains(got, s) {
				t.Errorf("%q => %q lacks %q", tc.source, got, s)
			}
		}
	}
}

func TestTeXAlwaysSingleLineInline(t *testing.T) {
	for _, source := range []string{`\dfrac{a}{b}`, `\displaystyle\sum_{i=1}^n i`, `e^{x^2}`, `\begin{bmatrix}a&b\\c&d\end{bmatrix}`} {
		got := renderTeXMath(source, true, 0)
		if got.status != texSuccess || strings.Contains(got.text, "\n") {
			t.Fatalf("%s: %+v", source, got)
		}
	}
}

func TestTeXInlineOverline(t *testing.T) {
	for _, tc := range []struct{ source, want string }{
		{`a_{ij} = \overline{a_{ji}}`, `aᵢⱼ = overline(aⱼᵢ)`},
		{`a_{ii} = \overline{a_{ii}}`, `aᵢᵢ = overline(aᵢᵢ)`},
		{`\overline{x}`, "x̅"},
		{`\overline{a+b}`, `overline(a + b)`},
		{`\overline{\frac{a+b}{c}}`, `overline((a + b)/(c))`},
		{`\overline{\overline{x}}`, `overline(x̅)`},
		{`\overline{a+b}^{T}`, `overline(a + b)ᵀ`},
		{`\text{\overline{x}}`, `\overline{x}`},
	} {
		got := renderTeXMath(tc.source, true, 0)
		if got.status != texSuccess || got.text != tc.want {
			t.Errorf("%q => %+v; want %q", tc.source, got, tc.want)
		}
	}
	if got := renderTeXMath(`\overline{a_{ji}}`, false, 80); got.status != texSuccess || !strings.Contains(got.text, "\n") {
		t.Fatalf("block overline lost 2D layout: %+v", got)
	}
	if got := renderTeXMath(`\overline{a+b+c}`, false, 2); got.status != texOverflow || got.text != "overline(a + b + c)" {
		t.Fatalf("narrow overline lost scope: %+v", got)
	}
}

func TestTeXLimitsAndMalformed(t *testing.T) {
	for _, source := range []string{strings.Repeat("{", 33) + "x" + strings.Repeat("}", 33), strings.Repeat("x", maxTeXBytes+1), `\unknown{x}`, `\begin{tikzpicture}x\end{tikzpicture}`, "x\x1b[31m"} {
		if got := renderTeXMath(source, false, 80); got.status != texUnsupported {
			t.Fatalf("accepted invalid source: %+v", got)
		}
	}
	if got := renderTeXMath(`\frac{x}{`, false, 80); got.status != texIncomplete {
		t.Fatalf("incomplete status: %+v", got)
	}
	if got := renderTeXMath(`\frac{abcdefghijklmnop}{2}`, false, 8); got.status != texOverflow || !strings.Contains(got.text, "abcdefghijklmnop") {
		t.Fatalf("lost wide fraction: %+v", got)
	}
}

func TestTeXDoesNotSilentlyDropUnsupportedTokens(t *testing.T) {
	for _, src := range []string{`\nexists x`, `\middle|`, `\text`, `\left(x`, `\right)`} {
		if got := renderTeXMath(src, false, 80); got.status == texSuccess {
			t.Errorf("silently accepted %q => %q", src, got.text)
		}
	}
}

func TestTeXBareAlignedRowsLinearize(t *testing.T) {
	got, ok := linearTeX(`a&=b+c\\d&=e`)
	if !ok || !strings.Contains(got, ";") || !strings.Contains(got, "= e") {
		t.Fatalf("%q %v", got, ok)
	}
}

func TestTeXInvisibleDelimiterInMatrix(t *testing.T) {
	got, ok := linearTeX(`\begin{matrix}\left.x\right]&b\\c&d\end{matrix}`)
	if !ok || !strings.Contains(got, "x], b; c, d") {
		t.Fatalf("lost rows: %q %v", got, ok)
	}
}

func TestTeXLinearAlignmentDoesNotInventCommas(t *testing.T) {
	got, ok := linearTeX(`\begin{aligned}a&=b+c\\d&=e\end{aligned}`)
	if !ok || got != "a = b + c; d = e" {
		t.Fatalf("alignment changed meaning: %q", got)
	}
}

func TestTeXInlineDoesNotRewriteLiteralText(t *testing.T) {
	got := renderTeXMath(`\text{\dfrac}`, true, 0)
	if got.status != texSuccess || got.text != `\dfrac` {
		t.Fatalf("text was rewritten: %+v", got)
	}
}

func TestTeXWidths(t *testing.T) {
	for _, source := range []string{`\text{输入}`, `\text{é}`, `\alpha`, `x_1`} {
		if !texWidthsCompatible(source) {
			t.Errorf("unnecessarily refused %q", source)
		}
	}
	if texWidthsCompatible(`\text{👩‍💻}`) {
		t.Fatal("upstream width model cannot lay out ZWJ emoji")
	}
}
