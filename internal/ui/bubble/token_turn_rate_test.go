package bubble

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"paw/internal/capability/model"
)

func TestTokenTurnAverageIncludesAllRequestsAndToolWait(t *testing.T) {
	at := time.Unix(100, 0)
	var live tokenLiveState
	live.beginTurn(at)
	live.start("one", at.Add(time.Second))
	live.text(strings.Repeat("a", 80), at.Add(2*time.Second))
	live.usage(model.Usage{RequestID: "one", CompletionTokens: 100})
	live.requestEnd("one", at.Add(10*time.Second))
	if got := live.view(at.Add(20 * time.Second)); got.TurnAverage != 5 || got.TurnTokens != 100 || !got.TurnActive || got.TurnEstimated {
		t.Fatalf("waiting for tools must keep whole-turn timing: %+v", got)
	}
	live.start("two", at.Add(20*time.Second))
	live.text("abcd", at.Add(22*time.Second))
	live.usage(model.Usage{RequestID: "two", CompletionTokens: 150})
	live.usage(model.Usage{RequestID: "two", CompletionTokens: 150})
	live.usage(model.Usage{RequestID: "one", CompletionTokens: 9999})
	live.requestEnd("two", at.Add(30*time.Second))
	live.endTurn(at.Add(40 * time.Second))
	live.endTurn(at.Add(50 * time.Second))
	got := live.view(at.Add(time.Hour))
	if got.TurnTokens != 250 || got.TurnAverage != 6.25 || got.TurnElapsed != 40*time.Second || got.TurnActive || got.TurnEstimated {
		t.Fatalf("whole-turn average must freeze and not double count: %+v", got)
	}
	live.beginTurn(at.Add(time.Hour))
	if got := live.view(at.Add(time.Hour)); got.TurnTokens != 0 || got.TurnObserved || got.RequestID != "" {
		t.Fatalf("new turn inherited output: %+v", got)
	}
}

func TestTokenTurnAverageRetainsEstimatedFailedRequests(t *testing.T) {
	at := time.Unix(100, 0)
	var live tokenLiveState
	live.start("interrupted", at)
	live.text(strings.Repeat("a", 40), at.Add(time.Second))
	live.requestEnd("interrupted", at.Add(10*time.Second))
	live.start("recovered", at.Add(20*time.Second))
	live.usage(model.Usage{RequestID: "recovered", CompletionTokens: 20})
	live.endTurn(at.Add(30 * time.Second))
	got := live.view(at.Add(time.Hour))
	if got.TurnTokens != 30 || math.Abs(got.TurnAverage-1) > 1e-9 || !got.TurnEstimated || !got.TurnObserved {
		t.Fatalf("recovery discarded prior output or estimate marker: %+v", got)
	}
}

func TestTokenTurnUsageSnapshotDoesNotHideLaterStream(t *testing.T) {
	at := time.Unix(100, 0)
	var live tokenLiveState
	live.start("anthropic", at)
	live.usage(model.Usage{RequestID: "anthropic", OutputTokens: 1})
	live.text(strings.Repeat("a", 400), at.Add(time.Second))
	if got := live.view(at.Add(2 * time.Second)); got.TurnTokens < 100 || !got.TurnEstimated {
		t.Fatalf("initial usage froze ongoing generation: %+v", got)
	}
	live.requestEnd("anthropic", at.Add(3*time.Second))
	if got := live.view(at.Add(4 * time.Second)); got.TurnTokens < 100 || !got.TurnEstimated {
		t.Fatalf("interruption lost unreported streamed output: %+v", got)
	}
	live.usage(model.Usage{RequestID: "anthropic", OutputTokens: 80})
	if got := live.view(at.Add(5 * time.Second)); got.TurnTokens != 80 || got.TurnEstimated {
		t.Fatalf("final provider total did not reconcile estimate: %+v", got)
	}
}

func TestTokenTurnDonePartDoesNotEndForegroundTask(t *testing.T) {
	m := newTestModel(&fakeRunner{})
	m.syncTokenMonitorSession()
	m.tokenMonitor.live.start("one", time.Now().Add(-time.Second))
	m.tokenMonitor.live.text("abcd", time.Now())
	next, _ := m.Update(doneMsg{})
	m = next.(appModel)
	if !m.tokenMonitor.live.view(time.Now()).TurnActive {
		t.Fatal("assistant part completion ended whole task token timing")
	}
	next, _ = m.Update(turnFinishedMsg{})
	m = next.(appModel)
	if m.tokenMonitor.live.view(time.Now()).TurnActive {
		t.Fatal("finished foreground task kept timing")
	}
}

func TestTokenHistoryLeadsWithWholeTurnAverage(t *testing.T) {
	v := tokenMonitorFixture()
	v.Rates.TurnAverage, v.Rates.TurnTokens = 12.5, 1000
	v.Rates.TurnElapsed, v.Rates.TurnObserved = 80*time.Second, true
	got := ansi.Strip(renderTokenHistory(v, 40, 18))
	for _, want := range []string{"Turn avg 12.5 tok/s", "80.0s", "incl tools"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "req avg") {
		t.Fatal("still presenting last-request average as the primary metric")
	}
}

func TestTokenTurnTaskCompletionStopsTiming(t *testing.T) {
	m := newTestModel(&fakeRunner{})
	m.syncTokenMonitorSession()
	m.tokenMonitor.live.start("task", time.Now().Add(-time.Second))
	next, _ := m.Update(taskFinishedMsg{})
	m = next.(appModel)
	if m.tokenMonitor.live.view(time.Now()).TurnActive {
		t.Fatal("standalone task completion kept whole-turn timer active")
	}
}
