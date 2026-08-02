package bubble

import (
	"context"
	"errors"
	"testing"
	"time"

	"paw/internal/todo"
)

func TestTodoBrokerEventCreatesCurrentSnapshot(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	broker := todo.NewBroker()
	model.todoBroker = broker
	snapshot := todo.Snapshot{
		Explanation: "start",
		Items:       []todo.Item{{ID: "build", Content: "Build page", Status: todo.StatusInProgress}},
		UpdatedAt:   time.Unix(100, 0).UTC(),
	}

	next, cmd := model.Update(todoBrokerEventMsg{snapshot: snapshot})
	got := next.(appModel)
	if !got.hasCurrentTodo {
		t.Fatal("hasCurrentTodo = false")
	}
	if got.currentTodo.Items[0].ID != "build" {
		t.Fatalf("currentTodo = %#v", got.currentTodo)
	}
	if cmd == nil {
		t.Fatal("Todo event did not schedule the next broker wait")
	}
}

func TestTodoBrokerClearResetsCurrentSnapshot(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.applyTodoSnapshot(todo.Snapshot{Items: []todo.Item{{ID: "a", Content: "A", Status: todo.StatusPending}}}, true)
	model.applyTodoSnapshot(todo.Snapshot{Items: []todo.Item{}}, true)
	if model.hasCurrentTodo || !model.todoWasCleared {
		t.Fatalf("state = has:%v cleared:%v", model.hasCurrentTodo, model.todoWasCleared)
	}
	if !model.transcript[model.latestTodoIndex].todoCleared {
		t.Fatalf("latest entry = %#v", model.transcript[model.latestTodoIndex])
	}
}

func TestApplyingNewTodoCollapsesPreviousSnapshot(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.applyTodoSnapshot(testTodoSnapshot(todo.StatusInProgress), true)
	model.transcript[model.latestTodoIndex].todoExpanded = true
	model.applyTodoSnapshot(testTodoSnapshot(todo.StatusCompleted), true)

	if model.transcript[0].todoExpanded || model.transcript[0].todoLatest {
		t.Fatalf("old entry not collapsed: %#v", model.transcript[0])
	}
	latest := model.transcript[model.latestTodoIndex]
	if !latest.todoExpanded || !latest.todoLatest {
		t.Fatalf("latest entry not expanded: %#v", latest)
	}
}

func TestApplyingTodoSnapshotCopiesSource(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	snapshot := testTodoSnapshot(todo.StatusPending)
	model.applyTodoSnapshot(snapshot, true)
	snapshot.Items[0].Content = "mutated"
	if model.currentTodo.Items[0].Content != "Task" || model.transcript[model.latestTodoIndex].todoSnapshot.Items[0].Content != "Task" {
		t.Fatalf("Todo state aliases source: current=%#v entry=%#v", model.currentTodo, model.transcript[model.latestTodoIndex])
	}
}

func TestInvalidTodoBrokerEventIsIgnoredAndListenerContinues(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.todoBroker = todo.NewBroker()
	next, cmd := model.Update(todoBrokerEventMsg{snapshot: todo.Snapshot{Items: []todo.Item{{ID: "a", Content: "A", Status: "blocked"}}}})
	got := next.(appModel)
	if got.hasCurrentTodo || len(got.transcript) != 0 || cmd == nil {
		t.Fatalf("invalid event state: has=%v transcript=%d cmd=%v", got.hasCurrentTodo, len(got.transcript), cmd != nil)
	}
}

func TestTodoBrokerCloseStopsListener(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.todoBroker = todo.NewBroker()
	for _, err := range []error{todo.ErrBrokerClosed, context.Canceled} {
		next, cmd := model.Update(todoBrokerEventMsg{err: err})
		if cmd != nil || next.(appModel).hasCurrentTodo {
			t.Fatalf("close error %v returned cmd/state", err)
		}
	}
}

func TestTodoBrokerOtherErrorContinuesListener(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.todoBroker = todo.NewBroker()
	_, cmd := model.Update(todoBrokerEventMsg{err: errors.New("temporary")})
	if cmd == nil {
		t.Fatal("temporary error stopped listener")
	}
}

func testTodoSnapshot(status todo.Status) todo.Snapshot {
	return todo.Snapshot{
		Items:     []todo.Item{{ID: "task", Content: "Task", Status: status}},
		UpdatedAt: time.Unix(100, 0).UTC(),
	}
}
