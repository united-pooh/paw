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
	if out[0].kind != entryWorkSegment || out[1].kind != entryAssistant || out[2].kind != entryWorkSegment {
		t.Fatalf("out kinds = %v/%v/%v", out[0].kind, out[1].kind, out[2].kind)
	}
	if !out[0].segment.hasReasoning {
		t.Fatal("first segment should have reasoning")
	}
	if out[2].segment.hasReasoning {
		t.Fatal("second segment is pure tools, must not have reasoning (Worked 命名)")
	}
	if out[2].segment.toolCalls != 1 {
		t.Fatalf("second segment toolCalls = %d, want 1", out[2].segment.toolCalls)
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

func TestFoldWorkSegmentsLiveFoldsOnlyTailRun(t *testing.T) {
	entries := []transcriptEntry{
		wsReasoning("old", 0),
		wsTool("Read", "ok", 2),
		wsAssistant("正文", 5),
		wsReasoning("current", 7),
		wsTool("Bash", "ok", 9),
	}
	out := foldWorkSegments(entries, foldLive)
	if len(out) != 4 {
		t.Fatalf("out len = %d, want 4 (old run stays raw)", len(out))
	}
	if out[0].kind != entryReasoning || out[1].kind != entryTool || out[2].kind != entryAssistant {
		t.Fatalf("non-tail entries folded in live mode: %#v", out)
	}
	tail := out[3]
	if tail.kind != entryWorkSegment || tail.segment == nil || !tail.segment.live {
		t.Fatalf("tail = %#v, want live work segment", tail)
	}
	if !tail.segment.hasReasoning || tail.segment.toolCalls != 1 {
		t.Fatalf("live segment stats = %#v", tail.segment)
	}
}

func TestFoldWorkSegmentsLiveNoFoldWhenTailIsBoundary(t *testing.T) {
	entries := []transcriptEntry{
		wsReasoning("think", 0),
		wsTool("Read", "ok", 2),
		wsAssistant("最终回答", 5),
	}
	out := foldWorkSegments(entries, foldLive)
	if len(out) != 3 || out[2].kind != entryAssistant {
		t.Fatalf("out = %#v, want all raw when tail is visible text", out)
	}
}

func TestFoldWorkSegmentsLiveNoFoldWhenTailIsInteractive(t *testing.T) {
	entries := []transcriptEntry{
		wsReasoning("think", 0),
		wsTool("select", "ok", 2),
	}
	out := foldWorkSegments(entries, foldLive)
	if len(out) != 2 || out[1].kind != entryTool {
		t.Fatalf("out = %#v, want all raw when tail is interactive tool", out)
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
	entries := []transcriptEntry{
		wsReasoning("推理正文", 0),
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
	// 映射：子条目指向自己的视图下标；标题行无 transcript 归属。
	if transcriptToView[0] != 1 || transcriptToView[1] != 2 {
		t.Fatalf("transcriptToView = %v", transcriptToView)
	}
	if viewToTranscript[0] != -1 || viewToTranscript[1] != 0 || viewToTranscript[2] != 1 {
		t.Fatalf("viewToTranscript = %v", viewToTranscript)
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
		if entry.kind == entryWorkSegment {
			segIdx = i
			break
		}
	}
	if segIdx < 0 {
		t.Fatalf("no folded segment in view: %#v", model.viewEntries)
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
