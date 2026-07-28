// 本文件实现紧凑工具事务轨道的键盘检查模式与行命中计算。
package bubble

import (
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type transcriptEntryLocation struct {
	transcriptIndex int
	startRow        int
	height          int
}

func transcriptEntryLocations(entries []transcriptEntry, width int, showThinking bool, at time.Time) []transcriptEntryLocation {
	locations := make([]transcriptEntryLocation, 0, len(entries))
	totalRows := 0
	hasPrevious := false
	var previousKind entryKind
	for index, entry := range entries {
		if entry.kind == entryThinking && !showThinking {
			continue
		}
		if !assistantEntryIsRenderable(entry) {
			continue
		}
		rendered := strings.TrimRight(renderEntryAt(entry, width, at), "\n")
		if rendered == "" {
			continue
		}
		startRow := 0
		if hasPrevious {
			startRow = totalRows + strings.Count(transcriptEntrySeparator(previousKind, entry.kind), "\n") - 1
		}
		height := maxInt(1, lipgloss.Height(rendered))
		locations = append(locations, transcriptEntryLocation{
			transcriptIndex: index,
			startRow:        startRow,
			height:          height,
		})
		totalRows = startRow + height
		previousKind = entry.kind
		hasPrevious = true
	}
	return locations
}

func (m appModel) toolIndexAtTranscriptRow(row int) (int, bool) {
	for _, location := range transcriptEntryLocations(m.transcript, maxInt(20, m.viewport.Width), m.showThinking, m.animationNow()) {
		if row < location.startRow || row >= location.startRow+location.height {
			continue
		}
		entry := m.transcript[location.transcriptIndex]
		if isToolTransaction(entry) {
			return location.transcriptIndex, true
		}
		return -1, false
	}
	return -1, false
}

func (m *appModel) openToolInspect() (tea.Model, tea.Cmd) {
	if m == nil {
		return appModel{}, nil
	}
	index := -1
	for i := len(m.transcript) - 1; i >= 0; i-- {
		if isToolTransaction(m.transcript[i]) {
			index = i
			break
		}
	}
	if index < 0 {
		return *m, nil
	}
	m.clearToolFocus()
	m.toolInspectActive = true
	m.toolInspectIndex = index
	m.transcript[index].toolFocused = true
	touchTranscriptEntry(&m.transcript[index])
	m.input.Blur()
	m.completion = nil
	m.relayout()
	m.refreshViewportPreservingOffset()
	m.ensureInspectedToolVisible()
	return *m, nil
}

func (m *appModel) handleToolInspectKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "ctrl+t":
		return m.closeToolInspect()
	case "up", "k":
		m.moveToolInspection(-1)
	case "down", "j":
		m.moveToolInspection(1)
	case "enter":
		m.toggleToolExpansion(m.toolInspectIndex)
	}
	return *m, nil
}

func (m *appModel) closeToolInspect() (tea.Model, tea.Cmd) {
	m.resetToolInspect()
	m.refreshViewportPreservingOffset()
	return *m, m.input.Focus()
}

func (m *appModel) resetToolInspect() {
	if m == nil {
		return
	}
	wasActive := m.toolInspectActive
	m.clearToolFocus()
	m.clearToolHover()
	m.toolInspectActive = false
	m.toolInspectIndex = -1
	m.transcriptKeyScrollActive = false
	if wasActive {
		m.input.Focus()
	}
}

func (m *appModel) setToolHover(index int) bool {
	if m == nil {
		return false
	}
	if index < 0 || index >= len(m.transcript) || !isToolTransaction(m.transcript[index]) {
		index = -1
	}
	if m.toolHoverIndex == index {
		return false
	}
	m.clearToolHover()
	m.toolHoverIndex = index
	if index >= 0 {
		m.transcript[index].toolHovered = true
		touchTranscriptEntry(&m.transcript[index])
	}
	return true
}

func (m *appModel) clearToolHover() {
	if m == nil {
		return
	}
	if m.toolHoverIndex >= 0 && m.toolHoverIndex < len(m.transcript) {
		entry := &m.transcript[m.toolHoverIndex]
		if entry.toolHovered {
			entry.toolHovered = false
			touchTranscriptEntry(entry)
		}
	} else {
		for index := range m.transcript {
			if !m.transcript[index].toolHovered {
				continue
			}
			m.transcript[index].toolHovered = false
			touchTranscriptEntry(&m.transcript[index])
		}
	}
	m.toolHoverIndex = -1
}

func (m *appModel) clearToolFocus() {
	for index := range m.transcript {
		if !m.transcript[index].toolFocused {
			continue
		}
		m.transcript[index].toolFocused = false
		touchTranscriptEntry(&m.transcript[index])
	}
}

func (m *appModel) moveToolInspection(direction int) {
	if m == nil || !m.toolInspectActive || direction == 0 {
		return
	}
	next := m.toolInspectIndex
	for {
		next += direction
		if next < 0 || next >= len(m.transcript) {
			return
		}
		if isToolTransaction(m.transcript[next]) {
			break
		}
	}
	m.clearToolFocus()
	m.toolInspectIndex = next
	m.transcript[next].toolFocused = true
	touchTranscriptEntry(&m.transcript[next])
	m.refreshViewportPreservingOffset()
	m.ensureInspectedToolVisible()
}

func (m *appModel) toggleToolExpansion(index int) bool {
	if m == nil || index < 0 || index >= len(m.transcript) {
		return false
	}
	entry := &m.transcript[index]
	if !isToolTransaction(*entry) || toolEntryStatus(*entry) == "running" {
		return false
	}
	entry.toolExpanded = !entry.toolExpanded
	touchTranscriptEntry(entry)
	m.refreshViewportPreservingOffset()
	if m.toolInspectActive {
		m.ensureInspectedToolVisible()
	}
	return true
}

func (m *appModel) selectInspectedTool(index int) {
	if m == nil || !m.toolInspectActive || index < 0 || index >= len(m.transcript) {
		return
	}
	m.clearToolFocus()
	m.toolInspectIndex = index
	m.transcript[index].toolFocused = true
	touchTranscriptEntry(&m.transcript[index])
}

func (m *appModel) ensureInspectedToolVisible() {
	if m == nil || !m.toolInspectActive || m.viewport.Height <= 0 {
		return
	}
	for _, location := range transcriptEntryLocations(m.transcript, maxInt(20, m.viewport.Width), m.showThinking, m.animationNow()) {
		if location.transcriptIndex != m.toolInspectIndex {
			continue
		}
		switch {
		case location.startRow < m.viewport.YOffset:
			m.viewport.SetYOffset(location.startRow)
		case location.startRow >= m.viewport.YOffset+m.viewport.Height:
			m.viewport.SetYOffset(maxInt(0, location.startRow-m.viewport.Height+1))
		}
		return
	}
}
