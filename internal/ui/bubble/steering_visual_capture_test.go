package bubble

import (
	"html"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
)

func TestCaptureSteeringVisualFixture(t *testing.T) {
	previousProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(previousProfile) })

	model := newSteeringVisualModel()
	model.relayout()
	model.refreshViewport()
	view := model.View()
	plain := ansi.Strip(view)
	if strings.TrimSpace(plain) == "" {
		t.Fatal("steering fixture view is empty")
	}
	for index, line := range strings.Split(plain, "\n") {
		if width := ansi.StringWidth(line); width > model.width {
			t.Fatalf("line %d width = %d, want <= %d", index, width, model.width)
		}
	}
	for _, want := range []string{"Use the repository pattern", "1 个任务排队中"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("fixture view does not contain %q", want)
		}
	}

	baseline := newActivityVisualModel(120, 30)
	baseline.relayout()
	baseline.refreshViewport()
	baselineWidth, baselineHeight := visualViewDimensions(ansi.Strip(baseline.View()))
	fixtureWidth, fixtureHeight := visualViewDimensions(plain)
	if fixtureWidth != baselineWidth || fixtureHeight != baselineHeight {
		t.Fatalf("fixture dimensions = %dx%d, baseline = %dx%d", fixtureWidth, fixtureHeight, baselineWidth, baselineHeight)
	}

	dir := strings.TrimSpace(os.Getenv("PAW_STEERING_VISUAL_DIR"))
	if dir == "" {
		return
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	page := `<!doctype html><html><head><meta charset="utf-8"><style>
body{margin:0;background:#0d0f13;color:#d8dde7;font:15px/1.35 ui-monospace,SFMono-Regular,Menlo,Monaco,Consolas,monospace}
main{display:inline-block;min-width:100vw;box-sizing:border-box;padding:28px 32px 36px}h1{font:600 20px/1.3 -apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif;color:#f0f3f8;margin:0 0 16px}
pre{display:inline-block;margin:0;padding:18px 20px;background:#11151b;border:1px solid #303846;border-radius:8px;box-shadow:0 16px 40px #0008;white-space:pre;color:#d8dde7}
</style></head><body><main><h1>` + html.EscapeString("Running input steering · 120×30") + `</h1><pre>` + activityVisualANSIToHTML(view) + `</pre></main></body></html>`
	if err := os.WriteFile(filepath.Join(dir, "running-input-steering.html"), []byte(page), 0o644); err != nil {
		t.Fatal(err)
	}
}

func newSteeringVisualModel() appModel {
	model := newActivityVisualModel(120, 30)
	model.cursorFrameAt = time.Date(2026, 8, 31, 10, 30, 0, 0, time.Local)
	model.transcript = []transcriptEntry{
		{kind: entryUser, title: "you", body: "Refactor the running input behavior without changing the layout."},
		{kind: entryAssistant, title: "assistant", body: "I am tracing the current turn and tool boundary behavior."},
		{kind: entryUser, title: "you (steer)", body: "Use the repository pattern and keep Enter distinct from Tab."},
		{kind: entryAssistant, title: "assistant", body: "Understood. I will continue this turn with that correction."},
	}
	model.activeAssistant = len(model.transcript) - 1
	if !model.queryGuard.StartModel() {
		panic("visual fixture model unexpectedly busy")
	}
	model.syncRunningFlags()
	model.chatQueue.Enqueue("Run the independent verification after this turn")
	model.input.SetValue("Type another steering instruction")
	return model
}

func visualViewDimensions(view string) (int, int) {
	lines := strings.Split(view, "\n")
	width := 0
	for _, line := range lines {
		if current := ansi.StringWidth(line); current > width {
			width = current
		}
	}
	return width, len(lines)
}
