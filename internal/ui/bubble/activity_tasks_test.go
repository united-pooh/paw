package bubble

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
	"paw/internal/task"
)

func TestTaskEntriesSortByAttentionPriorityThenNewest(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	now := time.Now().UTC()
	model.taskController = &fakeTaskController{tasks: []task.TaskSnapshot{
		{ID: "completed-new", ParentSessionID: model.sessionID, Status: task.TaskCompleted, StartedAt: now.Add(-time.Minute)},
		{ID: "running-old", ParentSessionID: model.sessionID, Status: task.TaskRunning, StartedAt: now.Add(-5 * time.Minute)},
		{ID: "stopped-new", ParentSessionID: model.sessionID, Status: task.TaskStopped, StartedAt: now.Add(-2 * time.Minute)},
		{ID: "failed-old", ParentSessionID: model.sessionID, Status: task.TaskFailed, StartedAt: now.Add(-4 * time.Minute)},
		{ID: "interrupted-new", ParentSessionID: model.sessionID, Status: task.TaskInterrupted, StartedAt: now.Add(-3 * time.Minute)},
		{ID: "running-new", ParentSessionID: model.sessionID, Status: task.TaskRunning, StartedAt: now.Add(-30 * time.Second)},
		{ID: "completed-old", ParentSessionID: model.sessionID, Status: task.TaskCompleted, StartedAt: now.Add(-6 * time.Minute)},
		{ID: "other-session", ParentSessionID: "other", Status: task.TaskRunning, StartedAt: now},
	}}

	got := model.taskEntries()
	want := []string{"running-new", "running-old", "interrupted-new", "failed-old", "stopped-new", "completed-new", "completed-old"}
	if len(got) != len(want) {
		t.Fatalf("taskEntries() len = %d, want %d: %#v", len(got), len(want), got)
	}
	for i, id := range want {
		if got[i].ID != id {
			t.Fatalf("taskEntries()[%d] = %q, want %q; tasks=%#v", i, got[i].ID, id, got)
		}
	}
}

func TestRenderActivityTaskSelectedShowsColoredWorkerStatusAndWrappedDescription(t *testing.T) {
	previousProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(previousProfile) })

	model := newTestModel(&fakeRunner{})
	model.activity.visible = true
	model.activity.selectedIndex = 0
	model.activity.selectedTaskID = "worker-1"
	model.activity.tasks = []task.TaskSnapshot{
		{
			ID:          "worker-1",
			Name:        "八潮瑠唯",
			Color:       "#669988",
			Status:      task.TaskRunning,
			StartedAt:   time.Now().Add(-84 * time.Second),
			Description: "Inspect the Activity worker identity, status, sorting, and selected task description layout.",
			UsedTokens:  8200,
		},
		{ID: "worker-2", Name: "高松灯", Color: "#CC0033", Status: task.TaskCompleted, Description: "Completed task."},
	}
	model.taskController = &fakeTaskController{tasks: append([]task.TaskSnapshot(nil), model.activity.tasks...)}

	rendered := model.renderActivityTasks(40, 8)
	plain := ansi.Strip(rendered)
	lines := strings.Split(plain, "\n")
	if len(lines) != 8 {
		t.Fatalf("rendered height = %d, want 8:\n%s", len(lines), plain)
	}
	nameLine := ""
	for _, line := range lines {
		if strings.Contains(line, "八潮瑠唯") {
			nameLine = line
			break
		}
	}
	if nameLine == "" || !strings.Contains(nameLine, "running") || !strings.Contains(nameLine, "1m 24s") {
		t.Fatalf("worker header must contain name and status/elapsed on one line: %q\n%s", nameLine, plain)
	}
	for _, want := range []string{
		"Inspect the Activity worker identity,",
		"status, sorting, and selected task",
		"description layout.",
		"8.2k tokens",
		"高松灯",
		"completed",
	} {
		if !strings.Contains(plain, want) {
			t.Fatalf("Activity task list missing %q:\n%s", want, plain)
		}
	}

	selectedWorkerStyle := model.styles.SelectionSelected.Copy().Foreground(lipgloss.Color("#669988"))
	if expected := selectedWorkerStyle.Render("八潮瑠唯"); !strings.Contains(rendered, expected) {
		t.Fatalf("selected worker name lost support color/background pairing\nwant fragment: %q\nrendered: %q", expected, rendered)
	}
}

func TestRenderActivityTaskDescriptionFallsBackToPrompt(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.activity.visible = true
	model.activity.tasks = []task.TaskSnapshot{{
		ID:     "worker-1",
		Name:   "高松灯",
		Status: task.TaskRunning,
		Prompt: "Investigate why queued worker tasks lose their assigned persona and color.",
	}}
	model.activity.selectedIndex = 0
	model.taskController = &fakeTaskController{tasks: append([]task.TaskSnapshot(nil), model.activity.tasks...)}

	plain := ansi.Strip(model.renderActivityTasks(36, 5))
	if !strings.Contains(plain, "Investigate why queued worker") {
		t.Fatalf("selected task did not fall back to prompt summary:\n%s", plain)
	}
}
