package bubble

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
)

func TestTokenMonitorCompactActualViewKeepsTools(t *testing.T) {
	for _, width := range []int{65, 80} {
		fixture := tokenMonitorFixture()
		model := newActivityVisualModel(width, 24)
		model.runner = &fakeRunner{stats: fixture.Context}
		model.openActivity(activityTabTasks)
		model.tokenMonitor.snapshot = fixture.Snapshot
		model.tokenMonitor.live.toolStart("read", time.Now())
		model.relayout()
		model.refreshViewport()
		got := ansi.Strip(model.View())
		for _, want := range []string{"TOKEN HISTORY", "Input", "Output", "Tools in"} {
			if !strings.Contains(got, want) {
				t.Errorf("width %d missing %q in actual View\n%s", width, want, got)
			}
		}
	}
}
