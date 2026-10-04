package bubble

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"paw/internal/runtime/task"
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
	if !model.activity.visible || !model.input.Focused() || model.activity.tab != activityTabTasks {
		t.Fatalf("opened activity = %+v", model.activity)
	}
	model.closeActivity()
	if model.activity.visible || !model.input.Focused() {
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

func TestActivityTogglePreservesInputAndCompletion(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.ready, model.width, model.height = true, 120, 30
	model.activity.visible = true
	model.input.SetValue("draft")
	model.completion = &completion{kind: completionKindCommand, items: []string{"/help"}}
	for _, visible := range []bool{false, true} {
		next, _ := model.Update(tea.KeyMsg{Type: tea.KeyCtrlG})
		model = next.(appModel)
		if model.activity.visible != visible || !model.input.Focused() || model.input.Value() != "draft" || model.completion == nil {
			t.Fatalf("toggle lost editor state: visible=%v input=%q focus=%v completion=%v", model.activity.visible, model.input.Value(), model.input.Focused(), model.completion)
		}
	}
}

func TestWideActivityEscReturnsFocusWithoutClosingDock(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = 120
	model.height = 30
	model.openActivity(activityTabTasks)

	next, _ := model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	model = next.(appModel)
	if !model.activity.visible || !model.input.Focused() {
		t.Fatalf("Esc state = %+v focused=%v", model.activity, model.input.Focused())
	}
}

func TestClosedActivityDoesNotInterceptCtrlW(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.closeActivity()
	if _, handled, _ := model.handleActivityGlobalKey(tea.KeyMsg{Type: tea.KeyCtrlW}); handled {
		t.Fatal("closed Activity intercepted Ctrl+W")
	}
}

func TestActivityCtrlWEditsWordWithoutSwallowingNextKey(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.ready, model.width, model.height = true, 120, 30
	model.openActivity(activityTabTasks)
	model.input.SetValue("keep draft")
	model.input.CursorEnd()
	next, _ := model.Update(tea.KeyMsg{Type: tea.KeyCtrlW})
	model = next.(appModel)
	next, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	model = next.(appModel)
	if model.input.Value() != "keep x" || !model.input.Focused() {
		t.Fatalf("Ctrl+W did not edit input: value=%q focus=%v", model.input.Value(), model.input.Focused())
	}
}
