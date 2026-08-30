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

func TestViewRendersFullHeightDockWithoutCoveringWorkspace(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = 120
	model.height = 30
	model.transcript = []transcriptEntry{{kind: entryAssistant, body: "LEFT-SENTINEL " + strings.Repeat("x", 70)}}
	model.openActivity(activityTabTasks)
	model.relayout()
	model.refreshViewport()

	raw := model.View()
	view := ansi.Strip(raw)
	assertFixedFrame(t, raw, 120, 30)
	layout := model.currentLayout()
	if layout.activityMode != activityLayoutDocked {
		t.Fatalf("layout = %+v", layout)
	}
	for row, line := range strings.Split(raw, "\n")[1:29] {
		joint := ansi.Strip(cutStyledCellsExact(line, layout.workspaceWidth, layout.workspaceWidth+1))
		if joint != "│" {
			t.Fatalf("row=%d joint=%q, want │: %q", row, joint, ansi.Strip(line))
		}
	}
	if !strings.Contains(view, "LEFT-SENTINEL") || !strings.Contains(view, "Activity") {
		t.Fatalf("dock lost content:\n%s", view)
	}
}

func TestActivityDockCursorAnchorsOnlyForWorkspaceFocus(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = 120
	model.height = 30
	model.openActivity(activityTabTasks)
	if model.shouldAnchorTextInputCursor() {
		t.Fatal("Activity focus should hide terminal cursor")
	}
	model.activity.focus = activityFocusWorkspace
	model.input.Focus()
	if !model.shouldAnchorTextInputCursor() {
		t.Fatal("workspace focus should anchor terminal cursor")
	}
	model.width = 80
	model.relayout()
	if model.shouldAnchorTextInputCursor() {
		t.Fatal("fullscreen Activity should hide terminal cursor")
	}
}
