package bubble

import (
	"html"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"paw/internal/loop"
	selecttool "paw/internal/tool/select"
)

func TestP5ViewsMatchGolden(t *testing.T) {
	tests := []struct {
		name   string
		width  int
		height int
		view   func() string
		golden string
	}{
		{name: "read permission", width: 120, height: 40, view: p5ReadPermissionView, golden: "p5_read_permission_120x40.golden"},
		{name: "session recovery", width: 80, height: 24, view: p5SessionRecoveryView, golden: "p5_session_recovery_80x24.golden"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			view := test.view()
			assertFixedFrame(t, view, test.width, test.height)
			got := normalizeP5GoldenView(view)
			path := filepath.Join("testdata", test.golden)
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read golden %s: %v\nactual:\n%s", path, err, got)
			}
			if got != string(want) {
				t.Fatalf("view differs from %s\nactual:\n%s\nwant:\n%s", path, got, want)
			}
		})
	}
}

func TestCaptureP5VisualFixtures(t *testing.T) {
	if os.Getenv("PAW_P5_VISUAL_FIXTURE") != "1" {
		t.Skip("set PAW_P5_VISUAL_FIXTURE=1 to capture P5 visual fixtures")
	}
	fixtures := []struct {
		path     string
		title    string
		viewport string
		view     string
	}{
		{path: "/tmp/paw-p5-read-permission-120x40.html", title: "Outside-workspace Read permission", viewport: "120×40", view: p5ReadPermissionView()},
		{path: "/tmp/paw-p5-session-recovery-80x24.html", title: "Goal/Plan session recovery", viewport: "80×24", view: p5SessionRecoveryView()},
	}
	for _, fixture := range fixtures {
		page := `<!doctype html><html><head><meta charset="utf-8"><style>
html,body{margin:0;min-height:100%;background:#090e17;color:#d9e2f2}body{display:flex;align-items:flex-start;justify-content:center;font-family:ui-monospace,SFMono-Regular,Menlo,Monaco,Consolas,monospace}.wrap{padding:24px}.meta{display:flex;justify-content:space-between;color:#8da2bf;font:600 13px/1.4 -apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif;margin:0 2px 10px}.terminal{background:#111827;border:1px solid #263449;border-radius:12px;box-shadow:0 22px 60px #0009;padding:16px 18px;overflow:hidden}pre{margin:0;white-space:pre;color:#dce6f5;font:14px/1.28 ui-monospace,SFMono-Regular,Menlo,Monaco,Consolas,monospace}
</style></head><body><div class="wrap"><div class="meta"><span>` + html.EscapeString(fixture.title) + `</span><span>` + fixture.viewport + ` terminal cells</span></div><div class="terminal"><pre>` + html.EscapeString(ansi.Strip(fixture.view)) + `</pre></div></div></body></html>`
		if err := os.WriteFile(fixture.path, []byte(page), 0o644); err != nil {
			t.Fatalf("write %s: %v", fixture.path, err)
		}
	}
}

func p5ReadPermissionView() string {
	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width, model.height = 120, 40
	model.cursorFrameAt = time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	model.worktree = worktreeSnapshot{name: "go-code", ref: "dev", state: worktreeDirty, isGit: true}
	model.transcript = []transcriptEntry{{kind: entryUser, title: "you", body: "读取工作区外的共享设计说明，并继续当前实现。"}}
	model.selectionDock = newSelectionDock(selecttool.Request{
		ID:          "permission-p5-read",
		Prompt:      "Read requests access outside the workspace:\n/Users/example/shared/architecture.md",
		Mode:        selecttool.ModeSingle,
		OptionsOnly: true,
		Options: []selecttool.Option{
			{ID: string(loop.PermissionAllowOnce), Label: "Allow once", Description: "Allow only this exact Read path for this tool call."},
			{ID: string(loop.PermissionDeny), Label: "Deny", Description: "Return a normal tool error without reading the file."},
		},
		MinSelect: 1,
		MaxSelect: 1,
	})
	model.relayout()
	model.refreshViewport()
	return model.View()
}

func p5SessionRecoveryView() string {
	runner := &restoredModeRunner{modes: &loop.SessionModeSnapshot{
		ActiveGoalID: "goal-p5", GoalStatus: "paused",
		ActivePlanID: "plan-p5", PlanStatus: "paused",
		PendingPermissionID: "permission-p5",
	}}
	model := newTestModel(runner)
	model.ready = true
	model.width, model.height = 80, 24
	model.cursorFrameAt = time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	model.worktree = worktreeSnapshot{name: "go-code", ref: "dev", state: worktreeDirty, isGit: true}
	model.goalController = &reboundGoalController{}
	model.planController = &reboundPlanController{}
	model.relayout()
	next, _ := model.Update(sessionRestoredMsg{sessionID: "session-p5"})
	return next.(appModel).View()
}

func normalizeP5GoldenView(view string) string {
	lines := strings.Split(ansi.Strip(view), "\n")
	for index := range lines {
		lines[index] = strings.TrimRight(lines[index], " ")
	}
	return strings.TrimRight(strings.Join(lines, "\n"), "\n") + "\n"
}
