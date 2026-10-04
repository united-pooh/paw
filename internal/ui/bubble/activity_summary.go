package bubble

import (
	"slices"
	"time"

	"paw/internal/runtime/task"
	"paw/internal/todo"
)

func (m *appModel) resetActivitySummary() {
	m.activity.summarySessionID = m.sessionID
	m.activity.summaryTasksSeeded = false
	m.activity.todoUpdatedAt = time.Time{}
	m.activity.tasksUpdatedAt = time.Time{}
	m.activity.tasks = nil
	m.activity.selectedIndex = 0
	m.activity.selectedTaskID = ""
	if m.hasCurrentTodo && !m.currentTodo.Cleared() {
		m.activity.todoUpdatedAt = m.currentTodo.UpdatedAt
	}
	m.activity.tab = m.activitySummaryTab()
}

func (m appModel) activitySummaryTab() activityTab {
	return m.activitySummaryTabFor(m.activity.tasks)
}

func (m appModel) activitySummaryTabFor(tasks []task.TaskSnapshot) activityTab {
	if !m.hasCurrentTodo || m.currentTodo.Cleared() {
		return activityTabTasks
	}
	if len(tasks) == 0 {
		return activityTabTodo
	}
	if m.activity.tasksUpdatedAt.After(m.activity.todoUpdatedAt) {
		return activityTabTasks
	}
	if m.activity.todoUpdatedAt.After(m.activity.tasksUpdatedAt) || m.activity.tab == activityTabTodo {
		return activityTabTodo
	}
	return activityTabTasks
}

func (m *appModel) observeActivityTasks(tasks []task.TaskSnapshot, now time.Time) {
	if m.activity.summarySessionID != m.sessionID {
		m.resetActivitySummary()
	}
	if !m.activity.summaryTasksSeeded {
		m.activity.summaryTasksSeeded = true
		for _, snapshot := range tasks {
			if at := activityTaskTime(snapshot); at.After(m.activity.tasksUpdatedAt) {
				m.activity.tasksUpdatedAt = at
			}
		}
	} else if len(tasks) == 0 {
		m.activity.tasksUpdatedAt = time.Time{}
	} else {
		previous := make(map[string]task.TaskSnapshot, len(m.activity.tasks))
		for _, snapshot := range m.activity.tasks {
			previous[snapshot.ID] = snapshot
		}
		for _, snapshot := range tasks {
			old, existed := previous[snapshot.ID]
			delete(previous, snapshot.ID)
			if existed && activityTaskEqual(old, snapshot) {
				continue
			}
			at := activityTaskTime(snapshot)
			if at.IsZero() || (existed && !at.After(activityTaskTime(old))) {
				at = now
			}
			if at.After(m.activity.tasksUpdatedAt) {
				m.activity.tasksUpdatedAt = at
			}
		}
		if len(previous) > 0 && now.After(m.activity.tasksUpdatedAt) {
			m.activity.tasksUpdatedAt = now
		}
	}
	m.activity.tab = m.activitySummaryTabFor(tasks)
}

func activityTaskTime(snapshot task.TaskSnapshot) time.Time {
	at := snapshot.StartedAt
	if snapshot.FinishedAt != nil && snapshot.FinishedAt.After(at) {
		at = *snapshot.FinishedAt
	}
	return at
}

func activityTaskEqual(a, b task.TaskSnapshot) bool {
	if a.ID != b.ID || a.SessionID != b.SessionID || a.ParentSessionID != b.ParentSessionID ||
		a.Name != b.Name || a.Color != b.Color || a.Description != b.Description || a.Prompt != b.Prompt ||
		a.Status != b.Status || a.Content != b.Content || a.Error != b.Error || a.UsedTokens != b.UsedTokens ||
		!a.StartedAt.Equal(b.StartedAt) || !taskUsageEqual(a.Usage, b.Usage) {
		return false
	}
	if (a.FinishedAt == nil) != (b.FinishedAt == nil) || (a.ExitCode == nil) != (b.ExitCode == nil) {
		return false
	}
	return (a.FinishedAt == nil || a.FinishedAt.Equal(*b.FinishedAt)) &&
		(a.ExitCode == nil || *a.ExitCode == *b.ExitCode)
}

func activityTodoEqual(a, b todo.Snapshot) bool {
	if a.Explanation != b.Explanation || len(a.Items) != len(b.Items) {
		return false
	}
	for i, item := range a.Items {
		other := b.Items[i]
		if item.ID != other.ID || item.Content != other.Content || item.Status != other.Status || len(item.BlockedBy) != len(other.BlockedBy) {
			return false
		}
		for _, dependency := range item.BlockedBy {
			if !slices.Contains(other.BlockedBy, dependency) {
				return false
			}
		}
	}
	return true
}
