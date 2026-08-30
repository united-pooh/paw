package bubble

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"paw/internal/task"
)

func TestDockedTaskPreviewKeepsActivityOpen(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = 120
	model.height = 30
	model.activity.visible = true
	model.activity.focus = activityFocusPanel
	model.activity.tasks = []task.TaskSnapshot{{ID: "worker", SessionID: "worker", Name: "worker"}}
	model.activity.selectedTaskID = "worker"

	preview := &taskTranscriptPreview{task: model.activity.tasks[0], parentSessionID: model.sessionID, parentTranscript: copyTranscriptEntries(model.transcript)}
	model.applyTaskPreviewRestore(sessionRestoredMsg{source: sessionRestoreTaskEnter, taskPreview: preview, entries: []transcriptEntry{{kind: entryAssistant, body: "task output"}}})
	if !model.activity.visible || model.taskPreview == nil || !strings.Contains(renderTranscript(model.transcript, 80, false), "task output") {
		t.Fatalf("preview state: visible=%v preview=%#v transcript=%#v", model.activity.visible, model.taskPreview, model.transcript)
	}
}

func TestTaskPreviewLoadErrorKeepsLastSnapshot(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = 120
	model.height = 30
	model.activity.visible = true
	preview := &taskTranscriptPreview{task: task.TaskSnapshot{ID: "worker", Name: "worker"}, parentSessionID: "main"}
	model.applyTaskPreviewError(sessionRestoredMsg{source: sessionRestoreTaskEnter, taskPreview: preview, err: errors.New("transcript unavailable")})
	if model.taskPreview == nil || model.taskPreview.loadError != "transcript unavailable" || !model.activity.visible {
		t.Fatalf("error preview = %#v activity=%+v", model.taskPreview, model.activity)
	}
}

func TestNarrowActivityEnterClosesPanelAndStartsPreview(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = 80
	model.height = 24
	model.activity.visible = true
	model.activity.focus = activityFocusPanel
	model.activity.tab = activityTabTasks
	model.activity.tasks = []task.TaskSnapshot{{ID: "worker", SessionID: "worker"}}

	next, cmd := model.handleActivityKey(tea.KeyMsg{Type: tea.KeyEnter})
	model = next.(appModel)
	if model.activity.visible || cmd == nil {
		t.Fatalf("narrow Enter visible=%v cmd=%v", model.activity.visible, cmd)
	}
}

func TestSubmitFromTaskPreviewRestoresMainAndKeepsDock(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = 120
	model.height = 30
	model.sessionID = "main"
	model.activity.visible = true
	model.activity.focus = activityFocusWorkspace
	model.taskPreview = &taskTranscriptPreview{
		parentSessionID:  "main",
		parentTranscript: []transcriptEntry{{kind: entryUser, body: "main transcript"}},
	}
	model.transcript = []transcriptEntry{{kind: entryAssistant, body: "task transcript"}}
	model.input.SetValue("continue main")

	next, _ := model.handleSubmit()
	model = next.(appModel)
	if model.taskPreview != nil || !model.activity.visible {
		t.Fatalf("submit state: preview=%#v activity=%+v", model.taskPreview, model.activity)
	}
	if len(model.transcript) == 0 || !strings.Contains(model.transcript[0].body, "main transcript") {
		t.Fatalf("main transcript not restored: %#v", model.transcript)
	}
}
