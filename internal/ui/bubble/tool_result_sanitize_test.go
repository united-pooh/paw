package bubble

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestToolResultDoesNotReintroduceTerminalHyperlinks(t *testing.T) {
	entry := transcriptEntry{
		kind:         entryTool,
		title:        "tool",
		toolName:     "Bash",
		toolStatus:   "ok",
		toolTarget:   "git pull origin dev",
		toolExpanded: true,
		toolResult:   "From \x1b]8;;https://github.com/united-pooh/go-code\x1b\\\x1b[1;4;38;2;125;207;255;4mhttps://github.com/united-pooh/go-code\x1b[0m\n",
	}

	result := toolResultForDisplay(entry)
	if result != "From https://github.com/united-pooh/go-code" {
		t.Fatalf("sanitized tool result = %q", result)
	}
	if strings.Contains(result, "]8;;") || strings.Contains(result, "38;2;") {
		t.Fatalf("terminal control payload leaked into tool result: %q", result)
	}

	rendered := renderEntry(entry, 100)
	plain := ansi.Strip(rendered)
	if strings.Contains(plain, "]8;;") || strings.Contains(plain, "38;2;") {
		t.Fatalf("terminal control payload leaked into rendered tool result: %q", plain)
	}
}
