package bubble

import (
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func TestTokenMonitorReviewModeCompletionEndsWaitingRequest(t *testing.T) {
	for _, tc := range []struct {
		name string
		msg  tea.Msg
	}{
		{"manual compaction", contextCompactionFinishedMsg{}},
		{"plan failure", planStoppedMsg{reason: "failed"}},
		{"goal cancellation", goalStoppedMsg{reason: "cancelled"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestModel(&fakeRunner{})
			m.syncTokenMonitorSession()
			m.tokenMonitor.live.start("waiting-request", time.Now().Add(-time.Second))
			next, _ := m.Update(tc.msg)
			m = next.(appModel)
			if got := m.tokenMonitor.live.view(time.Now()); got.Active {
				t.Fatalf("completed mode left the last request waiting: %+v", got)
			}
		})
	}
}

func TestTokenMonitorReviewQueuedTurnResetsActualToolCounters(t *testing.T) {
	m := newTestModel(&fakeRunner{})
	m.syncTokenMonitorSession()
	started := time.Now().Add(-2 * time.Second)
	m.tokenMonitor.live.toolStart("prior-turn-tool", started)
	m.tokenMonitor.live.toolEnd("prior-turn-tool", started.Add(time.Second))
	if _, ok := m.chatQueue.Enqueue("next user turn"); !ok {
		t.Fatal("fixture could not enqueue a turn")
	}
	if cmd := m.startNextQueuedTurn(); cmd == nil {
		t.Fatal("queued turn did not start")
	}
	if got := m.tokenMonitor.live.view(time.Now()); got.ToolCalls != 0 || got.ToolDuration != 0 {
		t.Fatalf("new queued turn kept previous tool statistics: %+v", got)
	}
}

func TestTokenMonitorReviewModeStartResetsToolCounters(t *testing.T) {
	for _, action := range []string{"goal input", "plan input", "/goal start next", "/goal resume", "/plan new next", "/plan resume"} {
		t.Run(action, func(t *testing.T) {
			m := newTestModel(&fakeRunner{})
			m.goalController = &fakeGoalController{}
			m.planController = &modeTestPlanController{}
			m.tokenMonitor.live.toolStart("previous", time.Now())
			switch action {
			case "goal input":
				next, _ := m.submitGoal("next")
				m = next.(appModel)
			case "plan input":
				next, _ := m.submitPlan("next")
				m = next.(appModel)
			default:
				m.handleCommand(action)
			}
			if got := m.tokenMonitor.live.view(time.Now()); got.ToolCalls != 0 || got.ToolActive {
				t.Fatalf("new mode kept previous tool statistics: %+v", got)
			}
		})
	}
}

func TestTokenMonitorReviewModeStopEndsLiveState(t *testing.T) {
	for _, action := range []string{"/goal pause", "/goal stop", "/plan stop"} {
		t.Run(action, func(t *testing.T) {
			m := newTestModel(&fakeRunner{})
			m.goalController = &fakeGoalController{}
			m.planController = &modeTestPlanController{}
			m.tokenMonitor.live.toolStart("running", time.Now())
			m.tokenMonitor.live.start("waiting", time.Now())
			m.handleCommand(action)
			if got := m.tokenMonitor.live.view(time.Now()); got.Active || got.ToolActive {
				t.Fatalf("stopped mode retained active telemetry: %+v", got)
			}
		})
	}
}
