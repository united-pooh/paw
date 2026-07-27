package bubble

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"codex-agent-go/internal/message"
	"codex-agent-go/internal/ui"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
)

func TestToolTrackUsesSemanticEntrySpacing(t *testing.T) {
	entries := []transcriptEntry{
		{kind: entryAssistant, title: "assistant", body: "before"},
		{kind: entryTool, title: "tool", toolName: "Read", toolTarget: "one.go", toolStatus: "ok"},
		{kind: entryTool, title: "tool", toolName: "Read", toolTarget: "two.go", toolStatus: "running"},
		{kind: entryAssistant, title: "assistant", body: "after"},
	}
	lines := strings.Split(ansi.Strip(renderTranscript(entries, 72, true)), "\n")
	before := lineContaining(t, lines, "before")
	firstTool := lineContaining(t, lines, "✓ Read one.go · ok")
	secondTool := lineContaining(t, lines, "◌ Read two.go · running")
	afterLabel := lineContainingAfter(t, lines, "assistant", secondTool+1)

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
	if model.toggleToolExpansion(entryIndex) {
		t.Fatalf("running transaction must remain single-line")
	}

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
	if rendered := ansi.Strip(renderTranscript(model.transcript, 80, true)); strings.Contains(rendered, "full README result") {
		t.Fatalf("collapsed success leaked result:\n%s", rendered)
	}
	if !model.toggleToolExpansion(entryIndex) {
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
	if !strings.Contains(rendered, "× Bash go test ./... · error") || !strings.Contains(rendered, "exit status 1") {
		t.Fatalf("rendered error transaction:\n%s", rendered)
	}
}

func TestToolTrackExpandedResultIsSanitizedAndClamped(t *testing.T) {
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
	if strings.Contains(rendered, "line-19") || !strings.Contains(rendered, "... 9 more lines hidden") {
		t.Fatalf("expanded result was not clamped to 12 display rows:\n%s", rendered)
	}
	if got := len(strings.Split(rendered, "\n")); got != 1+maxRenderedToolDetailLines {
		t.Fatalf("rendered rows = %d, want summary + %d detail rows\n%s", got, maxRenderedToolDetailLines, rendered)
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
	if rendered := ansi.Strip(renderTranscript(model.transcript, 80, true)); !strings.Contains(rendered, "› × Bash go test · error") {
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
	location := transcriptEntryLocations(model.transcript, model.viewport.Width, true)[0]
	y := 1 + location.startRow - model.viewport.YOffset
	x := 5

	next, _ := model.Update(tea.MouseMsg{X: x, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	model = next.(appModel)
	next, _ = model.Update(tea.MouseMsg{X: x, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionRelease})
	model = next.(appModel)
	if !model.transcript[0].toolExpanded {
		t.Fatalf("summary click did not expand tool")
	}

	// 展开后结果区也属于同一工具事务，点击任意详情行都应能折叠。
	detailY := y + 1
	next, _ = model.Update(tea.MouseMsg{X: x, Y: detailY, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	model = next.(appModel)
	next, _ = model.Update(tea.MouseMsg{X: x, Y: detailY, Button: tea.MouseButtonLeft, Action: tea.MouseActionRelease})
	model = next.(appModel)
	if model.transcript[0].toolExpanded {
		t.Fatalf("detail click did not collapse expanded tool")
	}

	next, _ = model.Update(tea.MouseMsg{X: x, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	model = next.(appModel)
	next, _ = model.Update(tea.MouseMsg{X: x, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionRelease})
	model = next.(appModel)
	if !model.transcript[0].toolExpanded {
		t.Fatalf("summary click did not re-expand tool")
	}

	previousWriteClipboard := writeClipboard
	writeClipboard = func(string) error { return nil }
	t.Cleanup(func() { writeClipboard = previousWriteClipboard })
	next, _ = model.Update(tea.MouseMsg{X: x, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	model = next.(appModel)
	next, _ = model.Update(tea.MouseMsg{X: x + 4, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionMotion})
	model = next.(appModel)
	next, _ = model.Update(tea.MouseMsg{X: x + 4, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionRelease})
	model = next.(appModel)
	if !model.selectionActive || !model.transcript[0].toolExpanded {
		t.Fatalf("drag selection toggled tool or failed selection: active=%v expanded=%v", model.selectionActive, model.transcript[0].toolExpanded)
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
	location := transcriptEntryLocations(model.transcript, model.viewport.Width, true)[0]
	y := 1 + location.startRow - model.viewport.YOffset

	next, _ := model.Update(tea.MouseMsg{X: 5, Y: y, Action: tea.MouseActionMotion})
	model = next.(appModel)
	if model.toolHoverIndex != 0 || !model.transcript[0].toolHovered {
		t.Fatalf("hover state = index:%d entry:%#v", model.toolHoverIndex, model.transcript[0])
	}
	if model.transcriptKeyScrollActive {
		t.Fatalf("hover must not steal keyboard scroll focus")
	}
	if rendered := ansi.Strip(renderTranscript(model.transcript, 80, true)); !strings.Contains(rendered, "┃ ✓ Read README.md · ok") {
		t.Fatalf("hover marker missing:\n%s", rendered)
	}

	next, _ = model.Update(tea.MouseMsg{X: 5, Y: model.height - 2, Action: tea.MouseActionMotion})
	model = next.(appModel)
	if model.toolHoverIndex != -1 || model.transcript[0].toolHovered {
		t.Fatalf("hover did not clear outside transcript: index:%d entry:%#v", model.toolHoverIndex, model.transcript[0])
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
	if !strings.Contains(plain, "┃ ✓ Read README.md · ok") {
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

func TestHistoricalToolCallAndResultMergeByID(t *testing.T) {
	callEntries := transcriptEntriesFromMessage(message.Message{
		Role: message.RoleAssistant,
		ToolUses: []message.ToolCall{{
			ID:    "call-1",
			Name:  "Glob",
			Input: json.RawMessage(`{"pattern":"**/*.go"}`),
		}},
	}, time.Now())
	resultEntries := transcriptEntriesFromMessage(message.Message{
		Role: message.RoleUser,
		ToolResults: []message.ToolResult{{
			ToolUseID: "call-1",
			Content:   "main.go",
		}},
	}, time.Now())
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

	if !model.toggleToolExpansion(0) {
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
	for _, width := range []int{8, 12, 20, 40} {
		rendered := renderEntry(entry, width)
		for _, line := range strings.Split(rendered, "\n") {
			if got := ansi.StringWidth(line); got > width {
				t.Fatalf("width=%d rendered cell width=%d line=%q", width, got, ansi.Strip(line))
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
