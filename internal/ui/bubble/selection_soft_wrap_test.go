package bubble

import (
	"strings"
	"testing"
)

// TestBuildTranscriptLineSnapshotsDetectsSoftWrapContinuation 验证
// buildTranscriptLineSnapshotsFromLines 能正确识别 wrapCompactAnnotated
// 插入的零宽标记（紧跟在 assistant gutter 之后），据此标记 continuation
// 字段，并把标记字符从 plain/styled 中剥离，不污染正文/渲染。
func TestBuildTranscriptLineSnapshotsDetectsSoftWrapContinuation(t *testing.T) {
	rawLines := []string{
		"ab",
		assistantNarrationGutter + string(softBreakSpace) + "cd",
		assistantNarrationGutter + string(softBreakGlue) + "ef",
		assistantNarrationGutter + "gh",
	}
	snapshots := buildTranscriptLineSnapshotsFromLines(rawLines)
	if len(snapshots) != 4 {
		t.Fatalf("want 4 snapshots, got %d", len(snapshots))
	}
	if snapshots[0].continuation != continuationNone {
		t.Fatalf("line0 continuation = %v, want none", snapshots[0].continuation)
	}
	if snapshots[1].continuation != continuationSpace {
		t.Fatalf("line1 continuation = %v, want space", snapshots[1].continuation)
	}
	if snapshots[1].plain != assistantNarrationGutter+"cd" {
		t.Fatalf("line1 plain = %q, want marker stripped", snapshots[1].plain)
	}
	if snapshots[2].continuation != continuationGlue {
		t.Fatalf("line2 continuation = %v, want glue", snapshots[2].continuation)
	}
	if snapshots[2].plain != assistantNarrationGutter+"ef" {
		t.Fatalf("line2 plain = %q, want marker stripped", snapshots[2].plain)
	}
	if snapshots[3].continuation != continuationNone {
		t.Fatalf("line3 continuation = %v, want none", snapshots[3].continuation)
	}
	if strings.ContainsRune(snapshots[1].styled, softBreakSpace) {
		t.Fatalf("line1 styled still contains marker: %q", snapshots[1].styled)
	}
	if strings.ContainsRune(snapshots[2].styled, softBreakGlue) {
		t.Fatalf("line2 styled still contains marker: %q", snapshots[2].styled)
	}
}

// TestJoinSelectedTranscriptLines 验证按 continuation 选择分隔符拼接：
// glue 不插入分隔符，space 插入一个空格，none 插入换行。
func TestJoinSelectedTranscriptLines(t *testing.T) {
	selected := []string{"abc", "def", "ghi", "jkl"}
	continuations := []continuationKind{continuationNone, continuationSpace, continuationGlue, continuationNone}
	got := joinSelectedTranscriptLines(selected, continuations)
	want := "abc defghi\njkl"
	if got != want {
		t.Fatalf("joinSelectedTranscriptLines() = %q, want %q", got, want)
	}
}

// TestSelectedTranscriptTextCollapsesAsciiSoftWrapToSpace 复现用户反馈场景：
// 一句没有真实换行的长英文句子，在终端宽度不够时被自动折成多屏幕行；
// 跨这些软换行行选中并复制，必须还原成原句（单个空格，没有多余换行）。
func TestSelectedTranscriptTextCollapsesAsciiSoftWrapToSpace(t *testing.T) {
	setTrueColorForTest(t)
	model := newRGBTestModel(t)
	model.ready = true
	model.width = 40
	model.height = 20
	model.relayout()
	sentence := "alpha beta gamma delta epsilon zeta eta theta iota kappa lambda mu nu xi"
	model.transcript = []transcriptEntry{{
		kind:  entryAssistant,
		title: "assistant",
		body:  sentence,
	}}
	model.refreshViewport()
	model.viewport.GotoTop()

	snapshots := model.transcriptLineSnapshots()
	if len(snapshots) < 3 {
		t.Fatalf("want wrapped into >=3 lines at width 40, got %d: %+v", len(snapshots), snapshots)
	}

	model.selectionActive = true
	model.selectionMode = selectionModeChar
	model.selectionStart = selectionPoint{row: 0, col: 0}
	model.selectionEnd = selectionPoint{row: len(snapshots) - 1, col: snapshots[len(snapshots)-1].width}

	got := model.selectedTranscriptText()
	want := glyphNarration + " " + sentence
	if got != want {
		t.Fatalf("selectedTranscriptText() = %q, want %q", got, want)
	}
}

// TestSelectedTranscriptTextGluesWideWordSoftWrap 复现中文长句被迫在词内
// 拆开的场景：软换行标记为 glue，复制时不应插入任何分隔符。
func TestSelectedTranscriptTextGluesWideWordSoftWrap(t *testing.T) {
	setTrueColorForTest(t)
	model := newRGBTestModel(t)
	model.ready = true
	model.width = 30
	model.height = 20
	model.relayout()
	sentence := "中文测试：这段文字没有空格，应该逐字填满每一行直到结束为止不再留白"
	model.transcript = []transcriptEntry{{
		kind:  entryAssistant,
		title: "assistant",
		body:  sentence,
	}}
	model.refreshViewport()
	model.viewport.GotoTop()

	snapshots := model.transcriptLineSnapshots()
	if len(snapshots) < 3 {
		t.Fatalf("want wrapped into >=3 lines, got %d: %+v", len(snapshots), snapshots)
	}

	model.selectionActive = true
	model.selectionMode = selectionModeChar
	model.selectionStart = selectionPoint{row: 0, col: 0}
	model.selectionEnd = selectionPoint{row: len(snapshots) - 1, col: snapshots[len(snapshots)-1].width}

	got := model.selectedTranscriptText()
	want := glyphNarration + " " + sentence
	if got != want {
		t.Fatalf("selectedTranscriptText() = %q, want %q", got, want)
	}
}

// TestSelectedTranscriptTextKeepsHardBreakBetweenParagraphs 验证原文真实的
// 段落换行（两个段落之间的空行）在复制时必须完整保留，不会被软换行的
// 合并逻辑误吞。
func TestSelectedTranscriptTextKeepsHardBreakBetweenParagraphs(t *testing.T) {
	setTrueColorForTest(t)
	model := newRGBTestModel(t)
	model.ready = true
	model.width = 40
	model.height = 20
	model.relayout()
	model.transcript = []transcriptEntry{{
		kind:  entryAssistant,
		title: "assistant",
		body:  "first paragraph line\n\nsecond paragraph line",
	}}
	model.refreshViewport()
	model.viewport.GotoTop()

	snapshots := model.transcriptLineSnapshots()
	model.selectionActive = true
	model.selectionMode = selectionModeChar
	model.selectionStart = selectionPoint{row: 0, col: 0}
	model.selectionEnd = selectionPoint{row: len(snapshots) - 1, col: snapshots[len(snapshots)-1].width}

	got := model.selectedTranscriptText()
	// 段落边界是真正的硬换行（continuationNone），非首行保留的 3 空格
	// assistant gutter 缩进是既有行为（独立于本次软换行修复），不在本次改动范围内。
	want := glyphNarration + " first paragraph line\n\n" + assistantNarrationGutter + "second paragraph line"
	if got != want {
		t.Fatalf("selectedTranscriptText() = %q, want %q", got, want)
	}
}

// TestSelectedTranscriptTextTrimsTrailingPadding 验证 lipgloss 为对齐/背景
// 补齐的行尾空格不会出现在复制结果里（修复 1，对所有消息类型通用）。
func TestSelectedTranscriptTextTrimsTrailingPadding(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = 80
	model.height = 12
	model.relayout()
	model.transcript = []transcriptEntry{{
		kind:  entryUser,
		title: "you",
		body:  "hi",
	}}
	model.refreshViewport()
	model.viewport.GotoTop()

	snapshots := model.transcriptLineSnapshots()
	model.selectionActive = true
	model.selectionMode = selectionModeLine
	// 跳过 2 空格 transcriptEntryGutter（既有的行首缩进，独立于本次修复），
	// 只验证行尾的 lipgloss 填充空格被裁剪掉。
	model.selectionStart = selectionPoint{row: 0, col: len(transcriptEntryGutter)}
	model.selectionEnd = selectionPoint{row: 0, col: snapshots[0].width}

	got := model.selectedTranscriptText()
	if got != "hi" {
		t.Fatalf("selectedTranscriptText() = %q, want %q (no trailing padding)", got, "hi")
	}
}
