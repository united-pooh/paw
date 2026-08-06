// 本文件定义聊天历史区的鼠标拖拽选择、边缘滚动和复制逻辑。
package bubble

import (
	"fmt"
	"os"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/atotto/clipboard"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// writeClipboard 写入系统剪贴板；测试中可以替换。
var writeClipboard = clipboard.WriteAll

// writeClipboardOSC52 通过 OSC 52 序列把文本写入终端剪贴板（覆盖 SSH/远程
// 以及不依赖 OS 剪贴板权限的场景），以 tea.Cmd 形式在渲染帧外输出；测试中可以替换。
var writeClipboardOSC52 = func(text string) tea.Cmd {
	return func() tea.Msg {
		_, _ = os.Stdout.WriteString(ansi.SetSystemClipboard(text))
		return nil
	}
}

// doubleClickInterval 是双击/三击判定窗口：两次左键按下间隔不超过该值且
// 位置基本重合时，依次进入选词 / 选行模式。单击动作（链接、todo、工具行）
// 也会延迟到该窗口结束后再执行，避免与双击冲突。
// 400ms 对齐 macOS 系统双击阈值（默认约 500ms）与 Alacritty 的默认值，
// 保证常见双击节奏都能落在窗口内；代价是单击动作最多延迟 400ms。
var doubleClickInterval = 400 * time.Millisecond

// clickSlopCells 是单击判定容差：按下与抬起（或按下期间的 motion）坐标差
// 不超过 1 格时仍视为单击。终端坐标按 cell 量化（transcriptContentColumn
// 是 x-padding），真实鼠标的 1px 抖动/漂移足以跨过 cell 边界，若按严格
// 相等判定，一次普通单击就会被误判为拖拽：触发复制、重置双击计数。
const clickSlopCells = 1

// selectionPointsWithinSlop 报告两个坐标是否在单击容差内（Chebyshev 距离
// <= clickSlopCells）。真实拖动一旦超出容差，立刻进入拖拽语义。
func selectionPointsWithinSlop(a, b selectionPoint) bool {
	rowDelta := a.row - b.row
	if rowDelta < 0 {
		rowDelta = -rowDelta
	}
	colDelta := a.col - b.col
	if colDelta < 0 {
		colDelta = -colDelta
	}
	return rowDelta <= clickSlopCells && colDelta <= clickSlopCells
}

// copyToastDuration 是复制反馈 toast 在状态栏的停留时长。
const copyToastDuration = 2 * time.Second

// scheduleCopyToastExpiry 在 toast 显示时长结束后派发清除消息；测试中可
// 替换为立即派发，避免 tea.Tick 的真实计时拖慢测试。
var scheduleCopyToastExpiry = func() tea.Cmd {
	return tea.Tick(copyToastDuration, func(time.Time) tea.Msg { return copyToastExpiredMsg{} })
}

// showCopyToast 在状态栏显示「已复制 N 字符」反馈，并返回到期清除消息。
func (m *appModel) showCopyToast(charCount int) tea.Cmd {
	m.copyToast = fmt.Sprintf("已复制 %d 字符", charCount)
	m.copyToastUntil = m.animationNow().Add(copyToastDuration)
	return scheduleCopyToastExpiry()
}

// scheduleClickAction 在双击窗口结束后派发一次延迟的单击动作；测试中可
// 替换为立即派发，避免 tea.Tick 的真实计时拖慢测试。
var scheduleClickAction = func(seq uint64, point selectionPoint) tea.Cmd {
	return tea.Tick(doubleClickInterval, func(time.Time) tea.Msg {
		return transcriptClickActionMsg{seq: seq, point: point}
	})
}

// transcriptLineSnapshot 保存一行 transcript 的样式文本、纯文本和显示宽度。
type transcriptLineSnapshot struct {
	styled          string
	plain           string
	width           int
	assistantMarker bool
}

// handleTranscriptMouse 处理 transcript 面板中的鼠标拖拽选择事件。
// 交互模型：
//   - 单击：按下即开始选区跟踪；释放时若未超出单击容差（clickSlopCells，
//     按下/抬起 1 格内的抖动与漂移都算单击），动作（链接 / todo / 工具行）
//     延迟到双击窗口结束后执行，保证双击/三击优先判定。
//   - 双击：按下瞬间建立「词」选区（wordBoundsAt 吸附），释放不复制。
//   - 三击：按下瞬间建立「行」选区，释放不复制。
//   - 拖拽（含从双击/三击继续拖动）：超出容差即按当前模式（字符/词/行）
//     扩展选区，释放时写入本地剪贴板并追加 OSC 52 双写。
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
		now := time.Now()
		hadSelection := m.selectionActive
		m.selecting = true
		m.selectionMoved = false
		m.selectionAnchor = point
		if now.Sub(m.lastClickAt) <= doubleClickInterval && sameClickPoint(m.lastClickPoint, point) {
			m.clickCount++
		} else {
			m.clickCount = 1
		}
		m.lastClickAt = now
		m.lastClickPoint = point
		switch m.clickCount {
		case 2:
			m.startWordSelection(point)
		case 3:
			m.startLineSelection(point)
		default:
			m.selectionMode = selectionModeChar
			m.selectionActive = false
			m.selectionStart = point
			m.selectionEnd = point
		}
		hoverIndex, _ := m.toolIndexAtTranscriptRow(point.row)
		hoverChanged := m.setToolHover(hoverIndex)
		if hadSelection || hoverChanged || m.clickCount >= 2 {
			// 双击/三击在按下瞬间建立了词/行选区：必须立刻刷新视口，
			// 否则选区高亮要等下一次鼠标移动/按键才出现，看起来像没反应。
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
		if point, ok := m.transcriptPointForMouse(msg.X, msg.Y); ok {
			if !selectionPointsWithinSlop(point, m.selectionAnchor) {
				// 真实拖动（超出单击容差）：拖到边缘时滚动，并打断单击/双击
				// 计数，避免拖拽起始按下被计入下一次双击。
				m.scrollTranscriptSelectionAtEdge(msg.Y)
				m.clickCount = 0
				m.lastClickAt = time.Time{}
				switch m.selectionMode {
				case selectionModeWord:
					m.extendWordSelection(point)
				case selectionModeLine:
					m.extendLineSelection(point)
				default:
					point.col++
					m.selectionEnd = point
					m.selectionActive = true
					m.selectionMoved = true
				}
			}
			// 容差内的 motion（按下期间手抖 1 格）不改选区、不打断双击计数。
		}
		m.refreshViewport()
		return m, true, nil
	case tea.MouseActionRelease:
		if !m.selecting {
			return m, false, nil
		}
		m.selecting = false
		if m.selectionMode == selectionModeChar {
			// 保留原行为：以释放点为准再修正一次选区终点；超出单击容差的
			// 位移才算拖拽（1 格内的按下/抬起漂移仍是单击，不复制）。
			if point, ok := m.transcriptPointForMouse(msg.X, msg.Y); ok && !selectionPointsWithinSlop(point, m.selectionAnchor) {
				point.col++
				m.selectionEnd = point
				m.selectionActive = true
				m.selectionMoved = true
			}
		}
		if m.selectionActive && m.selectionMoved {
			// 拖拽释放：复制（本地剪贴板 + OSC 52 双写），选区保留到下一次点击。
			m.resetClickTracking()
			if text := m.selectedTranscriptText(); text != "" {
				_ = writeClipboard(text)
				m.refreshViewport()
				return m, true, tea.Batch(
					writeClipboardOSC52(text),
					m.showCopyToast(utf8.RuneCountInString(text)),
				)
			}
			m.refreshViewport()
			return m, true, nil
		}
		// 单击（未拖动）：动作延迟到双击窗口结束后执行；双击/三击的选区已在
		// 按下时建立，释放不做额外动作。
		if m.clickCount <= 1 {
			m.clickActionPending = true
			m.clickActionSeq++
			seq := m.clickActionSeq
			point := m.selectionAnchor
			return m, true, scheduleClickAction(seq, point)
		}
		return m, true, nil
	default:
		return m, false, nil
	}
}

// performTranscriptClick 执行单击位置的“可点击位置”动作（链接 / todo / 工具行）。
// 由延迟消息触发；双击窗口内到达第二次按下时，seq 失配使消息被丢弃。
func (m appModel) performTranscriptClick(point selectionPoint) (appModel, bool, tea.Cmd) {
	if target := m.transcriptHyperlinkAtPoint(point); target != "" {
		m.refreshViewportPreservingOffset()
		return m, true, openTerminalURLCmd(target)
	}
	if m.toggleTodoAtTranscriptRow(point.row) {
		return m, true, nil
	}
	if index, header, ok := m.toolHitAtTranscriptRow(point.row); ok {
		if m.toolInspectActive {
			m.selectInspectedTool(index)
		}
		if !m.toggleToolExpansion(index, header) {
			m.refreshViewportPreservingOffset()
		}
		return m, true, nil
	}
	return m, false, nil
}

// resetClickTracking 在拖拽释放后清空单击/双击计数，使后续点击总是重新计数。
func (m *appModel) resetClickTracking() {
	m.clickCount = 0
	m.lastClickAt = time.Time{}
	m.clickActionPending = false
}

// sameClickPoint 判断两次按下是否落在同一个可点击位置（同行、允许 1 列抖动）。
func sameClickPoint(a, b selectionPoint) bool {
	colDelta := a.col - b.col
	if colDelta < 0 {
		colDelta = -colDelta
	}
	return a.row == b.row && colDelta <= 1
}

// startWordSelection 在双击按下时建立词选区：选中 point 所在行的完整“词”。
// 若点击处没有词（纯空白/标点），回退为字符模式。
func (m *appModel) startWordSelection(point selectionPoint) {
	lines := m.transcriptLineSnapshots()
	if point.row < 0 || point.row >= len(lines) {
		m.selectionMode = selectionModeChar
		m.selectionStart = point
		m.selectionEnd = point
		m.selectionActive = false
		return
	}
	left, right := wordBoundsAt(lines[point.row].plain, point.col)
	if right <= left {
		m.selectionMode = selectionModeChar
		m.selectionStart = point
		m.selectionEnd = point
		m.selectionActive = false
		return
	}
	m.selectionMode = selectionModeWord
	m.selectionStart = selectionPoint{row: point.row, col: left}
	m.selectionEnd = selectionPoint{row: point.row, col: right}
	m.selectionActive = true
}

// startLineSelection 在三击按下时建立整行选区。
func (m *appModel) startLineSelection(point selectionPoint) {
	lines := m.transcriptLineSnapshots()
	width := 0
	if point.row >= 0 && point.row < len(lines) {
		width = lines[point.row].width
	}
	m.selectionMode = selectionModeLine
	m.selectionStart = selectionPoint{row: point.row, col: 0}
	m.selectionEnd = selectionPoint{row: point.row, col: width}
	m.selectionActive = true
}

// extendWordSelection 按词边界扩展从双击按下点开始的选区：锚点词始终完整
// 包含，拖过词边界时把端点吸附到所在词的完整边界。
func (m *appModel) extendWordSelection(point selectionPoint) {
	lines := m.transcriptLineSnapshots()
	anchor := m.selectionAnchor
	aLeft, aRight := 0, 0
	if anchor.row >= 0 && anchor.row < len(lines) {
		aLeft, aRight = wordBoundsAt(lines[anchor.row].plain, anchor.col)
	}
	cLeft, cRight := 0, 0
	if point.row >= 0 && point.row < len(lines) {
		cLeft, cRight = wordBoundsAt(lines[point.row].plain, point.col)
	}
	if aRight <= aLeft || cRight <= cLeft {
		return
	}
	var start, end selectionPoint
	if compareSelectionPoints(point, anchor) >= 0 {
		start = selectionPoint{row: anchor.row, col: aLeft}
		end = selectionPoint{row: point.row, col: cRight}
	} else {
		start = selectionPoint{row: point.row, col: cLeft}
		end = selectionPoint{row: anchor.row, col: aRight}
	}
	if start != m.selectionStart || end != m.selectionEnd {
		m.selectionMoved = true
		m.selectionStart = start
		m.selectionEnd = end
	}
	m.selectionActive = true
}

// extendLineSelection 按整行扩展从三击按下点开始的选区。
func (m *appModel) extendLineSelection(point selectionPoint) {
	lines := m.transcriptLineSnapshots()
	anchor := m.selectionAnchor
	width := func(row int) int {
		if row >= 0 && row < len(lines) {
			return lines[row].width
		}
		return 0
	}
	var start, end selectionPoint
	if point.row >= anchor.row {
		start = selectionPoint{row: anchor.row, col: 0}
		end = selectionPoint{row: point.row, col: width(point.row)}
	} else {
		start = selectionPoint{row: point.row, col: 0}
		end = selectionPoint{row: anchor.row, col: width(anchor.row)}
	}
	if start != m.selectionStart || end != m.selectionEnd {
		m.selectionMoved = true
		m.selectionStart = start
		m.selectionEnd = end
	}
	m.selectionActive = true
}

// wordBoundsAt 返回 plain 文本中指定显示单元格所在“词”的显示单元格区间
// [left, right)。词 = 连续的 non-separator grapheme 序列；分隔符为空白、
// Unicode 标点与符号（中文/英文单词都按整体选中，标点单独断开）。
// 若单元格落在分隔符上，取左侧最近的词；没有则取第一个词。
func wordBoundsAt(plain string, cell int) (int, int) {
	type wordRun struct{ left, right int }
	var words []wordRun
	current := -1
	cellPos := 0
	for remaining := plain; remaining != ""; {
		cluster, clusterWidth := terminalFirstGraphemeCluster(remaining)
		remaining = remaining[len(cluster):]
		graphemeWidth := maxInt(1, clusterWidth)
		if isWordSeparator(cluster) {
			current = -1
		} else {
			if current < 0 {
				words = append(words, wordRun{left: cellPos})
				current = len(words) - 1
			}
			words[current].right = cellPos + graphemeWidth
		}
		cellPos += graphemeWidth
	}
	for _, word := range words {
		if cell >= word.left && cell < word.right {
			return word.left, word.right
		}
	}
	for i := len(words) - 1; i >= 0; i-- {
		if words[i].right <= cell {
			return words[i].left, words[i].right
		}
	}
	if len(words) > 0 {
		return words[0].left, words[0].right
	}
	return 0, 0
}

// isWordSeparator 判断一个 grapheme cluster 是否为词边界分隔符
// （空白、标点、符号；emoji 与组合字符按符号处理，不并入单词）。
func isWordSeparator(cluster string) bool {
	r, _ := utf8.DecodeRuneInString(cluster)
	return unicode.IsSpace(r) || unicode.IsPunct(r) || unicode.IsSymbol(r)
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

// isInputDockMouse 判断鼠标坐标是否落在底部输入 dock（输入框 + 队列面板）
// 区域内。布局行序固定为：header → transcript → status → input dock。
func (m appModel) isInputDockMouse(msg tea.MouseMsg) bool {
	if msg.Y < 1 {
		return false
	}
	layout := m.currentLayout()
	if msg.X < 0 || msg.X >= layout.frameWidth {
		return false
	}
	top := 1 + layout.headerHeight + layout.transcriptHeight + layout.statusHeight
	return msg.Y >= top && msg.Y < 1+layout.contentHeight
}

// transcriptPointForMouse 将鼠标屏幕坐标转换为 transcript 内容中的全局显示单元格坐标。
// Bubble Tea 的鼠标 X 是鼠标所在的 cell 坐标（0 基），直接作为 transcript 的
// 字符索引：点击哪个字符，选区就从哪个字符开始（不再左移一格，避免拖选时
// 多选按下点左侧一个字符/空格）。col 允许等于行宽（行尾后一格的虚拟 cell），
// 保证拖到行尾时最后一个字符（无论中文/英文）能被选中。
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
		col = minInt(col, width)
	} else {
		col = 0
	}
	// 选择结束点仍按 inclusive 语义处理（motion/release 时 +1 转排他）。
	return selectionPoint{row: line, col: col}, true
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
	return terminalHyperlinkAtPoint(content, normalizeTranscriptHyperlinkPoint(content, point))
}

// normalizeTranscriptHyperlinkPoint 将外部 assistant marker 的逻辑坐标
// 映射回实际渲染文本坐标。选择快照把 marker 视为 gutter，不计入正文；
// OSC 8 命中检测则遍历包含 marker 的原始渲染文本。
func normalizeTranscriptHyperlinkPoint(content string, point selectionPoint) selectionPoint {
	if point.row < 0 || point.col < 0 {
		return point
	}
	lines := strings.Split(content, "\n")
	if point.row < len(lines) && strings.HasPrefix(ansi.Strip(lines[point.row]), "✦ ") {
		point.col++
	}
	return point
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
		assistantMarker := strings.HasPrefix(plain, "✦ ")
		// The assistant marker is a visual gutter decoration, not transcript
		// body content. Keep the logical two-cell gutter in plain text for
		// hit testing while retaining the original styled line (marker +
		// markdown rich styling) for rendering, so selections do not strip
		// bold/code/link styles from the first line of assistant messages.
		if assistantMarker {
			plain = transcriptEntryGutter + strings.TrimPrefix(plain, "✦ ")
		}
		lines = append(lines, transcriptLineSnapshot{
			styled:          line,
			plain:           plain,
			width:           terminalCellWidth(plain),
			assistantMarker: assistantMarker,
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
		text := slicePlainCells(lines[row].plain, left, right)
		if row == start.row && lines[row].assistantMarker && left == 0 {
			// ✦ 是 transcript 外部 gutter 的视觉标记，不属于正文快照；
			// 选区从 gutter 开始时，复制内容仍保留该标记，避免用户看到的
			// 首行与剪贴板内容不一致。
			text = "✦ " + strings.TrimPrefix(text, transcriptEntryGutter)
		}
		selected = append(selected, text)
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
		right = end.col
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
	return prefix + applySelectionStyle(selected) + suffix
}

// selectionSGRPrefixes 返回当前选区样式的前缀 SGR 序列：full 为完整样式
// （背景 + 前景；16 色回退为反色 \x1b[7m），bgOnly 为仅背景部分（反色
// 模式为空字符串，背景由反色机制承担）。
func selectionSGRPrefixes() (full, bgOnly string) {
	full = sgrPrefixOf(selectedTranscriptLineStyle.Render(" "))
	bgOnly = sgrPrefixOf(lipgloss.NewStyle().
		Background(selectedTranscriptLineStyle.GetBackground()).
		Render(" "))
	return full, bgOnly
}

// sgrPrefixOf 提取 lipgloss Render 输出的前导 SGR 序列。lipgloss 的 Render
// 结果形如 "\x1b[<sgr>m \x1b[0m"，需要先去掉尾部的 reset，再去掉内容空格。
func sgrPrefixOf(rendered string) string {
	trimmed := strings.TrimSuffix(rendered, "\x1b[0m")
	trimmed = strings.TrimSuffix(trimmed, " ")
	return trimmed
}

// applySelectionStyle 将选区样式叠加到（可能含 markdown 富文本的）选中片段：
// 选区 SGR 置于片段开头；markdown 样式内部的每个 SGR reset 都会清掉选区
// 背景，因此每遇到 reset 就重新断言完整选区样式；markdown 自带的背景色
// （如行内代码）则在其后重新断言选区背景，保证选中区域底色连续统一，
// 同时保留 markdown 的前景颜色与加粗/斜体等属性。
func applySelectionStyle(fragment string) string {
	fullSGR, bgSGR := selectionSGRPrefixes()
	if fullSGR == "" {
		return fragment
	}
	parsed := parseStyledCellLine(fragment)
	var b strings.Builder
	b.Grow(len(fragment) + 16)
	b.WriteString(fullSGR)
	for index, atom := range parsed.atoms {
		if !atom.control {
			b.WriteString(atom.text)
			continue
		}
		b.WriteString(atom.text)
		last := index == len(parsed.atoms)-1
		switch {
		case sgrResetsState(atom.text):
			if !last {
				b.WriteString(fullSGR)
			}
		case bgSGR != "" && sgrSetsBackground(atom.text):
			b.WriteString(bgSGR)
		}
	}
	b.WriteString(ansi.ResetStyle)
	return b.String()
}

// sgrResetsState 报告 SGR 序列是否重置全部样式（空参数或含 0 参数）。
// 非 SGR 控制序列（如 OSC 8 超链接）返回 false，原样保留。
func sgrResetsState(sequence string) bool {
	params := sgrParams(sequence)
	if params == nil {
		return false
	}
	if len(params) == 0 {
		return true
	}
	for _, param := range params {
		if param == "" || param == "0" {
			return true
		}
	}
	return false
}

// sgrSetsBackground 报告 SGR 序列是否设置背景色（48 或 49 参数）。
func sgrSetsBackground(sequence string) bool {
	for _, param := range sgrParams(sequence) {
		if param == "48" || param == "49" {
			return true
		}
	}
	return false
}

// sgrParams 提取 \x1b[...m 的参数列表；非 SGR 控制序列返回 nil。
func sgrParams(sequence string) []string {
	if !strings.HasPrefix(sequence, "\x1b[") || !strings.HasSuffix(sequence, "m") {
		return nil
	}
	raw := sequence[2 : len(sequence)-1]
	if raw == "" {
		return []string{}
	}
	return strings.Split(raw, ";")
}
