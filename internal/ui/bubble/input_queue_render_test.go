package bubble

import (
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func TestQueuePanelIsRenderedBelowInputPanel(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = 80
	model.height = 24
	model.input.SetValue("current draft")
	model.chatQueue.Enqueue("queued draft")
	model.relayout()

	inputBox := ansi.Strip(model.renderInputBox())
	if strings.Contains(inputBox, "queued draft") {
		t.Fatalf("input box contains queue item; queue should be outside input panel: %q", inputBox)
	}
	view := ansi.Strip(model.View())
	if !strings.Contains(view, "current draft") || !strings.Contains(view, "1 个任务排队中") {
		t.Fatalf("view = %q, want input and independent queue summary", view)
	}
}

func TestQueueSelectionDownThenIPressesEdit(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = 80
	model.height = 24
	model.chatQueue.Enqueue("first queued")
	model.chatQueue.Enqueue("second queued")
	model.relayout()

	next, _ := model.Update(tea.KeyMsg{Type: tea.KeyDown})
	model = next.(appModel)
	if model.queueMode != queueModeSelecting {
		t.Fatalf("queue mode = %v, want selecting", model.queueMode)
	}

	next, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
	model = next.(appModel)
	if model.queueMode != queueModeEditing || model.queueEdit == nil {
		t.Fatalf("queue mode/edit = %v/%#v, want editing state", model.queueMode, model.queueEdit)
	}
	if got := model.input.Value(); got != "second queued" {
		t.Fatalf("input = %q, want selected queue item", got)
	}
}

func TestQueueSelectionDownDuringRunningTurnOpensQueue(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = 80
	model.height = 24
	if !model.queryGuard.StartModel() {
		t.Fatal("StartModel() failed")
	}
	model.syncRunningFlags()
	model = model.queueChatInput("first queued")
	model = model.queueChatInput("second queued")
	model.relayout()

	next, _ := model.Update(tea.KeyMsg{Type: tea.KeyDown})
	model = next.(appModel)
	if model.queueMode != queueModeSelecting {
		t.Fatalf("queue mode = %v, want selecting while turn is running", model.queueMode)
	}
}

func TestLongSoftWrappedInputUsesWidthAwareFoldProjection(t *testing.T) {
	value := strings.Repeat("x", 120)
	width := 10
	if !inputPasteFoldableWithWidth(value, width) {
		t.Fatalf("long single line should fold at width %d", width)
	}
	projected, hidden, ok := inputPasteFoldProjectionWithWidth(value, width)
	if !ok || hidden <= 0 {
		t.Fatalf("projection = %#v, hidden = %d, ok = %v", projected, hidden, ok)
	}
	if len(projected) > inputMaxVisibleLines {
		t.Fatalf("projected rows = %d, want <= %d", len(projected), inputMaxVisibleLines)
	}
	if !strings.Contains(strings.Join(projected, "\n"), "lines folded") {
		t.Fatalf("projection = %#v, want fold marker", projected)
	}
}

func TestTokenFoldProjectionUsesVisualRowIndexForLongLine(t *testing.T) {
	value := strings.Repeat("x", 120)
	projection := projectInput(value, nil, len([]rune(value)), 10, true)
	if len(projection.lines) == 0 {
		t.Fatal("projection has no lines")
	}
	visible := ""
	for _, line := range projection.lines {
		for _, atom := range line.atoms {
			visible += atom.text
		}
	}
	if !strings.Contains(visible, "...") {
		t.Fatalf("token projection = %q, want fold marker", visible)
	}
}

func TestCursorLineHighlightSurvivesTokenRendering(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.width = 80
	model.input.SetValue("/help rest")
	model.inputTokens = []inputToken{{Kind: inputTokenCommand, Start: 0, End: 5, Label: "/help"}}
	model.input.Focus()
	model.input.CursorStart()

	rendered := model.renderTokenInputContent()
	want := model.input.FocusedStyle.CursorLine.Render("/help rest")
	if ansi.Strip(rendered) == "" || !strings.Contains(rendered, want) {
		t.Fatalf("rendered token line = %q, want CursorLine styling", rendered)
	}
}

// Keep textarea imported in this regression file as a compile-time reminder that
// cursor/row calculations use the Bubble textarea model rather than logical lines.
var _ textarea.Model
