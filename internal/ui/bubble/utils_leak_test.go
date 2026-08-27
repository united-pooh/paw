package bubble

import (
	"strings"
	"testing"
)

func TestStripEmbeddedToolUseJSONRepairsInvalidEscapes(t *testing.T) {
	leak := "{\"type\":\"tool_use\",\"id\":\"call_1\",\"name\":\"Grep\"," +
		"\"input\":{\"pattern\":\"\\\\.`\",\"path\":\"internal/theme\"}}"

	got := stripEmbeddedToolUseJSON("前文 " + leak + " 后文")
	if strings.Contains(got, "tool_use") || strings.Contains(got, "Grep") {
		t.Fatalf("payload not stripped after repair: %q", got)
	}
	if !strings.Contains(got, "前文") || !strings.Contains(got, "后文") {
		t.Fatalf("prose around payload lost: %q", got)
	}
}

func TestStripToolUseFencesRepairsInvalidEscapes(t *testing.T) {
	leak := "```\n{\"type\":\"tool_use\",\"id\":\"call_1\",\"name\":\"Grep\"," +
		"\"input\":{\"pattern\":\"\\\\.`\",\"path\":\"internal/theme\"}}\n```"

	got := stripToolUseFences(leak)
	if strings.TrimSpace(got) != "" {
		t.Fatalf("fenced payload not stripped after repair: %q", got)
	}
}
