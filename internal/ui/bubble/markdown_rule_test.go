package bubble

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestMarkdownThematicBreakRendersTerminalDivider(t *testing.T) {
	const width = 24
	rendered := ansi.Strip(renderMarkdown("before\n\n---\n\nafter", width))
	lines := strings.Split(rendered, "\n")
	if len(lines) != 5 {
		t.Fatalf("rendered lines = %#v", lines)
	}
	if lines[2] != strings.Repeat("─", width) {
		t.Fatalf("divider = %q, want width %d", lines[2], width)
	}
	if strings.Contains(rendered, "---") {
		t.Fatalf("Markdown marker leaked: %q", rendered)
	}
}

func TestMarkdownThematicBreakRequiresMatchingMarkers(t *testing.T) {
	for _, valid := range []string{"---", "***", "___", "- - -"} {
		if !isMarkdownThematicBreak(valid) {
			t.Errorf("%q was not recognized", valid)
		}
	}
	for _, invalid := range []string{"--", "-*-", "--- text", "- item"} {
		if isMarkdownThematicBreak(invalid) {
			t.Errorf("%q was recognized unexpectedly", invalid)
		}
	}
}
