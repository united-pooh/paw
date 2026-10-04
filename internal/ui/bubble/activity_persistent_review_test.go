package bubble

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"paw/internal/tokentracer"
)

type persistentReviewReader struct{ sessions []string }

func (r *persistentReviewReader) Monitor(_, session string) (tokentracer.MonitorSnapshot, error) {
	r.sessions = append(r.sessions, session)
	return tokentracer.MonitorSnapshot{SessionID: session, GeneratedAt: time.Unix(100, 0), Summary: tokentracer.RequestSummary{Input: 100, InputKnown: 1, Attempts: 1}}, nil
}

func newPersistentReviewModel(t *testing.T) (appModel, *persistentReviewReader) {
	t.Helper()
	m := newModel(context.Background(), &fakeRunner{}, "persistent-review", nil, nil, nil, nil, newTerminalCursorAnchor())
	r := &persistentReviewReader{}
	m.tokenMonitor.reader = r
	m.worktreeCWD = t.TempDir()
	m.worktreeReader = func(context.Context, string) (worktreeSnapshot, error) { return worktreeSnapshot{}, nil }
	return m, r
}

func persistentReviewMessages(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		var messages []tea.Msg
		for _, child := range batch {
			messages = append(messages, persistentReviewMessages(child)...)
		}
		return messages
	}
	return []tea.Msg{msg}
}

func persistentReviewResult(t *testing.T, cmd tea.Cmd) tokenMonitorResultMsg {
	t.Helper()
	var result tokenMonitorResultMsg
	count := 0
	for _, msg := range persistentReviewMessages(cmd) {
		if candidate, ok := msg.(tokenMonitorResultMsg); ok {
			result, count = candidate, count+1
		}
	}
	if count != 1 {
		t.Fatalf("query produced %d monitor results, want 1", count)
	}
	return result
}

func TestPersistentReviewRealDefaultAndStartupSingleFlight(t *testing.T) {
	m, reader := newPersistentReviewModel(t)
	if !m.activity.visible || !m.input.Focused() {
		t.Fatalf("real newModel default: visible=%t, input focused=%t", m.activity.visible, m.input.Focused())
	}
	for _, msg := range persistentReviewMessages(m.Init()) {
		next, _ := m.Update(msg)
		m = next.(appModel)
	}
	if m.tokenMonitor.loading || len(reader.sessions) != 0 {
		t.Fatal("unmeasured startup issued hidden monitor IO")
	}
	next, query := m.Update(tea.WindowSizeMsg{Width: 120, Height: 36})
	m = next.(appModel)
	if !m.tokenMonitor.loading || query == nil {
		t.Fatal("first measured frame did not start the default monitor")
	}
	next, duplicate := m.Update(tea.WindowSizeMsg{Width: 120, Height: 36})
	m = next.(appModel)
	if duplicate != nil || m.tokenMonitorCommand() != nil {
		t.Fatal("repeated resize duplicated an outstanding monitor query")
	}
	result := persistentReviewResult(t, query)
	next, tick := m.Update(result)
	m = next.(appModel)
	if len(reader.sessions) != 1 || m.tokenMonitor.loading || !m.tokenMonitor.scheduled || tick == nil {
		t.Fatalf("poll lifecycle lost single-flight state: %+v, reads=%v", m.tokenMonitor, reader.sessions)
	}
}

func TestPersistentReviewResizesKeepVisibleInput(t *testing.T) {
	m, _ := newPersistentReviewModel(t)
	m.input.SetValue("保留输入 draft")
	for _, width := range []int{120, 80, 65, 64, 20, 80, 120} {
		next, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: 24})
		m = next.(appModel)
		layout := m.currentLayout()
		want := activityLayoutDocked
		if width < 65 {
			want = activityLayoutHidden
		}
		if layout.activityMode != want || !m.activity.visible || !m.input.Focused() || !m.shouldAnchorTextInputCursor() || m.input.Value() != "保留输入 draft" {
			t.Errorf("width %d: mode=%v, visible=%t, focused=%t, anchor=%t, draft=%q", width, layout.activityMode, m.activity.visible, m.input.Focused(), m.shouldAnchorTextInputCursor(), m.input.Value())
		}
		for row, line := range strings.Split(m.View(), "\n") {
			if got := terminalCellWidth(line); got != width {
				t.Errorf("width %d row %d renders %d cells", width, row, got)
			}
		}
	}
}

func TestPersistentReviewHiddenAndNarrowPollingRestarts(t *testing.T) {
	m, reader := newPersistentReviewModel(t)
	next, query := m.Update(tea.WindowSizeMsg{Width: 120, Height: 36})
	m = next.(appModel)
	if query == nil {
		t.Fatal("default monitor did not start")
	}
	next, _ = m.Update(tea.WindowSizeMsg{Width: 64, Height: 24})
	m = next.(appModel)
	next, tick := m.Update(persistentReviewResult(t, query))
	m = next.(appModel)
	if tick != nil || m.tokenMonitor.scheduled || m.tokenMonitorVisible() {
		t.Fatal("narrow hidden panel kept its polling chain")
	}
	next, query = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = next.(appModel)
	if query == nil || !m.tokenMonitor.loading {
		t.Fatal("widening did not restart the monitor")
	}
	next, _ = m.Update(persistentReviewResult(t, query))
	m = next.(appModel)
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlG})
	m = next.(appModel)
	next, hiddenQuery := m.Update(tokenMonitorTickMsg{})
	m = next.(appModel)
	if hiddenQuery != nil || m.activity.visible || m.tokenMonitor.loading || !m.input.Focused() {
		t.Fatal("explicitly hidden panel queried or stole input focus")
	}
	next, query = m.Update(tea.KeyMsg{Type: tea.KeyCtrlG})
	m = next.(appModel)
	if query == nil || !m.tokenMonitor.loading || !m.input.Focused() || len(reader.sessions) != 2 {
		t.Fatal("reopening lost input focus or failed to restart polling")
	}
}

func TestPersistentReviewIdleTypingDefersAndResumesPolling(t *testing.T) {
	m, reader := newPersistentReviewModel(t)
	next, query := m.Update(tea.WindowSizeMsg{Width: 120, Height: 36})
	m = next.(appModel)
	next, _ = m.Update(persistentReviewResult(t, query))
	m = next.(appModel)
	m.lastKeyEventAt = time.Now()
	next, deferred := m.Update(tokenMonitorTickMsg{})
	m = next.(appModel)
	if deferred == nil || !m.tokenMonitor.scheduled || m.tokenMonitor.loading || len(reader.sessions) != 1 {
		t.Fatal("idle typing must defer IO while keeping the next poll scheduled")
	}
	m.lastKeyEventAt = time.Time{}
	next, query = m.Update(tokenMonitorTickMsg{})
	m = next.(appModel)
	if query == nil || !m.tokenMonitor.loading || m.tokenMonitor.scheduled {
		t.Fatal("poll did not resume after the idle input guard expired")
	}
	next, tick := m.Update(persistentReviewResult(t, query))
	m = next.(appModel)
	if tick == nil || !m.tokenMonitor.scheduled || len(reader.sessions) != 2 || !m.input.Focused() {
		t.Fatal("resumed poll lost the chain or input focus")
	}
}

func TestPersistentReviewSessionRestoreRejectsInFlightOldData(t *testing.T) {
	m, reader := newPersistentReviewModel(t)
	next, oldQuery := m.Update(tea.WindowSizeMsg{Width: 120, Height: 36})
	m = next.(appModel)
	if oldQuery == nil {
		t.Fatal("default monitor did not start")
	}
	m.tokenMonitor.live.start("old-request", time.Now())
	next, _ = m.Update(sessionRestoredMsg{sessionID: "new-session", source: sessionRestorePicker})
	m = next.(appModel)
	if !m.activity.visible || !m.input.Focused() || m.tokenMonitor.live.view(time.Now()).RequestID != "" || !m.tokenMonitor.loading {
		t.Fatal("restore lost visible input or failed to preserve the outstanding IO guard")
	}
	next, _ = m.Update(persistentReviewResult(t, oldQuery))
	m = next.(appModel)
	if m.tokenMonitor.snapshot.SessionID != "" {
		t.Fatal("old-session result populated the new session")
	}
	next, query := m.Update(tokenMonitorTickMsg{})
	m = next.(appModel)
	next, _ = m.Update(persistentReviewResult(t, query))
	m = next.(appModel)
	if m.tokenMonitor.snapshot.SessionID != "new-session" || strings.Join(reader.sessions, ",") != "persistent-review,new-session" {
		t.Fatalf("new-session monitor did not resume: snapshot=%q reads=%v", m.tokenMonitor.snapshot.SessionID, reader.sessions)
	}
	m.closeActivity()
	next, _ = m.Update(sessionRestoredMsg{sessionID: "third-session", source: sessionRestoreNew})
	if next.(appModel).activity.visible {
		t.Fatal("restoring a session overrode the user's explicit hidden preference")
	}
}

func TestPersistentReviewPanelKeysRemainInputKeys(t *testing.T) {
	m, _ := newPersistentReviewModel(t)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 36})
	m = next.(appModel)
	m.openActivity(activityTabTodo)
	m.input.SetValue("")
	for _, char := range "fhjkl" {
		next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{char}})
		m = next.(appModel)
	}
	if m.input.Value() != "fhjkl" || !m.input.Focused() || m.currentLayout().activityMode != activityLayoutDocked {
		t.Fatalf("sidebar consumed ordinary typing: draft=%q focus=%t", m.input.Value(), m.input.Focused())
	}
	m.input.SetValue("alpha beta")
	m.input.CursorEnd()
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlW})
	m = next.(appModel)
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'l'}})
	m = next.(appModel)
	if m.input.Value() != "alpha l" || !m.input.Focused() {
		t.Fatalf("Ctrl+W still owns a pane chord: draft=%q focus=%t", m.input.Value(), m.input.Focused())
	}
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(appModel)
	if cmd == nil || !m.isModelWorkRunning() || m.input.Value() != "" || !m.input.Focused() {
		t.Fatal("Enter did not submit the input while the sidebar was visible")
	}
}

func TestPersistentReviewSidebarMouseDoesNotCountAsInputDock(t *testing.T) {
	m, _ := newPersistentReviewModel(t)
	m.width, m.height, m.ready = 120, 36, true
	m.openActivity(activityTabTasks)
	layout := m.currentLayout()
	y := 1 + layout.headerHeight + layout.transcriptHeight + layout.statusHeight
	for _, x := range []int{layout.workspaceWidth, layout.workspaceWidth + layout.activitySeparatorWidth, layout.frameWidth - 1} {
		mouse := tea.MouseMsg{X: x, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress}
		if m.isInputDockMouse(mouse) {
			t.Errorf("sidebar/separator cell %d incorrectly belongs to input dock", x)
		}
	}
	if !m.isInputDockMouse(tea.MouseMsg{X: layout.workspaceWidth - 1, Y: y}) {
		t.Fatal("last workspace column must remain in the input dock")
	}
	m.transcriptKeyScrollActive = true
	next, _ := m.Update(tea.MouseMsg{X: layout.frameWidth - 1, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	if !next.(appModel).transcriptKeyScrollActive || !next.(appModel).input.Focused() {
		t.Fatal("clicking the sidebar was treated as clicking the input editor")
	}
}
