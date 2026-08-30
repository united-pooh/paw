package bubble

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"paw/internal/task"
)

func TestRenderActivityPaneHasExactSizeAndNoNestedBorder(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.activity.visible = true
	model.activity.focus = activityFocusPanel
	model.activity.tab = activityTabTasks
	model.activity.tasks = []task.TaskSnapshot{{
		ID: "worker-1", Name: "layout-research", Status: task.TaskRunning,
		StartedAt: time.Now().Add(-84 * time.Second), UsedTokens: 8200,
	}}
	model.taskController = &fakeTaskController{tasks: append([]task.TaskSnapshot(nil), model.activity.tasks...)}
	model.activity.selectedTaskID = "worker-1"

	rendered := model.renderActivityPane(40, 18)
	lines := strings.Split(rendered, "\n")
	if len(lines) != 18 {
		t.Fatalf("height=%d, want 18", len(lines))
	}
	for row, line := range lines {
		if got := terminalCellWidth(line); got != 40 {
			t.Fatalf("row=%d width=%d, want 40: %q", row, got, ansi.Strip(line))
		}
	}
	plain := ansi.Strip(rendered)
	for _, want := range []string{"Activity", "Tasks 1", "Todo", "layout-research", "running", "8.2k"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("pane missing %q:\n%s", want, plain)
		}
	}
	for _, banned := range []string{"╭", "╮", "╰", "╯", "┌", "┐", "└", "┘"} {
		if strings.Contains(plain, banned) {
			t.Fatalf("nested border %q found:\n%s", banned, plain)
		}
	}
}
