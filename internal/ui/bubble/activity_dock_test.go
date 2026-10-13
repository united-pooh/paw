package bubble

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"paw/internal/runtime/task"
)

func TestRenderActivityPaneHasExactSizeAndNoNestedBorder(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.activity.visible = true
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
	for _, want := range []string{copyTasksTitle + " 1", copyTokenHistory, "layout-research", "running", "8.2k"} {
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
	if !strings.Contains(view, "LEFT-SENTINEL") || !strings.Contains(view, copyActivityTitle) {
		t.Fatalf("dock lost content:\n%s", view)
	}
}

func TestActivityDockKeepsInputCursorAtEverySize(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.ready, model.height = true, 30
	for _, width := range []int{120, 80, 64, 120} {
		model.width = width
		model.openActivity(activityTabTasks)
		if !model.shouldAnchorTextInputCursor() || !model.input.Focused() {
			t.Fatalf("width %d lost input cursor", width)
		}
	}
}

func TestClosedActivityUsesHeaderHintInsteadOfFloatingTaskCard(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = 120
	model.height = 30
	model.taskController = &activeTaskController{
		fakeTaskController: &fakeTaskController{tasks: []task.TaskSnapshot{{ID: "worker", Name: "worker", Status: task.TaskRunning}}},
		active:             []task.TaskSnapshot{{ID: "worker", Name: "worker", Status: task.TaskRunning}},
	}
	model.activity.visible = false
	model.relayout()

	plain := ansi.Strip(model.View())
	top := strings.Split(plain, "\n")[0]
	if !strings.Contains(top, "1 "+copyToolStatusRunning) || !strings.Contains(top, "Ctrl+G") {
		t.Fatalf("top border missing running hint:\n%s", plain)
	}
	if strings.Contains(plain, "taskController ·") || strings.Contains(plain, "╭") {
		t.Fatalf("legacy floating card still rendered:\n%s", plain)
	}
}

func TestOpenActivityDoesNotDuplicateClosedHint(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = 120
	model.height = 30
	model.taskController = &activeTaskController{
		fakeTaskController: &fakeTaskController{tasks: []task.TaskSnapshot{{ID: "worker", Status: task.TaskRunning}}},
		active:             []task.TaskSnapshot{{ID: "worker", Status: task.TaskRunning}},
	}
	model.openActivity(activityTabTasks)
	plain := ansi.Strip(model.View())
	if strings.Contains(strings.Split(plain, "\n")[0], copyToolStatusRunning+" · Ctrl+G") {
		t.Fatalf("closed hint leaked into open Activity:\n%s", plain)
	}
	if strings.Count(strings.Split(plain, "\n")[0], "● 1") != 1 {
		t.Fatalf("open Activity running count missing or duplicated:\n%s", plain)
	}
}

func TestCompletionStaysInsideWorkspaceWhenActivityDocked(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = 120
	model.height = 30
	model.openActivity(activityTabTasks)
	model.input.Focus()
	model.completion = &completion{kind: completionKindCommand, items: []string{"/help", "/tasks"}}
	model.relayout()

	raw := model.View()
	layout := model.currentLayout()
	for _, line := range strings.Split(raw, "\n")[1:29] {
		left := ansi.Strip(cutStyledCellsExact(line, 0, layout.workspaceWidth))
		right := ansi.Strip(cutStyledCellsExact(line, layout.workspaceWidth+1, layout.frameWidth))
		if strings.Contains(right, "/help") || strings.Contains(right, "/tasks") {
			t.Fatalf("completion leaked into Activity pane:\n%s", ansi.Strip(raw))
		}
		if strings.Contains(left, "/help") {
			return
		}
	}
	t.Fatalf("completion missing from workspace:\n%s", ansi.Strip(raw))
}

func TestWorkspaceModalLeavesActivityPaneUnchanged(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = 120
	model.height = 30
	model.openActivity(activityTabTasks)
	layout := model.currentLayout()
	baseline := strings.Split(model.View(), "\n")

	model.modelWizard = newModelWizard(model.currentModelConfig())
	modal := strings.Split(model.View(), "\n")
	for row := 1; row < model.height-1; row++ {
		baselineRight := ansi.Strip(cutStyledCellsExact(baseline[row], layout.workspaceWidth+1, layout.frameWidth))
		modalRight := ansi.Strip(cutStyledCellsExact(modal[row], layout.workspaceWidth+1, layout.frameWidth))
		if baselineRight != modalRight {
			t.Fatalf("row %d Activity changed under workspace modal:\nbaseline=%q\nmodal=%q", row, baselineRight, modalRight)
		}
	}
}
