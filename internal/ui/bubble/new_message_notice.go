package bubble

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
)

// transcriptNoticeBounds 描述浮空提示在终端中的可点击区域。
type transcriptNoticeBounds struct {
	x      int
	y      int
	width  int
	height int
}

func newMessageNoticeText(count int, hovered bool, width int) string {
	if count <= 0 || width <= 0 {
		return ""
	}
	full := fmt.Sprintf("↓ %d 条新消息", count)
	if hovered {
		full += "  回到底部"
	}
	if terminalCellWidth(full) <= width {
		return full
	}
	compact := fmt.Sprintf("↓ %d 条消息", count)
	return truncateDisplayWidth(compact, width)
}

func (m appModel) newMessageNoticeCanRender() bool {
	return m.modelWizard == nil &&
		m.settingWizard == nil &&
		m.sessionPicker == nil &&
		m.subagentPicker == nil &&
		m.todoPage == nil &&
		m.completion == nil
}

func (m appModel) renderNewMessageNotice(width int) string {
	if m.newMessageNoticeCount <= 0 || width <= 0 || !m.newMessageNoticeCanRender() {
		return ""
	}
	style := m.styles.Notice
	hoverStyle := m.styles.NoticeHover
	textWidth := maxInt(1, width-style.GetHorizontalPadding())
	text := newMessageNoticeText(m.newMessageNoticeCount, m.newMessageNoticeHovered, textWidth)
	if text == "" {
		return ""
	}
	if m.newMessageNoticeHovered {
		style = hoverStyle
	}
	return style.Render(text)
}

func (m appModel) transcriptNoticeBounds() transcriptNoticeBounds {
	if m.newMessageNoticeCount <= 0 || !m.newMessageNoticeCanRender() {
		return transcriptNoticeBounds{}
	}
	layout := m.currentLayout()
	if layout.transcriptHeight <= 0 || layout.contentWidth <= 0 {
		return transcriptNoticeBounds{}
	}
	style := m.styles.Notice
	hoverStyle := m.styles.NoticeHover
	textWidth := maxInt(1, layout.contentWidth-style.GetHorizontalPadding())
	width := terminalCellWidth(style.Render(
		newMessageNoticeText(m.newMessageNoticeCount, false, textWidth),
	))
	if m.newMessageNoticeHovered {
		width = terminalCellWidth(hoverStyle.Render(
			newMessageNoticeText(m.newMessageNoticeCount, true, textWidth),
		))
	}
	if width <= 0 {
		return transcriptNoticeBounds{}
	}
	return transcriptNoticeBounds{
		x:      maxInt(0, (layout.contentWidth-width)/2),
		y:      m.transcriptScreenTop() + layout.transcriptHeight - 1,
		width:  minInt(width, layout.contentWidth),
		height: 1,
	}
}

func (b transcriptNoticeBounds) contains(x, y int) bool {
	return b.width > 0 && b.height > 0 &&
		x >= b.x && x < b.x+b.width &&
		y >= b.y && y < b.y+b.height
}

func (m appModel) handleNewMessageNoticeMouse(msg tea.MouseMsg) (appModel, bool, tea.Cmd) {
	if m.selecting || m.newMessageNoticeCount <= 0 {
		return m, false, nil
	}
	bounds := m.transcriptNoticeBounds()
	inside := bounds.contains(msg.X, msg.Y)
	switch msg.Action {
	case tea.MouseActionMotion:
		changed := m.newMessageNoticeHovered != inside
		m.newMessageNoticeHovered = inside
		return m, changed, nil
	case tea.MouseActionPress:
		if msg.Button != tea.MouseButtonLeft || !inside {
			return m, false, nil
		}
		m.newMessageNoticePressed = true
		m.newMessageNoticeHovered = true
		return m, true, nil
	case tea.MouseActionRelease:
		if !m.newMessageNoticePressed {
			return m, false, nil
		}
		clicked := msg.Button == tea.MouseButtonLeft && inside
		m.newMessageNoticePressed = false
		m.newMessageNoticeHovered = inside
		if clicked {
			m.selectionActive = false
			m.selecting = false
			m.viewport.GotoBottom()
			m.clearNewMessageNotice()
			m.refreshViewport()
		}
		return m, true, nil
	default:
		return m, false, nil
	}
}

func (m *appModel) recordTranscriptEntryActivity(index int, countAtBottom bool) {
	if m == nil || index < 0 || index >= len(m.transcript) {
		return
	}
	if m.newMessageNoticeCycle == 0 {
		m.newMessageNoticeCycle = 1
	}
	entry := &m.transcript[index]
	if entry.newMessageNoticeCycle == m.newMessageNoticeCycle {
		return
	}
	atBottom := m.viewport.AtBottom()
	if atBottom && !countAtBottom {
		return
	}
	entry.newMessageNoticeCycle = m.newMessageNoticeCycle
	if !atBottom {
		m.newMessageNoticeCount++
	}
}

func (m *appModel) recordAssistantActivity(index int) {
	if m == nil || m.viewport.AtBottom() {
		return
	}
	m.recordTranscriptEntryActivity(index, false)
}

func (m *appModel) clearNewMessageNotice() {
	if m == nil {
		return
	}
	m.newMessageNoticeCount = 0
	m.newMessageNoticeHovered = false
	m.newMessageNoticePressed = false
	m.newMessageNoticeCycle++
	if m.newMessageNoticeCycle == 0 {
		m.newMessageNoticeCycle = 1
	}
}

func (m *appModel) syncNewMessageNoticeAfterScroll() {
	if m != nil && m.newMessageNoticeCount > 0 && m.viewport.AtBottom() {
		m.clearNewMessageNotice()
	}
}
