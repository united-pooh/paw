package bubble

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"paw/internal/ui"
)

func TestStreamLineBufferCommitsOnlyCompleteLines(t *testing.T) {
	var buffer streamLineBuffer
	if got := buffer.Push("hel", 5); got != "" {
		t.Fatalf("first push = %q, want hidden tail", got)
	}
	if got := buffer.Push("lo", 5); got != "" {
		t.Fatalf("second push = %q, want held final grapheme", got)
	}
	if got := buffer.Push("!", 5); got != "hello" {
		t.Fatalf("filled line = %q, want hello", got)
	}
	if got := buffer.Flush(5); got != "!" {
		t.Fatalf("flush = %q, want !", got)
	}
}

func TestStreamLineBufferCommitsHardNewlinesAndBlankLines(t *testing.T) {
	var buffer streamLineBuffer
	if got := buffer.Push("one\n\ntwo", 20); got != "one\n\n" {
		t.Fatalf("push = %q, want completed hard lines", got)
	}
	if got := buffer.Flush(20); got != "two" {
		t.Fatalf("flush = %q, want two", got)
	}
}

func TestStreamLineBufferUsesGraphemeCellWidth(t *testing.T) {
	var buffer streamLineBuffer
	if got := buffer.Push("你🙂", 4); got != "" {
		t.Fatalf("push = %q, want held final grapheme", got)
	}
	if got := buffer.Push("好", 4); got != "你🙂" {
		t.Fatalf("wide line = %q, want first four cells", got)
	}
	if got := buffer.Flush(4); got != "好" {
		t.Fatalf("flush = %q, want 好", got)
	}
}

func TestStreamLineBufferUsesIndicConjunctCellWidth(t *testing.T) {
	var buffer streamLineBuffer
	if got := buffer.Push("हिन्दीx!", 4); got != "हिन्दीx" {
		t.Fatalf("push = %q, want Hindi plus x in four cells", got)
	}
	if got := buffer.Flush(4); got != "!" {
		t.Fatalf("flush = %q, want held tail", got)
	}
}

func TestStreamLineBufferKeepsSplitGraphemeTogether(t *testing.T) {
	var buffer streamLineBuffer
	if got := buffer.Push("e", 1); got != "" {
		t.Fatalf("base push = %q, want hidden cluster", got)
	}
	if got := buffer.Push("\u0301x", 1); got != "e\u0301" {
		t.Fatalf("combining push = %q, want combined grapheme", got)
	}
	if got := buffer.Flush(1); got != "x" {
		t.Fatalf("flush = %q, want x", got)
	}

	var emojiBuffer streamLineBuffer
	if got := emojiBuffer.Push("👩", 2); got != "" {
		t.Fatalf("emoji base = %q, want hidden cluster", got)
	}
	if got := emojiBuffer.Push("\u200d💻x", 2); got != "👩‍💻" {
		t.Fatalf("emoji sequence = %q, want joined emoji", got)
	}
}

func TestStreamLineBufferSanitizesSplitTerminalSequences(t *testing.T) {
	var buffer streamLineBuffer
	if got := buffer.Push("\x1b[31", 20); got != "" {
		t.Fatalf("partial CSI = %q, want hidden", got)
	}
	if got := buffer.Push("mred\x1b[0m\n", 20); got != "red\n" {
		t.Fatalf("CSI output = %q, want red newline", got)
	}
	if got := buffer.Push("\x1b]0;owned", 20); got != "" {
		t.Fatalf("partial OSC = %q, want hidden", got)
	}
	if got := buffer.Push("\x07safe\n", 20); got != "safe\n" {
		t.Fatalf("OSC output = %q, want safe newline", got)
	}
	if got := buffer.Push("\x1bPpayload", 20); got != "" {
		t.Fatalf("partial DCS = %q, want hidden", got)
	}
	if got := buffer.Push("\x1b\\done\n", 20); got != "done\n" {
		t.Fatalf("DCS output = %q, want done newline", got)
	}

	var c1Buffer streamLineBuffer
	if got := c1Buffer.Push("\u009b31", 20); got != "" {
		t.Fatalf("partial C1 CSI = %q, want hidden", got)
	}
	if got := c1Buffer.Push("mred\u009b0m\n", 20); got != "red\n" {
		t.Fatalf("C1 CSI output = %q, want red newline", got)
	}
	if got := c1Buffer.Push("\u009d0;owned", 20); got != "" {
		t.Fatalf("partial C1 OSC = %q, want hidden", got)
	}
	if got := c1Buffer.Push("\u009csafe\n", 20); got != "safe\n" {
		t.Fatalf("C1 OSC output = %q, want safe newline", got)
	}
}

func TestStreamLineBufferDropsControlsAndNormalizesWhitespace(t *testing.T) {
	var buffer streamLineBuffer
	got := buffer.Push("a\x00\b\x07\tb\r", 20)
	if got != "" {
		t.Fatalf("push = %q, want buffered line", got)
	}
	got = buffer.Push("\nc\rd\n", 20)
	if got != "a   b\nc\nd\n" {
		t.Fatalf("normalized output = %q", got)
	}
}

func TestStreamLineBufferReplacesInvalidUTF8AcrossChunks(t *testing.T) {
	var buffer streamLineBuffer
	if got := buffer.Push(string([]byte{0xe4, 0xbd}), 20); got != "" {
		t.Fatalf("incomplete UTF-8 = %q, want hidden", got)
	}
	if got := buffer.Push(string([]byte{0xa0, '\n'}), 20); got != "你\n" {
		t.Fatalf("completed UTF-8 = %q, want 你 newline", got)
	}

	var invalid streamLineBuffer
	if got := invalid.Push(string([]byte{0xff, '\n'}), 20); got != "�\n" {
		t.Fatalf("invalid UTF-8 = %q, want replacement", got)
	}
}

func TestStreamLineBufferResizeReleasesNewlyCompleteLine(t *testing.T) {
	var buffer streamLineBuffer
	if got := buffer.Push("abcdef", 10); got != "" {
		t.Fatalf("push = %q, want hidden tail", got)
	}
	if got := buffer.Resize(5); got != "abcde" {
		t.Fatalf("resize = %q, want abcde", got)
	}
	if got := buffer.Flush(5); got != "f" {
		t.Fatalf("flush = %q, want f", got)
	}
}

func TestSanitizeTerminalTextRemovesEscapesAndExpandsTabs(t *testing.T) {
	input := "a\tb\x1b[31m red\x1b[0m\r\nnext\x00"
	got := sanitizeTerminalText(input)
	if got != "a   b red\nnext" {
		t.Fatalf("sanitized = %q", got)
	}
	if strings.Contains(got, "\x1b") || ansi.StringWidth(got) == 0 {
		t.Fatalf("sanitized output leaked an escape sequence: %q", got)
	}
}

func TestAssistantStreamRendersMarkdownForEachStableLine(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = 80
	model.height = 20
	model.relayout()

	next, _ := model.Update(assistantDeltaMsg("**bo"))
	model = next.(appModel)
	if strings.Contains(ansi.Strip(model.viewport.View()), "**bo") {
		t.Fatalf("viewport leaked incomplete markdown tail: %q", model.viewport.View())
	}

	next, _ = model.Update(assistantDeltaMsg("ld**\n"))
	model = next.(appModel)
	entry := model.transcript[model.activeAssistant]
	if entry.renderMode != transcriptRenderFormatted {
		t.Fatalf("stream render mode = %v, want formatted", entry.renderMode)
	}
	streamed := ansi.Strip(renderEntry(entry, 80))
	if !strings.Contains(streamed, "bold") || strings.Contains(streamed, "**bold**") {
		t.Fatalf("streamed entry = %q, want formatted markdown before done", streamed)
	}
	viewport := ansi.Strip(model.viewport.View())
	if !strings.Contains(viewport, "bold") || strings.Contains(viewport, "**bold**") {
		t.Fatalf("viewport = %q, want formatted markdown before done", viewport)
	}

	next, _ = model.Update(assistantDeltaMsg("\n- item\n"))
	model = next.(appModel)
	viewport = ansi.Strip(model.viewport.View())
	if !strings.Contains(viewport, "bold") || !strings.Contains(viewport, "item") {
		t.Fatalf("viewport after second stable line = %q, want both rendered lines", viewport)
	}

	next, _ = model.Update(doneMsg{})
	model = next.(appModel)
	entry = lastEntryOfKind(t, model.transcript, entryAssistant)
	if entry.renderMode != transcriptRenderFormatted {
		t.Fatalf("final render mode = %v", entry.renderMode)
	}
}

func TestAssistantStreamErrorFlushesPlainTailAndResets(t *testing.T) {
	model := newTestModel(&fakeRunner{})

	next, _ := model.Update(assistantDeltaMsg("partial **markdown"))
	model = next.(appModel)
	next, _ = model.Update(turnFinishedMsg{err: errors.New("stream failed")})
	model = next.(appModel)

	entry := lastEntryOfKind(t, model.transcript, entryAssistant)
	if entry.body != "partial **markdown" || entry.renderMode != transcriptRenderPlain {
		t.Fatalf("aborted assistant entry = %#v", entry)
	}
	if model.assistantStream.HasContent() || model.activeAssistant != -1 {
		t.Fatalf("assistant stream was not reset")
	}
	rendered := ansi.Strip(renderEntry(entry, 80))
	if !strings.Contains(rendered, "**markdown") {
		t.Fatalf("aborted entry = %q, want literal incomplete markdown", rendered)
	}

	next, _ = model.Update(assistantDeltaMsg("next\n"))
	model = next.(appModel)
	next, _ = model.Update(doneMsg{})
	model = next.(appModel)
	nextEntry := lastEntryOfKind(t, model.transcript, entryAssistant)
	if nextEntry.body != "next\n" {
		t.Fatalf("next assistant entry = %#v, want isolated next turn", nextEntry)
	}
}

func TestToolBoundaryFlushesAssistantTail(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	next, _ := model.Update(assistantDeltaMsg("before tool"))
	model = next.(appModel)
	next, _ = model.Update(toolCallMsg(ui.ToolCallEvent{Name: "Read", Input: []byte(`{"file_path":"go.mod"}`)}))
	model = next.(appModel)

	entry := lastEntryOfKind(t, model.transcript, entryAssistant)
	if entry.body != "before tool" || entry.renderMode != transcriptRenderFormatted {
		t.Fatalf("assistant before tool = %#v", entry)
	}
	if model.assistantStream.HasContent() {
		t.Fatalf("assistant stream retained content across tool boundary")
	}
}

func TestThinkingTailFlushesBeforeAssistantStream(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	next, _ := model.Update(thinkingDeltaMsg("private plan"))
	model = next.(appModel)
	next, _ = model.Update(assistantDeltaMsg("answer"))
	model = next.(appModel)

	thinking := lastEntryOfKind(t, model.transcript, entryThinking)
	if thinking.body != "private plan" {
		t.Fatalf("thinking entry = %#v, want flushed tail", thinking)
	}
	if model.thinkingStream.HasContent() || model.activeThinking != -1 {
		t.Fatalf("thinking stream remained active after assistant transition")
	}
	assistant := lastEntryOfKind(t, model.transcript, entryAssistant)
	if assistant.body != "" || !model.assistantStream.HasContent() {
		t.Fatalf("assistant tail was not isolated: entry=%#v", assistant)
	}
}

func TestCanceledStreamFlushesPlainAndDoesNotLeak(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	next, _ := model.Update(assistantDeltaMsg("canceled **tail"))
	model = next.(appModel)
	next, _ = model.Update(doneMsg{})
	model = next.(appModel)
	completed := lastEntryOfKind(t, model.transcript, entryAssistant)
	if completed.renderMode != transcriptRenderFormatted {
		t.Fatalf("done assistant entry = %#v, want tentative formatted mode", completed)
	}
	next, _ = model.Update(turnFinishedMsg{err: context.Canceled})
	model = next.(appModel)

	entry := lastEntryOfKind(t, model.transcript, entryAssistant)
	if entry.body != "canceled **tail" || entry.renderMode != transcriptRenderPlain {
		t.Fatalf("canceled assistant entry = %#v", entry)
	}

	next, _ = model.Update(assistantDeltaMsg("fresh\n"))
	model = next.(appModel)
	next, _ = model.Update(doneMsg{})
	model = next.(appModel)
	fresh := lastEntryOfKind(t, model.transcript, entryAssistant)
	if fresh.body != "fresh\n" || strings.Contains(fresh.body, "canceled") {
		t.Fatalf("fresh assistant entry leaked canceled buffer: %#v", fresh)
	}
}

func TestSessionRestoreClearsIncompleteStream(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	next, _ := model.Update(assistantDeltaMsg("old hidden tail"))
	model = next.(appModel)
	next, _ = model.Update(sessionRestoredMsg{
		sessionID: "replacement",
		entries: []transcriptEntry{
			{kind: entryAssistant, title: "assistant", body: "history"},
		},
	})
	model = next.(appModel)

	if model.assistantStream.HasContent() || model.activeAssistant != -1 {
		t.Fatalf("session restore retained assistant stream state")
	}
	next, _ = model.Update(assistantDeltaMsg("new\n"))
	model = next.(appModel)
	next, _ = model.Update(doneMsg{})
	model = next.(appModel)
	entry := lastEntryOfKind(t, model.transcript, entryAssistant)
	if entry.body != "new\n" || strings.Contains(entry.body, "old hidden tail") {
		t.Fatalf("restored session leaked old stream tail: %#v", entry)
	}
}

func TestDynamicTranscriptRenderingSanitizesTerminalControls(t *testing.T) {
	entries := []transcriptEntry{
		{kind: entryAssistant, title: "assistant\x1b]0;title\x07", body: "safe\x1b[31m red\x1b[0m"},
		{kind: entryThinking, title: "thinking", body: "thought\x00\x07"},
		{kind: entryTool, title: "tool", body: "tool\x1bPpayload\x1b\\ result"},
		{kind: entrySystem, title: "system\x1b[2J", body: "system\x08 message"},
		{kind: entryError, title: "error", body: "error\x1b]52;c;owned\x07 text"},
		{
			kind:  entryAssistant,
			title: "assistant",
			body:  "citation",
			citations: []toolCitation{
				{name: "Read\x1b[2J", target: "go.mod\x1b]0;x\x07", status: "ok"},
			},
		},
	}
	rendered := renderTranscript(entries, 80, true)
	if strings.Contains(rendered, "\x1b]0;title") ||
		strings.Contains(rendered, "\x1b[31m red") ||
		strings.Contains(rendered, "\x1bPpayload") ||
		strings.Contains(rendered, "\x1b]52;") {
		t.Fatalf("rendered transcript leaked raw terminal controls: %q", rendered)
	}
	plain := ansi.Strip(rendered)
	for _, want := range []string{"safe red", "thought", "tool result", "system message", "error text", "Read", "go.mod"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("rendered transcript = %q, want %q", plain, want)
		}
	}
}

func TestWindowResizeReleasesNewlyCompleteAssistantLine(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = 120
	model.height = 24
	model.relayout()
	bodyWidth := model.streamingBodyWidth()
	text := strings.Repeat("x", bodyWidth-1)

	next, _ := model.Update(assistantDeltaMsg(text))
	model = next.(appModel)
	if got := model.transcript[len(model.transcript)-1].body; got != "" {
		t.Fatalf("assistant body before resize = %q, want hidden tail", got)
	}

	next, _ = model.Update(tea.WindowSizeMsg{Width: 60, Height: 24})
	model = next.(appModel)
	entry := lastEntryOfKind(t, model.transcript, entryAssistant)
	if entry.body == "" {
		t.Fatalf("resize did not release newly complete visual line")
	}
}

func lastEntryOfKind(t *testing.T, entries []transcriptEntry, kind entryKind) transcriptEntry {
	t.Helper()
	for idx := len(entries) - 1; idx >= 0; idx-- {
		if entries[idx].kind == kind {
			return entries[idx]
		}
	}
	t.Fatalf("no transcript entry of kind %v", kind)
	return transcriptEntry{}
}
