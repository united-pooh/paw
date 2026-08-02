// 本文件定义聊天历史区的鼠标拖拽选择、边缘滚动和复制逻辑。
package bubble

import (
	"strings"

	"github.com/atotto/clipboard"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// writeClipboard 写入系统剪贴板；测试中可以替换。
var writeClipboard = clipboard.WriteAll

// transcriptLineSnapshot 保存一行 transcript 的样式文本、纯文本和显示宽度。
type transcriptLineSnapshot struct {
	styled string
	plain  string
	width  int
}

// handleTranscriptMouse 处理 transcript 面板中的鼠标拖拽选择事件。
func (m appModel) handleTranscriptMouse(msg tea.MouseMsg) (appModel, bool, tea.Cmd) {
	if msg.Button == tea.MouseButtonWheelUp || msg.Button == tea.MouseButtonWheelDown ||
		msg.Button == tea.MouseButtonWheelLeft || msg.Button == tea.MouseButtonWheelRight {
		return m, false, nil
	}

	switch msg.Action {
	case tea.MouseActionPress:
		if msg.Button != tea.MouseButtonLeft {
			return m, false, nil
		}
		point, ok := m.transcriptPointForMouse(msg.X, msg.Y)
		if !ok {
			return m, false, nil
		}
		hadSelection := m.selectionActive
		m.selecting = true
		m.selectionActive = false
		m.selectionStart = point
		m.selectionEnd = point
		hoverIndex, _ := m.toolIndexAtTranscriptRow(point.row)
		hoverChanged := m.setToolHover(hoverIndex)
		if hadSelection || hoverChanged {
			m.refreshViewportPreservingOffset()
		}
		return m, true, nil
	case tea.MouseActionMotion:
		if !m.selecting {
			hoverIndex := m.toolHoverIndexAtMouse(msg.X, msg.Y)
			changed := m.setToolHover(hoverIndex)
			if changed {
				m.refreshViewportPreservingOffset()
			}
			return m, changed || hoverIndex >= 0, nil
		}
		m.scrollTranscriptSelectionAtEdge(msg.Y)
		if point, ok := m.transcriptPointForMouse(msg.X, msg.Y); ok {
			m.selectionEnd = point
			if point != m.selectionStart {
				m.selectionActive = true
			}
		}
		m.refreshViewport()
		return m, true, nil
	case tea.MouseActionRelease:
		if !m.selecting {
			return m, false, nil
		}
		if point, ok := m.transcriptPointForMouse(msg.X, msg.Y); ok {
			m.selectionEnd = point
			if point != m.selectionStart {
				m.selectionActive = true
			}
		}
		m.selecting = false
		if m.selectionActive {
			if text := m.selectedTranscriptText(); text != "" {
				_ = writeClipboard(text)
			}
			m.refreshViewport()
		} else {
			if target := m.transcriptHyperlinkAtPoint(m.selectionStart); target != "" {
				m.refreshViewportPreservingOffset()
				return m, true, openTerminalURLCmd(target)
			}
			if m.toggleTodoAtTranscriptRow(m.selectionStart.row) {
				return m, true, nil
			}
			if index, header, ok := m.toolHitAtTranscriptRow(m.selectionStart.row); ok {
				if m.toolInspectActive {
					m.selectInspectedTool(index)
				}
				if !m.toggleToolExpansion(index, header) {
					m.refreshViewportPreservingOffset()
				}
			} else {
				m.refreshViewportPreservingOffset()
			}
		}
		return m, true, nil
	default:
		return m, false, nil
	}
}

func isMouseWheel(msg tea.MouseMsg) bool {
	switch msg.Button {
	case tea.MouseButtonWheelUp, tea.MouseButtonWheelDown, tea.MouseButtonWheelLeft, tea.MouseButtonWheelRight:
		return true
	}
	switch msg.Type {
	case tea.MouseWheelUp, tea.MouseWheelDown, tea.MouseWheelLeft, tea.MouseWheelRight:
		return true
	default:
		return false
	}
}

// isHorizontalMouseWheel reports the left/right wheel events produced by
// trackpad page-swipe gestures. The transcript has no horizontal scrolling,
// so these events must not reach the viewport or textarea path.
func isHorizontalMouseWheel(msg tea.MouseMsg) bool {
	switch msg.Button {
	case tea.MouseButtonWheelLeft, tea.MouseButtonWheelRight:
		return true
	}
	switch msg.Type {
	case tea.MouseWheelLeft, tea.MouseWheelRight:
		return true
	default:
		return false
	}
}

func (m appModel) isTranscriptViewportMouse(msg tea.MouseMsg) bool {
	if _, ok := m.transcriptContentRow(msg.Y); !ok {
		return false
	}
	if _, ok := m.transcriptContentColumn(msg.X); !ok {
		return false
	}
	return true
}

// transcriptPointForMouse 将鼠标屏幕坐标转换为 transcript 内容中的全局显示单元格坐标。
func (m appModel) transcriptPointForMouse(x, y int) (selectionPoint, bool) {
	row, ok := m.transcriptContentRow(y)
	if !ok {
		return selectionPoint{}, false
	}
	col, ok := m.transcriptContentColumn(x)
	if !ok {
		return selectionPoint{}, false
	}
	lines := m.transcriptLineSnapshots()
	if len(lines) == 0 {
		return selectionPoint{}, false
	}
	line := m.viewport.YOffset + row
	line = minInt(len(lines)-1, maxInt(0, line))
	if width := lines[line].width; width > 0 {
		col = minInt(col, width-1)
	} else {
		col = 0
	}
	return selectionPoint{row: line, col: maxInt(0, col)}, true
}

func (m appModel) toolHoverIndexAtMouse(x, y int) int {
	point, ok := m.transcriptPointForMouse(x, y)
	if !ok {
		return -1
	}
	index, _ := m.toolIndexAtTranscriptRow(point.row)
	return index
}

// transcriptContentRow 返回 y 坐标在 transcript 内容区内的行号。
func (m appModel) transcriptContentRow(y int) (int, bool) {
	row := y - m.transcriptScreenTop()
	if row < 0 || row >= m.viewport.Height {
		return 0, false
	}
	return row, true
}

func (m appModel) transcriptScreenTop() int {
	return 1 + m.currentLayout().headerHeight
}

// transcriptContentColumn 返回 x 坐标在 transcript 内容区内的显示列。
func (m appModel) transcriptContentColumn(x int) (int, bool) {
	// 主布局没有左侧外框，只保留 transcript 内边距。
	col := x - mainContentPadding
	if col < 0 || col >= m.viewport.Width {
		return 0, false
	}
	return col, true
}

// scrollTranscriptSelectionAtEdge 在拖拽到 transcript 顶部或底部时滚动 viewport。
func (m *appModel) scrollTranscriptSelectionAtEdge(y int) {
	row, ok := m.transcriptContentRow(y)
	if !ok {
		return
	}
	switch {
	case row <= 0:
		m.viewport.ScrollUp(1)
	case row >= maxInt(0, m.viewport.Height-1):
		m.viewport.ScrollDown(1)
	}
}

// transcriptContentLines 返回未应用选择高亮的 transcript 渲染行。
func (m appModel) transcriptContentLines() []string {
	snapshots := m.transcriptLineSnapshots()
	lines := make([]string, 0, len(snapshots))
	for _, line := range snapshots {
		lines = append(lines, line.styled)
	}
	return lines
}

// transcriptLineSnapshots 返回未应用选择高亮的 transcript 渲染行快照。
// 必须与 viewport 实际显示的内容同源（renderTranscriptContent 的组渲染器）：
// 否则一旦工具组展开或某个工具详情打开，非组渲染器的行数与屏幕显示的行数
// 分叉，鼠标 press/release 的行坐标就会错位，点击落在错误的工具行上。
func (m *appModel) transcriptLineSnapshots() []transcriptLineSnapshot {
	if m == nil {
		return nil
	}
	if m.transcriptLineCacheReady {
		return m.transcriptLineCache
	}
	content := m.transcriptRenderedContent
	if !m.transcriptContentCached {
		content = m.renderTranscriptContentAt(maxInt(20, m.viewport.Width), m.showThinking, m.animationNow())
		m.transcriptRenderedContent = content
		m.transcriptContentCached = true
	}
	m.transcriptLineCache = buildTranscriptLineSnapshots(content)
	m.transcriptLineCacheReady = true
	return m.transcriptLineCache
}

func (m *appModel) transcriptHyperlinkAtPoint(point selectionPoint) string {
	if m == nil {
		return ""
	}
	content := m.transcriptRenderedContent
	if !m.transcriptContentCached {
		content = m.renderTranscriptContentAt(maxInt(20, m.viewport.Width), m.showThinking, m.animationNow())
		m.transcriptRenderedContent = content
		m.transcriptContentCached = true
	}
	return terminalHyperlinkAtPoint(content, point)
}

// terminalHyperlinkAtPoint 返回渲染文本指定单元格上的 OSC 8 URL。
// activeTarget 跨换行保留，以支持被终端宽度折行的长链接。
func terminalHyperlinkAtPoint(content string, point selectionPoint) string {
	if content == "" || point.row < 0 || point.col < 0 {
		return ""
	}

	row := 0
	cell := 0
	activeTarget := ""
	for content != "" && row <= point.row {
		if target, consumed, ok := consumeTerminalHyperlinkSequence(content); ok {
			activeTarget = target
			content = content[consumed:]
			continue
		}
		if content[0] == '\x1b' {
			_, _, consumed, _ := ansi.DecodeSequence(content, ansi.NormalState, nil)
			if consumed > 0 {
				content = content[consumed:]
				continue
			}
		}

		cluster, width := terminalFirstGraphemeCluster(content)
		if cluster == "" {
			break
		}
		content = content[len(cluster):]
		if cluster == "\n" {
			row++
			cell = 0
			continue
		}

		graphemeWidth := maxInt(1, width)
		if row == point.row && point.col >= cell && point.col < cell+graphemeWidth {
			if isClickableTerminalURL(activeTarget) {
				return activeTarget
			}
			return ""
		}
		cell += graphemeWidth
	}
	return ""
}

func consumeTerminalHyperlinkSequence(text string) (target string, consumed int, ok bool) {
	const prefix = "\x1b]8;"
	if !strings.HasPrefix(text, prefix) {
		return "", 0, false
	}

	payloadStart := len(prefix)
	belOffset := strings.IndexByte(text[payloadStart:], '\a')
	stOffset := strings.Index(text[payloadStart:], "\x1b\\")
	payloadEnd := -1
	terminatorWidth := 0
	switch {
	case belOffset >= 0 && (stOffset < 0 || belOffset < stOffset):
		payloadEnd = payloadStart + belOffset
		terminatorWidth = 1
	case stOffset >= 0:
		payloadEnd = payloadStart + stOffset
		terminatorWidth = 2
	default:
		return "", 0, false
	}

	payload := text[payloadStart:payloadEnd]
	separator := strings.IndexByte(payload, ';')
	if separator < 0 {
		return "", 0, false
	}
	return payload[separator+1:], payloadEnd + terminatorWidth, true
}

// buildTranscriptLineSnapshots 将渲染文本拆成可按显示单元格寻址的行快照。
func buildTranscriptLineSnapshots(content string) []transcriptLineSnapshot {
	if content == "" {
		return nil
	}
	rawLines := strings.Split(content, "\n")
	lines := make([]transcriptLineSnapshot, 0, len(rawLines))
	for _, line := range rawLines {
		plain := ansi.Strip(line)
		lines = append(lines, transcriptLineSnapshot{
			styled: line,
			plain:  plain,
			width:  terminalCellWidth(plain),
		})
	}
	return lines
}

// selectedTranscriptText 返回当前选择范围中的纯文本内容。
func (m appModel) selectedTranscriptText() string {
	lines := m.transcriptLineSnapshots()
	start, end, ok := m.selectionBounds(len(lines))
	if !ok {
		return ""
	}
	selected := make([]string, 0, end.row-start.row+1)
	for row := start.row; row <= end.row; row++ {
		left, right, ok := selectionCellsForLine(lines[row], row, start, end)
		if !ok {
			selected = append(selected, "")
			continue
		}
		selected = append(selected, slicePlainCells(lines[row].plain, left, right))
	}
	return strings.TrimRight(strings.Join(selected, "\n"), "\n")
}

// renderTranscriptSelection 给当前选择范围内的 transcript 行加高亮。
func (m appModel) renderTranscriptSelection(content string) string {
	snapshots := buildTranscriptLineSnapshots(content)
	start, end, ok := m.selectionBounds(len(snapshots))
	if !ok {
		return content
	}
	lines := make([]string, 0, len(snapshots))
	for row, snapshot := range snapshots {
		if row < start.row || row > end.row {
			lines = append(lines, snapshot.styled)
			continue
		}
		left, right, ok := selectionCellsForLine(snapshot, row, start, end)
		if !ok {
			lines = append(lines, snapshot.styled)
			continue
		}
		lines = append(lines, renderSelectedLineFragment(snapshot.styled, left, right))
	}
	return strings.Join(lines, "\n")
}

// selectionBounds 返回经过排序和边界修正后的选择范围。
func (m appModel) selectionBounds(lineCount int) (selectionPoint, selectionPoint, bool) {
	if !m.selectionActive || lineCount == 0 {
		return selectionPoint{}, selectionPoint{}, false
	}
	start := m.selectionStart
	end := m.selectionEnd
	if compareSelectionPoints(start, end) > 0 {
		start, end = end, start
	}
	start.row = minInt(maxInt(0, start.row), lineCount-1)
	end.row = minInt(maxInt(0, end.row), lineCount-1)
	start.col = maxInt(0, start.col)
	end.col = maxInt(0, end.col)
	return start, end, compareSelectionPoints(start, end) <= 0
}

// compareSelectionPoints 按阅读顺序比较两个选区坐标。
func compareSelectionPoints(a, b selectionPoint) int {
	if a.row < b.row {
		return -1
	}
	if a.row > b.row {
		return 1
	}
	if a.col < b.col {
		return -1
	}
	if a.col > b.col {
		return 1
	}
	return 0
}

// selectionCellsForLine 返回某一行被选中的显示单元格范围，right 为开区间。
func selectionCellsForLine(line transcriptLineSnapshot, row int, start, end selectionPoint) (int, int, bool) {
	if line.width == 0 {
		return 0, 0, false
	}
	left := 0
	right := line.width
	if row == start.row {
		left = start.col
	}
	if row == end.row {
		right = end.col + 1
	}
	return snapCellRangeToGraphemes(line.plain, line.width, left, right)
}

// snapCellRangeToGraphemes 将显示单元格范围扩展到完整 grapheme，避免切开中文或 emoji。
func snapCellRangeToGraphemes(text string, width, left, right int) (int, int, bool) {
	left = minInt(maxInt(0, left), width)
	right = minInt(maxInt(0, right), width)
	if right <= left {
		return 0, 0, false
	}
	cell := 0
	snappedLeft := -1
	snappedRight := 0
	for remaining := text; remaining != ""; {
		cluster, clusterWidth := terminalFirstGraphemeCluster(remaining)
		remaining = remaining[len(cluster):]
		graphemeWidth := maxInt(1, clusterWidth)
		graphemeStart := cell
		graphemeEnd := cell + graphemeWidth
		if graphemeEnd > left && graphemeStart < right {
			if snappedLeft < 0 {
				snappedLeft = graphemeStart
			}
			snappedRight = graphemeEnd
		}
		cell = graphemeEnd
	}
	if snappedLeft < 0 {
		return 0, 0, false
	}
	return snappedLeft, snappedRight, true
}

// slicePlainCells 按显示单元格范围截取纯文本，返回完整 grapheme。
func slicePlainCells(text string, left, right int) string {
	cell := 0
	var selected strings.Builder
	for remaining := text; remaining != ""; {
		cluster, clusterWidth := terminalFirstGraphemeCluster(remaining)
		remaining = remaining[len(cluster):]
		graphemeWidth := maxInt(1, clusterWidth)
		graphemeStart := cell
		graphemeEnd := cell + graphemeWidth
		if graphemeEnd > left && graphemeStart < right {
			selected.WriteString(cluster)
		}
		cell = graphemeEnd
		if cell >= right {
			break
		}
	}
	return selected.String()
}

// renderSelectedLineFragment 只给一行中的选中片段添加选区样式。
func renderSelectedLineFragment(line string, left, right int) string {
	prefix := cutStyledCellsExact(line, 0, left)
	selected := cutStyledCellsExact(line, left, right)
	suffix := cutStyledCellsExact(line, right, terminalCellWidth(line))
	if selected == "" {
		return line
	}
	return prefix + selectedTranscriptLineStyle.Render(selected) + suffix
}
