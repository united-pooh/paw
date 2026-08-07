package bubble

import (
	"encoding/json"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"paw/internal/ui"
)

func TestOnToolOutputForwardsMessage(t *testing.T) {
	var got []tea.Msg
	u := New()
	u.sendMsg = func(msg tea.Msg) { got = append(got, msg) }

	if err := u.OnToolOutput(ui.ToolOutputEvent{ToolUseID: "call-1", Name: "Bash", Stream: "stderr", Chunk: "warning\n"}); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("forwarded messages = %d", len(got))
	}
	msg, ok := got[0].(toolOutputMsg)
	if !ok || msg.ToolUseID != "call-1" || msg.Stream != "stderr" || msg.Chunk != "warning\n" {
		t.Fatalf("message = %#v", got[0])
	}
}

func TestToolOutputTracksSeparateStreamsAndRefreshesEntry(t *testing.T) {
	m := newTestModel(&fakeRunner{})
	next, _ := m.Update(toolCallMsg(ui.ToolCallEvent{ID: "call-1", Name: "Bash", Input: json.RawMessage(`{"command":"run"}`)}))
	m = next.(appModel)
	if len(m.transcript) != 1 || m.transcript[0].toolStatus != "running" {
		t.Fatalf("running transcript = %#v", m.transcript)
	}
	version := m.transcript[0].version

	for _, event := range []ui.ToolOutputEvent{
		{ToolUseID: "call-1", Name: "Bash", Stream: "stdout", Chunk: "out-1\n"},
		{ToolUseID: "call-1", Name: "Bash", Stream: "stdout", Chunk: "out-2\n"},
		{ToolUseID: "call-1", Name: "Bash", Stream: "stderr", Chunk: "err-1\n"},
	} {
		next, _ = m.Update(toolOutputMsg(event))
		m = next.(appModel)
	}
	entry := m.transcript[0]
	if entry.toolStdout != "out-1\nout-2\n" || entry.toolStderr != "err-1\n" {
		t.Fatalf("streams were not accumulated separately: stdout=%q stderr=%q", entry.toolStdout, entry.toolStderr)
	}
	if entry.version <= version {
		t.Fatalf("entry version did not change: before=%d after=%d", version, entry.version)
	}
	if rendered := ansi.Strip(renderEntry(entry, 100)); !strings.Contains(rendered, "stdout:") || !strings.Contains(rendered, "stderr:") || !strings.Contains(rendered, "out-2") || !strings.Contains(rendered, "err-1") {
		t.Fatalf("live output not rendered separately: %q", rendered)
	}
}

func TestToolOutputResultPreservesOutputAndStatusFlags(t *testing.T) {
	m := newTestModel(&fakeRunner{})
	next, _ := m.Update(toolCallMsg(ui.ToolCallEvent{ID: "call-1", Name: "Bash"}))
	m = next.(appModel)
	next, _ = m.Update(toolOutputMsg(ui.ToolOutputEvent{ToolUseID: "call-1", Stream: "stdout", Chunk: "partial\n"}))
	m = next.(appModel)
	next, _ = m.Update(toolResultMsg(ui.ToolResultEvent{ToolUseID: "call-1", Name: "Bash", IsError: true, Content: "interrupted: canceled\n[truncated]"}))
	m = next.(appModel)

	entry := m.transcript[0]
	if entry.toolStatus != "error" || !entry.isError || !entry.toolInterrupted || !entry.toolTruncated {
		t.Fatalf("result flags = %#v", entry)
	}
	if entry.toolStdout != "partial\n" || entry.toolResult == "" {
		t.Fatalf("result lost live output/result: %#v", entry)
	}
	rendered := ansi.Strip(renderEntry(entry, 100))
	if !strings.Contains(rendered, "partial") || !strings.Contains(rendered, "interrupted") || !strings.Contains(rendered, "truncated") {
		t.Fatalf("status details missing: %q", rendered)
	}
}

func TestUnknownToolOutputDoesNotPanicOrCreateEntry(t *testing.T) {
	m := newTestModel(&fakeRunner{})
	next, _ := m.Update(toolOutputMsg(ui.ToolOutputEvent{ToolUseID: "missing", Stream: "stdout", Chunk: "ignored"}))
	m = next.(appModel)
	if len(m.transcript) != 0 {
		t.Fatalf("unknown output changed transcript: %#v", m.transcript)
	}
}

func TestToolOutputRenderCacheKeyIncludesLiveStreams(t *testing.T) {
	entry := transcriptEntry{kind: entryTool, title: "tool", toolName: "Bash", toolStatus: "running", toolStdout: "one"}
	first := transcriptRenderKey(entry, 80, entry.createdAt)
	entry.toolStdout = "two"
	second := transcriptRenderKey(entry, 80, entry.createdAt)
	if first == second {
		t.Fatalf("render cache key did not change for stdout: %#v", first)
	}
	entry.toolStdout = "one"
	entry.toolStderr = "err"
	third := transcriptRenderKey(entry, 80, entry.createdAt)
	if first == third {
		t.Fatalf("render cache key did not change for stderr")
	}
}

func TestToolOutputPreservesScrollOffset(t *testing.T) {
	m := newTestModel(&fakeRunner{})
	m.viewport.Width = 80
	m.viewport.Height = 2
	for i := 0; i < 8; i++ {
		m.addEntry(transcriptEntry{kind: entrySystem, title: "line", body: "content"})
	}
	m.viewport.SetYOffset(2)
	before := m.viewport.YOffset
	next, _ := m.Update(toolCallMsg(ui.ToolCallEvent{ID: "call-1", Name: "Bash"}))
	m = next.(appModel)
	m.viewport.SetYOffset(before)
	next, _ = m.Update(toolOutputMsg(ui.ToolOutputEvent{ToolUseID: "call-1", Stream: "stdout", Chunk: "live"}))
	m = next.(appModel)
	if m.viewport.YOffset != before {
		t.Fatalf("scroll offset changed: before=%d after=%d", before, m.viewport.YOffset)
	}
}
