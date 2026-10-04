package bubble

import (
	"fmt"
	"html"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
)

func TestContextLimitVisualFixture(t *testing.T) {
	previous := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(previous) })
	for _, width := range []int{80, 120} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			m, _, runner := openGeneralCenter(t)
			runner.stats.Known, runner.stats.TotalKnown = true, true
			runner.stats.UsedTokens = 12000
			m.width, m.height = width, 36
			m.relayout()
			m = editGeneralContextLimit(m, "270000")
			assertContextLimit(t, m, runner, 270000)
			m.configCenter.selected = generalFieldIndex(t, m, "ui.context_limit_tokens")
			captureContextLimitView(t, fmt.Sprintf("general-%d", width), m, "270000")
			m.configCenter = nil
			m.handleModelCommand("/model local/two")
			m.relayout()
			m.refreshViewport()
			captureContextLimitView(t, fmt.Sprintf("meter-%d", width), m, "270k")
		})
	}
}

func captureContextLimitView(t *testing.T, name string, m appModel, want string) {
	t.Helper()
	view := m.View()
	plain := ansi.Strip(view)
	if !strings.Contains(plain, want) {
		t.Fatalf("%s missing %q:\n%s", name, want, plain)
	}
	for i, line := range strings.Split(plain, "\n") {
		if width := ansi.StringWidth(line); width > m.width {
			t.Fatalf("%s row %d width %d > %d", name, i, width, m.width)
		}
	}
	if dir := os.Getenv("PAW_CONTEXT_VISUAL_DIR"); dir != "" {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		page := `<!doctype html><html><head><meta charset="utf-8"><style>body{margin:24px;background:#0d0f13;color:#d8dde7}pre{font:14px/1.4 Menlo,monospace;white-space:pre}h1{font:18px sans-serif}</style></head><body><h1>` + html.EscapeString(name) + `</h1><pre>` + activityVisualANSIToHTML(view) + `</pre></body></html>`
		if err := os.WriteFile(filepath.Join(dir, name+".html"), []byte(page), 0644); err != nil {
			t.Fatal(err)
		}
	}
}
