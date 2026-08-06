package bubble

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestRenderInlineMarkdownSupportsThemeStyledEmphasis(t *testing.T) {
	input := "**bold** __also bold__ *italic* _also italic_ ==highlight== and `**literal**`"
	rendered := renderInlineMarkdown(input)
	visible := ansi.Strip(rendered)
	// The code span intentionally preserves its literal ** markers; verify the
	// prose portion separately so this test checks Markdown parsing rather than
	// treating code contents as Markdown.
	prose := ansi.Strip(renderInlineMarkdown("**bold** __also bold__ *italic* _also italic_ ==highlight== and "))
	for _, marker := range []string{"**", "__", "=="} {
		if strings.Contains(prose, marker) {
			t.Fatalf("visible markdown retained marker %q: %q", marker, prose)
		}
	}
	for _, want := range []string{"bold", "also bold", "italic", "also italic", "highlight", "**literal**"} {
		if !strings.Contains(visible, want) {
			t.Fatalf("visible markdown missing %q: %q", want, visible)
		}
	}
	if !markdownBoldStyle.GetBold() {
		t.Fatal("bold style must enable bold attribute")
	}
	if !markdownItalicStyle.GetItalic() {
		t.Fatal("italic style must enable italic attribute")
	}
	if markdownHighlightStyle.GetBackground() == nil {
		t.Fatal("highlight style must provide a theme background")
	}
}

func TestRenderInlineMarkdownKeepsUnclosedEmphasisLiteral(t *testing.T) {
	for _, input := range []string{
		"keep **unfinished",
		"keep *unfinished",
		"keep ==unfinished",
		"https://example.com/a_b",
	} {
		got := ansi.Strip(renderInlineMarkdown(input))
		if got != input {
			t.Fatalf("renderInlineMarkdown(%q) = %q, want literal input", input, got)
		}
	}
}

func TestTranscriptAssistantBodyRendersInlineMarkdown(t *testing.T) {
	input := "**bold** ==highlight== `inline code`"
	output := renderAssistantBodyWithCitations(input, 80, nil)
	visible := strings.TrimSpace(ansi.Strip(output))
	wantVisible := "bold highlight  inline code"
	if visible != wantVisible {
		t.Fatalf("transcript visible text = %q, want %q", visible, wantVisible)
	}
	if strings.Contains(visible, "**") || strings.Contains(visible, "==") {
		t.Fatalf("transcript retained Markdown markers: %q", visible)
	}
	if !strings.Contains(output, markdownBoldStyle.Render("bold")) {
		t.Fatalf("transcript did not use the bold style: %q", output)
	}
	if !strings.Contains(output, markdownHighlightStyle.Render("highlight")) {
		t.Fatalf("transcript did not use the highlight style: %q", output)
	}
	if !strings.Contains(output, markdownCodeStyle.Render("inline code")) {
		t.Fatalf("transcript did not use the inline code style: %q", output)
	}
}
