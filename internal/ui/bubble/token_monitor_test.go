package bubble

import (
	"context"
	"errors"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"paw/internal/capability/model"
	"paw/internal/tokentracer"
)

type fakeTokenMonitorReader struct{ calls int }

func (r *fakeTokenMonitorReader) Monitor(workspace, session string) (tokentracer.MonitorSnapshot, error) {
	r.calls++
	return tokentracer.MonitorSnapshot{SessionID: session}, nil
}

func TestTokenMonitorPollOnlyWhenVisibleAndRejectsOldSession(t *testing.T) {
	m := newTestModel(&fakeRunner{})
	r := &fakeTokenMonitorReader{}
	m.tokenMonitor.reader = r
	m.width, m.height, m.ready = 120, 36, true
	m.closeActivity()
	if cmd := m.tokenMonitorCommand(); cmd != nil {
		t.Fatal("hidden monitor scheduled disk IO")
	}
	m.openActivity(activityTabTasks)
	cmd := m.tokenMonitorCommand()
	if cmd == nil || !m.tokenMonitor.loading {
		t.Fatal("visible monitor did not load")
	}
	if m.tokenMonitorCommand() != nil {
		t.Fatal("duplicate query while loading")
	}
	result := cmd().(tokenMonitorResultMsg)
	m.sessionID = "different"
	m.syncTokenMonitorSession()
	next, _ := m.Update(result)
	m = next.(appModel)
	if m.tokenMonitor.snapshot.SessionID != "" || m.tokenMonitor.loading {
		t.Fatalf("stale result applied: %+v", m.tokenMonitor)
	}
	if r.calls != 1 {
		t.Fatalf("IO count %d", r.calls)
	}
}

func TestTokenMonitorFailureRetainsSnapshotAndStopsHiddenPolling(t *testing.T) {
	m := newTestModel(&fakeRunner{})
	m.width, m.height, m.ready = 120, 36, true
	m.openActivity(activityTabTasks)
	m.syncTokenMonitorSession()
	m.tokenMonitor.snapshot.SessionID = m.sessionID
	m.tokenMonitor.loading = true
	next, cmd := m.Update(tokenMonitorResultMsg{session: m.sessionID, err: errors.New("read failed")})
	m = next.(appModel)
	if m.tokenMonitor.err == "" || m.tokenMonitor.snapshot.SessionID == "" || cmd == nil {
		t.Fatalf("failure erased snapshot or stopped retry: %+v", m.tokenMonitor)
	}
	m.closeActivity()
	next, cmd = m.Update(tokenMonitorTickMsg{})
	if cmd != nil || next.(appModel).tokenMonitor.loading {
		t.Fatal("hidden token history kept querying")
	}
}

func TestTokenMonitorUISeparatesArrivalSamplesFromDisplayFlush(t *testing.T) {
	u := New()
	var messages []tea.Msg
	u.sendMsg = func(msg tea.Msg) { messages = append(messages, msg) }
	u.OnModelRequestStart("r")
	u.OnModelStreamActivity("r", model.StreamActivity{At: time.Now(), Text: "hello"})
	if err := u.OnAssistantDelta("hello"); err != nil {
		t.Fatal(err)
	}
	if len(messages) != 3 {
		t.Fatalf("messages = %d", len(messages))
	}
	m := newTestModel(&fakeRunner{})
	for _, msg := range messages {
		next, _ := m.Update(msg)
		m = next.(appModel)
	}
	if got := m.tokenMonitor.live.view(time.Now()); got.RequestID != "r" || !got.FirstTextKnown {
		t.Fatalf("real UI callbacks lost timings: %+v", got)
	}
	if m.tokenMonitor.live.total != 1.25 {
		t.Fatal("display flush was counted again after its raw arrival sample")
	}
	before := m.tokenMonitor.live.total
	for _, msg := range []tea.Msg{assistantDeltaMsg("final fallback text"), thinkingDeltaMsg("replayed reasoning"), assistantPartMsg{lifecycle: "delta", blockIndex: 0, partType: "reasoning", delta: "replayed part"}} {
		next, _ := m.Update(msg)
		m = next.(appModel)
	}
	if m.tokenMonitor.live.total != before {
		t.Fatal("display-only fallback inflated live receiving speed")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	m.ctx = ctx
	m.openActivity(activityTabTasks)
	if cmd := m.tokenMonitorCommand(); cmd != nil {
		t.Fatal("canceled UI scheduled IO")
	}
}

func TestTokenMonitorUpdateKeepsQuerySingleFlight(t *testing.T) {
	m := newTestModel(&fakeRunner{})
	m.width, m.height, m.ready = 120, 36, true
	m.tokenMonitor.reader = &fakeTokenMonitorReader{}
	m.openActivity(activityTabTasks)
	next, cmd := m.Update(tokenMonitorStartMsg{id: "r", at: time.Now()})
	m = next.(appModel)
	if cmd == nil || !m.tokenMonitor.loading {
		t.Fatal("Update discarded query state")
	}
	_, duplicate := m.Update(tokenMonitorUsageMsg{})
	if duplicate != nil {
		t.Fatal("usage notification duplicated in-flight query")
	}
}
