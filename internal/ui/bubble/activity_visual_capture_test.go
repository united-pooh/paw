package bubble

import (
	"html"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"paw/internal/task"
)

func TestCaptureActivityDockVisualFixtures(t *testing.T) {
	dir := os.Getenv("PAW_ACTIVITY_VISUAL_DIR")
	if strings.TrimSpace(dir) == "" {
		t.Skip("set PAW_ACTIVITY_VISUAL_DIR to capture Activity visual fixtures")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	capture := func(name, title string, model appModel) {
		t.Helper()
		model.relayout()
		model.refreshViewport()
		view := ansi.Strip(model.View())
		if strings.TrimSpace(view) == "" {
			t.Fatalf("%s view is empty", name)
		}
		page := `<!doctype html><html><head><meta charset="utf-8"><style>
body{margin:0;background:#0d0f13;color:#d8dde7;font:15px/1.35 ui-monospace,SFMono-Regular,Menlo,Monaco,Consolas,monospace}
main{display:inline-block;min-width:100vw;box-sizing:border-box;padding:28px 32px 36px}h1{font:600 20px/1.3 -apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif;color:#f0f3f8;margin:0 0 16px}
pre{display:inline-block;margin:0;padding:18px 20px;background:#11151b;border:1px solid #303846;border-radius:8px;box-shadow:0 16px 40px #0008;white-space:pre;color:#d8dde7}
</style></head><body><main><h1>` + html.EscapeString(title) + `</h1><pre>` + html.EscapeString(view) + `</pre></main></body></html>`
		if err := os.WriteFile(filepath.Join(dir, name+".html"), []byte(page), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	base := newActivityVisualModel(120, 30)
	base.openActivity(activityTabTasks)
	capture("activity-docked-default", "Activity docked · default 36%", base)

	resized := newActivityVisualModel(120, 30)
	resized.activity.widthColumns = 52
	resized.openActivity(activityTabTasks)
	capture("activity-docked-resized", "Activity docked · resized to 52 columns", resized)

	preview := newActivityVisualModel(120, 30)
	preview.openActivity(activityTabTasks)
	preview.taskPreview = &taskTranscriptPreview{
		task:             preview.activity.tasks[0],
		parentSessionID:  preview.sessionID,
		parentTranscript: copyTranscriptEntries(preview.transcript),
		entries: []transcriptEntry{
			{kind: entryUser, title: "you", body: "Inspect the current Activity overlay"},
			{kind: entryAssistant, title: "assistant", body: "The old panel is composed with placeOpaqueOverlay."},
		},
	}
	preview.replaceTranscript(renderTaskPreviewTranscript(preview.taskPreview, preview.cursorFrameAt))
	preview.activity.focus = activityFocusWorkspace
	preview.input.Focus()
	capture("activity-docked-preview", "Task transcript preview · input stays on main session", preview)

	closed := newActivityVisualModel(120, 30)
	capture("activity-docked-closed", "Activity closed · running task in top hairline", closed)

	narrow := newActivityVisualModel(80, 24)
	narrow.openActivity(activityTabTasks)
	capture("activity-fullscreen-narrow", "Activity fullscreen · 80 columns", narrow)

	overlay := newActivityVisualModel(120, 30)
	overlay.openActivity(activityTabTasks)
	overlay.activity.focus = activityFocusWorkspace
	overlay.input.Focus()
	overlay.completion = &completion{kind: completionKindCommand, items: []string{"/help", "/tasks", "/todo"}}
	capture("activity-docked-overlay-priority", "Completion remains inside workspace", overlay)
}

func newActivityVisualModel(width, height int) appModel {
	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = width
	model.height = height
	model.cursorFrameAt = time.Date(2026, 8, 30, 12, 48, 0, 0, time.Local)
	model.taskController = &fakeTaskController{tasks: []task.TaskSnapshot{
		{ID: "layout-research", SessionID: "layout-research", ParentSessionID: model.sessionID, Name: "layout-research", Status: task.TaskRunning, StartedAt: time.Now().Add(-84 * time.Second), UsedTokens: 8200},
		{ID: "visual-review", SessionID: "visual-review", ParentSessionID: model.sessionID, Name: "visual-review", Status: task.TaskCompleted, UsedTokens: 3100},
		{ID: "implementation-plan", SessionID: "implementation-plan", ParentSessionID: model.sessionID, Name: "implementation-plan", Status: task.TaskStopped},
	}}
	model.transcript = []transcriptEntry{
		{kind: entryUser, title: "you", body: "把 Ctrl+G 面板改成侧边框"},
		{kind: entryAssistant, title: "assistant", body: "Activity 将参与布局，不再覆盖 transcript。"},
	}
	model.input.SetValue("继续给主 session 输入")
	return model
}
