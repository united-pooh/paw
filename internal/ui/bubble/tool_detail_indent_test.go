package bubble

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestToolDetailLinesPreserveOrdinaryOutputIndentation(t *testing.T) {
	// Read file contents and Bash output from commands such as sed or go test
	// share this rendering path. Runtime sanitization expands tabs first, and
	// the renderer must preserve the resulting source/output indentation after
	// its two-cell presentation gutter.
	content := sanitizeTerminalText("package example\n\treturn 1\n        nested")
	rendered := ansi.Strip(renderToolDetailLines(strings.Split(content, "\n"), 40))
	lines := strings.Split(rendered, "\n")
	if len(lines) != 3 {
		t.Fatalf("rendered line count = %d, want 3: %q", len(lines), rendered)
	}
	for index, wantPrefix := range []string{
		"  package example",
		"      return 1",
		"          nested",
	} {
		if !strings.HasPrefix(lines[index], wantPrefix) {
			t.Fatalf("line %d = %q, want prefix %q", index, lines[index], wantPrefix)
		}
	}
}
