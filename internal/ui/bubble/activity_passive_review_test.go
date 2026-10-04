package bubble

import (
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"paw/internal/runtime/task"
	"paw/internal/todo"
	"paw/internal/tokentracer"
)

func passiveReviewTodo(at time.Time, status todo.Status) todo.Snapshot {
	return todo.Snapshot{Items: []todo.Item{{ID: "one", Content: "fixture milestone", Status: status}}, UpdatedAt: at}
}

func applyPassiveReviewTasks(m *appModel, tasks []task.TaskSnapshot, at time.Time) {
	m.observeActivityTasks(tasks, at)
	m.activity.tasks = append([]task.TaskSnapshot(nil), tasks...)
}

func TestActivityPassiveLatestUsesSemanticUpdates(t *testing.T) {
	m := newTestModel(&fakeRunner{})
	t0 := time.Unix(1000, 0)
	m.applyTodoSnapshot(passiveReviewTodo(t0.Add(10*time.Second), todo.StatusPending), false)
	tasks := []task.TaskSnapshot{{ID: "worker", ParentSessionID: m.sessionID, Status: task.TaskRunning, StartedAt: t0}}
	applyPassiveReviewTasks(&m, tasks, t0.Add(time.Hour))
	if got := m.activitySummaryTab(); got != activityTabTodo {
		t.Fatalf("old task discovery overtook newer durable Todo: %v", got)
	}
	finished := t0.Add(20 * time.Second)
	tasks[0].Status, tasks[0].FinishedAt = task.TaskCompleted, &finished
	applyPassiveReviewTasks(&m, tasks, finished)
	if got := m.activitySummaryTab(); got != activityTabTasks {
		t.Fatalf("real task completion was not selected: %v", got)
	}
	m.applyTodoSnapshot(passiveReviewTodo(t0.Add(30*time.Second), todo.StatusPending), true)
	if got := m.activitySummaryTab(); got != activityTabTasks {
		t.Fatalf("duplicate Todo with fresh timestamp stole summary: %v", got)
	}
	m.applyTodoSnapshot(passiveReviewTodo(t0.Add(40*time.Second), todo.StatusCompleted), true)
	applyPassiveReviewTasks(&m, tasks, t0.Add(50*time.Second))
	if got := m.activitySummaryTab(); got != activityTabTodo {
		t.Fatalf("unchanged task poll overtook actual Todo change: %v", got)
	}
	tasks[0].Content = "new result without a separate task update timestamp"
	applyPassiveReviewTasks(&m, tasks, t0.Add(60*time.Second))
	if got := m.activitySummaryTab(); got != activityTabTasks {
		t.Fatalf("meaningful untimestamped task change was ignored: %v", got)
	}
	if !m.input.Focused() {
		t.Fatal("automatic summary selection blurred input")
	}
}

func TestActivityPassiveTaskOrderAndClonedValuesDoNotCountAsUpdates(t *testing.T) {
	m := newTestModel(&fakeRunner{})
	t0 := time.Unix(1000, 0)
	tasks := []task.TaskSnapshot{{ID: "a", StartedAt: t0, Usage: &tokentracer.Usage{Input: 10}}, {ID: "b", StartedAt: t0.Add(time.Second)}}
	applyPassiveReviewTasks(&m, tasks, t0)
	m.applyTodoSnapshot(passiveReviewTodo(t0.Add(2*time.Second), todo.StatusPending), false)
	tasks[0].Usage = &tokentracer.Usage{Input: 10}
	applyPassiveReviewTasks(&m, []task.TaskSnapshot{tasks[1], tasks[0]}, t0.Add(time.Hour))
	if got := m.activitySummaryTab(); got != activityTabTodo {
		t.Fatalf("reordered task poll stole summary: %v", got)
	}
}

func TestActivityPassiveClearAndSessionReset(t *testing.T) {
	m := newTestModel(&fakeRunner{})
	t0 := time.Unix(1000, 0)
	applyPassiveReviewTasks(&m, []task.TaskSnapshot{{ID: "old-worker", StartedAt: t0}}, t0)
	m.applyTodoSnapshot(passiveReviewTodo(t0.Add(time.Second), todo.StatusPending), false)
	m.applyTodoSnapshot(todo.Snapshot{Items: []todo.Item{}, UpdatedAt: t0.Add(2 * time.Second)}, true)
	if m.hasCurrentTodo || m.activitySummaryTab() != activityTabTasks {
		t.Fatal("cleared Todo did not fall back to available task content")
	}
	m.sessionID = "new-session"
	m.currentTodo = passiveReviewTodo(t0.Add(-time.Hour), todo.StatusPending)
	m.hasCurrentTodo = true
	m.resetActivitySummary()
	if len(m.activity.tasks) != 0 || m.activitySummaryTab() != activityTabTodo {
		t.Fatal("session reset retained old task content or selection")
	}
	applyPassiveReviewTasks(&m, []task.TaskSnapshot{{ID: "restored-worker", StartedAt: t0.Add(-time.Minute)}}, t0.Add(time.Hour))
	if m.activitySummaryTab() != activityTabTasks {
		t.Fatal("restored Todo was incorrectly stamped as a fresh live update")
	}
	before := m.currentTodo.Clone()
	next, _ := m.Update(todoBrokerEventMsg{sessionID: "previous-session", snapshot: passiveReviewTodo(t0.Add(3*time.Hour), todo.StatusCompleted)})
	m = next.(appModel)
	if m.currentTodo.Items[0].Status != before.Items[0].Status || m.activitySummaryTab() != activityTabTasks {
		t.Fatal("late previous-session Todo changed current summary")
	}
	applyPassiveReviewTasks(&m, nil, t0.Add(4*time.Hour))
	if m.activitySummaryTab() != activityTabTodo {
		t.Fatal("empty task list did not fall back to Todo")
	}
}

func TestActivityPassiveZeroTimeAndRepeatedEmptySnapshotsStayStable(t *testing.T) {
	m := newTestModel(&fakeRunner{})
	m.applyTodoSnapshot(passiveReviewTodo(time.Unix(100, 0), todo.StatusPending), false)
	applyPassiveReviewTasks(&m, []task.TaskSnapshot{{ID: "legacy-untimed"}}, time.Now())
	if m.activitySummaryTab() != activityTabTodo {
		t.Fatal("untimed restored task was promoted above timestamped Todo")
	}
	m.applyTodoSnapshot(todo.Snapshot{Items: []todo.Item{}}, true)
	applyPassiveReviewTasks(&m, nil, time.Now())
	before := m.activitySummaryTab()
	for range 3 {
		applyPassiveReviewTasks(&m, nil, time.Now())
		m.applyTodoSnapshot(todo.Snapshot{Items: []todo.Item{}}, true)
		if m.activitySummaryTab() != before {
			t.Fatal("empty snapshot polling changed selected source")
		}
	}
}

func TestActivityPassiveEventsPreserveInputFocusAndDraft(t *testing.T) {
	m := newTestModel(&fakeRunner{})
	m.width, m.height, m.ready = 120, 40, true
	m.activity.visible = true
	m.input.SetValue("draft remains in input")
	m.taskController = &fakeTaskController{tasks: []task.TaskSnapshot{{ID: "worker", ParentSessionID: m.sessionID, Status: task.TaskRunning, StartedAt: time.Unix(100, 0)}}}
	for _, msg := range []tea.Msg{
		taskUpdateMsg{},
		todoBrokerEventMsg{sessionID: m.sessionID, snapshot: passiveReviewTodo(time.Unix(200, 0), todo.StatusInProgress)},
		cursorFrameMsg(time.Unix(300, 0)),
	} {
		next, _ := m.Update(msg)
		m = next.(appModel)
		if !m.input.Focused() || m.input.Value() != "draft remains in input" {
			t.Fatal("passive update changed input focus or draft")
		}
	}
}

func TestActivityPassiveReopenSelectsUpdatesWhileHidden(t *testing.T) {
	m := newTestModel(&fakeRunner{})
	m.ready, m.width, m.height = true, 120, 40
	now := time.Now()
	m.applyTodoSnapshot(passiveReviewTodo(now.Add(-time.Minute), todo.StatusPending), false)
	m.refreshActivityTasks()
	m.closeActivity()
	m.taskController = &fakeTaskController{tasks: []task.TaskSnapshot{{ID: "new-worker", ParentSessionID: m.sessionID, Status: task.TaskRunning, StartedAt: now}}}
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlG})
	m = next.(appModel)
	if m.activity.tab != activityTabTasks || !m.input.Focused() {
		t.Fatalf("reopen reused stale summary: %+v", m.activity)
	}
}
