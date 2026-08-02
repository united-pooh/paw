package bubble

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
	"paw/internal/loop"
	coremcp "paw/internal/mcp"
	"paw/internal/message"
	"paw/internal/model"
	"paw/internal/tool"
	toolmcp "paw/internal/tool/mcp"
	"paw/internal/ui"
)

func TestUpdateTodoToolCallUsesCompactDisplay(t *testing.T) {
	entry := transcriptEntry{
		kind:       entryTool,
		title:      "tool",
		toolName:   "update_todo",
		toolStatus: "running",
		toolInput: json.RawMessage(`{
            "explanation":"start build",
            "items":[
                {"id":"secret-internal-id","content":"Build page","status":"in_progress"},
                {"id":"tests","content":"Add tests","status":"pending"}
            ]
        }`),
		toolTarget: displayToolTarget("update_todo", json.RawMessage(`{"items":[]}`), ""),
	}
	rendered := ansi.Strip(renderEntry(entry, 100))
	if !strings.Contains(rendered, "Todo") || !strings.Contains(rendered, "update") {
		t.Fatalf("render = %q", rendered)
	}
	for _, forbidden := range []string{"secret-internal-id", "Build page", "pending", `"items"`} {
		if strings.Contains(rendered, forbidden) {
			t.Fatalf("render leaked %q: %q", forbidden, rendered)
		}
	}
}

func TestUpdateTodoToolResultSummary(t *testing.T) {
	updated := compactUpdateTodoResult(`{"accepted":true,"snapshot":{"items":[{"id":"a","content":"A","status":"completed"},{"id":"b","content":"B","status":"in_progress"}],"updated_at":"2026-08-02T10:00:00Z"}}`)
	if updated != "updated 1/2" {
		t.Fatalf("summary = %q", updated)
	}
	cleared := compactUpdateTodoResult(`{"accepted":true,"snapshot":{"items":[],"updated_at":"2026-08-02T10:00:00Z"}}`)
	if cleared != "cleared" {
		t.Fatalf("summary = %q", cleared)
	}
	if invalid := compactUpdateTodoResult(`{`); invalid != "updated" {
		t.Fatalf("invalid summary = %q", invalid)
	}
}

func TestUpdateTodoToolTrackKeepsRawInspectData(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	input := json.RawMessage(`{"items":[{"id":"build","content":"Build page","status":"in_progress"}]}`)
	result := `{"accepted":true,"snapshot":{"items":[{"id":"build","content":"Build page","status":"in_progress"}],"updated_at":"2026-08-02T10:00:00Z"}}`
	model.recordToolCallEntry("call-1", "update_todo", input, false, false, nil)
	model.recordToolResultEntry("call-1", "update_todo", "ok", result, false, false, false, nil)

	if len(model.transcript) != 1 {
		t.Fatalf("transcript length = %d", len(model.transcript))
	}
	entry := model.transcript[0]
	if string(entry.toolInput) != string(input) || entry.toolResult != result || entry.toolTarget != "updated 0/1" {
		t.Fatalf("entry = %#v", entry)
	}
	compact := ansi.Strip(renderEntry(entry, 100))
	if strings.Contains(compact, "Build page") || !strings.Contains(compact, "updated 0/1") {
		t.Fatalf("compact render = %q", compact)
	}
	entry.toolExpanded = true
	expanded := ansi.Strip(renderEntry(entry, 100))
	if !strings.Contains(expanded, "Build page") {
		t.Fatalf("expanded inspect data missing: %q", expanded)
	}
}

func TestToolTrackUsesSemanticEntrySpacing(t *testing.T) {
	entries := []transcriptEntry{
		{kind: entryAssistant, title: "assistant", body: "before"},
		{kind: entryTool, title: "tool", toolName: "Read", toolTarget: "one.go", toolStatus: "ok"},
		{kind: entryTool, title: "tool", toolName: "Read", toolTarget: "two.go", toolStatus: "running"},
		{kind: entryAssistant, title: "assistant", body: "after"},
	}
	lines := strings.Split(ansi.Strip(renderTranscript(entries, 72, true)), "\n")
	before := lineContaining(t, lines, "before")
	firstTool := lineContaining(t, lines, "✓ Read: one.go  完成")
	secondTool := lineContaining(t, lines, "◌ Read: two.go  运行中")
	afterLabel := lineContainingAfter(t, lines, "agent >", secondTool+1)

	if firstTool != before+1 {
		t.Fatalf("assistant -> tool rows = %d -> %d, want adjacent\n%s", before, firstTool, strings.Join(lines, "\n"))
	}
	if secondTool != firstTool+1 {
		t.Fatalf("tool -> tool rows = %d -> %d, want adjacent\n%s", firstTool, secondTool, strings.Join(lines, "\n"))
	}
	if afterLabel != secondTool+2 {
		t.Fatalf("tool -> assistant rows = %d -> %d, want one blank row\n%s", secondTool, afterLabel, strings.Join(lines, "\n"))
	}
}

func TestToolTrackLifecycleAndResultVisibility(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	startedAt := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	model.cursorFrameAt = startedAt
	next, _ := model.Update(toolCallMsg(ui.ToolCallEvent{
		ID:    "call-1",
		Name:  "Read",
		Input: []byte(`{"file_path":"README.md"}`),
	}))
	model = next.(appModel)
	entryIndex := len(model.transcript) - 1
	if entry := model.transcript[entryIndex]; entry.toolStatus != "running" || entry.toolTarget != "README.md" || entry.toolExpanded {
		t.Fatalf("running entry = %#v", entry)
	}
	if model.transcript[entryIndex].toolStartedAt.IsZero() {
		t.Fatalf("running entry = %#v, want tool start time", model.transcript[entryIndex])
	}
	runningAt := model.transcript[entryIndex].toolStartedAt.Add(12 * time.Second)
	runningRendered := ansi.Strip(renderTranscriptAt(model.transcript, 80, true, runningAt))
	if !strings.Contains(runningRendered, "运行中  12s") {
		t.Fatalf("running transcript = %q, want elapsed status", runningRendered)
	}
	if model.toggleToolExpansion(entryIndex, false) {
		t.Fatalf("running transaction must remain single-line")
	}

	model.cursorFrameAt = startedAt.Add(13*time.Second + 100*time.Millisecond)
	next, _ = model.Update(toolResultMsg(ui.ToolResultEvent{
		ToolUseID: "call-1",
		Name:      "Read",
		Content:   "full README result",
	}))
	model = next.(appModel)
	entry := model.transcript[entryIndex]
	if entry.toolStatus != "ok" || entry.toolResult != "full README result" || entry.toolExpanded {
		t.Fatalf("success entry = %#v", entry)
	}
	if len(model.transcript) != 1 {
		t.Fatalf("completed transcript entries = %d, want the running row updated in place", len(model.transcript))
	}
	if !entry.toolFinishedAt.Equal(startedAt.Add(13*time.Second + 100*time.Millisecond)) {
		t.Fatalf("finished at = %v, want %v", entry.toolFinishedAt, startedAt.Add(13*time.Second+100*time.Millisecond))
	}
	if rendered := ansi.Strip(renderTranscript(model.transcript, 80, true)); !strings.Contains(rendered, "✓ Read: README.md  完成  13.1s") {
		t.Fatalf("completed duration = %q, want formatted actual duration", rendered)
	}
	if rendered := ansi.Strip(renderTranscript(model.transcript, 80, true)); strings.Contains(rendered, "full README result") {
		t.Fatalf("collapsed success leaked result:\n%s", rendered)
	}
	if !model.toggleToolExpansion(entryIndex, false) {
		t.Fatalf("success entry did not expand")
	}
	if rendered := ansi.Strip(renderTranscript(model.transcript, 80, true)); !strings.Contains(rendered, "full README result") {
		t.Fatalf("expanded success hid result:\n%s", rendered)
	}

	next, _ = model.Update(toolCallMsg(ui.ToolCallEvent{
		ID:    "call-2",
		Name:  "Bash",
		Input: []byte(`{"command":"go test ./..."}`),
	}))
	model = next.(appModel)
	next, _ = model.Update(toolResultMsg(ui.ToolResultEvent{
		ToolUseID: "call-2",
		Name:      "Bash",
		Content:   "exit status 1",
		IsError:   true,
	}))
	model = next.(appModel)
	errorEntry := model.transcript[len(model.transcript)-1]
	if errorEntry.toolStatus != "error" || !errorEntry.toolExpanded {
		t.Fatalf("error entry = %#v, want expanded error", errorEntry)
	}
	rendered := ansi.Strip(renderTranscript(model.transcript, 80, true))
	if !strings.Contains(rendered, "× Bash: go test ./...  出错") || !strings.Contains(rendered, "exit status 1") {
		t.Fatalf("rendered error transaction:\n%s", rendered)
	}

	failed := newTestModel(&fakeRunner{})
	next, _ = failed.Update(toolCallMsg(ui.ToolCallEvent{ID: "call-3", Name: "Read", Input: []byte(`{"file_path":"stuck.go"}`)}))
	failed = next.(appModel)
	next, _ = failed.Update(turnFinishedMsg{err: errors.New("tool failed")})
	failed = next.(appModel)
	if got := failed.transcript[0].toolStatus; got != "error" {
		t.Fatalf("unfinished tool status = %q, want error", got)
	}
}

type bubbleScriptedModel struct {
	rounds [][]model.StreamEvent
	calls  int
}

func (m *bubbleScriptedModel) StreamMessage(context.Context, []message.Message, []model.ToolDefinition) (<-chan model.StreamEvent, error) {
	ch := make(chan model.StreamEvent, len(m.rounds[m.calls]))
	for _, event := range m.rounds[m.calls] {
		ch <- event
	}
	m.calls++
	close(ch)
	return ch, nil
}

type bubbleMCPBroker struct{}

func (bubbleMCPBroker) Snapshot() coremcp.Snapshot { return coremcp.Snapshot{} }
func (bubbleMCPBroker) Call(context.Context, string, json.RawMessage) (string, error) {
	return "mcp ok", nil
}

func TestRunnerBubbleSameNameNonMutationEndToEnd(t *testing.T) {
	for _, test := range []struct {
		name string
		tool tool.Tool
	}{
		{name: "ordinary", tool: &bubbleSameNameTool{}},
		{name: "mcp", tool: toolmcp.NewTool(coremcp.ToolSpec{Name: "Edit", MCPName: "Edit", Kind: coremcp.KindTool}, bubbleMCPBroker{})},
	} {
		t.Run(test.name, func(t *testing.T) {
			registry := tool.NewRegistry()
			registry.Register(test.tool)
			bubbleUI := New()
			app := newTestModel(&fakeRunner{})
			bubbleUI.sendMsg = func(msg tea.Msg) {
				next, _ := app.Update(msg)
				app = next.(appModel)
			}
			streamer := &bubbleScriptedModel{rounds: [][]model.StreamEvent{
				{{ToolCalls: []message.ToolCall{{ID: "edit-1", Name: "Edit", Input: json.RawMessage(`{"file_path":"a.go","old_string":"return 1","new_string":"return 2"}`)}}, Done: true}},
				{{Delta: "done", Done: true}},
			}}
			runner := loop.NewRunner(streamer, bubbleUI, registry, nil, "")
			if _, err := runner.RunTurn(context.Background(), "test"); err != nil {
				t.Fatal(err)
			}
			if len(app.transcript) == 0 {
				t.Fatal("missing Bubble tool transcript entry")
			}
			body := app.transcript[0].body
			if strings.Contains(body, "+1 -1") || strings.Contains(body, "- │") || strings.Contains(body, "+ │") {
				t.Fatalf("%s same-name non-capability rendered diff end-to-end: %q", test.name, body)
			}
		})
	}
}

type bubbleSameNameTool struct{}

func (*bubbleSameNameTool) Name() string                                         { return "Edit" }
func (*bubbleSameNameTool) Description() string                                  { return "same-name non-mutation" }
func (*bubbleSameNameTool) InputSchema() json.RawMessage                         { return json.RawMessage(`{"type":"object"}`) }
func (*bubbleSameNameTool) Run(context.Context, json.RawMessage) (string, error) { return "ok", nil }

func TestBubbleToolIngressDeepCopiesMutableEventData(t *testing.T) {
	var messages []tea.Msg
	uiInstance := New()
	uiInstance.sendMsg = func(msg tea.Msg) { messages = append(messages, msg) }

	input := json.RawMessage(`{"file_path":"a.txt","content":"after"}`)
	callSnapshot := &ui.FileMutationSnapshot{Before: "before", BeforeExists: true}
	call := ui.ToolCallEvent{ID: "write-1", Name: "Write", Input: input, FileMutationKnown: true, IsFileMutation: true, FileMutation: callSnapshot}
	if err := uiInstance.OnToolCall(call); err != nil {
		t.Fatal(err)
	}
	input[2] = 'X'
	callSnapshot.Before = "mutated"

	resultSnapshot := &ui.FileMutationSnapshot{Before: "before", After: "after", BeforeExists: true, AfterExists: true}
	result := ui.ToolResultEvent{ToolUseID: "write-1", Name: "Write", FileMutationKnown: true, IsFileMutation: true, FileMutation: resultSnapshot}
	if err := uiInstance.OnToolResult(result); err != nil {
		t.Fatal(err)
	}
	resultSnapshot.After = "mutated"

	callMsg := messages[0].(toolCallMsg)
	if string(callMsg.Input) != `{"file_path":"a.txt","content":"after"}` || callMsg.FileMutation.Before != "before" {
		t.Fatalf("queued call changed after ingress: input=%s snapshot=%+v", callMsg.Input, callMsg.FileMutation)
	}
	resultMsg := messages[1].(toolResultMsg)
	if resultMsg.FileMutation.After != "after" {
		t.Fatalf("queued result changed after ingress: snapshot=%+v", resultMsg.FileMutation)
	}

	model := newTestModel(&fakeRunner{})
	next, _ := model.Update(callMsg)
	model = next.(appModel)
	if string(model.transcript[0].toolInput) != `{"file_path":"a.txt","content":"after"}` || !strings.Contains(model.transcript[0].body, "before") || !strings.Contains(model.transcript[0].body, "after") {
		t.Fatalf("transcript entry did not retain copied call data: %#v", model.transcript[0])
	}
	next, _ = model.Update(resultMsg)
	model = next.(appModel)
	if body := model.transcript[0].body; !strings.Contains(body, "before") || !strings.Contains(body, "after") || strings.Contains(body, "mutated") {
		t.Fatalf("transcript entry did not retain copied result snapshot: %q", body)
	}
}

func TestResolvedSameNameNonMutationToolsDoNotRenderDiff(t *testing.T) {
	for _, test := range []struct {
		name   string
		toolID string
	}{
		{name: "ordinary", toolID: "ordinary-edit"},
		{name: "mcp", toolID: "mcp-edit"},
	} {
		t.Run(test.name, func(t *testing.T) {
			model := newTestModel(&fakeRunner{})
			input := json.RawMessage(`{"file_path":"a.go","old_string":"return 1","new_string":"return 2"}`)
			next, _ := model.Update(toolCallMsg(ui.ToolCallEvent{ID: test.toolID, Name: "Edit", Input: input, FileMutationKnown: true, IsFileMutation: false}))
			model = next.(appModel)
			next, _ = model.Update(toolResultMsg(ui.ToolResultEvent{ToolUseID: test.toolID, Name: "Edit", Content: "ok", FileMutationKnown: true, IsFileMutation: false}))
			model = next.(appModel)
			body := model.transcript[0].body
			if strings.Contains(body, "+1 -1") || strings.Contains(body, "- │") || strings.Contains(body, "+ │") {
				t.Fatalf("resolved %s same-name tool rendered mutation diff: %q", test.name, body)
			}
		})
	}
}

func TestFileMutationResultRebuildsDiffFromCompleteSnapshot(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	input := json.RawMessage(`{"file_path":"a.txt","old_string":"old","new_string":"new\nline","replace_all":true}`)
	before := &ui.FileMutationSnapshot{Before: "old\nkeep\nold\n", BeforeExists: true}
	next, _ := model.Update(toolCallMsg(ui.ToolCallEvent{ID: "edit-1", Name: "Edit", Input: input, FileMutation: before}))
	model = next.(appModel)
	if body := model.transcript[0].body; !strings.Contains(body, "+4 -2") {
		t.Fatalf("running body = %q, want full-file preview counts", body)
	}

	after := &ui.FileMutationSnapshot{
		Before: "old\nkeep\nold\n", After: "new\nline\nkeep\nnew\nline\n",
		BeforeExists: true, AfterExists: true,
	}
	next, _ = model.Update(toolResultMsg(ui.ToolResultEvent{ToolUseID: "edit-1", Name: "Edit", Content: "edited", FileMutation: after}))
	model = next.(appModel)
	entry := model.transcript[0]
	if !strings.Contains(entry.body, "Edit  ok  +4 -2") || strings.Count(entry.body, "- │ old") != 2 {
		t.Fatalf("completed body was not rebuilt from result snapshot: %q", entry.body)
	}
}

func TestFileMutationResultWithoutSnapshotFallsBackToLegacyInput(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	input := json.RawMessage(`{"file_path":"a.go","old_string":"return 1","new_string":"return 2"}`)
	next, _ := model.Update(toolCallMsg(ui.ToolCallEvent{ID: "edit-legacy", Name: "Edit", Input: input}))
	model = next.(appModel)
	next, _ = model.Update(toolResultMsg(ui.ToolResultEvent{ToolUseID: "edit-legacy", Name: "Edit", Content: "edited"}))
	model = next.(appModel)
	if body := model.transcript[0].body; !strings.Contains(body, "+1 -1") || !strings.Contains(body, "return 2") {
		t.Fatalf("legacy result body = %q, want input-derived diff", body)
	}
}

func TestResolvedMutationAfterCaptureFailurePreservesCallPreview(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	input := json.RawMessage(`{"file_path":"a.go","old_string":"return 1","new_string":"return 2"}`)
	before := &ui.FileMutationSnapshot{Before: "return 1\n", BeforeExists: true}
	next, _ := model.Update(toolCallMsg(ui.ToolCallEvent{
		ID: "edit-capture-fail", Name: "Edit", Input: input,
		FileMutationKnown: true, IsFileMutation: true, FileMutation: before,
	}))
	model = next.(appModel)
	preview := model.transcript[0].body
	if !strings.Contains(preview, "+1 -1") || !strings.Contains(preview, "return 2") {
		t.Fatalf("running preview = %q", preview)
	}
	next, _ = model.Update(toolResultMsg(ui.ToolResultEvent{
		ToolUseID: "edit-capture-fail", Name: "Edit", Content: "edited",
		FileMutationKnown: true, IsFileMutation: true,
	}))
	model = next.(appModel)
	body := model.transcript[0].body
	if !strings.Contains(body, "Edit  ok  +1 -1") || !strings.Contains(body, "return 1") || !strings.Contains(body, "return 2") {
		t.Fatalf("successful result without after snapshot replaced call preview: %q", body)
	}
}

func TestFailedFileMutationDoesNotShowSuccessDiff(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	input := json.RawMessage(`{"file_path":"a.go","old_string":"return 1","new_string":"return 2"}`)
	before := &ui.FileMutationSnapshot{Before: "return 1\n", BeforeExists: true}
	next, _ := model.Update(toolCallMsg(ui.ToolCallEvent{ID: "edit-error", Name: "Edit", Input: input, FileMutation: before}))
	model = next.(appModel)
	next, _ = model.Update(toolResultMsg(ui.ToolResultEvent{ToolUseID: "edit-error", Name: "Edit", Content: "old_string not found", IsError: true}))
	model = next.(appModel)
	entry := model.transcript[0]
	if strings.Contains(entry.body, "+") || strings.Contains(entry.body, "- │") || strings.Contains(entry.body, "return 2") {
		t.Fatalf("failed mutation retained success diff: %q", entry.body)
	}
	if entry.toolResult != "old_string not found" || !entry.toolExpanded {
		t.Fatalf("failed mutation result state = %#v", entry)
	}
}

func TestIdenticalFileMutationSnapshotShowsNoDiff(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	input := json.RawMessage(`{"file_path":"empty.txt","content":""}`)
	next, _ := model.Update(toolCallMsg(ui.ToolCallEvent{ID: "write-identical", Name: "Write", Input: input,
		FileMutation: &ui.FileMutationSnapshot{BeforeExists: true}}))
	model = next.(appModel)
	next, _ = model.Update(toolResultMsg(ui.ToolResultEvent{ToolUseID: "write-identical", Name: "Write", Content: "written",
		FileMutation: &ui.FileMutationSnapshot{BeforeExists: true, AfterExists: true}}))
	model = next.(appModel)
	body := model.transcript[0].body
	if strings.Contains(body, "+0 -0") || strings.Contains(body, "│") {
		t.Fatalf("identical snapshot displayed diff: %q", body)
	}
}

func TestToolResultWithoutIDUpdatesRunningEntryByName(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	next, _ := model.Update(toolCallMsg(ui.ToolCallEvent{
		ID:    "call-with-id",
		Name:  "Read",
		Input: []byte(`{"file_path":"README.md"}`),
	}))
	model = next.(appModel)
	next, _ = model.Update(toolResultMsg(ui.ToolResultEvent{
		Name:    "Read",
		Content: "completed",
	}))
	model = next.(appModel)

	if len(model.transcript) != 1 {
		t.Fatalf("transcript entries = %d, want one row after ID-less result", len(model.transcript))
	}
	if entry := model.transcript[0]; entry.toolStatus != "ok" || entry.toolResult != "completed" {
		t.Fatalf("updated entry = %#v, want completed running entry", entry)
	}
	rendered := ansi.Strip(renderTranscript(model.transcript, 80, true))
	if strings.Contains(rendered, "运行中") || !strings.Contains(rendered, "✓ Read: README.md  完成") {
		t.Fatalf("rendered transcript = %q, want only completed row", rendered)
	}
}

func TestToolDurationFormatting(t *testing.T) {
	startedAt := time.Unix(0, 0)
	tests := []struct {
		name string
		at   time.Time
		want string
	}{
		{name: "milliseconds", at: startedAt.Add(338 * time.Millisecond), want: "338ms"},
		{name: "short seconds", at: startedAt.Add(1*time.Second + 120*time.Millisecond), want: "1.12s"},
		{name: "seconds", at: startedAt.Add(13*time.Second + 100*time.Millisecond), want: "13.1s"},
		{name: "minutes", at: startedAt.Add(13*time.Minute + 52*time.Second), want: "13m52s"},
		{name: "hours", at: startedAt.Add(1*time.Hour + 26*time.Minute + 13*time.Second), want: "1h 26m 13s"},
		{name: "days", at: startedAt.Add(24*time.Hour + 52*time.Minute + 9*time.Second), want: "1d 52m 9s"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := formatToolDuration(startedAt, test.at); got != test.want {
				t.Fatalf("duration = %q, want %q", got, test.want)
			}
		})
	}
}

func TestToolTrackExpandedResultIsSanitizedAndComplete(t *testing.T) {
	rawLines := make([]string, 20)
	for index := range rawLines {
		rawLines[index] = fmt.Sprintf("line-%02d", index)
	}
	rawLines[2] = "\x1b[31mowned\x1b[0m\x00"
	rawLines[3] = "invalid-\xff"
	rawResult := strings.Join(rawLines, "\n")
	entry := transcriptEntry{
		kind:         entryTool,
		title:        "tool",
		toolName:     "Bash",
		toolStatus:   "error",
		toolTarget:   "danger",
		toolResult:   rawResult,
		isError:      true,
		toolExpanded: true,
	}
	rendered := ansi.Strip(renderEntry(entry, 70))
	if strings.Contains(rendered, "\x1b") || strings.Contains(rendered, "\x00") {
		t.Fatalf("control sequence leaked into rendered result: %q", rendered)
	}
	if !strings.Contains(rendered, "owned") || !strings.Contains(rendered, "invalid-�") {
		t.Fatalf("sanitized result lost visible content: %q", rendered)
	}
	if !strings.Contains(rendered, "line-19") || strings.Contains(rendered, "lines hidden") {
		t.Fatalf("expanded result did not show complete content:\n%s", rendered)
	}
	if got := len(strings.Split(rendered, "\n")); got != 1+len(rawLines) {
		t.Fatalf("rendered rows = %d, want summary + %d detail rows\n%s", got, len(rawLines), rendered)
	}
	if entry.toolResult != rawResult {
		t.Fatalf("render clamp mutated stored result")
	}
}

func TestToolInspectModePreservesInputAndNavigatesTransactions(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = 80
	model.height = 24
	model.input.SetValue("draft stays")
	model.transcript = []transcriptEntry{
		{kind: entryTool, title: "tool", toolName: "Read", toolTarget: "one.go", toolStatus: "ok"},
		{kind: entryAssistant, title: "assistant", body: "between"},
		{kind: entryTool, title: "tool", toolName: "Bash", toolTarget: "go test", toolStatus: "error", toolResult: "failed", toolExpanded: true, isError: true},
	}
	model.relayout()
	model.refreshViewport()

	next, _ := model.Update(tea.KeyMsg{Type: tea.KeyCtrlT})
	model = next.(appModel)
	if !model.toolInspectActive || model.toolInspectIndex != 2 || model.input.Focused() || model.shouldAnchorTextInputCursor() {
		t.Fatalf("inspect state = active:%v index:%d focused:%v anchor:%v", model.toolInspectActive, model.toolInspectIndex, model.input.Focused(), model.shouldAnchorTextInputCursor())
	}
	if rendered := ansi.Strip(renderTranscript(model.transcript, 80, true)); !strings.Contains(rendered, "› × Bash: go test  出错") {
		t.Fatalf("inspect focus lacks non-color marker:\n%s", rendered)
	}
	if got := model.input.Value(); got != "draft stays" {
		t.Fatalf("input = %q, want preserved", got)
	}

	next, _ = model.Update(tea.KeyMsg{Type: tea.KeyUp})
	model = next.(appModel)
	if model.toolInspectIndex != 0 || !model.transcript[0].toolFocused {
		t.Fatalf("up selected index=%d entries=%#v", model.toolInspectIndex, model.transcript)
	}
	next, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = next.(appModel)
	if !model.transcript[0].toolExpanded {
		t.Fatalf("enter did not expand selected success")
	}
	next, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	model = next.(appModel)
	if model.toolInspectActive || model.toolInspectIndex != -1 || model.input.Value() != "draft stays" {
		t.Fatalf("closed inspect state = active:%v index:%d input:%q", model.toolInspectActive, model.toolInspectIndex, model.input.Value())
	}
	if !model.input.Focused() {
		t.Fatalf("closing inspect did not restore input focus (cmd=%v)", cmd)
	}
}

func TestToolTrackMouseClickTogglesButDragSelects(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = 80
	model.height = 24
	model.transcript = []transcriptEntry{{
		kind:       entryTool,
		title:      "tool",
		toolName:   "Read",
		toolTarget: "README.md",
		toolStatus: "ok",
		toolResult: "result",
	}}
	model.relayout()
	model.refreshViewport()
	location := transcriptEntryLocations(model.transcript, model.viewport.Width, true, model.animationNow())[0]
	y := 1 + model.currentLayout().headerHeight + location.startRow - model.viewport.YOffset
	x := 5

	click := func(row int) {
		next, _ := model.Update(tea.MouseMsg{X: x, Y: row, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
		model = next.(appModel)
		next, _ = model.Update(tea.MouseMsg{X: x, Y: row, Button: tea.MouseButtonLeft, Action: tea.MouseActionRelease})
		model = next.(appModel)
	}

	// A collapsed group is a single header row; clicking it expands the group
	// to list each tool's summary (the result detail stays closed).
	click(y)
	if !model.transcript[0].toolExpanded {
		t.Fatalf("summary click did not expand tool")
	}
	if got := ansi.Strip(model.renderTranscriptContent()); !strings.Contains(got, "✓ Read: README.md  完成") {
		t.Fatalf("expanded group hid tool summary:\n%s", got)
	}
	if got := ansi.Strip(model.renderTranscriptContent()); strings.Contains(got, "result") {
		t.Fatalf("expanded group leaked result before detail open:\n%s", got)
	}

	// Clicking the tool's own summary row opens just that tool's detail
	// (toolGroupOpen); the group itself stays expanded.
	detailY := y + 1
	click(detailY)
	if !model.transcript[0].toolExpanded {
		t.Fatalf("tool detail click collapsed the whole group")
	}
	if !model.transcript[0].toolGroupOpen {
		t.Fatalf("tool detail click did not open per-tool detail")
	}
	if got := ansi.Strip(model.renderTranscriptContent()); !strings.Contains(got, "result") {
		t.Fatalf("opened detail missing result:\n%s", got)
	}

	// Clicking the tool row again closes just that detail, group stays open.
	click(detailY)
	if model.transcript[0].toolGroupOpen {
		t.Fatalf("second tool detail click did not close detail")
	}
	if !model.transcript[0].toolExpanded {
		t.Fatalf("closing detail collapsed the whole group")
	}
	if got := ansi.Strip(model.renderTranscriptContent()); strings.Contains(got, "result") {
		t.Fatalf("closed detail still visible:\n%s", got)
	}

	// Re-open the detail so the drag-selection assertion below has a stable state.
	click(detailY)
	if !model.transcript[0].toolGroupOpen {
		t.Fatalf("third tool detail click did not re-open detail")
	}

	previousWriteClipboard := writeClipboard
	writeClipboard = func(string) error { return nil }
	t.Cleanup(func() { writeClipboard = previousWriteClipboard })
	next, _ := model.Update(tea.MouseMsg{X: x, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	model = next.(appModel)
	next, _ = model.Update(tea.MouseMsg{X: x + 4, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionMotion})
	model = next.(appModel)
	next, _ = model.Update(tea.MouseMsg{X: x + 4, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionRelease})
	model = next.(appModel)
	if !model.selectionActive {
		t.Fatalf("drag selection failed: active=%v", model.selectionActive)
	}
	// The drag must not have collapsed the group or closed the detail.
	if !model.transcript[0].toolExpanded || !model.transcript[0].toolGroupOpen {
		t.Fatalf("drag selection toggled group state: expanded=%v open=%v", model.transcript[0].toolExpanded, model.transcript[0].toolGroupOpen)
	}
}

// mouseClickAt 在指定屏幕坐标发送一次左键单击（press + release）。
func mouseClickAt(model appModel, x, y int) appModel {
	next, _ := model.Update(tea.MouseMsg{X: x, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	model = next.(appModel)
	next, _ = model.Update(tea.MouseMsg{X: x, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionRelease})
	model = next.(appModel)
	return model
}

// TestReadyStateCollapsedGroupClickExpands 回归：ready（空闲/一轮结束）状态下，
// 工具组折叠为单个 header 行（▸ Tools），点击该行应整组展开，列出每个工具的摘要。
func TestReadyStateCollapsedGroupClickExpands(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = 80
	model.height = 24

	model.transcript = []transcriptEntry{
		{kind: entryUser, title: "you", body: "帮我看看项目"},
		{kind: entryAssistant, title: "assistant", body: "好的，先读取一下"},
	}
	model.addEntry(transcriptEntry{kind: entryTool, title: "tool", toolName: "Read", toolTarget: "a.go", toolStatus: "ok", toolResult: "full content"})
	model.addEntry(transcriptEntry{kind: entryTool, title: "tool", toolName: "Bash", toolTarget: "go test", toolStatus: "ok", toolResult: "ok"})
	model.toolGroupExpanded = false
	model.relayout()
	model.refreshViewport()

	rendered := ansi.Strip(model.renderTranscriptContent())
	if !strings.Contains(rendered, "▸ Tools  2 calls") {
		t.Fatalf("want collapsed ready group, got:\n%s", rendered)
	}

	// 工具组是 transcript 中的第 3 条（user、assistant 之后）；定位它，不能点 user 行。
	locs := model.transcriptEntryLocationsAt()
	var loc transcriptEntryLocation
	found := false
	for _, candidate := range locs {
		if model.transcript[candidate.transcriptIndex].toolName == "Read" {
			loc = candidate
			found = true
			break
		}
	}
	if !found {
		t.Fatal("tool group location not found")
	}
	y := 1 + model.currentLayout().headerHeight + loc.startRow - model.viewport.YOffset

	model = mouseClickAt(model, 5, y)

	if !model.transcript[2].toolExpanded {
		t.Fatalf("click on collapsed header in ready state did not expand: %#v", model.transcript[2])
	}
	expanded := ansi.Strip(model.renderTranscriptContent())
	if !strings.Contains(expanded, "✓ Read: a.go") || !strings.Contains(expanded, "✓ Bash: go test") {
		t.Fatalf("expanded group missing tool summaries:\n%s", expanded)
	}
}

// TestReadyStateSecondGroupClickExpands 回归：多轮会话中第二个折叠工具组在 ready 态
// 点击只能展开自己，不能错位误展开第一个组。
func TestReadyStateSecondGroupClickExpands(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = 80
	model.height = 24

	model.transcript = []transcriptEntry{
		{kind: entryUser, title: "you", body: "第一轮"},
		{kind: entryTool, title: "tool", toolName: "LS", toolTarget: ".", toolStatus: "ok", toolResult: "a.go"},
		{kind: entryAssistant, title: "assistant", body: "第一轮结果"},
		{kind: entryUser, title: "you", body: "第二轮"},
		{kind: entryTool, title: "tool", toolName: "Read", toolTarget: "b.go", toolStatus: "ok", toolResult: "content"},
	}
	model.toolGroupExpanded = false
	model.relayout()
	model.refreshViewport()

	locs := model.transcriptEntryLocationsAt()
	if len(locs) < 2 {
		t.Fatalf("want >=2 locations, got %d", len(locs))
	}
	var second transcriptEntryLocation
	for _, loc := range locs {
		if model.transcript[loc.transcriptIndex].toolName == "Read" {
			second = loc
		}
	}
	if second.transcriptIndex == 0 {
		t.Fatal("second group location not found")
	}
	y := 1 + model.currentLayout().headerHeight + second.startRow - model.viewport.YOffset

	model = mouseClickAt(model, 5, y)

	readIdx := second.transcriptIndex
	if !model.transcript[readIdx].toolExpanded {
		t.Fatalf("click on second group header did not expand: idx=%d %#v", readIdx, model.transcript[readIdx])
	}
	if model.transcript[1].toolExpanded {
		t.Fatalf("first group unexpectedly expanded: %#v", model.transcript[1])
	}
	expanded := ansi.Strip(model.renderTranscriptContent())
	if !strings.Contains(expanded, "✓ Read: b.go") {
		t.Fatalf("second expanded group missing summary:\n%s", expanded)
	}
}

// TestReadyStateLifecycleCollapsedGroupClickExpands 回归：走真实事件流
// toolCall → toolResult → turnFinished 进入 ready 态后，折叠的工具组点击可展开。
func TestReadyStateLifecycleCollapsedGroupClickExpands(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = 80
	model.height = 24

	next, _ := model.Update(toolCallMsg(ui.ToolCallEvent{ID: "call-1", Name: "Read", Input: []byte(`{"file_path":"a.go"}`)}))
	model = next.(appModel)
	next, _ = model.Update(toolResultMsg(ui.ToolResultEvent{ToolUseID: "call-1", Name: "Read", Content: "full content"}))
	model = next.(appModel)
	next, _ = model.Update(turnFinishedMsg{})
	model = next.(appModel)

	model.relayout()
	model.refreshViewport()

	rendered := ansi.Strip(model.renderTranscriptContent())
	if !strings.Contains(rendered, "▸ Tools") {
		t.Fatalf("want collapsed ready group, got:\n%s", rendered)
	}

	loc := model.transcriptEntryLocationsAt()[0]
	y := 1 + model.currentLayout().headerHeight + loc.startRow - model.viewport.YOffset
	model = mouseClickAt(model, 5, y)

	if !model.transcript[0].toolExpanded {
		t.Fatalf("lifecycle ready-state click did not expand: %#v", model.transcript[0])
	}
}

// TestSessionRestoreFinalizesOrphanedRunningToolsAndExpands 回归：恢复一个
// 上一轮被中断的会话（工具调用没有对应结果记录）时，孤儿调用会以 running
// 状态进入历史。必须把它们收尾为 error（等价于实时流程 markRunningToolsError
// 的处理），否则工具组渲染为折叠（running 组用全局 toolGroupExpanded=false）
// 但点击被 toggleToolExpansion 拒绝（running 不可切换），看起来"无法展开"。
func TestSessionRestoreFinalizesOrphanedRunningToolsAndExpands(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = 80
	model.height = 24
	model.relayout()

	orphanCalls := transcriptEntriesFromMessage(message.Message{
		Role: message.RoleAssistant,
		ToolUses: []message.ToolCall{{
			ID:    "call-orphan",
			Name:  "Read",
			Input: json.RawMessage(`{"file_path":"stuck.go"}`),
		}},
	}, time.Now(), "")
	okCalls := transcriptEntriesFromMessage(message.Message{
		Role: message.RoleAssistant,
		ToolUses: []message.ToolCall{{
			ID:    "call-ok",
			Name:  "Bash",
			Input: json.RawMessage(`{"command":"go test ./..."}`),
		}},
	}, time.Now(), "")
	okResults := transcriptEntriesFromMessage(message.Message{
		Role:        message.RoleUser,
		ToolResults: []message.ToolResult{{ToolUseID: "call-ok", Content: "ok"}},
	}, time.Now(), "")

	entries := []transcriptEntry{{kind: entryUser, title: "you", body: "第一轮"}}
	entries = append(entries, orphanCalls...)
	entries = append(entries, okCalls...)
	entries = append(entries, okResults...)

	next, _ := model.Update(sessionRestoredMsg{sessionID: "interrupted-session", entries: entries})
	model = next.(appModel)

	orphanIdx, okIdx := -1, -1
	for i := range model.transcript {
		switch model.transcript[i].toolUseID {
		case "call-orphan":
			orphanIdx = i
		case "call-ok":
			okIdx = i
		}
	}
	if orphanIdx < 0 || okIdx < 0 {
		t.Fatalf("restored transcript missing tools: %#v", model.transcript)
	}
	if got := model.transcript[orphanIdx]; got.toolStatus != "error" || !got.toolExpanded || !got.isError {
		t.Fatalf("orphan tool = %#v, want error + expanded", got)
	}
	if got := model.transcript[okIdx]; got.toolStatus != "ok" {
		t.Fatalf("completed tool = %#v, want ok preserved", got)
	}

	model.relayout()
	model.refreshViewport()
	rendered := ansi.Strip(model.renderTranscriptContent())
	for _, want := range []string{"× Read: stuck.go", "✓ Bash: go test"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("restored group missing %q:\n%s", want, rendered)
		}
	}

	// 组内不再有 running：点击 header 可折叠、再点击可重新展开。
	var loc transcriptEntryLocation
	found := false
	for _, candidate := range model.transcriptEntryLocationsAt() {
		if model.transcript[candidate.transcriptIndex].toolUseID == "call-orphan" {
			loc = candidate
			found = true
			break
		}
	}
	if !found {
		t.Fatal("tool group location not found")
	}
	y := 1 + model.currentLayout().headerHeight + loc.startRow - model.viewport.YOffset

	model = mouseClickAt(model, 5, y)
	if model.transcript[orphanIdx].toolExpanded {
		t.Fatalf("header click on expanded group did not collapse: %#v", model.transcript[orphanIdx])
	}
	collapsed := ansi.Strip(model.renderTranscriptContent())
	if !strings.Contains(collapsed, "▸ Tools") {
		t.Fatalf("group did not collapse:\n%s", collapsed)
	}

	model = mouseClickAt(model, 5, y)
	if !model.transcript[orphanIdx].toolExpanded {
		t.Fatalf("click on collapsed group did not expand: %#v", model.transcript[orphanIdx])
	}
	expanded := ansi.Strip(model.renderTranscriptContent())
	if !strings.Contains(expanded, "× Read: stuck.go") || !strings.Contains(expanded, "✓ Bash: go test") {
		t.Fatalf("group did not re-expand:\n%s", expanded)
	}
}

func TestToolTrackMouseHoverHighlightsAndClears(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = 80
	model.height = 24
	model.transcript = []transcriptEntry{{
		kind:       entryTool,
		title:      "tool",
		toolName:   "Read",
		toolTarget: "README.md",
		toolStatus: "ok",
	}}
	model.relayout()
	model.refreshViewport()
	location := transcriptEntryLocations(model.transcript, model.viewport.Width, true, model.animationNow())[0]
	y := 1 + model.currentLayout().headerHeight + location.startRow - model.viewport.YOffset

	next, _ := model.Update(tea.MouseMsg{X: 5, Y: y, Action: tea.MouseActionMotion})
	model = next.(appModel)
	if model.toolHoverIndex != 0 || !model.transcript[0].toolHovered {
		t.Fatalf("hover state = index:%d entry:%#v", model.toolHoverIndex, model.transcript[0])
	}
	if model.transcriptKeyScrollActive {
		t.Fatalf("hover must not steal keyboard scroll focus")
	}
	if rendered := ansi.Strip(renderTranscript(model.transcript, 80, true)); !strings.Contains(rendered, "┃ ✓ Read: README.md  完成") {
		t.Fatalf("hover marker missing:\n%s", rendered)
	}

	next, _ = model.Update(tea.MouseMsg{X: 5, Y: model.height - 2, Action: tea.MouseActionMotion})
	model = next.(appModel)
	if model.toolHoverIndex != -1 || model.transcript[0].toolHovered {
		t.Fatalf("hover did not clear outside transcript: index:%d entry:%#v", model.toolHoverIndex, model.transcript[0])
	}
}

func TestIdleMouseMotionFilterPassesToolHoverTransitions(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = 80
	model.height = 24
	model.transcript = []transcriptEntry{{
		kind:       entryTool,
		title:      "tool",
		toolName:   "Read",
		toolTarget: "README.md",
		toolStatus: "ok",
	}}
	model.relayout()
	model.refreshViewport()
	location := transcriptEntryLocations(model.transcript, model.viewport.Width, true, model.animationNow())[0]
	y := 1 + model.currentLayout().headerHeight + location.startRow - model.viewport.YOffset
	inside := tea.MouseMsg{X: 5, Y: y, Button: tea.MouseButtonNone, Action: tea.MouseActionMotion}
	outside := tea.MouseMsg{X: model.width - 1, Y: model.height - 1, Button: tea.MouseButtonNone, Action: tea.MouseActionMotion}

	if got := filterIdleMouseMotion(model, inside); got == nil {
		t.Fatal("tool enter motion was filtered")
	}
	model.toolHoverIndex = 0
	if got := filterIdleMouseMotion(model, inside); got != nil {
		t.Fatalf("unchanged tool motion = %#v, want filtered", got)
	}
	if got := filterIdleMouseMotion(model, outside); got == nil {
		t.Fatal("tool leave motion was filtered")
	}
	model.toolHoverIndex = -1
	if got := filterIdleMouseMotion(model, outside); got != nil {
		t.Fatalf("unchanged outside motion = %#v, want filtered", got)
	}
}

func TestToolTrackHoverDoesNotLeakANSISequences(t *testing.T) {
	previousProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	t.Cleanup(func() {
		lipgloss.SetColorProfile(previousProfile)
	})

	rendered := renderEntry(transcriptEntry{
		kind:        entryTool,
		title:       "tool",
		toolName:    "Read",
		toolTarget:  "README.md",
		toolStatus:  "ok",
		toolHovered: true,
	}, 80)
	plain := ansi.Strip(rendered)
	if !strings.Contains(plain, "┃ ✓ Read: README.md  完成") {
		t.Fatalf("hovered tool = %q, want intact summary", plain)
	}
	for _, leaked := range []string{"[0m", "[1;", "[4m", "[38;"} {
		if strings.Contains(plain, leaked) {
			t.Fatalf("hovered tool leaked ANSI parameter %q: %q", leaked, plain)
		}
	}
}

func TestToolInspectResetsWithClear(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.transcript = []transcriptEntry{{
		kind:       entryTool,
		title:      "tool",
		toolName:   "Read",
		toolStatus: "ok",
	}}
	next, _ := model.Update(tea.KeyMsg{Type: tea.KeyCtrlT})
	model = next.(appModel)
	if !model.toolInspectActive || model.input.Focused() {
		t.Fatalf("tool inspect did not open")
	}
	handled, _ := model.handleCommand("/clear")
	if !handled || model.toolInspectActive || model.toolInspectIndex != -1 || !model.input.Focused() {
		t.Fatalf("clear did not restore input state: handled=%v active=%v index=%d focused=%v", handled, model.toolInspectActive, model.toolInspectIndex, model.input.Focused())
	}
}

func TestHistoricalFileMutationRestoresExpandedDiffFromToolInput(t *testing.T) {
	callEntries := transcriptEntriesFromMessage(message.Message{
		Role: message.RoleAssistant,
		ToolUses: []message.ToolCall{{
			ID: "edit-history", Name: "Edit",
			Input: json.RawMessage(`{"file_path":"a.go","old_string":"return 1","new_string":"return 2"}`),
		}},
	}, time.Now(), "")
	resultEntries := transcriptEntriesFromMessage(message.Message{
		Role:        message.RoleUser,
		ToolResults: []message.ToolResult{{ToolUseID: "edit-history", Content: "edited a.go (1 replacement)"}},
	}, time.Now(), "")
	merged := mergeTranscriptToolEntries(append(callEntries, resultEntries...))
	if len(merged) != 1 || !merged[0].toolExpanded {
		t.Fatalf("restored mutation = %#v, want one expanded transaction", merged)
	}
	rendered := ansi.Strip(renderTranscript(merged, 100, true))
	for _, want := range []string{"return 1", "return 2"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("restored mutation missing %q:\n%s", want, rendered)
		}
	}
	if strings.Contains(rendered, "edited a.go") {
		t.Fatalf("restored mutation rendered result text instead of diff:\n%s", rendered)
	}
}

func TestHistoricalToolCallAndResultMergeByID(t *testing.T) {
	callEntries := transcriptEntriesFromMessage(message.Message{
		Role: message.RoleAssistant,
		ToolUses: []message.ToolCall{{
			ID:    "call-1",
			Name:  "Glob",
			Input: json.RawMessage(`{"pattern":"**/*.go"}`),
		}},
	}, time.Now(), "")
	resultEntries := transcriptEntriesFromMessage(message.Message{
		Role: message.RoleUser,
		ToolResults: []message.ToolResult{{
			ToolUseID: "call-1",
			Content:   "main.go",
		}},
	}, time.Now(), "")
	merged := mergeTranscriptToolEntries(append(callEntries, resultEntries...))
	if len(merged) != 1 {
		t.Fatalf("merged entries = %#v, want one transaction", merged)
	}
	if merged[0].toolStatus != "ok" || merged[0].toolTarget != "**/*.go" || merged[0].toolResult != "main.go" || merged[0].toolExpanded {
		t.Fatalf("merged transaction = %#v", merged[0])
	}

	unmatched := mergeTranscriptToolEntries(resultEntries)
	if len(unmatched) != 1 || !unmatched[0].toolResultOnly {
		t.Fatalf("unmatched result = %#v, want independent tool row", unmatched)
	}
}

func TestToolExpansionKeepsFixedFrameAndDock(t *testing.T) {
	model := newModel(context.Background(), &fakeRunner{}, "session-1", &fakeModelConfigController{}, nil, nil, nil, newTerminalCursorAnchor())
	model.ready = true
	model.width = 80
	model.height = 24
	model.transcript = []transcriptEntry{{
		kind:       entryTool,
		title:      "tool",
		toolName:   "Bash",
		toolTarget: "go test ./...",
		toolStatus: "error",
		toolResult: strings.Repeat("failure\n", 20),
		isError:    true,
	}}
	model.relayout()
	model.refreshViewport()
	layoutBefore := model.currentLayout()
	assertFixedFrame(t, model.View(), 80, 24)

	if !model.toggleToolExpansion(0, false) {
		t.Fatalf("tool did not expand")
	}
	assertFixedFrame(t, model.View(), 80, 24)
	if got := model.currentLayout(); got != layoutBefore {
		t.Fatalf("tool expansion changed fixed layout:\nbefore=%+v\nafter=%+v", layoutBefore, got)
	}
}

func TestCompactToolSummaryFitsNarrowWidths(t *testing.T) {
	entry := transcriptEntry{
		kind:       entryTool,
		title:      "tool",
		toolName:   "LongWide工具Name",
		toolTarget: "路径/👩‍💻/非常长的目标文件.go",
		toolStatus: "running",
	}
	for _, width := range []int{8, 12, 20, 40, 80, 120} {
		rendered := renderEntry(entry, width)
		for _, line := range strings.Split(rendered, "\n") {
			if got := ansi.StringWidth(line); got > width {
				t.Fatalf("width=%d rendered cell width=%d line=%q", width, got, ansi.Strip(line))
			}
		}
	}
}

func TestCompactToolSummaryKeepsStatusAtNarrowWidths(t *testing.T) {
	entry := transcriptEntry{
		kind:       entryTool,
		title:      "tool",
		toolName:   "codegraph__read_url",
		toolTarget: "https://example.com/a-very-long-documentation-page",
		toolStatus: "running",
	}
	for _, width := range []int{20, 40, 80, 120} {
		rendered := ansi.Strip(renderEntry(entry, width))
		if !strings.Contains(rendered, "运行中") {
			t.Fatalf("width=%d rendered=%q, want running status", width, rendered)
		}
		for _, line := range strings.Split(rendered, "\n") {
			if got := lipgloss.Width(line); got > width {
				t.Fatalf("width=%d rendered cell width=%d line=%q", width, got, line)
			}
		}
	}
}

func lineContaining(t *testing.T, lines []string, needle string) int {
	t.Helper()
	return lineContainingAfter(t, lines, needle, 0)
}

func lineContainingAfter(t *testing.T, lines []string, needle string, start int) int {
	t.Helper()
	for index := maxInt(0, start); index < len(lines); index++ {
		if strings.Contains(lines[index], needle) {
			return index
		}
	}
	t.Fatalf("line containing %q not found in:\n%s", needle, strings.Join(lines, "\n"))
	return -1
}

func TestSelectToolTrackCollapsesToOneLineAndExpandsReadableDetail(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	input := json.RawMessage(`{"prompt":"Which animals are mammals?","mode":"multiple","options":[{"id":"whale","label":"Whale","description":"Breathes with lungs"},{"id":"shark","label":"Shark","description":"A fish"}]}`)
	next, _ := model.Update(toolCallMsg(ui.ToolCallEvent{ID: "select-1", Name: "Select", Input: input}))
	model = next.(appModel)
	running := ansi.Strip(renderTranscript(model.transcript, 100, true))
	if strings.Contains(running, "Which animals") || strings.Contains(running, "Whale") {
		t.Fatalf("running Select leaked question or answer: %q", running)
	}
	next, _ = model.Update(toolResultMsg(ui.ToolResultEvent{
		ToolUseID: "select-1",
		Name:      "Select",
		Content:   `{"cancelled":false,"selected_options":[{"id":"whale","label":"Whale"},{"id":"custom_option","label":"Platypus"}]}`,
	}))
	model = next.(appModel)

	collapsed := ansi.Strip(renderTranscript(model.transcript, 100, true))
	if len(strings.Split(strings.TrimSpace(collapsed), "\n")) != 1 {
		t.Fatalf("collapsed Select uses multiple rows:\n%s", collapsed)
	}
	if !strings.Contains(collapsed, "selected 2 options") || strings.Contains(collapsed, "Which animals") || strings.Contains(collapsed, "Whale") || strings.Contains(collapsed, "Platypus") {
		t.Fatalf("collapsed Select=%q", collapsed)
	}

	if !model.toggleToolExpansion(0, false) {
		t.Fatal("Select transaction did not expand")
	}
	expanded := ansi.Strip(renderTranscript(model.transcript, 100, true))
	for _, want := range []string{"Which animals are mammals?", "Whale", "Breathes with lungs", "Platypus", "Custom option"} {
		if !strings.Contains(expanded, want) {
			t.Fatalf("expanded Select missing %q:\n%s", want, expanded)
		}
	}
	for _, unwanted := range []string{"Shark", "selected_options", `"cancelled"`} {
		if strings.Contains(expanded, unwanted) {
			t.Fatalf("expanded Select leaked %q:\n%s", unwanted, expanded)
		}
	}
}

func TestHistoricalSelectTransactionPreservesInputForReadableExpansion(t *testing.T) {
	callEntries := transcriptEntriesFromMessage(message.Message{
		Role: message.RoleAssistant,
		ToolUses: []message.ToolCall{{
			ID: "select-1", Name: "Select",
			Input: json.RawMessage(`{"prompt":"Pick a signal","mode":"single","options":[{"id":"logs","label":"Logs","description":"Application logs"}]}`),
		}},
	}, time.Now(), "")
	if strings.Contains(callEntries[0].toolTarget, "Pick a signal") {
		t.Fatalf("historical running Select leaked prompt: %#v", callEntries[0])
	}
	resultEntries := transcriptEntriesFromMessage(message.Message{
		Role: message.RoleUser,
		ToolResults: []message.ToolResult{{
			ToolUseID: "select-1",
			Content:   `{"cancelled":false,"selected_options":[{"id":"logs","label":"Logs"}]}`,
		}},
	}, time.Now(), "")
	merged := mergeTranscriptToolEntries(append(callEntries, resultEntries...))
	if len(merged) != 1 || string(merged[0].toolInput) == "" || merged[0].toolTarget != "selected 1 option" {
		t.Fatalf("merged=%#v", merged)
	}
	collapsed := ansi.Strip(renderTranscript(merged, 100, true))
	if len(strings.Split(strings.TrimSpace(collapsed), "\n")) != 1 || strings.Contains(collapsed, "Pick a signal") || strings.Contains(collapsed, "Logs") {
		t.Fatalf("historical collapsed Select=%q", collapsed)
	}
	merged[0].toolExpanded = true
	rendered := ansi.Strip(renderTranscript(merged, 100, true))
	if !strings.Contains(rendered, "Pick a signal") || !strings.Contains(rendered, "Logs") || !strings.Contains(rendered, "Application logs") {
		t.Fatalf("historical Select detail missing:\n%s", rendered)
	}
}

func TestSelectToolCallInputIsDeepCopiedAndMalformedFallsBackToRawResult(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	input := json.RawMessage(`{"prompt":"Pick","mode":"single","options":[{"id":"a","label":"A"}]}`)
	next, _ := model.Update(toolCallMsg(ui.ToolCallEvent{ID: "select-copy", Name: "Select", Input: input}))
	model = next.(appModel)
	input[0] = '['
	if got := string(model.transcript[0].toolInput); !strings.HasPrefix(got, "{") {
		t.Fatalf("live tool input was not deep copied: %q", got)
	}

	next, _ = model.Update(toolResultMsg(ui.ToolResultEvent{
		ToolUseID: "select-copy",
		Name:      "Select",
		Content:   `{"cancelled":false,"selected_options":null,"diagnostic":"raw fallback"}`,
	}))
	model = next.(appModel)
	model.transcript[0].toolExpanded = true
	rendered := ansi.Strip(renderTranscript(model.transcript, 100, true))
	if !strings.Contains(rendered, "raw fallback") || !strings.Contains(rendered, "selected_options") {
		t.Fatalf("malformed Select result did not use raw fallback:\n%s", rendered)
	}
}
