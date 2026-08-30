package bubble

import (
	"testing"

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
