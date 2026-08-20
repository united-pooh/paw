package bubble

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"paw/internal/ui/bubble/viewportx"
)

// A canceled active tail can be expensive to finalize. Let manual scrolling
// settle before paying that one-time render cost.
const transcriptCanceledRefreshIdleInterval = 75 * time.Millisecond

type transcriptDeferredRefreshMsg struct {
	generation uint64
}

// transcriptWheelBatchMsg carries one scroll tick. Each wheel event is applied
// immediately (event-driven) instead of being coalesced onto a fixed 60fps
// flush timer: the quantized 16ms cadence made fast scrolling feel laggy.
// Render cost stays bounded because viewport.View() caches the visible-window
// string — scrolling only changes YOffset, it never re-lays-out the transcript.
type transcriptWheelBatchMsg struct {
	lines int
	x     int
	y     int
}

// programEventFilter routes wheel events straight into the model update and
// keeps the idle-mouse-motion filter for hover accounting. It no longer merges
// consecutive wheel events, so the viewport follows the input 1:1.
type programEventFilter struct{}

func newProgramEventFilter() *programEventFilter {
	return &programEventFilter{}
}

func (f *programEventFilter) Filter(model tea.Model, msg tea.Msg) tea.Msg {
	if f == nil {
		return filterIdleMouseMotion(model, msg)
	}
	if mouse, ok := msg.(tea.MouseMsg); ok {
		m, modelOK := transcriptWheelFilterModel(model)
		if modelOK {
			if lines, wheelOK := transcriptWheelLines(m, mouse); wheelOK {
				if wheelClampsAtBoundary(m.viewport, lines) {
					// 边界无效滚动（底部继续下滑/顶部继续上滑）：视口不可能
					// 移动，直接丢弃不占队列槽位。惯性滚动在边界的连发不再
					// 积压，紧随其后的反向滚动零等待生效。
					return nil
				}
				return transcriptWheelBatchMsg{lines: lines, x: mouse.X, y: mouse.Y}
			}
		}
	}
	return filterIdleMouseMotion(model, msg)
}

// wheelClampsAtBoundary 报告本次滚动是否撞边界：底部继续下滑或顶部继续上滑。
// 撞边界的事件不可能改变 YOffset（ScrollUp/ScrollDown 提前返回），在 filter
// 层丢弃是语义等价的，同时消掉队列占用。
func wheelClampsAtBoundary(vp viewportx.Model, lines int) bool {
	return (lines > 0 && vp.AtBottom()) || (lines < 0 && vp.AtTop())
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
