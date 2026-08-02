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

// transcriptEntryLocationsAt computes the layout of the rendered transcript
// exactly as renderTranscriptContentAt will draw it, so mouse hit-testing and
// scroll-into-view use the same row arithmetic as the visible content. A
// collapsed Tools group therefore occupies only its header row, an expanded
// group the header plus every tool summary and open result detail row.
func (m *appModel) transcriptEntryLocationsAt() []transcriptEntryLocation {
	if m == nil {
		return nil
	}
	if m.transcriptLocationsReady {
		return m.transcriptLocationCache
	}
	m.transcriptLocationCache = transcriptEntryLocationsWith(m.transcript, maxInt(20, m.viewport.Width), m.showThinking, m.animationNow(), m.toolGroupExpanded, m.toolGroupFullResult)
	m.transcriptLocationsReady = true
	return m.transcriptLocationCache
}

// transcriptEntryLocations is the free-function form used by tests; running
// groups count as expanded, completed groups use their own toolExpanded flag.
func transcriptEntryLocations(entries []transcriptEntry, width int, showThinking bool, at time.Time) []transcriptEntryLocation {
	return transcriptEntryLocationsWith(entries, width, showThinking, at, false, false)
}

func transcriptEntryLocationsWith(entries []transcriptEntry, width int, showThinking bool, at time.Time, globalGroupExpanded, fullResult bool) []transcriptEntryLocation {
	locations := make([]transcriptEntryLocation, 0, len(entries))
	totalRows := 0
	hasPrevious := false
	var previousKind entryKind
	for index := 0; index < len(entries); index++ {
		entry := entries[index]
		if entry.kind == entryThinking && !showThinking {
			continue
		}
		if !assistantEntryIsRenderable(entry) {
			continue
		}

		rendered := ""
		locationIndex := index
		kind := entry.kind
		if isToolTransaction(entry) {
			first, last := toolGroupRange(entries, index)
			if first != index {
				continue
			}
			groupEntries := toolEntriesForGroup(entries, index)
			expanded := globalGroupExpanded
			if !toolGroupHasRunning(entries, first, last) {
				expanded = entries[first].toolExpanded
			}
			rendered = renderToolsGroup(groupEntries, width, at, expanded, fullResult)
			locationIndex = first
			kind = entryTool
		} else {
			rendered = renderEntryAt(entry, width, at)
		}
		rendered = strings.TrimRight(rendered, "\n")
		if rendered == "" {
			continue
		}
		startRow := 0
		if hasPrevious {
			startRow = totalRows + strings.Count(transcriptEntrySeparator(previousKind, kind), "\n") - 1
		}
		height := maxInt(1, lipgloss.Height(rendered))
		locations = append(locations, transcriptEntryLocation{transcriptIndex: locationIndex, startRow: startRow, height: height})
		totalRows = startRow + height
		previousKind = kind
		hasPrevious = true
	}
	return locations
}

// toolIndexAtTranscriptRow maps a rendered transcript row back to the group
// (or per-tool detail) that contains it. When a completed group is expanded
// and the clicked row lies inside a tool's result detail block, it resolves
// to that individual tool entry instead of the whole group.
func (m appModel) toolIndexAtTranscriptRow(row int) (int, bool) {
	index, _, ok := m.toolHitAtTranscriptRow(row)
	return index, ok
}

// toolHitAtTranscriptRow resolves a rendered transcript row to the tool entry
// it belongs to. isHeaderRow reports whether the row is the Tools group header
// row. The resolution mirrors the render path: a collapsed group maps every
// row to the group; an expanded group maps the header to the group and each
// summary/detail row to its owning tool entry.
func (m appModel) toolHitAtTranscriptRow(row int) (index int, isHeaderRow bool, ok bool) {
	for _, location := range m.transcriptEntryLocationsAt() {
		if row < location.startRow || row >= location.startRow+location.height {
			continue
		}
		if !isToolTransaction(m.transcript[location.transcriptIndex]) {
			return -1, false, false
		}
		first, last := toolGroupRange(m.transcript, location.transcriptIndex)
		groupExpanded := m.toolGroupExpanded
		if !toolGroupHasRunning(m.transcript, first, last) {
			groupExpanded = m.transcript[first].toolExpanded
		}
		if row == location.startRow {
			return first, true, true
		}
		if !groupExpanded {
			return first, false, true
		}
		entry, _ := m.toolDetailEntryAtRow(row, location)
		return entry, false, true
	}
	return -1, false, false
}

// toolDetailEntryAtRow resolves which single tool entry owns the given row
// inside an expanded, completed Tools group. The group header row and every
// tool summary row belong to that tool; rows inside a tool's result detail
// block belong to that tool. The first tool owns its summary row plus its
// detail block (or the remaining header rows).
func (m *appModel) toolDetailEntryAtRow(row int, location transcriptEntryLocation) (int, bool) {
	if m == nil || location.transcriptIndex < 0 || location.transcriptIndex >= len(m.transcript) {
		return location.transcriptIndex, true
	}
	groupEntries := toolEntriesForGroup(m.transcript, location.transcriptIndex)
	if len(groupEntries) == 0 {
		return location.transcriptIndex, true
	}
	inner := maxInt(1, location.height-2)
	// The group header row belongs to the group as a whole; tool rows start
	// on the row right below it.
	cursor := location.startRow + 1
	for i, entry := range groupEntries {
		summaryHeight := renderToolSummaryHeight(entry, inner)
		detailHeight := 0
		if entry.toolGroupOpen && toolEntryStatus(entry) != "running" {
			detailHeight = renderToolResultDetailHeight(entry, maxInt(1, inner-6), m.toolGroupFullResult)
		}
		entryHeight := summaryHeight + detailHeight
		end := cursor + entryHeight
		if row < end {
			return m.transcriptIndexForGroupEntry(location.transcriptIndex, i), true
		}
		cursor = end
	}
	return location.transcriptIndex, true
}

// transcriptIndexForGroupEntry returns the absolute transcript index of the
// i-th tool entry inside the group that starts at groupStart.
func (m *appModel) transcriptIndexForGroupEntry(groupStart, i int) int {
	count := 0
	for index := groupStart; index < len(m.transcript); index++ {
		if m.transcript[index].kind == entryUser {
			break
		}
		if !isToolTransaction(m.transcript[index]) {
			break
		}
		if count == i {
			return index
		}
		count++
	}
	return groupStart
}

// renderToolSummaryHeight returns the number of terminal rows a tool's
// compact summary occupies for the given inner group width. renderToolsGroup
// renders the summary truncated to innerWidth-4 cells and indents it by two
// cells, so it never exceeds innerWidth and always occupies exactly one row.
func renderToolSummaryHeight(entry transcriptEntry, innerWidth int) int {
	return 1
}

// renderToolResultDetailHeight returns the number of terminal rows a tool's
// expanded result detail block occupies. It mirrors renderToolsGroup: unless
// fullResult is set (toolGroupFullResult with the tool focused), the result is
// truncated to maxRenderedToolDetailLines, and every line is truncated to the
// detail width before rendering, so each line occupies exactly one row.
func renderToolResultDetailHeight(entry transcriptEntry, width int, fullResult bool) int {
	result := toolResultForDisplay(entry)
	if result == "" {
		return 1
	}
	detailLines := strings.Split(result, "\n")
	if !(fullResult && entry.toolFocused) {
		detailLines = limitRenderedDetailLines(detailLines, maxRenderedToolDetailLines)
	}
	if len(detailLines) == 0 {
		return 1
	}
	return len(detailLines)
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
		m.toolGroupExpanded = true
		m.toggleToolExpansion(m.toolInspectIndex, false)
	case "ctrl+o":
		if m.toolInspectIndex >= 0 && m.toolInspectIndex < len(m.transcript) {
			entry := &m.transcript[m.toolInspectIndex]
			if isToolTransaction(*entry) && toolEntryStatus(*entry) != "running" {
				m.toolGroupExpanded = true
				m.toolGroupFullResult = !m.toolGroupFullResult
				m.touchToolGroupRender()
			}
		}
	}
	return *m, nil
}

func (m *appModel) touchToolGroupRender() {
	m.transcriptRenderCache = nil
	m.refreshViewportPreservingOffset()
	if m.toolInspectActive {
		m.ensureInspectedToolVisible()
	}
}

func (m *appModel) closeToolInspect() (tea.Model, tea.Cmd) {
	m.resetToolInspect()
	m.toolGroupExpanded = false
	m.toolGroupFullResult = false
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
	m.toolGroupFullResult = false
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
			if m.transcript[index].toolHovered {
				m.transcript[index].toolHovered = false
				touchTranscriptEntry(&m.transcript[index])
			}
		}
	}
	m.toolHoverIndex = -1
}

func (m *appModel) clearToolFocus() {
	for index := range m.transcript {
		if m.transcript[index].toolFocused {
			m.transcript[index].toolFocused = false
			touchTranscriptEntry(&m.transcript[index])
		}
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

// anyToolGroupOpen reports whether any tool in the group has its per-tool
// detail expanded. Used to keep the group marker pointing down while a
// sub-entry stays open.
func anyToolGroupOpen(entries []transcriptEntry) bool {
	for _, entry := range entries {
		if entry.toolGroupOpen {
			return true
		}
	}
	return false
}

// toggleToolGroupSubEntry flips the per-tool detail expansion of the entry at
// index. The group must already be expanded; this only affects that one tool's
// result detail block.
func (m *appModel) toggleToolGroupSubEntry(index int) bool {
	if m == nil || index < 0 || index >= len(m.transcript) {
		return false
	}
	entry := &m.transcript[index]
	if !isToolTransaction(*entry) || toolEntryStatus(*entry) == "running" {
		return false
	}
	entry.toolGroupOpen = !entry.toolGroupOpen
	touchTranscriptEntry(entry)
	m.transcriptRenderCache = nil
	m.refreshViewportPreservingOffset()
	if m.toolInspectActive {
		m.ensureInspectedToolVisible()
	}
	return true
}

// toggleToolExpansion implements two-level expand/collapse for the Tools
// group. When the group is collapsed, clicking anywhere inside it expands the
// whole group (lists each tool's summary). When the group is expanded, the
// group header row collapses the whole group, and clicking a tool's own
// summary row expands or collapses just that tool's result detail.
func (m *appModel) toggleToolExpansion(index int, isHeaderRow bool) bool {
	if m == nil || index < 0 || index >= len(m.transcript) {
		return false
	}
	entry := &m.transcript[index]
	if !isToolTransaction(*entry) || toolEntryStatus(*entry) == "running" {
		return false
	}
	first, last := toolGroupRange(m.transcript, index)
	if first < 0 {
		first = index
	}
	groupExpanded := m.toolGroupExpanded
	if !toolGroupHasRunning(m.transcript, first, last) {
		groupExpanded = m.transcript[first].toolExpanded
	}
	if !groupExpanded {
		// Collapsed group: any click anywhere inside expands the whole group.
		entry = &m.transcript[first]
		entry.toolExpanded = !entry.toolExpanded
		entry.toolGroupOpen = false
		touchTranscriptEntry(entry)
	} else if isHeaderRow {
		// Expanded group, click on the group header row: collapse the group.
		entry = &m.transcript[first]
		entry.toolExpanded = !entry.toolExpanded
		entry.toolGroupOpen = false
		for i := first + 1; i <= last; i++ {
			m.transcript[i].toolGroupOpen = false
			touchTranscriptEntry(&m.transcript[i])
		}
		touchTranscriptEntry(entry)
	} else {
		// Expanded group, click on a tool's own summary/detail row: toggle just
		// that tool's result detail, leaving the group expanded.
		return m.toggleToolGroupSubEntry(index)
	}
	m.transcriptRenderCache = nil
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
	for _, location := range m.transcriptEntryLocationsAt() {
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
