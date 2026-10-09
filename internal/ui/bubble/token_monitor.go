package bubble

import (
	"context"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"paw/internal/capability/model"
	"paw/internal/platform/pawpath"
	"paw/internal/tokentracer"
)

type tokenMonitorReader interface {
	Monitor(workspace, session string) (tokentracer.MonitorSnapshot, error)
}

type tokenMonitorState struct {
	sessionID          string
	reader             tokenMonitorReader
	snapshot           tokentracer.MonitorSnapshot
	live               tokenLiveState
	loading, scheduled bool
	err                string
	selected           int
	detail             bool
}

type tokenMonitorStartMsg struct {
	id string
	at time.Time
}
type tokenMonitorActivityMsg struct {
	id       string
	activity model.StreamActivity
}
type tokenMonitorEndMsg struct {
	id string
	at time.Time
}
type tokenMonitorUsageMsg model.Usage
type tokenMonitorTickMsg struct{}
type tokenMonitorResultMsg struct {
	session  string
	snapshot tokentracer.MonitorSnapshot
	err      error
}

func (u *UI) OnModelRequestStart(id string) {
	_ = u.send(tokenMonitorStartMsg{id: id, at: time.Now()})
}

func (u *UI) OnModelUsage(usage model.Usage) {
	_ = u.send(tokenMonitorUsageMsg(usage))
}

func (u *UI) OnModelStreamActivity(id string, activity model.StreamActivity) {
	_ = u.send(tokenMonitorActivityMsg{id: id, activity: activity})
}

func (u *UI) OnModelRequestEnd(id string, at time.Time) {
	_ = u.send(tokenMonitorEndMsg{id: id, at: at})
}

func (m *appModel) syncTokenMonitorSession() {
	if m.tokenMonitor.sessionID == m.sessionID {
		return
	}
	previous := m.tokenMonitor
	m.tokenMonitor = tokenMonitorState{sessionID: m.sessionID, reader: previous.reader, loading: previous.loading, scheduled: previous.scheduled}
}

func (m appModel) tokenMonitorVisible() bool {
	return m.ready && m.activity.visible && m.currentLayout().activityMode == activityLayoutDocked
}

func (m *appModel) tokenMonitorCommand() tea.Cmd {
	m.syncTokenMonitorSession()
	if !m.tokenMonitorVisible() || m.tokenMonitor.loading || m.tokenMonitor.scheduled || m.sessionID == "" {
		return nil
	}
	if m.ctx != nil && m.ctx.Err() != nil {
		return nil
	}
	if m.tokenMonitor.reader == nil {
		home, err := pawpath.Home()
		if err != nil {
			m.tokenMonitor.err = err.Error()
			return nil
		}
		m.tokenMonitor.reader = tokentracer.NewLedgerReader(home)
	}
	workspace := m.worktreeCWD
	if provider, ok := m.runner.(WorkspaceRootProvider); ok && provider.WorkspaceRoot() != "" {
		workspace = provider.WorkspaceRoot()
	}
	session, reader, ctx := m.sessionID, m.tokenMonitor.reader, m.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	m.tokenMonitor.loading = true
	return func() tea.Msg {
		if err := ctx.Err(); err != nil {
			return tokenMonitorResultMsg{session: session, err: err}
		}
		snapshot, err := reader.Monitor(workspace, session)
		return tokenMonitorResultMsg{session: session, snapshot: snapshot, err: err}
	}
}

func (m *appModel) applyTokenMonitorResult(msg tokenMonitorResultMsg) tea.Cmd {
	m.tokenMonitor.loading = false
	if msg.session == m.sessionID {
		if msg.err != nil {
			m.tokenMonitor.err = msg.err.Error()
		} else {
			selectedID := ""
			if m.tokenMonitor.selected < len(m.tokenMonitor.snapshot.Requests) {
				selectedID = m.tokenMonitor.snapshot.Requests[m.tokenMonitor.selected].ID
			}
			m.tokenMonitor.snapshot = msg.snapshot
			m.tokenMonitor.err = ""
			m.tokenMonitor.selected = clampInt(m.tokenMonitor.selected, 0, maxInt(0, len(msg.snapshot.Requests)-1))
			for i, r := range msg.snapshot.Requests {
				if r.ID == selectedID {
					m.tokenMonitor.selected = i
					break
				}
			}
		}
	}
	return m.scheduleTokenMonitorTick()
}

func (m *appModel) scheduleTokenMonitorTick() tea.Cmd {
	if !m.tokenMonitorVisible() || m.tokenMonitor.scheduled {
		return nil
	}
	m.tokenMonitor.scheduled = true
	return tea.Tick(time.Second, func(time.Time) tea.Msg { return tokenMonitorTickMsg{} })
}

func (m appModel) tokenMonitorView() tokenMonitorView {
	return tokenMonitorView{Snapshot: m.tokenMonitor.snapshot, Rates: m.tokenMonitor.live.view(time.Now()), Context: m.contextStats(), Loading: m.tokenMonitor.loading && m.tokenMonitor.snapshot.GeneratedAt.IsZero(), Err: m.tokenMonitor.err, Selected: m.tokenMonitor.selected, Detail: m.tokenMonitor.detail}
}
