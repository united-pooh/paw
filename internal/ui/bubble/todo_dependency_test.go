package bubble

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"paw/internal/runtime/task"
	"paw/internal/todo"
)

func dependencyTodo(t *testing.T, dependencies string) todo.Snapshot {
	t.Helper()
	var snapshot todo.Snapshot
	if err := json.Unmarshal([]byte(fmt.Sprintf(`{"items":[
		{"id":"ship","content":"交付修改","status":"pending","blocked_by":%s},
		{"id":"fix","content":"修复测试","status":"in_progress"},
		{"id":"review","content":"独立审查","status":"pending"},
		{"id":"done","content":"调查完成","status":"completed"}
	]}`, dependencies)), &snapshot); err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func TestTodoDependencyRendersBlockersAndUnlocks(t *testing.T) {
	m := newTestModel(&fakeRunner{})
	m.currentTodo = dependencyTodo(t, `["fix","done"]`)
	m.hasCurrentTodo = true
	blocked := ansi.Strip(m.renderActivityTodo(52, 15))
	for _, want := range []string{"1/4", "⊘", "等待", "fix", "修复测试"} {
		if !strings.Contains(blocked, want) {
			t.Fatalf("blocked Todo missing %q:\n%s", want, blocked)
		}
	}
	for _, line := range strings.Split(blocked, "\n") {
		if strings.Contains(line, "等待") && strings.Contains(line, "done") {
			t.Fatalf("completed prerequisite remains a blocker: %s", line)
		}
	}
	m.currentTodo.Items[1].Status = todo.StatusCompleted
	unblocked := ansi.Strip(m.renderActivityTodo(52, 15))
	if strings.Contains(unblocked, "等待") || strings.Contains(unblocked, "⊘") || !strings.Contains(unblocked, "○ 交付修改") || !strings.Contains(unblocked, "2/4") {
		t.Fatalf("completed prerequisite did not restore pending display:\n%s", unblocked)
	}
}

func TestTodoDependencyRendersWithinNarrowPane(t *testing.T) {
	m := newTestModel(&fakeRunner{})
	m.currentTodo = dependencyTodo(t, `["fix","review"]`)
	m.hasCurrentTodo = true
	for _, width := range []int{20, 24, 32, 52} {
		view := m.renderActivityTodo(width, 12)
		for _, line := range strings.Split(view, "\n") {
			if got := terminalCellWidth(line); got != width {
				t.Fatalf("%d-column Todo has line width %d: %q", width, got, ansi.Strip(line))
			}
		}
		plain := ansi.Strip(view)
		if !strings.Contains(plain, "等待") || !strings.Contains(plain, "fix") || !strings.Contains(plain, "review") {
			t.Fatalf("%d-column Todo loses blockers:\n%s", width, plain)
		}
	}
}

func TestTodoDependencyOverflowKeepsHiddenItemsVisible(t *testing.T) {
	m := newTestModel(&fakeRunner{})
	m.hasCurrentTodo = true
	m.currentTodo.Items = []todo.Item{{ID: "ship", Content: "交付修改", Status: todo.StatusPending}}
	for i := range 8 {
		id := fmt.Sprintf("fix-%d", i)
		m.currentTodo.Items[0].BlockedBy = append(m.currentTodo.Items[0].BlockedBy, id)
		m.currentTodo.Items = append(m.currentTodo.Items, todo.Item{ID: id, Content: "修复测试", Status: todo.StatusPending})
	}
	view := ansi.Strip(m.renderActivityTodo(32, 6))
	for _, want := range []string{"⊘", "等待 fix-0", "+7 more blockers", "+8 more items"} {
		if !strings.Contains(view, want) {
			t.Fatalf("dependency details hid overflow %q:\n%s", want, view)
		}
	}
}

func TestTodoDependencyComparisonUsesDependencySet(t *testing.T) {
	first := dependencyTodo(t, `["fix","review"]`)
	reordered := dependencyTodo(t, `["review","fix"]`)
	if !activityTodoEqual(first, reordered) {
		t.Fatal("dependency order alone changed Todo semantics")
	}
	changed := dependencyTodo(t, `["fix"]`)
	if activityTodoEqual(first, changed) {
		t.Fatal("dependency removal was ignored")
	}
	if !activityTodoEqual(dependencyTodo(t, `[]`), dependencyTodo(t, `null`)) {
		t.Fatal("empty dependency representations changed Todo semantics")
	}
}

func TestTodoDependencyUpdateSelectsTodoWithoutStealingInput(t *testing.T) {
	m := newTestModel(&fakeRunner{})
	m.input.SetValue("保留草稿")
	t0 := time.Unix(1000, 0)
	first := dependencyTodo(t, `["fix"]`)
	first.UpdatedAt = t0
	if !m.applyTodoSnapshot(first, false) {
		t.Fatal("first dependency snapshot was rejected")
	}
	tasks := []task.TaskSnapshot{{ID: "worker", ParentSessionID: m.sessionID, Status: task.TaskRunning, StartedAt: t0.Add(time.Second)}}
	applyPassiveReviewTasks(&m, tasks, t0.Add(time.Second))
	if m.activitySummaryTab() != activityTabTasks {
		t.Fatal("task update did not select Tasks")
	}
	changed := dependencyTodo(t, `["fix","review"]`)
	changed.UpdatedAt = t0.Add(2 * time.Second)
	if !m.applyTodoSnapshot(changed, true) || m.activitySummaryTab() != activityTabTodo {
		t.Fatal("dependency-only change did not select Todo")
	}
	if !m.input.Focused() || m.input.Value() != "保留草稿" {
		t.Fatal("dependency update changed input focus or draft")
	}

	tasks[0].Content = "new worker result"
	applyPassiveReviewTasks(&m, tasks, t0.Add(3*time.Second))
	reordered := dependencyTodo(t, `["review","fix"]`)
	reordered.UpdatedAt = t0.Add(4 * time.Second)
	if !m.applyTodoSnapshot(reordered, true) || m.activitySummaryTab() != activityTabTasks {
		t.Fatal("reordered dependencies stole summary selection")
	}
}
