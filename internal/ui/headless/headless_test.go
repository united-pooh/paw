package headless

import (
	"bytes"
	uiiface "gocode/internal/ui"
	"testing"
)

func TestOnDoneAddsSingleTrailingNewlineAfterDeltas(t *testing.T) {
	var out bytes.Buffer
	output := New(&out)

	if err := output.OnAssistantDelta("he"); err != nil {
		t.Fatalf("OnAssistantDelta() error = %v", err)
	}
	if err := output.OnAssistantDelta("llo"); err != nil {
		t.Fatalf("OnAssistantDelta() error = %v", err)
	}
	if err := output.OnDone(); err != nil {
		t.Fatalf("OnDone() error = %v", err)
	}

	if got := out.String(); got != "hello\n" {
		t.Fatalf("out.String() = %q, want %q", got, "hello\n")
	}
}

func TestOnDoneWithoutOutputDoesNotWriteBlankLine(t *testing.T) {
	var out bytes.Buffer
	output := New(&out)

	if err := output.OnDone(); err != nil {
		t.Fatalf("OnDone() error = %v", err)
	}

	if got := out.String(); got != "" {
		t.Fatalf("out.String() = %q, want empty", got)
	}
}

func TestToolEventsWriteReadableLines(t *testing.T) {
	var out bytes.Buffer
	output := New(&out)

	if err := output.OnToolCall(uiiface.ToolCallEvent{
		Name:  "Read",
		Input: []byte(`{"file_path":"go.mod"}`),
	}); err != nil {
		t.Fatalf("OnToolCall() error = %v", err)
	}
	if err := output.OnToolResult(uiiface.ToolResultEvent{
		Name:    "Read",
		Content: "module gocode",
	}); err != nil {
		t.Fatalf("OnToolResult() error = %v", err)
	}

	want := "[tool] Read {\"file_path\":\"go.mod\"}\n[tool-result] Read ok: module gocode\n"
	if got := out.String(); got != want {
		t.Fatalf("out.String() = %q, want %q", got, want)
	}
}

func TestToolCallStartsOnNewLineAfterAssistantOutput(t *testing.T) {
	var out bytes.Buffer
	output := New(&out)

	if err := output.OnAssistantDelta("hello"); err != nil {
		t.Fatalf("OnAssistantDelta() error = %v", err)
	}
	if err := output.OnToolCall(uiiface.ToolCallEvent{
		Name:  "Bash",
		Input: []byte(`{"command":"pwd"}`),
	}); err != nil {
		t.Fatalf("OnToolCall() error = %v", err)
	}

	want := "hello\n[tool] Bash {\"command\":\"pwd\"}\n"
	if got := out.String(); got != want {
		t.Fatalf("out.String() = %q, want %q", got, want)
	}
}
