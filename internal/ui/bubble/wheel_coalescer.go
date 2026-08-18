package bubble

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

const transcriptWheelFlushInterval = time.Second / 60

// A canceled active tail can be expensive to finalize. Let manual scrolling
// settle before paying that one-time render cost.
const transcriptCanceledRefreshIdleInterval = 75 * time.Millisecond

type transcriptWheelFlushMsg struct {
	generation uint64
}

type transcriptDeferredRefreshMsg struct {
	generation uint64
}

type transcriptWheelBatchMsg struct {
	lines      int
	x          int
	y          int
	generation uint64
	flush      tea.Cmd
}

type programEventFilter struct {
	scheduleWheelFlush func(uint64) tea.Cmd
	pendingLines       int
	pendingX           int
	pendingY           int
	direction          int
	generation         uint64
	flushScheduled     bool
}

func newProgramEventFilter(scheduleWheelFlush func(uint64) tea.Cmd) *programEventFilter {
	return &programEventFilter{scheduleWheelFlush: scheduleWheelFlush}
}

func scheduleTranscriptWheelFlush(generation uint64) tea.Cmd {
	return tea.Tick(transcriptWheelFlushInterval, func(time.Time) tea.Msg {
		return transcriptWheelFlushMsg{generation: generation}
	})
}

func (f *programEventFilter) Filter(model tea.Model, msg tea.Msg) tea.Msg {
	if f == nil {
		return filterIdleMouseMotion(model, msg)
	}
	if flush, ok := msg.(transcriptWheelFlushMsg); ok {
		return f.flush(model, flush)
	}
	if mouse, ok := msg.(tea.MouseMsg); ok {
		m, modelOK := transcriptWheelFilterModel(model)
		if modelOK {
			if lines, wheelOK := transcriptWheelLines(m, mouse); wheelOK {
				return f.pushWheel(lines, mouse.X, mouse.Y)
			}
		}
		f.cancelPending()
		return filterIdleMouseMotion(model, msg)
	}
	if _, ok := msg.(tea.KeyMsg); ok {
		f.cancelPending()
	}
	return filterIdleMouseMotion(model, msg)
}

func transcriptWheelLines(m appModel, msg tea.MouseMsg) (int, bool) {
	if !m.viewport.MouseWheelEnabled || msg.Action != tea.MouseActionPress || msg.Shift || !m.isTranscriptViewportMouse(msg) {
		return 0, false
	}
	delta := m.viewport.MouseWheelDelta
	if delta <= 0 {
		return 0, false
	}
	switch msg.Button {
	case tea.MouseButtonWheelUp:
		return -delta, true
	case tea.MouseButtonWheelDown:
		return delta, true
	default:
		return 0, false
	}
}

func (m appModel) applyTranscriptWheelBatch(lines, x, y int) appModel {
	m.rawMouseEscapePending = ""
	if m.clickActionPending {
		m.clickActionPending = false
		m.clickActionSeq++
	}
	beforeOffset := m.viewport.YOffset
	switch {
	case lines < 0:
		m.viewport.ScrollUp(-lines)
	case lines > 0:
		m.viewport.ScrollDown(lines)
	}
	if m.viewport.YOffset != beforeOffset {
		m.reconcileToolHoverAtMouse(x, y)
	}
	m.syncNewMessageNoticeAfterScroll()
	m.transcriptKeyScrollActive = true
	return m
}

func (m *appModel) renewDeferredTranscriptRefresh() tea.Cmd {
	if m == nil || !m.transcriptRefreshDeferred {
		return nil
	}
	m.transcriptRefreshDeferredGeneration++
	generation := m.transcriptRefreshDeferredGeneration
	return tea.Tick(transcriptCanceledRefreshIdleInterval, func(time.Time) tea.Msg {
		return transcriptDeferredRefreshMsg{generation: generation}
	})
}

func (f *programEventFilter) pushWheel(lines, x, y int) tea.Msg {
	direction := 1
	if lines < 0 {
		direction = -1
	}
	if !f.flushScheduled {
		f.generation++
		f.flushScheduled = true
		f.direction = direction
		return transcriptWheelBatchMsg{
			lines:      lines,
			x:          x,
			y:          y,
			generation: f.generation,
			flush:      f.nextFlush(),
		}
	}
	if direction != f.direction {
		f.pendingLines = 0
		f.direction = direction
		return transcriptWheelBatchMsg{
			lines:      lines,
			x:          x,
			y:          y,
			generation: f.generation,
		}
	}
	f.pendingLines += lines
	f.pendingX = x
	f.pendingY = y
	return nil
}

func (f *programEventFilter) flush(model tea.Model, msg transcriptWheelFlushMsg) tea.Msg {
	if !f.flushScheduled || msg.generation != f.generation {
		return nil
	}
	if f.pendingLines == 0 {
		f.flushScheduled = false
		f.direction = 0
		return nil
	}
	m, ok := transcriptWheelFilterModel(model)
	if !ok || !m.isTranscriptViewportMouse(tea.MouseMsg{X: f.pendingX, Y: f.pendingY}) {
		f.cancelPending()
		return nil
	}
	batch := transcriptWheelBatchMsg{
		lines:      f.pendingLines,
		x:          f.pendingX,
		y:          f.pendingY,
		generation: f.generation,
		flush:      f.nextFlush(),
	}
	f.pendingLines = 0
	return batch
}

func transcriptWheelFilterModel(model tea.Model) (appModel, bool) {
	if m, ok := model.(appModel); ok {
		return m, true
	}
	provider, ok := model.(interface{ transcriptWheelAppModel() appModel })
	if !ok {
		return appModel{}, false
	}
	return provider.transcriptWheelAppModel(), true
}

func (f *programEventFilter) nextFlush() tea.Cmd {
	if f.scheduleWheelFlush == nil {
		return nil
	}
	return f.scheduleWheelFlush(f.generation)
}

func (f *programEventFilter) cancelPending() {
	if !f.flushScheduled && f.pendingLines == 0 {
		return
	}
	f.pendingLines = 0
	f.direction = 0
	f.flushScheduled = false
	f.generation++
}
