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
	// foldLive 用于回合进行中：已完成的运行按 resting 语义折叠（Thought 保
	// 留在真实推理位置），仅尾部仍在进行的连续运行保留为 live 实时块。
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
	// 段时长取真实工作窗口：reasoning 的计时窗口（reasoningWindowStart，从请求
	// 发出起算）可能显著早于条目落库时刻 createdAt（网关缓冲场景），仅用
	// createdAt 会把 30s 的思考显示成 1s，与子条目自身的时长口径矛盾。
	data.startedAt = children[0].createdAt
	if started := children[0].reasoningStartedAt; started != nil && started.Before(data.startedAt) {
		data.startedAt = *started
	}
	data.finishedAt = children[len(children)-1].createdAt
	if finished := children[len(children)-1].reasoningFinishedAt; finished != nil && finished.After(data.finishedAt) {
		data.finishedAt = *finished
	}
	return transcriptEntry{
		kind:      entryWorkSegment,
		createdAt: data.startedAt,
		segment:   data,
	}, true
}

// foldWorkSegments 把 entries 中的工作段连续运行在视图层折叠。
// foldResting 折叠所有连续运行；foldLive 下已完成的运行同样折叠，仅尾部
// 仍在进行的运行保留为 live 实时块。
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
		return foldWorkSegmentsLiveView(entries, expandedFor)
	}
	view = make([]transcriptEntry, 0, len(entries))
	transcriptToView = make([]int, len(entries))
	runStart := -1
	// flush 把一段连续运行聚合为一个工作段条目：段内多段思考与工具合并为
	// 一行摘要（思考之间只有工具调用、没有正文输出时逐块拆行只会得到
	// Thought/Worked 交替的噪声墙）。段落在真实推理位置（响应之前）；
	// 返回段在视图中的下标（被抑制或无段为 -1），供调用方判定该运行是否
	// 成段（成段运行后跟随的响应条目带时钟行）。
	flush := func(run []transcriptEntry) int {
		lastSegment := -1
		if len(run) == 0 {
			return -1
		}
		if entry, ok := buildWorkSegmentEntry(run); ok {
			if expandedFor != nil && expandedFor(entry.segment) {
				lastSegment = len(view)
				view, transcriptToView, viewToTranscript = emitExpandedWorkSegment(view, transcriptToView, viewToTranscript, run, runStart, entry)
			} else {
				segIndex := len(view)
				for i := runStart; i < runStart+len(run); i++ {
					transcriptToView[i] = segIndex
				}
				view = append(view, entry)
				viewToTranscript = append(viewToTranscript, -1)
				lastSegment = segIndex
			}
		} else {
			for i, child := range run {
				transcriptToView[runStart+i] = len(view)
				view = append(view, child)
				viewToTranscript = append(viewToTranscript, runStart+i)
			}
		}
		runStart = -1
		return lastSegment
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
			flush(run)
			run = nil
			emitRaw(index, entry)
		case isWorkSegmentCollectable(entry):
			if len(run) == 0 {
				runStart = index
			}
			run = append(run, entry)
		case entry.kind == entryAssistant && len(run) > 0:
			// Thought 保持在真实推理位置（响应之前）；响应时间不再挂在段标
			// 题上，而是标记在响应条目上，渲染在响应结束的下一行。
			if flush(run) >= 0 {
				entry.responseClock = true
			}
			run = nil
			emitRaw(index, entry)
		default:
			flush(run)
			run = nil
			emitRaw(index, entry)
		}
	}
	flush(run)
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

// foldWorkSegmentsLiveView 是 foldLive 模式的映射版本：已完成的前缀运行按
// resting 语义折叠（Thought 保留在真实推理位置，回合进行中同样成立），仅
// 尾部仍在进行的连续运行保留为 live 实时块。旧实现把前缀条目原样平铺，已
// 完成的思考块会以裸条目滞留在视图中（与 resting 结构不一致），且折叠结
// 构变化还会导致渲染缓存复用过期的 live 块行。
func foldWorkSegmentsLiveView(entries []transcriptEntry, expandedFor func(*workSegmentData) bool) (view []transcriptEntry, transcriptToView, viewToTranscript []int) {
	end := len(entries)
	start := end
	for start > 0 {
		entry := entries[start-1]
		if isInteractiveToolEntry(entry) || !isWorkSegmentCollectable(entry) {
			break
		}
		start--
	}
	// 尾部无可收编运行（或为空）：全部按 resting 语义折叠。
	view, transcriptToView, viewToTranscript = foldWorkSegmentsView(entries[:start], foldResting, expandedFor)
	if start == end {
		return view, transcriptToView, viewToTranscript
	}
	segment, ok := buildWorkSegmentEntry(entries[start:end])
	if !ok {
		// 尾部仅簿记工具（被抑制成段）：保持原样平铺。
		for i := start; i < end; i++ {
			transcriptToView = append(transcriptToView, len(view))
			view = append(view, entries[i])
			viewToTranscript = append(viewToTranscript, i)
		}
		return view, transcriptToView, viewToTranscript
	}
	segment.segment.live = true
	segIndex := len(view)
	for i := start; i < end; i++ {
		transcriptToView = append(transcriptToView, segIndex)
	}
	view = append(view, segment)
	viewToTranscript = append(viewToTranscript, -1)
	return view, transcriptToView, viewToTranscript
}

// viewSlotSignature 描述一个视图槽位的结构身份。渲染缓存按视图下标复用，
// 而折叠投影可能在 transcript 条目本身未变时改变槽位内容（如 live 段解散
// 为原始条目、段吸收新子条目）：仅靠 transcript 空间的失效下标无法发现，
// 必须按签名比对，从首个分歧槽位强制重渲染。
type viewSlotSignature struct {
	kind     entryKind
	source   int  // viewToTranscript：所属 transcript 下标；段条目为 -1
	children int  // 段的子条目数；非段条目为 -1
	live     bool // 段：live 实时块
	header   bool // 段：展开态标题行
}

// viewProjectionSignature 计算视图投影的结构签名序列。
func viewProjectionSignature(view []transcriptEntry, viewToTranscript []int) []viewSlotSignature {
	sig := make([]viewSlotSignature, len(view))
	for i, entry := range view {
		slot := viewSlotSignature{kind: entry.kind, source: -1, children: -1}
		if i < len(viewToTranscript) {
			slot.source = viewToTranscript[i]
		}
		if entry.segment != nil {
			slot.children = len(entry.segment.children)
			slot.live = entry.segment.live
			slot.header = entry.segment.header
		}
		sig[i] = slot
	}
	return sig
}

// firstViewSignatureDiff 返回前后两份投影签名的首个分歧下标；一致返回 -1，
// 长度不同（一方为另一方前缀）返回较短长度。
func firstViewSignatureDiff(previous, current []viewSlotSignature) int {
	shared := minInt(len(previous), len(current))
	for i := 0; i < shared; i++ {
		if previous[i] != current[i] {
			return i
		}
	}
	if len(previous) != len(current) {
		return shared
	}
	return -1
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
