package bubble

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"paw/internal/todo"
	selecttool "paw/internal/tool/select"
)

func TestCtrlPOpensTodoPageAndPreservesDraft(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.input.SetValue("keep this draft")

	next, _ := model.Update(tea.KeyMsg{Type: tea.KeyCtrlP})
	got := next.(appModel)
	if got.todoPage == nil {
		t.Fatal("Ctrl+P did not open Todo page")
	}
	if got.input.Value() != "keep this draft" {
		t.Fatalf("draft changed to %q", got.input.Value())
	}
	if got.input.Focused() {
		t.Fatal("input remained focused while Todo page is open")
	}
}

func TestTodoPageClosesWithEscapeAndCtrlP(t *testing.T) {
	for _, keyMsg := range []tea.KeyMsg{{Type: tea.KeyEsc}, {Type: tea.KeyCtrlP}} {
		model := newTestModel(&fakeRunner{})
		model.todoPage = newTodoPage()
		model.input.Blur()
		next, _ := model.Update(keyMsg)
		got := next.(appModel)
		if got.todoPage != nil {
			t.Fatalf("page remained open for %s", keyMsg.String())
		}
		if !got.input.Focused() {
			t.Fatalf("closing %s did not restore input focus", keyMsg.String())
		}
	}
}

func TestTodoPageConsumesTyping(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.input.SetValue("draft")
	model.todoPage = newTodoPage()
	model.input.Blur()

	next, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	got := next.(appModel)
	if got.input.Value() != "draft" || got.todoPage == nil {
		t.Fatalf("typing leaked to input: value=%q page=%v", got.input.Value(), got.todoPage != nil)
	}
}

func TestCtrlPDoesNotOverrideSelectionDock(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.selectionDock = newSelectionDock(selectionRequest("todo-page", selecttool.ModeSingle))
	next, _ := model.Update(tea.KeyMsg{Type: tea.KeyCtrlP})
	if next.(appModel).todoPage != nil {
		t.Fatal("Ctrl+P opened Todo page while Selection Dock was active")
	}
}

func TestTodoPageShowsCurrentSnapshot(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.hasCurrentTodo = true
	model.currentTodo = todo.Snapshot{
		Explanation: "Start implementation",
		Items: []todo.Item{
			{ID: "a", Content: "Inspect code", Status: todo.StatusCompleted},
			{ID: "b", Content: "Build page", Status: todo.StatusInProgress},
			{ID: "c", Content: "Add tests", Status: todo.StatusPending},
		},
	}
	model.todoPage = newTodoPage()

	got := ansi.Strip(model.renderTodoPage(80, 20))
	for _, want := range []string{"Todo", "1/3", "Start implementation", "Inspect code", "Build page", "Add tests", "↑↓ scroll", "esc close"} {
		if !strings.Contains(got, want) {
			t.Fatalf("page missing %q: %q", want, got)
		}
	}
}

func TestTodoPageEmptyState(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.todoPage = newTodoPage()
	got := ansi.Strip(model.renderTodoPage(60, 12))
	for _, want := range []string{"No active todo list", "automatically for complex tasks", "esc close"} {
		if !strings.Contains(got, want) {
			t.Fatalf("empty page missing %q: %q", want, got)
		}
	}
}

func TestTodoPageScrollsAndClamps(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = 40
	model.height = 10
	model.hasCurrentTodo = true
	for i := 0; i < 20; i++ {
		model.currentTodo.Items = append(model.currentTodo.Items, todo.Item{ID: string(rune('a' + i)), Content: "Task " + string(rune('A'+i)), Status: todo.StatusPending})
	}
	model.todoPage = newTodoPage()
	model.input.Blur()

	next, _ := model.Update(tea.KeyMsg{Type: tea.KeyEnd})
	got := next.(appModel)
	if got.todoPage.offset == 0 {
		t.Fatal("End did not scroll Todo page")
	}
	maxOffset := got.todoPageMaxOffset(maxInt(1, got.currentLayout().contentHeight-2))
	if got.todoPage.offset != maxOffset {
		t.Fatalf("offset = %d, want %d", got.todoPage.offset, maxOffset)
	}
	next, _ = got.Update(tea.KeyMsg{Type: tea.KeyHome})
	if next.(appModel).todoPage.offset != 0 {
		t.Fatal("Home did not return to top")
	}
}

func TestTodoPageResetsScrollForNewSnapshot(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.todoPage = &todoPage{offset: 9}
	model.applyTodoSnapshot(todo.Snapshot{Items: []todo.Item{{ID: "a", Content: "A", Status: todo.StatusInProgress}}}, true)
	if model.todoPage.offset != 0 {
		t.Fatalf("offset = %d", model.todoPage.offset)
	}
}

func TestTodoPageViewFitsTerminal(t *testing.T) {
	for _, size := range []struct{ width, height int }{{120, 30}, {80, 24}, {40, 10}, {20, 6}, {8, 3}} {
		model := newTestModel(&fakeRunner{})
		model.ready = true
		model.width = size.width
		model.height = size.height
		model.todoPage = newTodoPage()
		model.hasCurrentTodo = true
		model.currentTodo = todo.Snapshot{Items: []todo.Item{{ID: "a", Content: "很长的 Todo content 🚀", Status: todo.StatusInProgress}}}
		view := model.View()
		plain := ansi.Strip(view)
		lines := strings.Split(plain, "\n")
		if len(lines) != size.height {
			t.Fatalf("%dx%d height = %d", size.width, size.height, len(lines))
		}
		for _, line := range lines {
			if got := lipgloss.Width(line); got != size.width {
				t.Fatalf("%dx%d line width = %d: %q", size.width, size.height, got, line)
			}
		}
	}
}

func TestTodoPageHidesNewMessageNoticeAndCursorAnchor(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.todoPage = newTodoPage()
	model.newMessageNoticeCount = 2
	if model.newMessageNoticeCanRender() {
		t.Fatal("new message notice renders over Todo page")
	}
	if model.shouldAnchorTextInputCursor() {
		t.Fatal("input cursor anchors while Todo page is open")
	}
}
