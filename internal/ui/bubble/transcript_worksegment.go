// 工作段（WorkSegment）折叠：把 transcript 中"可见正文之间"的 reasoning 与
// 工具事务连续运行在视图层收编为一个条目。本文件只包含纯函数与统计聚合，
// 不触碰原始 transcript 与事件流。设计见
// docs/superpowers/specs/2026-08-24-thought-worksegment-fold-design.md。
package bubble

import (
	"fmt"
	"strings"
	"time"
)

// foldMode 控制工作段折叠的形态。
type foldMode int

const (
	// foldLive 用于回合进行中：仅尾部连续运行折叠为 live 块，其余条目保持原样。
	foldLive foldMode = iota
	// foldResting 用于回合结束与历史渲染：全部连续运行折叠为一行摘要块。
	foldResting
)

// isWorkSegmentCollectable 报告条目是否可被工作段收编。
// 正文、用户消息、系统与错误条目是段边界，不可收编。
func isWorkSegmentCollectable(entry transcriptEntry) bool {
	switch entry.kind {
	case entryReasoning, entryThinking, entryTool:
		return true
	default:
		return false
	}
}

// isBookkeepingToolEntry 报告条目是否为簿记工具（update_todo）：
// 可收编进段（不切段），但不计入工具统计。
func isBookkeepingToolEntry(entry transcriptEntry) bool {
	return entry.kind == entryTool && strings.EqualFold(strings.TrimSpace(entry.toolName), "update_todo")
}

// isInteractiveToolEntry 报告条目是否为交互/权限类工具：
// 不收编且切段（需要用户输入，前后工具不并入同一段）。
func isInteractiveToolEntry(entry transcriptEntry) bool {
	if entry.kind != entryTool {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(entry.toolName)) {
	case "question", "select":
		return true
	default:
		return false
	}
}

// buildWorkSegmentEntry 把一段连续运行聚合为工作段条目。
// 仅含簿记工具的运行（无 reasoning、无有效工具事务）被抑制，返回 ok=false。
func buildWorkSegmentEntry(children []transcriptEntry) (transcriptEntry, bool) {
	if len(children) == 0 {
		return transcriptEntry{}, false
	}
	data := &workSegmentData{children: append([]transcriptEntry(nil), children...)}
	for _, child := range children {
		switch child.kind {
		case entryReasoning, entryThinking:
			data.hasReasoning = true
		case entryTool:
			if isBookkeepingToolEntry(child) {
				continue
			}
			data.toolCalls++
			if toolEntryStatus(child) == "error" {
				data.failed++
			}
		}
	}
	if !data.hasReasoning && data.toolCalls == 0 {
		return transcriptEntry{}, false
	}
	data.startedAt = children[0].createdAt
	data.finishedAt = children[len(children)-1].createdAt
	return transcriptEntry{
		kind:      entryWorkSegment,
		createdAt: data.startedAt,
		segment:   data,
	}, true
}

// foldWorkSegments 把 entries 中的工作段连续运行在视图层折叠。
// foldResting 折叠所有连续运行；foldLive 只折叠尾部连续运行并标记 live，
// 其余条目原样保留（回合进行中不折旧段）。
func foldWorkSegments(entries []transcriptEntry, mode foldMode) []transcriptEntry {
	view, _, _ := foldWorkSegmentsView(entries, mode, nil)
	return view
}

// foldWorkSegmentsView 在折叠之外额外返回下标映射：
//   - transcriptToView[i]：transcript 条目 i 在视图中的下标（被收编的子条目
//     指向所属段）；
//   - viewToTranscript[j]：视图条目 j 对应的 transcript 下标，段条目为 -1。
//
// 渲染管线用它们把 transcript 空间的失效标记与交互写回翻译到视图空间。
func foldWorkSegmentsView(entries []transcriptEntry, mode foldMode, expandedFor func(*workSegmentData) bool) (view []transcriptEntry, transcriptToView, viewToTranscript []int) {
	if mode == foldLive {
		return foldWorkSegmentsLiveView(entries)
	}
	view = make([]transcriptEntry, 0, len(entries))
	transcriptToView = make([]int, len(entries))
	runStart := -1
	flush := func(run []transcriptEntry, respondedAt time.Time) {
		if len(run) == 0 {
			return
		}
		if entry, ok := buildWorkSegmentEntry(run); ok {
			entry.segment.respondedAt = respondedAt
			if expandedFor != nil && expandedFor(entry.segment) {
				view, transcriptToView, viewToTranscript = emitExpandedWorkSegment(view, transcriptToView, viewToTranscript, run, runStart, entry)
			} else {
				segIndex := len(view)
				for i := runStart; i < runStart+len(run); i++ {
					transcriptToView[i] = segIndex
				}
				view = append(view, entry)
				viewToTranscript = append(viewToTranscript, -1)
			}
		} else {
			for i, child := range run {
				transcriptToView[runStart+i] = len(view)
				view = append(view, child)
				viewToTranscript = append(viewToTranscript, runStart+i)
			}
		}
		runStart = -1
	}
	emitRaw := func(index int, entry transcriptEntry) {
		transcriptToView[index] = len(view)
		view = append(view, entry)
		viewToTranscript = append(viewToTranscript, index)
	}
	var run []transcriptEntry
	for index, entry := range entries {
		switch {
		case isInteractiveToolEntry(entry):
			flush(run, time.Time{})
			run = nil
			emitRaw(index, entry)
		case isWorkSegmentCollectable(entry):
			if len(run) == 0 {
				runStart = index
			}
			run = append(run, entry)
		case entry.kind == entryAssistant && len(run) > 0:
			// 响应在前、Thought 在后：段摘要跟随产生它的模型响应，
			// 并把响应时间记到段上（标题尾部展示）。
			emitRaw(index, entry)
			flush(run, entry.createdAt)
			run = nil
		default:
			flush(run, time.Time{})
			run = nil
			emitRaw(index, entry)
		}
	}
	flush(run, time.Time{})
	return view, transcriptToView, viewToTranscript
}

// emitExpandedWorkSegment 把展开段以「标题行 + 子条目平铺」的形式内联进
// 视图：子条目成为独立视图条目，工具的悬停/点击/检查沿用现有机制。
// reasoning 子条目强制内容展开（不嵌套自身的 "Thought for N s" 折叠标
// 题）；工具子条目标记 toolGroupPending，避免重新嵌套 Tools 组折叠。
func emitExpandedWorkSegment(view []transcriptEntry, transcriptToView, viewToTranscript []int, run []transcriptEntry, runStart int, entry transcriptEntry) ([]transcriptEntry, []int, []int) {
	entry.segment.header = true
	view = append(view, entry)
	viewToTranscript = append(viewToTranscript, -1)
	for i, child := range run {
		transcriptToView[runStart+i] = len(view)
		inline := child
		switch inline.kind {
		case entryReasoning, entryThinking:
			inline.kind = entryReasoning
			inline.reasoningExpansionSet = true
			inline.reasoningExpanded = true
		case entryTool:
			inline.toolGroupPending = true
		}
		view = append(view, inline)
		viewToTranscript = append(viewToTranscript, runStart+i)
	}
	return view, transcriptToView, viewToTranscript
}

// foldWorkSegmentsLiveView 是 foldLive 模式的映射版本。
func foldWorkSegmentsLiveView(entries []transcriptEntry) (view []transcriptEntry, transcriptToView, viewToTranscript []int) {
	transcriptToView = make([]int, len(entries))
	identity := func() {
		view = append([]transcriptEntry(nil), entries...)
		for i := range entries {
			transcriptToView[i] = i
			viewToTranscript = append(viewToTranscript, i)
		}
	}
	end := len(entries)
	start := end
	for start > 0 {
		entry := entries[start-1]
		if isInteractiveToolEntry(entry) || !isWorkSegmentCollectable(entry) {
			break
		}
		start--
	}
	if start == end {
		identity()
		return view, transcriptToView, viewToTranscript
	}
	segment, ok := buildWorkSegmentEntry(entries[start:end])
	if !ok {
		identity()
		return view, transcriptToView, viewToTranscript
	}
	segment.segment.live = true
	view = make([]transcriptEntry, 0, start+1)
	for i := 0; i < start; i++ {
		transcriptToView[i] = i
		view = append(view, entries[i])
		viewToTranscript = append(viewToTranscript, i)
	}
	segIndex := len(view)
	for i := start; i < end; i++ {
		transcriptToView[i] = segIndex
	}
	view = append(view, segment)
	viewToTranscript = append(viewToTranscript, -1)
	return view, transcriptToView, viewToTranscript
}

// workSegmentTitle 生成 resting 态标题：
// 含 reasoning → "Thought for …"；纯工具段 → "Worked for …"；
// 计数与失败按需追加（"· 15 tool calls · 1 failed"）。
func workSegmentTitle(data *workSegmentData, at time.Time) string {
	if data == nil {
		return ""
	}
	end := data.finishedAt
	if data.live {
		end = at
	}
	duration := formatSegmentDuration(end.Sub(data.startedAt))
	var b strings.Builder
	if data.hasReasoning {
		b.WriteString("Thought for ")
	} else {
		b.WriteString("Worked for ")
	}
	b.WriteString(duration)
	if data.toolCalls > 0 {
		fmt.Fprintf(&b, " · %d tool calls", data.toolCalls)
	}
	if data.failed > 0 {
		fmt.Fprintf(&b, " · %d failed", data.failed)
	}
	if responded := formatResponseClock(data.respondedAt, at); responded != "" {
		b.WriteString(" · ")
		b.WriteString(responded)
	}
	return b.String()
}

// formatResponseClock 格式化模型响应时间：今天只显示时分（14:32）；同年
// 带月日（8月24日 14:32）；跨年再带年份（2025年8月24日 14:32）。
func formatResponseClock(t, at time.Time) string {
	if t.IsZero() {
		return ""
	}
	clock := fmt.Sprintf("%02d:%02d", t.Hour(), t.Minute())
	if t.Year() == at.Year() && t.YearDay() == at.YearDay() {
		return clock
	}
	if t.Year() == at.Year() {
		return fmt.Sprintf("%d月%d日 %s", int(t.Month()), t.Day(), clock)
	}
	return fmt.Sprintf("%d年%d月%d日 %s", t.Year(), int(t.Month()), t.Day(), clock)
}

// workSegmentLiveTitle 生成 live 态标题（统一 Thoughts + 实时时长与计数）。
func workSegmentLiveTitle(data *workSegmentData, at time.Time) string {
	if data == nil {
		return ""
	}
	duration := formatSegmentDuration(at.Sub(data.startedAt))
	var b strings.Builder
	b.WriteString("Thoughts · ")
	b.WriteString(duration)
	if data.toolCalls > 0 {
		fmt.Fprintf(&b, " · %d tool calls", data.toolCalls)
	}
	return b.String()
}

// renderWorkSegmentEntry 渲染工作段条目：折叠态与展开态标题行都渲染为一
// 行标题（展开段的子条目已作为独立视图条目内联平铺）；live 态渲染实时块。
func renderWorkSegmentEntry(entry transcriptEntry, width int, at time.Time, showThinking bool) string {
	data := entry.segment
	if data == nil {
		return ""
	}
	bodyWidth := transcriptBodyWidth(width)
	if data.live {
		return renderWorkSegmentLive(data, width, bodyWidth, at)
	}
	title := thinkingBodyStyle.Width(bodyWidth).Render(workSegmentTitle(data, at))
	return indentLines(title, transcriptEntryGutter)
}

// renderWorkSegmentChild 平铺渲染段内子条目：reasoning 直接渲染暗色正文
// （不嵌套自身的 "Thought for N s" 折叠标题），工具条目渲染为单行 compact
// 事务行（含状态标记）。live 滚动窗口与（未来的）段内平铺共用。
func renderWorkSegmentChild(child transcriptEntry, width, bodyWidth int, at time.Time, showThinking bool) string {
	switch child.kind {
	case entryReasoning, entryThinking:
		content := strings.TrimSpace(child.body)
		if content == "" {
			return ""
		}
		return indentLines(thinkingBodyStyle.Width(bodyWidth).Render(content), transcriptEntryGutter)
	case entryTool:
		line := firstToolEntryLine(child.body)
		if line == "" {
			line = strings.TrimSpace(child.toolName)
		}
		if line == "" {
			return ""
		}
		var body string
		switch {
		case child.isError || toolEntryStatus(child) == "error":
			body = renderToolEntryBodyWithMarker(line, bodyWidth, 1, toolErrorBorderStyle, "!")
		case toolEntryHasCompletedStatus(child):
			body = renderToolEntryBodyWithMarker(line, bodyWidth, 1, toolResultBorderStyle, "✓")
		default:
			body = renderToolEntryBody(line, bodyWidth, 1)
		}
		return indentLines(body, transcriptEntryGutter)
	default:
		return strings.TrimRight(renderEntryAt(child, width, at, showThinking), "\n")
	}
}

// workSegmentLiveWindowRows 是 live 滚动窗口的行数。
const workSegmentLiveWindowRows = 12

// renderWorkSegmentLive 渲染 live 工作段：实时标题 + 12 行滚动窗口。
func renderWorkSegmentLive(data *workSegmentData, width, bodyWidth int, at time.Time) string {
	title := thinkingBodyStyle.Width(bodyWidth).Render("● " + workSegmentLiveTitle(data, at))
	rendered := indentLines(title, transcriptEntryGutter)
	window := workSegmentLiveWindow(data, width, bodyWidth, at)
	if window == "" {
		return rendered
	}
	return rendered + "\n" + window
}

// workSegmentLiveWindow 渲染 12 行滚动窗口：子条目按时间序 compact 渲染，
// 只保留最后 12 行（自动滚动到最新）。
func workSegmentLiveWindow(data *workSegmentData, width, bodyWidth int, at time.Time) string {
	var lines []string
	for _, child := range data.children {
		childRendered := renderWorkSegmentChild(child, width, bodyWidth, at, false)
		if childRendered == "" {
			continue
		}
		lines = append(lines, strings.Split(childRendered, "\n")...)
	}
	if len(lines) == 0 {
		return ""
	}
	if len(lines) > workSegmentLiveWindowRows {
		lines = lines[len(lines)-workSegmentLiveWindowRows:]
	}
	return strings.Join(lines, "\n")
}

// workSegmentHitAtTranscriptRow 命中的行是否属于一个可点击的工作段标题
// （resting 折叠块或展开段的标题行；live 块不可点击）。
func (m appModel) workSegmentHitAtTranscriptRow(row int) (int, bool) {
	for _, location := range m.transcriptEntryLocationsAt() {
		if row < location.startRow || row >= location.startRow+location.height {
			continue
		}
		if location.transcriptIndex < 0 || location.transcriptIndex >= len(m.viewEntries) {
			return -1, false
		}
		entry := m.viewEntries[location.transcriptIndex]
		if entry.kind != entryWorkSegment || entry.segment == nil || entry.segment.live {
			return -1, false
		}
		if row != location.startRow {
			return -1, false
		}
		return location.transcriptIndex, true
	}
	return -1, false
}

// toggleWorkSegmentExpansion 切换一个 resting 工作段的展开态并刷新视图。
func (m *appModel) toggleWorkSegmentExpansion(index int) bool {
	if m == nil || index < 0 || index >= len(m.viewEntries) {
		return false
	}
	entry := m.viewEntries[index]
	if entry.kind != entryWorkSegment || entry.segment == nil || entry.segment.live {
		return false
	}
	expanded := m.segmentExpanded[workSegmentKey(entry.segment)]
	m.setWorkSegmentExpanded(entry.segment, !expanded)
	m.refreshViewportPreservingOffset()
	return true
}

// workSegmentKeySnapshot 生成工作段的渲染缓存指纹：覆盖统计、展开/live 态、
// 子条目版本与内容长度；live 态附带秒级时间截断以驱动计时刷新。
func workSegmentKeySnapshot(data *workSegmentData, at time.Time) string {
	if data == nil {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d/%d/%t/%t/%d/%d", data.toolCalls, data.failed, data.hasReasoning, data.live, data.startedAt.UnixNano(), data.finishedAt.UnixNano())
	for _, child := range data.children {
		fmt.Fprintf(&b, "|%d:%d:%d:%s", child.version, len(child.body), len(child.toolResult), toolEntryStatus(child))
	}
	if data.live {
		fmt.Fprintf(&b, "|at:%d", at.Unix())
	}
	return b.String()
}

// formatSegmentDuration 人性化段时长：最小 1 s，格式 "12 s" / "2m 15s" / "1h 3m"。
func formatSegmentDuration(d time.Duration) string {
	secs := int(d.Seconds())
	if secs < 1 {
		secs = 1
	}
	if secs < 60 {
		return fmt.Sprintf("%d s", secs)
	}
	mins := secs / 60
	remSecs := secs % 60
	if mins < 60 {
		if remSecs == 0 {
			return fmt.Sprintf("%dm", mins)
		}
		return fmt.Sprintf("%dm %ds", mins, remSecs)
	}
	hours := mins / 60
	remMins := mins % 60
	if remMins == 0 {
		return fmt.Sprintf("%dh", hours)
	}
	return fmt.Sprintf("%dh %dm", hours, remMins)
}
