package bubble

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
)

var wsBase = time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)

func wsAt(secs int) time.Time { return wsBase.Add(time.Duration(secs) * time.Second) }

func wsReasoning(body string, secs int) transcriptEntry {
	return transcriptEntry{kind: entryReasoning, body: body, createdAt: wsAt(secs)}
}

func wsThinking(body string, secs int) transcriptEntry {
	return transcriptEntry{kind: entryThinking, body: body, createdAt: wsAt(secs)}
}

func wsTool(name, status string, secs int) transcriptEntry {
	return transcriptEntry{kind: entryTool, toolName: name, toolStatus: status, isError: status == "error", createdAt: wsAt(secs)}
}

func wsAssistant(body string, secs int) transcriptEntry {
	return transcriptEntry{kind: entryAssistant, body: body, createdAt: wsAt(secs)}
}

func wsUser(body string, secs int) transcriptEntry {
	return transcriptEntry{kind: entryUser, body: body, createdAt: wsAt(secs)}
}

// 一个连续运行聚合为一个工作段条目：含思考命名为 Thought，统计工具数。
func TestFoldWorkSegmentsCollectsReasoningAndTools(t *testing.T) {
	entries := []transcriptEntry{
		wsReasoning("plan", 0),
		wsTool("Read", "ok", 3),
		wsTool("Bash", "ok", 9),
	}
	out := foldWorkSegments(entries, foldResting)
	if len(out) != 1 || out[0].kind != entryWorkSegment {
		t.Fatalf("out = %#v, want single work segment", out)
	}
	data := out[0].segment
	if data == nil || data.toolCalls != 2 || data.failed != 0 || !data.hasReasoning {
		t.Fatalf("segment stats = %#v", data)
	}
	if !data.startedAt.Equal(wsAt(0)) || !data.finishedAt.Equal(wsAt(9)) {
		t.Fatalf("segment time range = %v → %v", data.startedAt, data.finishedAt)
	}
	if len(data.children) != 3 {
		t.Fatalf("children = %d, want 3", len(data.children))
	}
}

// 思考之间只有工具调用、没有正文输出时，多段思考合并为一行；响应时间回
// 填到该运行（唯一/最后一段）的标题尾部。
func TestFoldWorkSegmentsMergesThoughtsLinkedOnlyByTools(t *testing.T) {
	entries := []transcriptEntry{
		wsReasoning("first", 0),
		wsTool("Read", "ok", 2),
		wsReasoning("second", 4),
		wsAssistant("回答", 6),
	}
	out := foldWorkSegments(entries, foldResting)
	if len(out) != 2 || out[0].kind != entryWorkSegment || out[1].kind != entryAssistant {
		t.Fatalf("out = %#v, want merged segment followed by response", out)
	}
	data := out[0].segment
	if !data.hasReasoning || data.toolCalls != 1 || len(data.children) != 3 {
		t.Fatalf("merged segment = %#v, want both thoughts + tool in one segment", data)
	}
	if !out[1].responseClock {
		t.Fatal("response following a run must carry the response clock")
	}
}

func TestFoldWorkSegmentsCutsOnVisibleText(t *testing.T) {
	entries := []transcriptEntry{
		wsReasoning("first", 0),
		wsTool("Read", "ok", 2),
		wsAssistant("中间正文", 5),
		wsTool("Write", "ok", 8),
	}
	out := foldWorkSegments(entries, foldResting)
	if len(out) != 3 {
		t.Fatalf("out len = %d, want 3 (segment, assistant, segment)", len(out))
	}
	// Thought 保留在真实推理位置（响应之前）；响应时间记在该运行的段标题尾部。
	if out[0].kind != entryWorkSegment || out[1].kind != entryAssistant || out[2].kind != entryWorkSegment {
		t.Fatalf("out kinds = %v/%v/%v", out[0].kind, out[1].kind, out[2].kind)
	}
	if !out[0].segment.hasReasoning {
		t.Fatal("first segment should have reasoning")
	}
	if !out[1].responseClock {
		t.Fatal("response following a run must carry the response clock")
	}
	if out[2].segment.hasReasoning {
		t.Fatal("trailing segment is pure tools, must not have reasoning (Worked 命名)")
	}
	if out[2].segment.toolCalls != 1 {
		t.Fatalf("trailing segment toolCalls = %d, want 1", out[2].segment.toolCalls)
	}
}

func TestFoldWorkSegmentsCutsOnUserAndSystem(t *testing.T) {
	entries := []transcriptEntry{
		wsUser("问题", 0),
		wsReasoning("think", 1),
		{kind: entrySystem, body: "auto-continue", createdAt: wsAt(2)},
		wsTool("Read", "ok", 3),
	}
	out := foldWorkSegments(entries, foldResting)
	if len(out) != 4 || out[1].kind != entryWorkSegment || out[3].kind != entryWorkSegment {
		t.Fatalf("out = %#v", out)
	}
}

// workSegmentKey 不含会增长的计数：live 段在流式中 children/toolCalls/failed
// 持续增长，key 必须保持稳定，否则展开态在段成长时丢失。
func TestWorkSegmentKeyStaysStableAsLiveSegmentGrows(t *testing.T) {
	data := &workSegmentData{hasReasoning: true, startedAt: wsAt(0)}
	key := workSegmentKey(data)
	data.children = append(data.children, wsReasoning("plan", 0), wsTool("Read", "ok", 1))
	data.toolCalls = 1
	data.failed = 0
	if got := workSegmentKey(data); got != key {
		t.Fatalf("key changed as segment grew: %q → %q", key, got)
	}
	data.toolCalls = 2
	data.failed = 1
	if got := workSegmentKey(data); got != key {
		t.Fatalf("key changed after stats update: %q → %q", key, got)
	}
}

// foldLive 下尾部 live 段支持手动展开：expandedFor 命中时平铺为标题行 +
// 子条目（与 resting 展开同构），否则保持折叠 live 块。
func TestFoldLiveSegmentExpandsWhenExpandedForMatches(t *testing.T) {
	entries := []transcriptEntry{
		wsAssistant("done", 0), // 前缀边界：尾部 run 从 reasoning 开始
		wsReasoning("plan", 1),
		wsTool("Read", "ok", 3),
	}
	// 折叠态：尾部为单个 live 段。
	view, _, _ := foldWorkSegmentsView(entries, foldLive, nil)
	if len(view) != 2 || view[1].kind != entryWorkSegment || !view[1].segment.live || view[1].segment.header {
		t.Fatalf("collapsed live view = %#v, want assistant + folded live segment", view)
	}
	// 展开态：标题行 + reasoning + 工具子条目平铺。
	expanded := func(data *workSegmentData) bool { return data.live }
	view, _, viewToTranscript := foldWorkSegmentsView(entries, foldLive, expanded)
	if len(view) != 4 {
		t.Fatalf("expanded live view len = %d, want 4 (assistant + header + 2 children)", len(view))
	}
	header := view[1]
	if header.kind != entryWorkSegment || !header.segment.header || !header.segment.live {
		t.Fatalf("expanded header = %#v, want live header segment", header)
	}
	if view[2].kind != entryReasoning || view[3].kind != entryTool {
		t.Fatalf("expanded children kinds = %v/%v", view[2].kind, view[3].kind)
	}
	// 子条目映射回各自的 transcript 下标（hover/inspect 复用）。
	if viewToTranscript[2] != 1 || viewToTranscript[3] != 2 {
		t.Fatalf("child viewToTranscript = %#v", viewToTranscript)
	}
}

// live 展开态标题只渲染实时标题行（滚动窗口交给平铺的子条目）。
func TestRenderWorkSegmentLiveHeaderOmitsWindow(t *testing.T) {
	data := &workSegmentData{
		hasReasoning: true,
		live:         true,
		header:       true,
		startedAt:    wsAt(0),
		children:     []transcriptEntry{wsReasoning("scrolling content marker", 0)},
	}
	rendered := ansi.Strip(renderWorkSegmentEntry(transcriptEntry{kind: entryWorkSegment, segment: data}, 80, wsAt(5), false))
	if !strings.Contains(rendered, "Thoughts") {
		t.Fatalf("live header must render title:\n%s", rendered)
	}
	if strings.Contains(rendered, "scrolling content marker") {
		t.Fatalf("header must not render live window children:\n%s", rendered)
	}
}

func TestFoldWorkSegmentsTrailsResponse(t *testing.T) {
	entries := []transcriptEntry{
		wsUser("读一下这篇文章", 0),
		wsReasoning("think", 1),
		wsTool("WebFetch", "ok", 4),
		wsAssistant("GeoRA: Geometry-Aware ...", 18),
	}
	out := foldWorkSegments(entries, foldResting)
	if len(out) != 3 || out[0].kind != entryUser || out[1].kind != entryWorkSegment || out[2].kind != entryAssistant {
		t.Fatalf("out = %#v, want user → thought → response", out)
	}
	// 响应时间在响应结束的下一行，而不是段标题尾部。
	if !out[2].responseClock {
		t.Fatal("response following a run must carry the response clock")
	}
	rendered := ansi.Strip(renderEntryAt(out[2], 80, wsAt(20), false))
	bodyRow := strings.Index(rendered, "GeoRA")
	// 时钟按本地时区显示（wsAt 是 UTC 基准，换算到本地后再断言，时区无关）。
	localClock := wsAt(18).Local().Format("15:04")
	clockRow := strings.Index(rendered, localClock)
	if bodyRow < 0 || clockRow < bodyRow {
		t.Fatalf("clock must render on the line after the response:\n%s", rendered)
	}
	if title := workSegmentTitle(out[1].segment, wsAt(20)); strings.Contains(title, localClock) {
		t.Fatalf("segment title must not carry the clock: %q", title)
	}
}

func TestFoldWorkSegmentsBookkeepingSkippedNotCut(t *testing.T) {
	entries := []transcriptEntry{
		wsReasoning("plan", 0),
		wsTool("update_todo", "ok", 1),
		wsTool("Read", "ok", 4),
	}
	out := foldWorkSegments(entries, foldResting)
	if len(out) != 1 {
		t.Fatalf("out len = %d, want 1 (bookkeeping must not cut the segment)", len(out))
	}
	data := out[0].segment
	if data.toolCalls != 1 {
		t.Fatalf("toolCalls = %d, want 1 (update_todo excluded)", data.toolCalls)
	}
	if len(data.children) != 3 {
		t.Fatalf("children = %d, want 3 (update_todo kept in children)", len(data.children))
	}
}

func TestFoldWorkSegmentsBookkeepingOnlyRunSuppressed(t *testing.T) {
	entries := []transcriptEntry{
		wsAssistant("回答", 0),
		wsTool("update_todo", "ok", 1),
	}
	out := foldWorkSegments(entries, foldResting)
	if len(out) != 2 || out[1].kind != entryTool {
		t.Fatalf("out = %#v, want bookkeeping entry left raw (no block)", out)
	}
}

func TestFoldWorkSegmentsInteractiveCutsAndStaysRaw(t *testing.T) {
	entries := []transcriptEntry{
		wsReasoning("plan", 0),
		wsTool("Read", "ok", 2),
		wsTool("question", "ok", 4),
		wsTool("Write", "ok", 6),
	}
	out := foldWorkSegments(entries, foldResting)
	if len(out) != 3 {
		t.Fatalf("out len = %d, want 3 (segment, question raw, segment)", len(out))
	}
	if out[1].kind != entryTool || out[1].toolName != "question" {
		t.Fatalf("interactive entry folded away: %#v", out[1])
	}
	if out[0].segment.toolCalls != 1 || out[2].segment.toolCalls != 1 {
		t.Fatalf("segment stats = %#v / %#v", out[0].segment, out[2].segment)
	}
}

func TestFoldWorkSegmentsCountsFailedExcludingBookkeeping(t *testing.T) {
	entries := []transcriptEntry{
		wsReasoning("plan", 0),
		wsTool("Bash", "error", 2),
		wsTool("Read", "ok", 5),
		wsTool("update_todo", "error", 7),
	}
	out := foldWorkSegments(entries, foldResting)
	if len(out) != 1 {
		t.Fatalf("out len = %d", len(out))
	}
	data := out[0].segment
	if data.toolCalls != 2 || data.failed != 1 {
		t.Fatalf("stats = %#v, want toolCalls=2 failed=1 (bookkeeping error excluded)", data)
	}
}

// live 模式下已完成的运行与 resting 一致折叠（Thought 保留在真实推理位
// 置、响应时间记在段标题尾部），只有尾部仍在进行的运行保留为 live 实时块。
func TestFoldWorkSegmentsLiveFoldsCompletedRunsAtNaturalPosition(t *testing.T) {
	entries := []transcriptEntry{
		wsReasoning("old", 0),
		wsTool("Read", "ok", 2),
		wsAssistant("正文", 5),
		wsReasoning("current", 7),
		wsTool("Bash", "ok", 9),
	}
	out := foldWorkSegments(entries, foldLive)
	if len(out) != 3 {
		t.Fatalf("out len = %d, want 3 (completed segment, response, live tail)", len(out))
	}
	completed := out[0]
	if completed.kind != entryWorkSegment || completed.segment == nil || completed.segment.live {
		t.Fatalf("out[0] = %#v, want resting segment for completed run", completed)
	}
	if !completed.segment.hasReasoning || completed.segment.toolCalls != 1 {
		t.Fatalf("completed segment stats = %#v", completed.segment)
	}
	if !out[1].responseClock {
		t.Fatal("response following a run must carry the response clock")
	}
	if out[1].kind != entryAssistant {
		t.Fatalf("out[1] = %#v, want response after its thoughts", out[1])
	}
	tail := out[2]
	if tail.kind != entryWorkSegment || tail.segment == nil || !tail.segment.live {
		t.Fatalf("tail = %#v, want live work segment", tail)
	}
	if !tail.segment.hasReasoning || tail.segment.toolCalls != 1 {
		t.Fatalf("live segment stats = %#v", tail.segment)
	}
}

// 尾部是可见正文时整个前缀按 resting 语义折叠：Thought 保留在响应之前。
func TestFoldWorkSegmentsLiveFoldsAllWhenTailIsBoundary(t *testing.T) {
	entries := []transcriptEntry{
		wsReasoning("think", 0),
		wsTool("Read", "ok", 2),
		wsAssistant("最终回答", 5),
	}
	out := foldWorkSegments(entries, foldLive)
	if len(out) != 2 || out[0].kind != entryWorkSegment || out[1].kind != entryAssistant {
		t.Fatalf("out = %#v, want folded segment followed by response", out)
	}
	if out[0].segment.live {
		t.Fatal("trailing boundary means no live segment")
	}
}

func TestFoldWorkSegmentsLiveKeepsInteractiveTailRaw(t *testing.T) {
	entries := []transcriptEntry{
		wsReasoning("think", 0),
		wsTool("select", "ok", 2),
	}
	out := foldWorkSegments(entries, foldLive)
	if len(out) != 2 || out[1].kind != entryTool {
		t.Fatalf("out = %#v, want interactive tool left raw", out)
	}
	if out[0].kind != entryWorkSegment || out[0].segment.live {
		t.Fatalf("out[0] = %#v, want completed reasoning folded as resting segment", out[0])
	}
}

func TestFoldWorkSegmentsLiveSuppressesBookkeepingOnlyTail(t *testing.T) {
	entries := []transcriptEntry{
		wsAssistant("回答", 0),
		wsTool("update_todo", "ok", 1),
	}
	out := foldWorkSegments(entries, foldLive)
	if len(out) != 2 || out[1].kind != entryTool {
		t.Fatalf("out = %#v, want bookkeeping tail left raw", out)
	}
}

func TestFoldWorkSegmentsThinkingCollectable(t *testing.T) {
	entries := []transcriptEntry{
		wsThinking("legacy thinking", 0),
		wsTool("Read", "ok", 2),
	}
	out := foldWorkSegments(entries, foldResting)
	if len(out) != 1 || !out[0].segment.hasReasoning {
		t.Fatalf("out = %#v, want legacy thinking collected as reasoning", out)
	}
}

func TestWorkSegmentTitle(t *testing.T) {
	at := wsAt(200)
	cases := []struct {
		name string
		data *workSegmentData
		want string
	}{
		{"reasoning+tools+failed", &workSegmentData{hasReasoning: true, toolCalls: 15, failed: 1, startedAt: wsAt(0), finishedAt: wsAt(135)}, "Thought for 2m 15s · 15 tool calls · 1 failed"},
		{"pure reasoning", &workSegmentData{hasReasoning: true, startedAt: wsAt(0), finishedAt: wsAt(12)}, "Thought for 12 s"},
		{"pure tools", &workSegmentData{toolCalls: 5, startedAt: wsAt(0), finishedAt: wsAt(48)}, "Worked for 48 s · 5 tool calls"},
		{"live uses at", &workSegmentData{hasReasoning: true, toolCalls: 2, startedAt: wsAt(0), finishedAt: wsAt(10), live: true}, "Thought for 3m 20s · 2 tool calls"},
		{"no count when zero", &workSegmentData{hasReasoning: true, startedAt: wsAt(0), finishedAt: wsAt(3)}, "Thought for 3 s"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if got := workSegmentTitle(tt.data, at); got != tt.want {
				t.Fatalf("workSegmentTitle() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestWorkSegmentLiveTitle(t *testing.T) {
	data := &workSegmentData{hasReasoning: true, toolCalls: 7, startedAt: wsAt(0), live: true}
	got := workSegmentLiveTitle(data, wsAt(83))
	if got != "Thoughts · 1m 23s · 7 tool calls" {
		t.Fatalf("live title = %q", got)
	}
	empty := &workSegmentData{startedAt: wsAt(0), live: true}
	if got := workSegmentLiveTitle(empty, wsAt(0)); got != "Thoughts · 1 s" {
		t.Fatalf("live title without tools = %q", got)
	}
}

func TestFormatResponseClock(t *testing.T) {
	at := time.Date(2026, 8, 25, 9, 30, 0, 0, time.Local)
	cases := []struct {
		name string
		t    time.Time
		want string
	}{
		{"zero", time.Time{}, ""},
		{"today", time.Date(2026, 8, 25, 14, 32, 0, 0, time.Local), "14:32"},
		{"same year", time.Date(2026, 8, 24, 14, 32, 0, 0, time.Local), "8月24日 14:32"},
		{"other year", time.Date(2025, 8, 24, 14, 32, 0, 0, time.Local), "2025年8月24日 14:32"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatResponseClock(tt.t, at); got != tt.want {
				t.Fatalf("formatResponseClock() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFormatSegmentDuration(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{500 * time.Millisecond, "1 s"},
		{12 * time.Second, "12 s"},
		{59 * time.Second, "59 s"},
		{60 * time.Second, "1m"},
		{135 * time.Second, "2m 15s"},
		{3600 * time.Second, "1h"},
		{3780 * time.Second, "1h 3m"},
	}
	for _, tt := range cases {
		if got := formatSegmentDuration(tt.d); got != tt.want {
			t.Errorf("formatSegmentDuration(%s) = %q, want %q", tt.d, got, tt.want)
		}
	}
}

func TestWorkSegmentTitleStylingIsNeutral(t *testing.T) {
	// 失败只在标题追加中性计数，不含任何 error 标记词。
	data := &workSegmentData{hasReasoning: true, toolCalls: 3, failed: 2, startedAt: wsAt(0), finishedAt: wsAt(10)}
	title := workSegmentTitle(data, wsAt(10))
	if !strings.Contains(title, "· 2 failed") {
		t.Fatalf("title = %q, want neutral failed count", title)
	}
	if strings.Contains(title, "!") || strings.Contains(title, "error") {
		t.Fatalf("title = %q, must stay neutral", title)
	}
}

func wsToolWithBody(name, status, body string, secs int) transcriptEntry {
	entry := wsTool(name, status, secs)
	entry.body = body
	entry.toolTarget = "go.mod"
	return entry
}

func TestRenderWorkSegmentEntryCollapsed(t *testing.T) {
	seg, ok := buildWorkSegmentEntry([]transcriptEntry{
		wsReasoning("思考内容不应显示", 0),
		wsToolWithBody("Read", "ok", "Read ok go.mod", 3),
	})
	if !ok {
		t.Fatal("want segment")
	}
	out := ansi.Strip(renderWorkSegmentEntry(seg, 80, wsAt(10), false))
	if !strings.Contains(out, "Thought for") {
		t.Fatalf("collapsed render = %q, want title", out)
	}
	if strings.Contains(out, "思考内容不应显示") || strings.Contains(out, "go.mod") {
		t.Fatalf("collapsed render leaked children: %q", out)
	}
}

func TestFoldWorkSegmentsViewExpandedInlinesChildren(t *testing.T) {
	started, finished := wsAt(0), wsAt(2)
	entries := []transcriptEntry{
		{
			kind:                entryReasoning,
			body:                "推理正文",
			reasoningStartedAt:  &started,
			reasoningFinishedAt: &finished,
			createdAt:           wsAt(0),
		},
		wsToolWithBody("Read", "ok", "Read ok go.mod", 3),
	}
	view, transcriptToView, viewToTranscript := foldWorkSegmentsView(entries, foldResting, func(*workSegmentData) bool { return true })
	if len(view) != 3 {
		t.Fatalf("view len = %d, want header + 2 inline children", len(view))
	}
	header := view[0]
	if header.kind != entryWorkSegment || header.segment == nil || !header.segment.header {
		t.Fatalf("header = %#v, want segment header", header)
	}
	// 标题行只渲染标题。
	out := ansi.Strip(renderWorkSegmentEntry(header, 80, wsAt(10), false))
	if !strings.Contains(out, "Thought for") || strings.Contains(out, "推理正文") {
		t.Fatalf("header render = %q", out)
	}
	// reasoning 子条目强制内容展开（不嵌套折叠标题）；工具子条目平铺防重组。
	if view[1].kind != entryReasoning || !view[1].reasoningExpansionSet || !view[1].reasoningExpanded {
		t.Fatalf("inline reasoning = %#v", view[1])
	}
	if view[2].kind != entryTool || !view[2].toolGroupPending {
		t.Fatalf("inline tool = %#v", view[2])
	}
	// 内联的 reasoning 子条目只渲染正文，不嵌套自身的 "Thought for" 标题
	// （标题只属于段头一行）。
	childOut := ansi.Strip(renderEntryAt(view[1], 80, wsAt(10), false))
	if !strings.Contains(childOut, "推理正文") || strings.Contains(childOut, "Thought for") {
		t.Fatalf("inline reasoning render = %q, want content without nested title", childOut)
	}
	// 映射：子条目指向自己的视图下标；标题行无 transcript 归属。
	if transcriptToView[0] != 1 || transcriptToView[1] != 2 {
		t.Fatalf("transcriptToView = %v", transcriptToView)
	}
	if viewToTranscript[0] != -1 || viewToTranscript[1] != 0 || viewToTranscript[2] != 1 {
		t.Fatalf("viewToTranscript = %v", viewToTranscript)
	}
}

func TestWorkSegmentSpanUsesReasoningWindow(t *testing.T) {
	started, finished := wsAt(0), wsAt(30)
	// 网关缓冲场景：思考从请求发出起算（0s），条目落库时刻已到 30s；
	// 段时长必须取真实窗口而非 createdAt 的零宽跨度（否则显示 1s）。
	seg, ok := buildWorkSegmentEntry([]transcriptEntry{{
		kind:                entryReasoning,
		body:                "buffered thinking",
		reasoningStartedAt:  &started,
		reasoningFinishedAt: &finished,
		createdAt:           wsAt(30),
	}})
	if !ok {
		t.Fatal("want segment")
	}
	if got := workSegmentTitle(seg.segment, wsAt(40)); !strings.Contains(got, "Thought for 30 s") {
		t.Fatalf("title = %q, want reasoning window duration", got)
	}
}

// 回归：正文开始流出后，已完成的 Thought 折叠在其真实推理位置（响应之
// 前），过期的 live 展开块不得滞留（旧实现复用缓存行，把 "● Thoughts +
// 正文" 滞留在视图中）。
func TestLiveThoughtCollapsesAtNaturalPositionOnceTextStreams(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = 80
	model.height = 20
	started := time.Unix(100, 0)
	model.cursorFrameAt = started
	model.relayout()
	if !model.queryGuard.StartModel() {
		t.Fatal("StartModel failed")
	}

	next, _ := model.Update(assistantPartMsg{lifecycle: "start", blockIndex: 0, partType: "reasoning"})
	model = next.(appModel)
	next, _ = model.Update(assistantPartMsg{lifecycle: "delta", blockIndex: 0, partType: "reasoning", delta: "thinking out loud"})
	model = next.(appModel)
	model.refreshViewport()
	if got := ansi.Strip(model.viewport.View()); !strings.Contains(got, "Thoughts") || !strings.Contains(got, "thinking out loud") {
		t.Fatalf("live reasoning window not visible while streaming:\n%s", got)
	}

	model.cursorFrameAt = started.Add(3 * time.Second)
	next, _ = model.Update(assistantPartMsg{lifecycle: "end", blockIndex: 0, partType: "reasoning"})
	model = next.(appModel)
	next, _ = model.Update(assistantPartMsg{lifecycle: "delta", blockIndex: 1, partType: "text", delta: "你好喵～\n"})
	model = next.(appModel)
	model.refreshViewport()

	got := ansi.Strip(model.viewport.View())
	textRow := strings.Index(got, "你好喵～")
	thoughtRow := strings.Index(got, "Thought for 3 s")
	if textRow < 0 || thoughtRow < 0 {
		t.Fatalf("response or folded thought missing mid-turn:\n%s", got)
	}
	if thoughtRow > textRow {
		t.Fatalf("thought must stay at its natural position (before the response):\n%s", got)
	}
	// 响应时间在响应结束的下一行，而不是段标题上。
	if clockRow := strings.Index(got, "08:01"); clockRow < 0 || clockRow < textRow {
		t.Fatalf("response clock must render on the line after the response:\n%s", got)
	}
	if strings.Contains(got, "Thoughts ·") || strings.Contains(got, "thinking out loud") {
		t.Fatalf("stale live thought block survived the response:\n%s", got)
	}
}

// 落库时间戳是 UTC：formatResponseClock 必须转本地时区显示，否则 resume
// 后的响应时钟全部显示 0 时区。
func TestFormatResponseClockNormalizesUTCToLocal(t *testing.T) {
	at := time.Date(2026, 8, 25, 9, 30, 0, 0, time.Local)
	utc := time.Date(2026, 8, 25, 1, 30, 0, 0, time.UTC)
	want := utc.Local().Format("15:04")
	if got := formatResponseClock(utc, at); got != want {
		t.Fatalf("formatResponseClock(UTC) = %q, want local %q", got, want)
	}
}

func TestWorkSegmentSeparator(t *testing.T) {
	cases := []struct {
		prev, cur entryKind
		want      string
	}{
		{entryAssistant, entryWorkSegment, "\n"},
		{entryWorkSegment, entryAssistant, "\n"},
		{entryWorkSegment, entryWorkSegment, "\n"},
		{entryUser, entryWorkSegment, "\n\n"},
		{entryWorkSegment, entryUser, "\n\n"},
	}
	for _, tt := range cases {
		if got := transcriptEntrySeparator(tt.prev, tt.cur); got != tt.want {
			t.Errorf("separator(%v, %v) = %q, want %q", tt.prev, tt.cur, got, tt.want)
		}
	}
}

func TestWorkSegmentRenderKeySensitive(t *testing.T) {
	seg, ok := buildWorkSegmentEntry([]transcriptEntry{
		wsReasoning("think", 0),
		wsToolWithBody("Read", "ok", "Read ok go.mod", 3),
	})
	if !ok {
		t.Fatal("want segment")
	}
	base := transcriptRenderKey(seg, 80, wsAt(10), false)

	modified := seg
	modifiedData := *seg.segment
	modifiedData.children = append([]transcriptEntry(nil), seg.segment.children...)
	modifiedData.children[1].body = "Read ok main.go changed"
	modified.segment = &modifiedData
	if transcriptRenderKey(modified, 80, wsAt(10), false) == base {
		t.Fatal("render key did not change after child change")
	}

	live := seg
	liveData := *seg.segment
	liveData.live = true
	live.segment = &liveData
	if transcriptRenderKey(live, 80, wsAt(10), false) == transcriptRenderKey(live, 80, wsAt(11), false) {
		t.Fatal("live render key did not tick with seconds")
	}
}

func TestToggleWorkSegmentExpansionRenders(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = 80
	model.height = 24
	model.relayout()
	model.transcript = []transcriptEntry{
		{kind: entryUser, body: "q", createdAt: wsAt(0)},
		wsReasoning("think", 1),
		wsToolWithBody("Read", "ok", "Read ok go.mod", 3),
		wsAssistant("done", 5),
	}
	model.refreshViewport()
	segIdx := -1
	for i, entry := range model.viewEntries {
		if entry.kind == entryWorkSegment && entry.segment != nil && entry.segment.toolCalls > 0 {
			segIdx = i
			break
		}
	}
	if segIdx < 0 {
		t.Fatalf("no folded tool segment in view: %#v", model.viewEntries)
	}
	if !model.toggleWorkSegmentExpansion(segIdx) {
		t.Fatal("toggle returned false")
	}
	hasHeader, hasInlineTool := false, false
	for _, entry := range model.viewEntries {
		if entry.kind == entryWorkSegment && entry.segment != nil && entry.segment.header {
			hasHeader = true
		}
		if entry.kind == entryTool && entry.toolGroupPending {
			hasInlineTool = true
		}
	}
	if !hasHeader || !hasInlineTool {
		t.Fatalf("view after expand = %#v", model.viewEntries)
	}
	if !model.toggleWorkSegmentExpansion(segIdx) {
		t.Fatal("collapse returned false")
	}
}

func TestEnsureTranscriptLinesOutOfRangeDirtyIndexRebuilds(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = 80
	model.relayout()
	model.transcript = []transcriptEntry{
		{kind: entryUser, body: "q", createdAt: wsAt(0)},
		wsAssistant("a", 1),
	}
	model.refreshViewport()
	// 越界失效下标：原 panic 路径（invalidate 清空缓存后直接渲染）。
	model.transcriptInvalidation.markFrom(len(model.transcript) + 5)
	changed, _, _ := model.ensureTranscriptLinesAt(80, false, model.animationNow())
	if !changed {
		t.Fatal("want full rebuild for out-of-range dirty index")
	}
}
