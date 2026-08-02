package bubble

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"paw/internal/todo"
)

func TestRenderCollapsedTodoUpdated(t *testing.T) {
	entry := transcriptEntry{
		kind: entryTodo,
		todoSnapshot: &todo.Snapshot{Items: []todo.Item{
			{ID: "a", Content: "A", Status: todo.StatusCompleted},
			{ID: "b", Content: "B", Status: todo.StatusInProgress},
			{ID: "c", Content: "C", Status: todo.StatusPending},
		}},
		todoExpanded: false,
	}
	got := ansi.Strip(renderEntry(entry, 80))
	if !strings.Contains(got, "▸ Todo updated · 1/3") {
		t.Fatalf("render = %q", got)
	}
}

func TestRenderExpandedTodoCard(t *testing.T) {
	entry := transcriptEntry{
		kind: entryTodo,
		todoSnapshot: &todo.Snapshot{
			Explanation: "Start implementation",
			Items: []todo.Item{
				{ID: "a", Content: "Inspect history", Status: todo.StatusCompleted},
				{ID: "b", Content: "Build page", Status: todo.StatusInProgress},
				{ID: "c", Content: "Add tests", Status: todo.StatusPending},
			},
		},
		todoExpanded: true,
		todoLatest:   true,
	}
	got := ansi.Strip(renderEntry(entry, 80))
	for _, want := range []string{"Todo", "1/3", "✓", "Inspect history", "●", "Build page", "○", "Add tests", "Start implementation"} {
		if !strings.Contains(got, want) {
			t.Fatalf("render missing %q: %q", want, got)
		}
	}
}

func TestRenderTodoSummaryVariants(t *testing.T) {
	completed := transcriptEntry{
		kind: entryTodo,
		todoSnapshot: &todo.Snapshot{Items: []todo.Item{
			{ID: "a", Content: "A", Status: todo.StatusCompleted},
		}},
		todoCompletedFold: true,
	}
	if got := ansi.Strip(renderEntry(completed, 40)); !strings.Contains(got, "✓ Todo completed · 1/1") {
		t.Fatalf("completed render = %q", got)
	}
	cleared := transcriptEntry{kind: entryTodo, todoSnapshot: &todo.Snapshot{Items: []todo.Item{}}, todoCleared: true}
	if got := ansi.Strip(renderEntry(cleared, 40)); !strings.Contains(got, "─ Todo cleared") {
		t.Fatalf("cleared render = %q", got)
	}
}

func TestTodoRenderFitsWidths(t *testing.T) {
	entry := transcriptEntry{
		kind: entryTodo,
		todoSnapshot: &todo.Snapshot{
			Explanation: "中文 explanation 🙂",
			Items:       []todo.Item{{ID: "a", Content: "构建一个很长很长的 Todo 页面 with English and emoji 🚀", Status: todo.StatusInProgress}},
		},
		todoExpanded: true,
	}
	for _, width := range []int{120, 80, 40, 20, 8, 1} {
		rendered := renderEntry(entry, width)
		for _, line := range strings.Split(rendered, "\n") {
			if got := lipgloss.Width(line); got > width {
				t.Fatalf("width %d overflow (%d): %q", width, got, ansi.Strip(line))
			}
		}
	}
}

func TestTodoTranscriptLocationMatchesRenderedHeight(t *testing.T) {
	entry := transcriptEntry{
		kind: entryTodo,
		todoSnapshot: &todo.Snapshot{Items: []todo.Item{{
			ID: "a", Content: strings.Repeat("long content ", 10), Status: todo.StatusInProgress,
		}}},
		todoExpanded: true,
	}
	locations := transcriptEntryLocations([]transcriptEntry{entry}, 40, true, zeroTime())
	rendered := renderEntry(entry, 40)
	if len(locations) != 1 || locations[0].height != lipgloss.Height(rendered) {
		t.Fatalf("locations = %#v, rendered height = %d", locations, lipgloss.Height(rendered))
	}
}

func TestToggleTodoAtTranscriptRow(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.viewport.Width = 80
	model.applyTodoSnapshot(todo.Snapshot{Items: []todo.Item{{ID: "a", Content: "A", Status: todo.StatusPending}}}, false)
	if !model.toggleTodoAtTranscriptRow(0) || model.transcript[0].todoExpanded {
		t.Fatalf("todo did not collapse: %#v", model.transcript[0])
	}
	if !model.toggleTodoAtTranscriptRow(0) || !model.transcript[0].todoExpanded {
		t.Fatalf("todo did not expand: %#v", model.transcript[0])
	}
}

func zeroTime() time.Time { return time.Time{} }
