package bubble

import "strings"

type transcriptToolRenderRow struct {
	toolOffset   int
	header       bool
	hoverVisible bool
}

type transcriptToolRenderRows []transcriptToolRenderRow

type transcriptInteractionRow struct {
	width        int
	toolIndex    int
	header       bool
	hoverVisible bool
}

type transcriptToolInteraction struct {
	patchStart   int
	patchHeight  int
	groupStart   int
	hoverVisible bool
}

type transcriptInteractionIndex struct {
	rows  []transcriptInteractionRow
	tools []transcriptToolInteraction
	valid bool
}

func (index *transcriptInteractionIndex) set(rows []transcriptInteractionRow, tools []transcriptToolInteraction, valid bool) {
	if index == nil {
		return
	}
	index.rows = rows
	index.tools = tools
	index.valid = valid
}

func (index *transcriptInteractionIndex) replace(start int, rows []transcriptInteractionRow, totalRows int, startIdx int, tools []transcriptToolInteraction, totalTools int, valid bool) {
	if index == nil {
		return
	}
	if start < 0 || start > len(index.rows) {
		index.set(nil, nil, false)
		return
	}
	if startIdx < 0 || startIdx > len(index.tools) {
		index.set(nil, nil, false)
		return
	}
	index.rows = append(index.rows[:start], rows...)
	index.tools = append(index.tools[:startIdx], tools...)
	index.valid = valid && len(index.rows) == totalRows && len(index.tools) == totalTools
}

func (index *transcriptInteractionIndex) invalidate() {
	if index == nil {
		return
	}
	index.valid = false
}

func (index *transcriptInteractionIndex) rowAt(row int) (transcriptInteractionRow, bool) {
	if index == nil || !index.valid || row < 0 || row >= len(index.rows) {
		return transcriptInteractionRow{}, false
	}
	return index.rows[row], true
}

func (index *transcriptInteractionIndex) toolAt(toolIndex int) (transcriptToolInteraction, bool) {
	if index == nil || !index.valid || toolIndex < 0 || toolIndex >= len(index.tools) {
		return transcriptToolInteraction{}, false
	}
	tool := index.tools[toolIndex]
	return tool, tool.patchStart >= 0 && tool.patchHeight > 0
}

func (m *appModel) buildTranscriptInteractionRows(startRow int, lines []string, startIdx int) ([]transcriptInteractionRow, []transcriptToolInteraction, bool) {
	if m != nil {
		m.transcriptInteractionVisits = 0
		m.transcriptInteractionRangeCalls = 0
	}
	rows := make([]transcriptInteractionRow, len(lines))
	for index, line := range lines {
		rows[index] = transcriptInteractionRow{
			width:     terminalCellWidth(line),
			toolIndex: -1,
		}
	}
	toolCount := 0
	if m != nil && startIdx >= 0 && startIdx <= len(m.transcript) {
		toolCount = len(m.transcript) - startIdx
	}
	tools := make([]transcriptToolInteraction, toolCount)
	for index := range tools {
		tools[index].patchStart = -1
		tools[index].groupStart = -1
	}
	if m == nil || len(rows) == 0 {
		return rows, tools, m != nil
	}
	endRow := startRow + len(rows)
	valid := true
	pendingToolSegment := false
	if startIdx >= 0 && startIdx < len(m.transcript) && isToolTransaction(m.transcript[startIdx]) {
		if startIdx > 0 && isToolTransaction(m.transcript[startIdx-1]) {
			m.transcriptInteractionRangeCalls++
			if first, ok := toolSegmentStart(m.transcript, startIdx); ok {
				pendingToolSegment = m.transcript[first].toolGroupPending
			}
		} else {
			pendingToolSegment = m.transcript[startIdx].toolGroupPending
		}
	}
	for index := maxInt(0, startIdx); index < len(m.transcript); index++ {
		m.transcriptInteractionVisits++
		if index >= len(m.transcriptEntrySpans) {
			valid = false
			break
		}
		span := m.transcriptEntrySpans[index]
		if span.startRow < 0 || span.height <= 0 {
			continue
		}
		if span.startRow >= endRow {
			break
		}
		if span.startRow+span.height <= startRow || !isToolTransaction(m.transcript[index]) {
			continue
		}
		if index > startIdx && !isToolTransaction(m.transcript[index-1]) {
			pendingToolSegment = m.transcript[index].toolGroupPending
		}
		if !pendingToolSegment {
			last := toolSegmentEnd(m.transcript, index)
			cachedToolRows := m.transcriptRenderCache[index].toolRows
			if cachedToolRows == nil {
				valid = false
				continue
			}
			toolRows := *cachedToolRows
			if len(toolRows) != span.height {
				valid = false
				continue
			}
			for relativeRow, toolRow := range toolRows {
				globalRow := span.startRow + relativeRow
				if globalRow < startRow || globalRow >= endRow {
					continue
				}
				row := &rows[globalRow-startRow]
				row.toolIndex = index + toolRow.toolOffset
				row.header = toolRow.header
				row.hoverVisible = toolRow.hoverVisible
				owner := index + toolRow.toolOffset
				if owner >= startIdx && owner-startIdx < len(tools) {
					tools[owner-startIdx] = transcriptToolInteraction{
						patchStart:   span.startRow,
						patchHeight:  span.height,
						groupStart:   index,
						hoverVisible: toolRow.hoverVisible,
					}
				}
			}
			index = last
			continue
		}
		from := maxInt(span.startRow, startRow)
		to := minInt(span.startRow+span.height, endRow)
		for globalRow := from; globalRow < to; globalRow++ {
			row := &rows[globalRow-startRow]
			row.toolIndex = index
			row.hoverVisible = true
		}
		if index >= startIdx && index-startIdx < len(tools) {
			tools[index-startIdx] = transcriptToolInteraction{
				patchStart:   span.startRow,
				patchHeight:  span.height,
				groupStart:   -1,
				hoverVisible: true,
			}
		}
	}
	return rows, tools, valid
}

func (m *appModel) setTranscriptInteractionRows(lines []string, startIdx int) {
	if m == nil {
		return
	}
	rows, tools, valid := m.buildTranscriptInteractionRows(0, lines, startIdx)
	m.transcriptInteraction.set(rows, tools, valid && len(rows) == len(m.transcriptLines) && len(tools) == len(m.transcript))
	m.rebuildTranscriptHoverPatch()
}

func (m *appModel) replaceTranscriptInteractionRows(startRow int, lines []string, startIdx int) {
	if m == nil {
		return
	}
	previousPatch := m.transcriptHoverPatch
	rows, tools, valid := m.buildTranscriptInteractionRows(startRow, lines, startIdx)
	m.transcriptInteraction.replace(startRow, rows, len(m.transcriptLines), startIdx, tools, len(m.transcript), valid)
	if previousPatch.valid {
		if unit, ok := m.currentTranscriptHoverPatchUnit(); ok && previousPatch.unit == unit &&
			unit.toolIndex < startIdx && unit.start+unit.height <= startRow {
			return
		}
	}
	m.rebuildTranscriptHoverPatch()
}

type transcriptHoverPatchUnit struct {
	start       int
	height      int
	groupStart  int
	toolIndex   int
	hoverActive bool
}

type transcriptHoverPatchCache struct {
	unit  transcriptHoverPatchUnit
	lines []string
	valid bool
}

func (m appModel) currentTranscriptHoverPatchUnit() (transcriptHoverPatchUnit, bool) {
	if m.toolHoverIndex < 0 || !m.transcriptInteraction.valid || m.toolHoverIndex >= len(m.transcript) || m.transcript[m.toolHoverIndex].toolFocused {
		return transcriptHoverPatchUnit{}, false
	}
	interaction, ok := m.transcriptInteraction.toolAt(m.toolHoverIndex)
	if !ok || !interaction.hoverVisible {
		return transcriptHoverPatchUnit{}, false
	}
	return transcriptHoverPatchUnit{
		start:       interaction.patchStart,
		height:      interaction.patchHeight,
		groupStart:  interaction.groupStart,
		toolIndex:   m.toolHoverIndex,
		hoverActive: true,
	}, true
}

func (m *appModel) rebuildTranscriptHoverPatch() {
	if m == nil {
		return
	}
	m.transcriptHoverPatch = transcriptHoverPatchCache{}
	unit, ok := m.currentTranscriptHoverPatchUnit()
	if !ok {
		return
	}
	lines, valid := m.renderTranscriptHoverPatch(unit)
	m.transcriptHoverPatch = transcriptHoverPatchCache{unit: unit, lines: lines, valid: valid}
}

func overlayTranscriptViewport(content string, patch []string, valid bool, unit transcriptHoverPatchUnit, yOffset, width int) string {
	if !valid || len(patch) != unit.height {
		return content
	}
	visible := strings.Split(content, "\n")
	first := maxInt(unit.start, yOffset)
	last := minInt(unit.start+unit.height, yOffset+len(visible))
	for row := first; row < last; row++ {
		visible[row-yOffset] = fitStyledCellLine(patch[row-unit.start], width)
	}
	return strings.Join(visible, "\n")
}

func (m appModel) renderTranscriptHoverPatch(unit transcriptHoverPatchUnit) ([]string, bool) {
	if m.transcriptHoverPatchRenderSpy != nil {
		*m.transcriptHoverPatchRenderSpy++
	}
	if unit.start < 0 || unit.height <= 0 {
		return nil, false
	}
	width := maxInt(20, m.viewport.Width)
	at := m.animationNow()
	var rendered string
	if unit.groupStart >= 0 {
		if unit.groupStart >= len(m.transcript) {
			return nil, false
		}
		first, last := toolGroupRange(m.transcript, unit.groupStart)
		entries := toolEntriesForGroup(m.transcript, first)
		expanded := m.toolGroupExpanded
		if !toolGroupHasRunning(m.transcript, first, last) {
			expanded = m.transcript[first].toolExpanded
		}
		hoveredOffset := -1
		if unit.hoverActive && m.toolHoverIndex >= first && m.toolHoverIndex <= last {
			hoveredOffset = m.toolHoverIndex - first
		}
		rendered, _ = renderToolsGroupWithHoverRows(entries, width, at, expanded, m.toolGroupFullResult, hoveredOffset)
	} else {
		if unit.toolIndex < 0 || unit.toolIndex >= len(m.transcript) {
			return nil, false
		}
		entry := m.transcript[unit.toolIndex]
		body := renderToolTransactionEntryWithHover(entry, transcriptBodyWidth(width), at, unit.hoverActive)
		if body != "" {
			rendered = indentLines(body, transcriptEntryGutter)
		}
	}
	rendered = strings.TrimRight(rendered, "\n")
	if rendered == "" {
		return nil, false
	}
	return strings.Split(rendered, "\n"), true
}
