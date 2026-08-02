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

func TestCompletedTodoStaysExpandedBeforeFinalAnswer(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.applyTodoSnapshot(allCompletedTodo(), true)
	entry := model.transcript[model.latestTodoIndex]
	if !entry.todoExpanded || entry.todoCompletedFold {
		t.Fatalf("completed snapshot folded too early: %#v", entry)
	}
}

func TestSuccessfulFinalAnswerCollapsesCompletedTodo(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.applyTodoSnapshot(allCompletedTodo(), true)
	model.transcript = append(model.transcript, transcriptEntry{kind: entryAssistant, title: "assistant", body: "done"})
	model.doneAssistant = len(model.transcript) - 1
	model.turnHasModelOutput = true

	next, _ := model.Update(turnFinishedMsg{})
	got := next.(appModel)
	entry := got.transcript[got.latestTodoIndex]
	if entry.todoExpanded || !entry.todoCompletedFold {
		t.Fatalf("completed snapshot not folded: %#v", entry)
	}
}

func TestFinalAnswerDoesNotFoldIncompleteTodo(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.applyTodoSnapshot(testTodoSnapshot(todo.StatusInProgress), true)
	model.transcript = append(model.transcript, transcriptEntry{kind: entryAssistant, title: "assistant", body: "partial"})
	model.doneAssistant = len(model.transcript) - 1
	model.turnHasModelOutput = true

	next, _ := model.Update(turnFinishedMsg{})
	entry := next.(appModel).transcript[model.latestTodoIndex]
	if !entry.todoExpanded || entry.todoCompletedFold {
		t.Fatalf("incomplete Todo folded: %#v", entry)
	}
}

func TestFailedOrEmptyFinalAnswerDoesNotFoldCompletedTodo(t *testing.T) {
	for _, tt := range []struct {
		name string
		err  error
		body string
	}{
		{name: "failed", err: errors.New("boom"), body: "failed output"},
		{name: "cancelled", err: context.Canceled, body: "cancelled output"},
		{name: "empty", body: "   "},
	} {
		t.Run(tt.name, func(t *testing.T) {
			model := newTestModel(&fakeRunner{})
			model.applyTodoSnapshot(allCompletedTodo(), true)
			model.transcript = append(model.transcript, transcriptEntry{kind: entryAssistant, title: "assistant", body: tt.body})
			model.doneAssistant = len(model.transcript) - 1
			model.turnHasModelOutput = tt.body != ""
			next, _ := model.Update(turnFinishedMsg{err: tt.err})
			entry := next.(appModel).transcript[model.latestTodoIndex]
			if !entry.todoExpanded || entry.todoCompletedFold {
				t.Fatalf("Todo folded without successful visible final answer: %#v", entry)
			}
		})
	}
}

func TestNewTodoAfterCompletedFoldStartsExpanded(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.applyTodoSnapshot(allCompletedTodo(), true)
	model.foldCompletedTodoAfterFinalAnswer()
	model.applyTodoSnapshot(testTodoSnapshot(todo.StatusPending), true)

	old := model.transcript[0]
	latest := model.transcript[model.latestTodoIndex]
	if !old.todoCompletedFold || old.todoExpanded {
		t.Fatalf("old completed entry changed: %#v", old)
	}
	if latest.todoCompletedFold || !latest.todoExpanded {
		t.Fatalf("new entry not expanded: %#v", latest)
	}
}

func TestTodoUpdatePreservesDetachedScrollAndCountsOnce(t *testing.T) {
	model := newTranscriptScrollTestModel()
	model.viewport.SetYOffset(3)
	if model.viewport.AtBottom() {
		t.Fatal("test model unexpectedly at bottom")
	}
	before := model.viewport.YOffset

	model.applyTodoSnapshot(testTodoSnapshot(todo.StatusInProgress), true)
	if model.viewport.YOffset != before {
		t.Fatalf("YOffset = %d, want %d", model.viewport.YOffset, before)
	}
	if model.newMessageNoticeCount != 1 {
		t.Fatalf("newMessageNoticeCount = %d, want 1", model.newMessageNoticeCount)
	}
	model.refreshViewportPreservingOffset()
	if model.newMessageNoticeCount != 1 {
		t.Fatalf("refresh recounted Todo: %d", model.newMessageNoticeCount)
	}
}

func TestTodoUpdateAtBottomFollowsWithoutNotice(t *testing.T) {
	model := newTranscriptScrollTestModel()
	model.viewport.GotoBottom()
	model.applyTodoSnapshot(testTodoSnapshot(todo.StatusInProgress), true)
	if !model.viewport.AtBottom() || model.newMessageNoticeCount != 0 {
		t.Fatalf("bottom state: atBottom=%v count=%d", model.viewport.AtBottom(), model.newMessageNoticeCount)
	}
}

func TestTodoExpansionAndCompletionFoldDoNotCountAsNewMessages(t *testing.T) {
	model := newTranscriptScrollTestModel()
	model.viewport.SetYOffset(3)
	model.applyTodoSnapshot(allCompletedTodo(), true)
	if model.newMessageNoticeCount != 1 {
		t.Fatalf("initial count = %d", model.newMessageNoticeCount)
	}
	model.foldCompletedTodoAfterFinalAnswer()
	if model.newMessageNoticeCount != 1 {
		t.Fatalf("fold recounted Todo: %d", model.newMessageNoticeCount)
	}
	row := transcriptEntryLocations(model.transcript, model.viewport.Width, true, model.animationNow())[model.latestTodoIndex].startRow
	model.toggleTodoAtTranscriptRow(row)
	if model.newMessageNoticeCount != 1 {
		t.Fatalf("toggle recounted Todo: %d", model.newMessageNoticeCount)
	}
}

func TestTodoUpdateWhilePageOpenCountsOnceAndResetsScroll(t *testing.T) {
	model := newTranscriptScrollTestModel()
	model.viewport.SetYOffset(3)
	model.todoPage = &todoPage{offset: 5}
	model.applyTodoSnapshot(testTodoSnapshot(todo.StatusInProgress), true)
	if model.newMessageNoticeCount != 1 || model.todoPage.offset != 0 {
		t.Fatalf("count=%d offset=%d", model.newMessageNoticeCount, model.todoPage.offset)
	}
	if model.newMessageNoticeCanRender() {
		t.Fatal("notice rendered while Todo page was open")
	}
	model.todoPage = nil
	if !model.newMessageNoticeCanRender() || model.newMessageNoticeCount != 1 {
		t.Fatalf("notice not restored after close: canRender=%v count=%d", model.newMessageNoticeCanRender(), model.newMessageNoticeCount)
	}
}

func allCompletedTodo() todo.Snapshot {
	return todo.Snapshot{Items: []todo.Item{
		{ID: "a", Content: "A", Status: todo.StatusCompleted},
		{ID: "b", Content: "B", Status: todo.StatusCompleted},
	}}
}

func testTodoSnapshot(status todo.Status) todo.Snapshot {
	return todo.Snapshot{
		Items:     []todo.Item{{ID: "task", Content: "Task", Status: status}},
		UpdatedAt: time.Unix(100, 0).UTC(),
	}
}
