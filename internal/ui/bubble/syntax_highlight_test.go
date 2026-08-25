package bubble

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
)

func enableSyntaxColor(t *testing.T) {
	t.Helper()
	previous := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(previous) })
}

func TestSyntaxLanguageForHintRecognizesToolFileTargets(t *testing.T) {
	cases := map[string]string{
		"main.go":     "go",
		"main.py":     "py",
		"app.tsx":     "tsx",
		"data.json":   "json",
		"deploy.sh":   "sh",
		"query.sql":   "sql",
		"unknown.xyz": "",
	}
	for hint, want := range cases {
		if got := syntaxLanguageFromText("", hint); got != want {
			t.Errorf("syntaxLanguageFromText(%q) = %q, want %q", hint, got, want)
		}
	}
}

func TestHighlightSyntaxLineSupportsMultipleLanguages(t *testing.T) {
	enableSyntaxColor(t)
	cases := []struct {
		name     string
		language string
		line     string
		keywords []string
	}{
		{"go", "go", "func main() { return 42 }", []string{"func", "return", "42"}},
		{"python", "py", "def main(): return True", []string{"def", "return", "True"}},
		{"typescript", "ts", "const ready: boolean = true", []string{"const", "boolean", "true"}},
		{"rust", "rs", "fn main() { let ready = true; }", []string{"fn", "let", "true"}},
		{"sql", "sql", "SELECT id FROM users WHERE active = 1", []string{"SELECT", "FROM", "WHERE", "1"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ansi.Strip(highlightToolDetailLine(tc.line, tc.language))
			if got != tc.line {
				t.Fatalf("stripped highlight = %q, want original %q", got, tc.line)
			}
			rendered := highlightToolDetailLine(tc.line, tc.language)
			if !strings.Contains(rendered, "\x1b[") {
				t.Fatalf("rendered %q has no ANSI styling", rendered)
			}
			for _, keyword := range tc.keywords {
				if !strings.Contains(ansi.Strip(rendered), keyword) {
					t.Errorf("rendered %q lost token %q", rendered, keyword)
				}
			}
		})
	}
}

func TestUnknownLanguageFallsBackToOriginalText(t *testing.T) {
	const line = "mystery keyword 123"
	got := highlightToolDetailLine(line, "unknown-language")
	if got != line {
		t.Fatalf("unknown language changed line: got %q, want %q", got, line)
	}
}

func TestToolDetailHighlightPreservesDiffBackgroundAndLineNumbers(t *testing.T) {
	enableSyntaxColor(t)
	lines := []string{"1 + │ func main() {", "2 + │     return 42", "3   │ }"}
	rendered := renderToolDetailLinesWithHint(lines, 60, "main.go")
	stripped := ansi.Strip(rendered)
	for _, want := range []string{"1 + │ func main() {", "2 + │     return 42", "3   │ }"} {
		if !strings.Contains(stripped, want) {
			t.Fatalf("stripped diff = %q, want %q", stripped, want)
		}
	}
	if !strings.Contains(rendered, "\x1b[") {
		t.Fatal("highlighted diff has no ANSI styling")
	}
	for _, line := range strings.Split(stripped, "\n") {
		if got := ansi.StringWidth(line); got > 60 {
			t.Fatalf("diff line width = %d, want <= 60: %q", got, line)
		}
	}
}

func TestToolDetailHighlightUsesExplicitTargetForDiff(t *testing.T) {
	enableSyntaxColor(t)
	lines := []string{"1 + │ def handler():", "2 + │     return True"}
	got := renderToolDetailLinesWithHint(lines, 50, "handler.py")
	if !strings.Contains(ansi.Strip(got), "def handler()") {
		t.Fatalf("python diff lost source: %q", got)
	}
	if !strings.Contains(got, "\x1b[") {
		t.Fatal("python diff did not receive syntax styling")
	}
}

func TestToolDetailHighlightKeepsUnknownExtensionUnstyled(t *testing.T) {
	lines := []string{"1 + │ keyword 123", "2 + │ another value"}
	got := renderToolDetailLinesWithHint(lines, 50, "file.unknown")
	if ansi.Strip(got) == "" {
		t.Fatal("unknown extension unexpectedly removed content")
	}
	if strings.Contains(got, "\x1b[1;") || strings.Contains(got, "\x1b[3;") {
		t.Fatalf("unknown extension added syntax emphasis: %q", got)
	}
}

func TestSyntaxLanguageFromLinesFindsKnownExtension(t *testing.T) {
	if got := syntaxLanguageFromLines([]string{"path: docs/readme.md", "content"}); got != "md" {
		t.Fatalf("markdown should resolve to md language, got %q", got)
	}
	if got := syntaxLanguageFromLines([]string{"file: main.go", "func main() {}"}); got != "go" {
		t.Fatalf("syntaxLanguageFromLines = %q, want go", got)
	}
}

// markdown 代码块按 fence 语言做 token 级语法高亮；无语言标签时保持纯文本。
func TestCodeBlockPanelAppliesSyntaxHighlighting(t *testing.T) {
	enableSyntaxColor(t)
	const src = "package main\n\nfunc main() { return }\n"
	colored := renderCodeBlock("go", src, 80)
	plain := renderCodeBlock("", src, 80)
	if colored == plain {
		t.Fatal("go fence did not change code block rendering (no syntax highlighting)")
	}
	stripped := ansi.Strip(colored)
	if !strings.Contains(stripped, "package main") || !strings.Contains(stripped, "func main() { return }") {
		t.Fatalf("highlighted code block lost content:\n%s", stripped)
	}
}
