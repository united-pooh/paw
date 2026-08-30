package bubble

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"paw/internal/task"
)

func TestOpenAndCloseActivityPreserveWidthTabAndSelection(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.width = 120
	model.height = 30
	model.ready = true
	model.activity.widthColumns = 44
	model.activity.tab = activityTabTodo
	model.activity.selectedTaskID = "task-2"
	model.taskController = &fakeTaskController{tasks: []task.TaskSnapshot{{ID: "task-2", ParentSessionID: model.sessionID}}}

	model.openActivity(activityTabTasks)
	if !model.activity.visible || model.activity.focus != activityFocusPanel || model.activity.tab != activityTabTasks {
		t.Fatalf("opened activity = %+v", model.activity)
	}
	model.closeActivity()
	if model.activity.visible || model.activity.focus != activityFocusWorkspace {
		t.Fatalf("closed activity = %+v", model.activity)
	}
	if model.activity.widthColumns != 44 || model.activity.selectedTaskID != "task-2" {
		t.Fatalf("persistent fields lost: %+v", model.activity)
	}
}

func TestRefreshActivityTasksPreservesSelectionByID(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.activity.visible = true
	model.activity.tasks = []task.TaskSnapshot{{ID: "a", ParentSessionID: model.sessionID}, {ID: "b", ParentSessionID: model.sessionID}, {ID: "c", ParentSessionID: model.sessionID}}
	model.activity.selectedIndex = 1
	model.activity.selectedTaskID = "b"
	model.taskController = &fakeTaskController{tasks: []task.TaskSnapshot{{ID: "c", ParentSessionID: model.sessionID}, {ID: "b", ParentSessionID: model.sessionID}, {ID: "a", ParentSessionID: model.sessionID}}}

	model.refreshActivityTasks()
	if model.activity.selectedIndex != 1 || model.activity.selectedTaskID != "b" {
		t.Fatalf("selection after reorder = %+v", model.activity)
	}

	model.taskController = &fakeTaskController{tasks: []task.TaskSnapshot{{ID: "c", ParentSessionID: model.sessionID}, {ID: "a", ParentSessionID: model.sessionID}}}
	model.refreshActivityTasks()
	if model.activity.selectedIndex != 1 || model.activity.selectedTaskID != "a" {
		t.Fatalf("selection after removal = %+v, want adjacent task a", model.activity)
	}
}

func TestActivityGlobalKeysToggleFocusAndResize(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = 120
	model.height = 30
	model.relayout()

	next, _ := model.Update(tea.KeyMsg{Type: tea.KeyCtrlG})
	model = next.(appModel)
	if !model.activity.visible || model.activity.focus != activityFocusPanel {
		t.Fatalf("ctrl+g open = %+v", model.activity)
	}

	next, _ = model.Update(tea.KeyMsg{Type: tea.KeyCtrlW})
	model = next.(appModel)
	next, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}})
	model = next.(appModel)
	if model.activity.focus != activityFocusWorkspace || !model.input.Focused() {
		t.Fatalf("ctrl+w h = %+v focused=%v", model.activity, model.input.Focused())
	}

	next, _ = model.Update(tea.KeyMsg{Type: tea.KeyCtrlW})
	model = next.(appModel)
	next, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'l'}})
	model = next.(appModel)
	if model.activity.focus != activityFocusPanel || model.input.Focused() {
		t.Fatalf("ctrl+w l = %+v focused=%v", model.activity, model.input.Focused())
	}

	model.activity.widthColumns = 40
	for _, key := range []rune{'<', '>'} {
		next, _ = model.Update(tea.KeyMsg{Type: tea.KeyCtrlW})
		model = next.(appModel)
		next, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{key}})
		model = next.(appModel)
	}
	if model.activity.widthColumns != 40 {
		t.Fatalf("shrink+grow width = %d, want 40", model.activity.widthColumns)
	}
}

func TestClosedActivityDoesNotInterceptCtrlW(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	if _, handled, _ := model.handleActivityGlobalKey(tea.KeyMsg{Type: tea.KeyCtrlW}); handled {
		t.Fatal("closed Activity intercepted Ctrl+W")
	}
}

func TestActivityInvalidCtrlWChordIsConsumed(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = 120
	model.height = 30
	model.openActivity(activityTabTasks)
	model.activity.focus = activityFocusWorkspace
	model.input.Focus()
	model.input.SetValue("draft")

	next, _ := model.Update(tea.KeyMsg{Type: tea.KeyCtrlW})
	model = next.(appModel)
	next, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	model = next.(appModel)
	if model.input.Value() != "draft" || model.activity.commandPrefix != activityCommandIdle {
		t.Fatalf("invalid chord leaked: value=%q activity=%+v", model.input.Value(), model.activity)
	}
}
