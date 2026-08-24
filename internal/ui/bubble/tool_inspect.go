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
//
// 位置直接由增量渲染的行区间缓存（transcriptEntrySpans）构建，与 viewport
// 显示逐行一致，不再重复渲染条目。
func (m *appModel) transcriptEntryLocationsAt() []transcriptEntryLocation {
	if m == nil {
		return nil
	}
	if m.transcriptLocationsReady {
		return m.transcriptLocationCache
	}
	m.ensureTranscriptLinesAt(maxInt(20, m.viewport.Width), m.showThinking, m.animationNow())
	m.transcriptLocationCache = m.transcriptEntryLocationsFromSpans()
	m.transcriptLocationsReady = true
	return m.transcriptLocationCache
}

func (m *appModel) transcriptEntryLocationsFromSpans() []transcriptEntryLocation {
	locations := make([]transcriptEntryLocation, 0, len(m.transcriptEntrySpans))
	for index, span := range m.transcriptEntrySpans {
		if span.startRow < 0 || span.height <= 0 {
			continue
		}
		locations = append(locations, transcriptEntryLocation{
			transcriptIndex: index,
			startRow:        span.startRow,
			height:          span.height,
		})
	}
	return locations
}

// transcriptEntryLocations is the free-function form used by tests; pending
// tools stay itemized, while ready groups count as expanded only when their
// first entry's group toggle is open.
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
		if toolEntryUsesReadyGroup(entries, index) {
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
			rendered = renderEntryAt(entry, width, at, showThinking)
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

// toolIndexAtTranscriptRow maps a rendered transcript row back to the tool
// entry that owns it. Pending tools resolve directly to the individual entry;
// ready groups resolve to the group header or the owning grouped entry.
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
	if m.transcriptInteraction.valid {
		interactionRow, found := m.transcriptInteraction.rowAt(row)
		if !found || interactionRow.toolIndex < 0 || interactionRow.toolIndex >= len(m.viewEntries) {
			return -1, false, false
		}
		return interactionRow.toolIndex, interactionRow.header, true
	}
	for _, location := range m.transcriptEntryLocationsAt() {
		if row < location.startRow || row >= location.startRow+location.height {
			continue
		}
		entry := m.viewEntries[location.transcriptIndex]
		if !isToolTransaction(entry) {
			return -1, false, false
		}
		if !toolEntryUsesReadyGroup(m.viewEntries, location.transcriptIndex) {
			return location.transcriptIndex, false, true
		}
		first, last := toolGroupRange(m.viewEntries, location.transcriptIndex)
		groupExpanded := m.toolGroupExpanded
		if !toolGroupHasRunning(m.viewEntries, first, last) {
			groupExpanded = m.viewEntries[first].toolExpanded
		}
		if row == location.startRow {
			return first, true, true
		}
		if !groupExpanded {
			return first, false, true
		}
		entryIndex, _ := m.toolDetailEntryAtRow(row, location)
		return entryIndex, false, true
	}
	return -1, false, false
}

// reasoningHitAtTranscriptRow resolves a rendered transcript row to a
// completed, non-redacted reasoning entry. Only completed reasoning headers
// are clickable (live reasoning is streaming and cannot be folded).
func (m appModel) reasoningHitAtTranscriptRow(row int) (int, bool) {
	for _, location := range m.transcriptEntryLocationsAt() {
		if row < location.startRow || row >= location.startRow+location.height {
			continue
		}
		entry := m.viewEntries[location.transcriptIndex]
		if entry.kind == entryReasoning && entry.reasoningFinishedAt != nil && !entry.redacted {
			return location.transcriptIndex, true
		}
	}
	return -1, false
}

// toggleReasoningExpansion flips the per-entry expansion override of a
// completed reasoning entry. Once set, the override wins over the global
// Ctrl+O toggle for that single entry.
func (m *appModel) toggleReasoningExpansion(index int) bool {
	if m == nil || index < 0 || index >= len(m.viewEntries) {
		return false
	}
	transcriptIndex, ok := m.transcriptIndexForViewEntry(index)
	if !ok {
		return false
	}
	entry := &m.transcript[transcriptIndex]
	if entry.kind != entryReasoning || entry.reasoningFinishedAt == nil || entry.redacted {
		return false
	}
	entry.reasoningExpansionSet = true
	entry.reasoningExpanded = !entry.reasoningExpanded
	m.touchTranscriptEntryAt(transcriptIndex)
	m.refreshViewportPreservingOffset()
	return true
}

// toolDetailEntryAtRow resolves which single tool entry owns the given row
// inside an expanded, completed Tools group. The group header row and every
// tool summary row belong to that tool; rows inside a tool's result detail
// block belong to that tool. The first tool owns its summary row plus its
// detail block (or the remaining header rows).
func (m *appModel) toolDetailEntryAtRow(row int, location transcriptEntryLocation) (int, bool) {
	if m == nil || location.transcriptIndex < 0 || location.transcriptIndex >= len(m.viewEntries) {
		return location.transcriptIndex, true
	}
	groupEntries := toolEntriesForGroup(m.viewEntries, location.transcriptIndex)
	if len(groupEntries) == 0 {
		return location.transcriptIndex, true
	}
	style := toolGroupBorderStyle(groupEntries)
	contentWidth := toolEntryContentWidth(maxInt(20, m.viewport.Width), style)
	innerWidth := maxInt(1, contentWidth-style.GetHorizontalFrameSize())
	cursor := location.startRow + 1
	for i, entry := range groupEntries {
		entryHeight := maxInt(1, lipgloss.Height(renderGroupedToolEntry(entry, innerWidth, m.animationNow(), m.toolGroupFullResult)))
		end := cursor + entryHeight
		if row < end {
			return m.transcriptIndexForGroupEntry(location.transcriptIndex, i), true
		}
		cursor = end
	}
	return location.transcriptIndex, true
}

// transcriptIndexForGroupEntry returns the absolute view index of the
// i-th tool entry inside the group that starts at groupStart.
func (m *appModel) transcriptIndexForGroupEntry(groupStart, i int) int {
	count := 0
	for index := groupStart; index < len(m.viewEntries); index++ {
		if m.viewEntries[index].kind == entryUser {
			break
		}
		if !isToolTransaction(m.viewEntries[index]) {
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
	for i := len(m.viewEntries) - 1; i >= 0 && index < 0; i-- {
		entry := m.viewEntries[i]
		if isToolTransaction(entry) {
			index = i
			break
		}
		// 折叠段透视：找到段内最后一个工具子条目时自动展开该段，
		// 让检查模式落在内联后的可见工具行上。
		if entry.kind == entryWorkSegment && entry.segment != nil {
			for j := len(entry.segment.children) - 1; j >= 0; j-- {
				if !isToolTransaction(entry.segment.children[j]) {
					continue
				}
				m.setWorkSegmentExpanded(entry.segment, true)
				m.recomputeViewEntries()
				for k := len(m.viewEntries) - 1; k >= 0; k-- {
					if isToolTransaction(m.viewEntries[k]) {
						index = k
						break
					}
				}
				break
			}
		}
	}
	if index < 0 {
		return *m, nil
	}
	transcriptIndex, ok := m.transcriptIndexForViewEntry(index)
	if !ok {
		return *m, nil
	}
	m.clearToolFocus()
	m.toolInspectActive = true
	m.toolInspectIndex = index
	m.transcript[transcriptIndex].toolFocused = true
	m.touchTranscriptEntryAt(transcriptIndex)
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
		if m.toolInspectIndex >= 0 && m.toolInspectIndex < len(m.viewEntries) {
			entry := m.viewEntries[m.toolInspectIndex]
			if isToolTransaction(entry) && toolEntryStatus(entry) != "running" {
				m.toolGroupExpanded = true
				m.toolGroupFullResult = !m.toolGroupFullResult
				m.touchToolGroupRender()
			}
		}
	}
	return *m, nil
}

func (m *appModel) touchToolGroupRender() {
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
	if index < 0 || index >= len(m.viewEntries) || !isToolTransaction(m.viewEntries[index]) {
		index = -1
	}
	if m.toolHoverIndex == index {
		return false
	}
	m.toolHoverIndex = index
	m.rebuildTranscriptHoverPatch()
	return true
}

func (m *appModel) clearToolHover() {
	if m == nil {
		return
	}
	if m.toolHoverIndex < 0 {
		return
	}
	m.toolHoverIndex = -1
	m.transcriptHoverPatch = transcriptHoverPatchCache{}
}

func (m *appModel) clearToolFocus() {
	for index := range m.transcript {
		if m.transcript[index].toolFocused {
			m.transcript[index].toolFocused = false
			m.touchTranscriptEntryAt(index)
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
		if next < 0 || next >= len(m.viewEntries) {
			return
		}
		if isToolTransaction(m.viewEntries[next]) {
			break
		}
	}
	transcriptIndex, ok := m.transcriptIndexForViewEntry(next)
	if !ok {
		return
	}
	m.clearToolFocus()
	m.toolInspectIndex = next
	m.transcript[transcriptIndex].toolFocused = true
	m.touchTranscriptEntryAt(transcriptIndex)
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
	if m == nil || index < 0 || index >= len(m.viewEntries) {
		return false
	}
	transcriptIndex, ok := m.transcriptIndexForViewEntry(index)
	if !ok {
		return false
	}
	entry := &m.transcript[transcriptIndex]
	if !isToolTransaction(*entry) || toolEntryStatus(*entry) == "running" {
		return false
	}
	entry.toolGroupOpen = !entry.toolGroupOpen
	m.touchTranscriptEntryAt(transcriptIndex)
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
	if m == nil || index < 0 || index >= len(m.viewEntries) {
		return false
	}
	viewEntry := m.viewEntries[index]
	if !isToolTransaction(viewEntry) || toolEntryStatus(viewEntry) == "running" {
		return false
	}
	transcriptIndexFor := func(viewIndex int) (int, bool) {
		return m.transcriptIndexForViewEntry(viewIndex)
	}
	if !toolEntryUsesReadyGroup(m.viewEntries, index) {
		transcriptIndex, ok := transcriptIndexFor(index)
		if !ok {
			return false
		}
		entry := &m.transcript[transcriptIndex]
		entry.toolExpanded = !entry.toolExpanded
		entry.toolGroupOpen = false
		m.touchTranscriptEntryAt(transcriptIndex)
		m.refreshViewportPreservingOffset()
		if m.toolInspectActive {
			m.ensureInspectedToolVisible()
		}
		return true
	}
	first, last := toolGroupRange(m.viewEntries, index)
	if first < 0 {
		first = index
	}
	if toolGroupHasRunning(m.viewEntries, first, last) {
		return false
	}
	groupExpanded := m.toolGroupExpanded
	if !toolGroupHasRunning(m.viewEntries, first, last) {
		groupExpanded = m.viewEntries[first].toolExpanded
	}
	firstTranscriptIndex, ok := transcriptIndexFor(first)
	if !ok {
		return false
	}
	if !groupExpanded {
		// Collapsed group: any click anywhere inside expands the whole group.
		entry := &m.transcript[firstTranscriptIndex]
		entry.toolExpanded = !entry.toolExpanded
		entry.toolGroupOpen = false
		m.touchTranscriptEntryAt(firstTranscriptIndex)
	} else if isHeaderRow {
		// Expanded group, click on the group header row: collapse the group.
		entry := &m.transcript[firstTranscriptIndex]
		entry.toolExpanded = !entry.toolExpanded
		entry.toolGroupOpen = false
		for i := first + 1; i <= last; i++ {
			if childIndex, childOK := transcriptIndexFor(i); childOK {
				m.transcript[childIndex].toolGroupOpen = false
				m.touchTranscriptEntryAt(childIndex)
			}
		}
		m.touchTranscriptEntryAt(firstTranscriptIndex)
	} else {
		// Expanded group, click on a tool's own summary/detail row: toggle just
		// that tool's result detail, leaving the group expanded.
		return m.toggleToolGroupSubEntry(index)
	}
	m.refreshViewportPreservingOffset()
	if m.toolInspectActive {
		m.ensureInspectedToolVisible()
	}
	return true
}

func (m *appModel) selectInspectedTool(index int) {
	if m == nil || !m.toolInspectActive || index < 0 || index >= len(m.viewEntries) {
		return
	}
	transcriptIndex, ok := m.transcriptIndexForViewEntry(index)
	if !ok {
		return
	}
	m.clearToolFocus()
	m.toolInspectIndex = index
	m.transcript[transcriptIndex].toolFocused = true
	m.touchTranscriptEntryAt(transcriptIndex)
}

func (m *appModel) ensureInspectedToolVisible() {
	if m == nil || !m.toolInspectActive || m.viewport.Height <= 0 {
		return
	}
	beforeOffset := m.viewport.YOffset
	targetIndex := m.toolInspectIndex
	if targetIndex >= 0 && targetIndex < len(m.viewEntries) && toolEntryUsesReadyGroup(m.viewEntries, targetIndex) {
		targetIndex, _ = toolGroupRange(m.viewEntries, targetIndex)
	}
	for _, location := range m.transcriptEntryLocationsAt() {
		if location.transcriptIndex != targetIndex {
			continue
		}
		switch {
		case location.startRow < m.viewport.YOffset:
			m.viewport.SetYOffset(location.startRow)
		case location.startRow >= m.viewport.YOffset+m.viewport.Height:
			m.viewport.SetYOffset(maxInt(0, location.startRow-m.viewport.Height+1))
		}
		if m.viewport.YOffset != beforeOffset {
			m.clearToolHover()
		}
		return
	}
}
