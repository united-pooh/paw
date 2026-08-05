package bubble

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestAssistantMarkerUsesExternalGutterAndStableBodyColumn(t *testing.T) {
	rendered := ansi.Strip(renderEntry(transcriptEntry{
		kind:  entryAssistant,
		title: "assistant",
		body:  "first line\nsecond line",
	}, 60))
	lines := strings.Split(rendered, "\n")
	if len(lines) != 2 {
		t.Fatalf("rendered lines = %#v, want two lines", lines)
	}
	if !strings.HasPrefix(lines[0], "✦ ") {
		t.Fatalf("first line = %q, want external marker gutter", lines[0])
	}
	if strings.Contains(rendered, "agent >") || strings.Contains(rendered, "you >") {
		t.Fatalf("rendered assistant = %q, legacy labels must be absent", rendered)
	}
	firstText := terminalCellWidth(strings.Split(lines[0], "first line")[0])
	secondText := terminalCellWidth(strings.Split(lines[1], "second line")[0])
	if firstText != 2 || secondText != 2 {
		t.Fatalf("body columns = first:%d second:%d, want both 2: lines=%q", firstText, secondText, lines)
	}
	if strings.Contains(lines[1], "✦") {
		t.Fatalf("continuation line contains marker: %q", lines[1])
	}
}

func TestAssistantMarkerDoesNotChangeRenderedBodyWidth(t *testing.T) {
	const width = 60
	rendered := ansi.Strip(renderEntry(transcriptEntry{
		kind: entryAssistant,
		body: "answer",
	}, width))
	line := strings.Split(rendered, "\n")[0]
	if got := terminalCellWidth(line); got != width {
		t.Fatalf("rendered width = %d, want %d: %q", got, width, line)
	}
}
