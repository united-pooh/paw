package viewportx

import (
	"math"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// New returns a new model with the given width and height as well as default
// key mappings.
func New(width, height int) (m Model) {
	m.Width = width
	m.Height = height
	m.setInitialValues()
	return m
}

// Model is the Bubble Tea model for this viewport element.
type Model struct {
	Width  int
	Height int
	KeyMap KeyMap

	// Whether or not to respond to the mouse. The mouse must be enabled in
	// Bubble Tea for this to work. For details, see the Bubble Tea docs.
	MouseWheelEnabled bool

	// The number of lines the mouse wheel will scroll. By default, this is 3.
	MouseWheelDelta int

	// YOffset is the vertical scroll position.
	YOffset int

	// xOffset is the horizontal scroll position.
	xOffset int

	// horizontalStep is the number of columns we move left or right during a
	// default horizontal scroll.
	horizontalStep int

	// YPosition is the position of the viewport in relation to the terminal
	// window. It's used in high performance rendering only.
	YPosition int

	// Style applies a lipgloss style to the viewport. Realistically, it's most
	// useful for setting borders, margins and padding.
	Style lipgloss.Style

	// HighPerformanceRendering bypasses the normal Bubble Tea renderer to
	// provide higher performance rendering. Most of the time the normal Bubble
	// Tea rendering methods will suffice, but if you're passing content with
	// a lot of ANSI escape codes you may see improved rendering in certain
	// terminals with this enabled.
	//
	// This should only be used in program occupying the entire terminal,
	// which is usually via the alternate screen buffer.
	//
	// Deprecated: high performance rendering is now deprecated in Bubble Tea.
	HighPerformanceRendering bool

	initialized        bool
	lines              []string
	lineWidths         []int
	linePrefixMax      []int
	longestLineWidth   int
	replaceWidthVisits int

	// linesVersion 在内容变更（SetContent/SetLines/ReplaceLines）时递增，
// 作为 View 渲染缓存的内容指纹。
	linesVersion uint64

	// cache 指向跨拷贝共享的渲染缓存。View 是值接收者，只有通过指针
// 写入的缓存才能在下一次 View 调用（从 bubbletea 存储的模型发起）中
// 复用。setInitialValues/New 分配；零值 Model（cache==nil）则不缓存。
	cache *viewRenderCache
}

// viewRenderCache 记录 View() 输出对应的输入状态。任一字段不匹配则重渲染。
// 该结构体仅从事件循环单线程访问（View/变更方法同串行调用），无需加锁。
type viewRenderCache struct {
	valid     bool
	version   uint64
	yOffset   int
	xOffset   int
	width     int
	height    int
	frameSize int
	output    string
}

func (m *Model) setInitialValues() {
	m.KeyMap = DefaultKeyMap()
	m.MouseWheelEnabled = true
	m.MouseWheelDelta = 3
	m.initialized = true
	if m.cache == nil {
		m.cache = &viewRenderCache{}
	}
}

// Init exists to satisfy the tea.Model interface for composability purposes.
func (m Model) Init() tea.Cmd {
	return nil
}

// AtTop returns whether or not the viewport is at the very top position.
func (m Model) AtTop() bool {
	return m.YOffset <= 0
}

// AtBottom returns whether or not the viewport is at or past the very bottom
// position.
func (m Model) AtBottom() bool {
	return m.YOffset >= m.maxYOffset()
}

// PastBottom returns whether or not the viewport is scrolled beyond the last
// line. This can happen when adjusting the viewport height.
func (m Model) PastBottom() bool {
	return m.YOffset > m.maxYOffset()
}

// ScrollPercent returns the amount scrolled as a float between 0 and 1.
func (m Model) ScrollPercent() float64 {
	if m.Height >= len(m.lines) {
		return 1.0
	}
	y := float64(m.YOffset)
	h := float64(m.Height)
	t := float64(len(m.lines))
	v := y / (t - h)
	return math.Max(0.0, math.Min(1.0, v))
}

// HorizontalScrollPercent returns the amount horizontally scrolled as a float
// between 0 and 1.
func (m Model) HorizontalScrollPercent() float64 {
	if m.xOffset >= m.longestLineWidth-m.Width {
		return 1.0
	}
	y := float64(m.xOffset)
	h := float64(m.Width)
	t := float64(m.longestLineWidth)
	v := y / (t - h)
	return math.Max(0.0, math.Min(1.0, v))
}

// SetContent set the pager's text content.
func (m *Model) SetContent(s string) {
	s = strings.ReplaceAll(s, "\r\n", "\n") // normalize line endings
	m.SetLines(strings.Split(s, "\n"))
}

// SetLines replaces the viewport content with the given pre-split lines.
// It is equivalent to SetContent(strings.Join(lines, "\n")) but avoids the
// intermediate string allocation and re-split.
func (m *Model) SetLines(lines []string) {
	m.lines = append([]string(nil), lines...)
	m.lineWidths = make([]int, len(lines))
	m.linePrefixMax = make([]int, len(lines))
	m.longestLineWidth = 0
	for i, line := range lines {
		w := ansi.StringWidth(line)
		m.lineWidths[i] = w
		if w > m.longestLineWidth {
			m.longestLineWidth = w
		}
		m.linePrefixMax[i] = m.longestLineWidth
	}
	m.linesVersion++
	m.SetXOffset(m.xOffset)
	if m.YOffset > len(m.lines)-1 {
		m.GotoBottom()
	}
}

// ReplaceLines replaces the lines starting at start with newLines. The
// replacement may be shorter or longer than the removed suffix; lines after
// the replacement are discarded, which makes it suitable for re-rendering a
// tail segment of the content (e.g. a streaming assistant entry whose row
// count changes as it grows). An empty newLines truncates the content to
// start lines.
//
// Only the width of the new lines is computed. The prefix maximum aligned with
// lineWidths makes the longest-line update proportional to the replacement
// suffix instead of all retained history.
func (m *Model) ReplaceLines(start int, newLines []string) {
	m.replaceWidthVisits = 0
	if start < 0 {
		start = 0
	}
	if start > len(m.lines) {
		start = len(m.lines)
	}
	newWidths := make([]int, len(newLines))
	for i, line := range newLines {
		m.replaceWidthVisits++
		newWidths[i] = ansi.StringWidth(line)
	}
	oldLen := len(m.lines)
	newLen := start + len(newLines)
	if newLen <= oldLen {
		copy(m.lines[start:], newLines)
		copy(m.lineWidths[start:], newWidths)
		m.lines = m.lines[:newLen]
		m.lineWidths = m.lineWidths[:newLen]
		m.linePrefixMax = m.linePrefixMax[:newLen]
	} else {
		// append(m.lines[:start], newLines...) 的结果长度恰好是 newLen
		// （start 之后的旧行被 newLines 覆盖），不需要再补差值。
		m.lines = append(m.lines[:start], newLines...)
		m.lineWidths = append(m.lineWidths[:start], newWidths...)
		m.linePrefixMax = append(m.linePrefixMax[:start], make([]int, len(newLines))...)
	}
	maxWidth := 0
	if start > 0 {
		maxWidth = m.linePrefixMax[start-1]
	}
	for i := start; i < newLen; i++ {
		if m.lineWidths[i] > maxWidth {
			maxWidth = m.lineWidths[i]
		}
		m.linePrefixMax[i] = maxWidth
	}
	m.longestLineWidth = maxWidth
	m.linesVersion++
	m.SetXOffset(m.xOffset)
	if m.YOffset > len(m.lines)-1 {
		m.GotoBottom()
	}
}

// maxYOffset returns the maximum possible value of the y-offset based on the
// viewport's content and set height.
func (m Model) maxYOffset() int {
	return max(0, len(m.lines)-m.Height+m.Style.GetVerticalFrameSize())
}

// visibleLines returns the lines that should currently be visible in the
// viewport.
func (m Model) visibleLines() (lines []string) {
	h := m.Height - m.Style.GetVerticalFrameSize()
	w := m.Width - m.Style.GetHorizontalFrameSize()

	if len(m.lines) > 0 {
		top := max(0, m.YOffset)
		bottom := clamp(m.YOffset+h, top, len(m.lines))
		lines = m.lines[top:bottom]
	}

	if (m.xOffset == 0 && m.longestLineWidth <= w) || w == 0 {
		return lines
	}

	cutLines := make([]string, len(lines))
	for i := range lines {
		cutLines[i] = ansi.Cut(lines[i], m.xOffset, m.xOffset+w)
	}
	return cutLines
}

// scrollArea returns the scrollable boundaries for high performance rendering.
//
// Deprecated: high performance rendering is deprecated in Bubble Tea.
func (m Model) scrollArea() (top, bottom int) {
	top = max(0, m.YPosition)
	bottom = max(top, top+m.Height)
	if top > 0 && bottom > top {
		bottom--
	}
	return top, bottom
}

// SetYOffset sets the Y offset.
func (m *Model) SetYOffset(n int) {
	m.YOffset = clamp(n, 0, m.maxYOffset())
}

// ViewDown moves the view down by the number of lines in the viewport.
// Basically, "page down".
//
// Deprecated: use [Model.PageDown] instead.
func (m *Model) ViewDown() []string {
	return m.PageDown()
}

// PageDown moves the view down by the number of lines in the viewport.
func (m *Model) PageDown() []string {
	if m.AtBottom() {
		return nil
	}

	return m.ScrollDown(m.Height)
}

// ViewUp moves the view up by one height of the viewport.
// Basically, "page up".
//
// Deprecated: use [Model.PageUp] instead.
func (m *Model) ViewUp() []string {
	return m.PageUp()
}

// PageUp moves the view up by one height of the viewport.
func (m *Model) PageUp() []string {
	if m.AtTop() {
		return nil
	}

	return m.ScrollUp(m.Height)
}

// HalfViewDown moves the view down by half the height of the viewport.
//
// Deprecated: use [Model.HalfPageDown] instead.
func (m *Model) HalfViewDown() (lines []string) {
	return m.HalfPageDown()
}

// HalfPageDown moves the view down by half the height of the viewport.
func (m *Model) HalfPageDown() (lines []string) {
	if m.AtBottom() {
		return nil
	}

	return m.ScrollDown(m.Height / 2) //nolint:mnd
}

// HalfViewUp moves the view up by half the height of the viewport.
//
// Deprecated: use [Model.HalfPageUp] instead.
func (m *Model) HalfViewUp() (lines []string) {
	return m.HalfPageUp()
}

// HalfPageUp moves the view up by half the height of the viewport.
func (m *Model) HalfPageUp() (lines []string) {
	if m.AtTop() {
		return nil
	}

	return m.ScrollUp(m.Height / 2) //nolint:mnd
}

// LineDown moves the view down by the given number of lines.
//
// Deprecated: use [Model.ScrollDown] instead.
func (m *Model) LineDown(n int) (lines []string) {
	return m.ScrollDown(n)
}

// ScrollDown moves the view down by the given number of lines.
func (m *Model) ScrollDown(n int) (lines []string) {
	if m.AtBottom() || n == 0 || len(m.lines) == 0 {
		return nil
	}

	// Make sure the number of lines by which we're going to scroll isn't
	// greater than the number of lines we actually have left before we reach
	// the bottom.
	m.SetYOffset(m.YOffset + n)

	// Gather lines to send off for performance scrolling.
	//
	// XXX: high performance rendering is deprecated in Bubble Tea.
	bottom := clamp(m.YOffset+m.Height, 0, len(m.lines))
	top := clamp(m.YOffset+m.Height-n, 0, bottom)
	return m.lines[top:bottom]
}

// LineUp moves the view down by the given number of lines. Returns the new
// lines to show.
//
// Deprecated: use [Model.ScrollUp] instead.
func (m *Model) LineUp(n int) (lines []string) {
	return m.ScrollUp(n)
}

// ScrollUp moves the view down by the given number of lines. Returns the new
// lines to show.
func (m *Model) ScrollUp(n int) (lines []string) {
	if m.AtTop() || n == 0 || len(m.lines) == 0 {
		return nil
	}

	// Make sure the number of lines by which we're going to scroll isn't
	// greater than the number of lines we are from the top.
	m.SetYOffset(m.YOffset - n)

	// Gather lines to send off for performance scrolling.
	//
	// XXX: high performance rendering is deprecated in Bubble Tea.
	top := max(0, m.YOffset)
	bottom := clamp(m.YOffset+n, 0, m.maxYOffset())
	return m.lines[top:bottom]
}

// SetHorizontalStep sets the default amount of columns to scroll left or right
// with the default viewport key map.
//
// If set to 0 or less, horizontal scrolling is disabled.
//
// On v1, horizontal scrolling is disabled by default.
func (m *Model) SetHorizontalStep(n int) {
	m.horizontalStep = max(n, 0)
}

// SetXOffset sets the X offset.
func (m *Model) SetXOffset(n int) {
	m.xOffset = clamp(n, 0, max(0, m.longestLineWidth-m.Width))
}

// ScrollLeft moves the viewport to the left by the given number of columns.
func (m *Model) ScrollLeft(n int) {
	m.SetXOffset(m.xOffset - n)
}

// ScrollRight moves viewport to the right by the given number of columns.
func (m *Model) ScrollRight(n int) {
	m.SetXOffset(m.xOffset + n)
}

// TotalLineCount returns the total number of lines (both hidden and visible) within the viewport.
func (m Model) TotalLineCount() int {
	return len(m.lines)
}

// VisibleLineCount returns the number of the visible lines within the viewport.
func (m Model) VisibleLineCount() int {
	return len(m.visibleLines())
}

// GotoTop sets the viewport to the top position.
func (m *Model) GotoTop() (lines []string) {
	if m.AtTop() {
		return nil
	}

	m.SetYOffset(0)
	return m.visibleLines()
}

// GotoBottom sets the viewport to the bottom position.
func (m *Model) GotoBottom() (lines []string) {
	m.SetYOffset(m.maxYOffset())
	return m.visibleLines()
}

// Sync tells the renderer where the viewport will be located and requests
// a render of the current state of the viewport. It should be called for the
// first render and after a window resize.
//
// For high performance rendering only.
//
// Deprecated: high performance rendering is deprecated in Bubble Tea.
func Sync(m Model) tea.Cmd {
	if len(m.lines) == 0 {
		return nil
	}
	top, bottom := m.scrollArea()
	return tea.SyncScrollArea(m.visibleLines(), top, bottom)
}

// ViewDown is a high performance command that moves the viewport up by a given
// number of lines. Use Model.ViewDown to get the lines that should be rendered.
// For example:
//
//	lines := model.ViewDown(1)
//	cmd := ViewDown(m, lines)
//
// Deprecated: high performance rendering is deprecated in Bubble Tea.
func ViewDown(m Model, lines []string) tea.Cmd {
	if len(lines) == 0 {
		return nil
	}
	top, bottom := m.scrollArea()

	// XXX: high performance rendering is deprecated in Bubble Tea. In a v2 we
	// won't need to return a command here.
	return tea.ScrollDown(lines, top, bottom)
}

// ViewUp is a high performance command the moves the viewport down by a given
// number of lines height. Use Model.ViewUp to get the lines that should be
// rendered.
//
// Deprecated: high performance rendering is deprecated in Bubble Tea.
func ViewUp(m Model, lines []string) tea.Cmd {
	if len(lines) == 0 {
		return nil
	}
	top, bottom := m.scrollArea()

	// XXX: high performance rendering is deprecated in Bubble Tea. In a v2 we
	// won't need to return a command here.
	return tea.ScrollUp(lines, top, bottom)
}

// Update handles standard message-based viewport updates.
func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	var cmd tea.Cmd
	m, cmd = m.updateAsModel(msg)
	return m, cmd
}

// Author's note: this method has been broken out to make it easier to
// potentially transition Update to satisfy tea.Model.
func (m Model) updateAsModel(msg tea.Msg) (Model, tea.Cmd) {
	if !m.initialized {
		m.setInitialValues()
	}

	var cmd tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch {
		case key.Matches(msg, m.KeyMap.PageDown):
			lines := m.PageDown()
			if m.HighPerformanceRendering {
				cmd = ViewDown(m, lines)
			}

		case key.Matches(msg, m.KeyMap.PageUp):
			lines := m.PageUp()
			if m.HighPerformanceRendering {
				cmd = ViewUp(m, lines)
			}

		case key.Matches(msg, m.KeyMap.HalfPageDown):
			lines := m.HalfPageDown()
			if m.HighPerformanceRendering {
				cmd = ViewDown(m, lines)
			}

		case key.Matches(msg, m.KeyMap.HalfPageUp):
			lines := m.HalfPageUp()
			if m.HighPerformanceRendering {
				cmd = ViewUp(m, lines)
			}

		case key.Matches(msg, m.KeyMap.Down):
			lines := m.ScrollDown(1)
			if m.HighPerformanceRendering {
				cmd = ViewDown(m, lines)
			}

		case key.Matches(msg, m.KeyMap.Up):
			lines := m.ScrollUp(1)
			if m.HighPerformanceRendering {
				cmd = ViewUp(m, lines)
			}

		case key.Matches(msg, m.KeyMap.Left):
			m.ScrollLeft(m.horizontalStep)

		case key.Matches(msg, m.KeyMap.Right):
			m.ScrollRight(m.horizontalStep)
		}

	case tea.MouseMsg:
		if !m.MouseWheelEnabled || msg.Action != tea.MouseActionPress {
			break
		}
		switch msg.Button { //nolint:exhaustive
		case tea.MouseButtonWheelUp:
			if msg.Shift {
				// Note that not every terminal emulator sends the shift event for mouse actions by default (looking at you Konsole)
				m.ScrollLeft(m.horizontalStep)
			} else {
				lines := m.ScrollUp(m.MouseWheelDelta)
				if m.HighPerformanceRendering {
					cmd = ViewUp(m, lines)
				}
			}

		case tea.MouseButtonWheelDown:
			if msg.Shift {
				m.ScrollRight(m.horizontalStep)
			} else {
				lines := m.ScrollDown(m.MouseWheelDelta)
				if m.HighPerformanceRendering {
					cmd = ViewDown(m, lines)
				}
			}
		// Note that not every terminal emulator sends the horizontal wheel events by default (looking at you Konsole)
		case tea.MouseButtonWheelLeft:
			m.ScrollLeft(m.horizontalStep)
		case tea.MouseButtonWheelRight:
			m.ScrollRight(m.horizontalStep)
		}
	}

	return m, cmd
}

// View renders the viewport into a string.
func (m Model) View() string {
	if m.HighPerformanceRendering {
		// Just send newlines since we're going to be rendering the actual
		// content separately. We still need to send something that equals the
		// height of this view so that the Bubble Tea standard renderer can
		// position anything below this view properly.
		return strings.Repeat("\n", max(0, m.Height-1))
	}

	w, h := m.Width, m.Height
	if sw := m.Style.GetWidth(); sw != 0 {
		w = min(w, sw)
	}
	if sh := m.Style.GetHeight(); sh != 0 {
		h = min(h, sh)
	}
	if !m.viewCacheApplicable(w, h) {
		return m.viewLegacy(w, h)
	}

	frameSize := m.Style.GetVerticalFrameSize()
	if m.cache == nil {
		return m.renderViewFast(w, h, frameSize)
	}
	c := m.cache
	if c.valid &&
		c.version == m.linesVersion &&
		c.yOffset == m.YOffset &&
		c.xOffset == m.xOffset &&
		c.width == w &&
		c.height == h &&
		c.frameSize == frameSize {
		return c.output
	}

	output := m.renderViewFast(w, h, frameSize)
	*c = viewRenderCache{
		valid:     true,
		version:   m.linesVersion,
		yOffset:   m.YOffset,
		xOffset:   m.xOffset,
		width:     w,
		height:    h,
		frameSize: frameSize,
		output:    output,
	}
	return output
}

// viewCacheApplicable reports whether the fast render path can be used:
// the style must carry no visual properties the fast path would drop,
// and the geometry must be positive.
func (m Model) viewCacheApplicable(w, h int) bool {
	if w <= 0 || h <= 0 {
		return false
	}
	return m.Style.GetWidth() == 0 &&
		m.Style.GetHeight() == 0 &&
		m.Style.GetHorizontalFrameSize() == 0 &&
		m.Style.GetVerticalFrameSize() == 0 &&
		m.Style.GetForeground() == nil &&
		m.Style.GetBackground() == nil
}

// renderViewFast renders without lipgloss: it is semantically equivalent to
// lipgloss.NewStyle().Width(w).Height(h).MaxHeight(h).MaxWidth(w).Render(join(visibleLines))
// followed by a plain Style render: every line is truncated/padded to exactly
// w cells and the block is padded with blank (space-filled) lines to h rows.
func (m Model) renderViewFast(w, h, frameSize int) string {
	contentHeight := h - frameSize
	if contentHeight <= 0 {
		return ""
	}
	lines := m.visibleLines()
	if len(lines) > contentHeight {
		lines = lines[:contentHeight]
	}
	out := make([]string, len(lines), contentHeight)
	for i, line := range lines {
		out[i] = fitViewLine(line, w)
	}
	blank := strings.Repeat(" ", w)
	for len(out) < contentHeight {
		out = append(out, blank)
	}
	return strings.Join(out, "\n")
}

// fitViewLine truncates or pads a styled line to exactly width terminal
// cells. ansi.Truncate mirrors the lipgloss MaxWidth behavior (cell-boundary
// cut without ellipsis).
func fitViewLine(line string, width int) string {
	lw := ansi.StringWidth(line)
	if lw > width {
		return ansi.Truncate(line, width, "")
	}
	if lw < width {
		return line + strings.Repeat(" ", width-lw)
	}
	return line
}

// viewLegacy is the original lipgloss-based render, kept for styles the fast
// path does not cover.
func (m Model) viewLegacy(w, h int) string {
	contentWidth := w - m.Style.GetHorizontalFrameSize()
	contentHeight := h - m.Style.GetVerticalFrameSize()
	contents := lipgloss.NewStyle().
		Width(contentWidth).      // pad to width.
		Height(contentHeight).    // pad to height.
		MaxHeight(contentHeight). // truncate height if taller.
		MaxWidth(contentWidth).   // truncate width if wider.
		Render(strings.Join(m.visibleLines(), "\n"))
	return m.Style.
		UnsetWidth().UnsetHeight(). // Style size already applied in contents.
		Render(contents)
}

func clamp(v, low, high int) int {
	if high < low {
		low, high = high, low
	}
	return min(high, max(low, v))
}

func findLongestLineWidth(lines []string) int {
	w := 0
	for _, l := range lines {
		if ww := ansi.StringWidth(l); ww > w {
			w = ww
		}
	}
	return w
}
