package bubble

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"paw/internal/message"
)

// TestBufferedReasoningDurationMeasuredFromTurnStart 验证思考内容被网关缓冲、
// 最后才一次性到达时，"Thought for N s" 从请求发出（turn 开始）计时而非首条
// delta 到达时刻。回归：旧实现起点≈终点，任何缓冲下发的思考都显示 1s。
func TestBufferedReasoningDurationMeasuredFromTurnStart(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = 80
	model.height = 20
	turnStart := time.Unix(1000, 0)
	model.turnStartedAt = turnStart
	// 请求发出 30 秒后，被缓冲的思考才随正文前一次性到达。
	model.cursorFrameAt = turnStart.Add(30 * time.Second)
	model.relayout()

	next, _ := model.Update(thinkingDeltaMsg("buffered reasoning"))
	model = next.(appModel)
	next, _ = model.Update(assistantDeltaMsg("answer"))
	model = next.(appModel)
	model.refreshViewport()

	got := ansi.Strip(model.viewport.View())
	if !strings.Contains(got, "Thought for 30 s") {
		t.Fatalf("buffered reasoning duration did not measure from turn start:\n%s", got)
	}
}

// TestReasoningWindowStartPrefersLatestActivity 验证工具链后续思考块的计时
// 起点取最近一次模型可见活动（≈下一次请求发出），不回溯到 turn 开始。
func TestReasoningWindowStartPrefersLatestActivity(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	turnStart := time.Unix(2000, 0)
	toolResultAt := turnStart.Add(60 * time.Second)
	model.turnStartedAt = turnStart
	model.lastModelVisibleActivityAt = toolResultAt

	now := toolResultAt.Add(5 * time.Second)
	if got := model.reasoningWindowStart(now); !got.Equal(toolResultAt) {
		t.Fatalf("reasoningWindowStart = %v, want latest activity %v", got, toolResultAt)
	}

	// 上一 turn 的残留活动不污染新 turn：turn 开始后起点被 turnStartedAt 覆盖。
	model.turnStartedAt = toolResultAt.Add(70 * time.Second)
	if got := model.reasoningWindowStart(model.turnStartedAt.Add(time.Second)); !got.Equal(model.turnStartedAt) {
		t.Fatalf("reasoningWindowStart = %v, want new turn start %v", got, model.turnStartedAt)
	}

	// 无任何计时锚点时回退为当前时刻。
	empty := appModel{}
	if got := empty.reasoningWindowStart(now); !got.Equal(now) {
		t.Fatalf("reasoningWindowStart without anchors = %v, want now %v", got, now)
	}
}

func TestBubbleReasoningReceiverRoutesLifecycle(t *testing.T) {
	output := New()
	var received []tea.Msg
	output.sendMsg = func(msg tea.Msg) {
		received = append(received, msg)
	}

	if err := output.OnReasoningStart(7, true); err != nil {
		t.Fatal(err)
	}
	if err := output.OnReasoningDelta(7, "private plan"); err != nil {
		t.Fatal(err)
	}
	if err := output.OnReasoningEnd(7); err != nil {
		t.Fatal(err)
	}

	if len(received) != 3 {
		t.Fatalf("received %d messages, want 3", len(received))
	}
	start, ok := received[0].(assistantPartMsg)
	if !ok || start.lifecycle != "start" || start.blockIndex != 7 || start.partType != "reasoning" || !start.redacted {
		t.Fatalf("start message = %#v", received[0])
	}
	delta, ok := received[1].(assistantPartMsg)
	if !ok || delta.lifecycle != "delta" || delta.blockIndex != 7 || delta.delta != "private plan" {
		t.Fatalf("delta message = %#v", received[1])
	}
	end, ok := received[2].(assistantPartMsg)
	if !ok || end.lifecycle != "end" || end.blockIndex != 7 {
		t.Fatalf("end message = %#v", received[2])
	}
}

func TestLiveReasoningIsVisibleThenFoldsByDefault(t *testing.T) {
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

	next, _ := model.Update(assistantPartMsg{lifecycle: "start", blockIndex: 3, partType: "reasoning"})
	model = next.(appModel)
	next, _ = model.Update(assistantPartMsg{lifecycle: "delta", blockIndex: 3, partType: "reasoning", delta: "checking the implementation"})
	model = next.(appModel)
	model.refreshViewport()
	if got := ansi.Strip(model.viewport.View()); !strings.Contains(got, "checking the implementation") {
		t.Fatalf("live reasoning is hidden by default:\n%s", got)
	}

	model.cursorFrameAt = started.Add(2 * time.Second)
	next, _ = model.Update(assistantPartMsg{lifecycle: "end", blockIndex: 3, partType: "reasoning"})
	model = next.(appModel)
	// 回合结束（resting）：思考块折叠为时长标题。
	model.queryGuard.FinishModel()
	model.refreshViewport()
	got := ansi.Strip(model.viewport.View())
	if !strings.Contains(got, "Thought for 2 s") {
		t.Fatalf("completed reasoning did not fold to a duration title:\n%s", got)
	}
	if strings.Contains(got, "checking the implementation") {
		t.Fatalf("completed reasoning body stayed expanded by default:\n%s", got)
	}
}

func TestCtrlOTogglesCompletedReasoningBodies(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = 80
	model.height = 20
	started := time.Unix(200, 0)
	finished := started.Add(3 * time.Second)
	model.transcript = []transcriptEntry{{
		kind:                entryReasoning,
		title:               "reasoning",
		body:                "restored reasoning body",
		reasoningPartIndex:  1,
		reasoningStartedAt:  &started,
		reasoningFinishedAt: &finished,
		createdAt:           started,
	}}
	model.relayout()
	model.refreshViewport()

	next, _ := model.Update(tea.KeyMsg{Type: tea.KeyCtrlO})
	model = next.(appModel)
	got := ansi.Strip(model.viewport.View())
	if !strings.Contains(got, "restored reasoning body") {
		t.Fatalf("Ctrl+O did not expand completed reasoning:\n%s", got)
	}
	// 展开后思考块只渲染正文：整个视图只剩段头一行 Thought 摘要，
	// 块自身不再重复第二行标题。
	if occurrences := strings.Count(got, "Thought for"); occurrences != 1 {
		t.Fatalf("expanded reasoning renders %d Thought titles, want exactly one segment header:\n%s", occurrences, got)
	}

	next, _ = model.Update(tea.KeyMsg{Type: tea.KeyCtrlO})
	model = next.(appModel)
	got = ansi.Strip(model.viewport.View())
	if !strings.Contains(got, "Thought for 3 s") || strings.Contains(got, "restored reasoning body") {
		t.Fatalf("second Ctrl+O did not collapse completed reasoning:\n%s", got)
	}
}

// 已完成的 reasoning 折叠为工作段后，点击段标题展开内联正文，再次点击折叠。
func TestCompletedReasoningClickTogglesSegment(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.ready = true
	model.width = 80
	model.height = 20
	started := time.Unix(300, 0)
	finished := started.Add(time.Second)
	model.transcript = []transcriptEntry{{
		kind:                entryReasoning,
		body:                "clickable reasoning",
		reasoningPartIndex:  1,
		reasoningStartedAt:  &started,
		reasoningFinishedAt: &finished,
		createdAt:           started,
	}}
	model.relayout()
	model.refreshViewport()
	if len(model.transcriptEntrySpans) != 1 || model.transcriptEntrySpans[0].startRow < 0 {
		t.Fatalf("reasoning span = %#v", model.transcriptEntrySpans)
	}
	if got := ansi.Strip(model.viewport.View()); strings.Contains(got, "clickable reasoning") {
		t.Fatalf("collapsed segment leaked reasoning body:\n%s", got)
	}

	updated, handled, _ := model.performTranscriptClick(selectionPoint{row: model.transcriptEntrySpans[0].startRow, col: 1})
	if !handled {
		t.Fatal("segment header click was not handled")
	}
	model = updated
	if got := ansi.Strip(model.viewport.View()); !strings.Contains(got, "clickable reasoning") {
		t.Fatalf("expanded segment did not reveal reasoning body:\n%s", got)
	}

	updated, handled, _ = model.performTranscriptClick(selectionPoint{row: model.transcriptEntrySpans[0].startRow, col: 1})
	if !handled {
		t.Fatal("second segment header click was not handled")
	}
	model = updated
	if got := ansi.Strip(model.viewport.View()); strings.Contains(got, "clickable reasoning") {
		t.Fatalf("collapsed segment still shows reasoning body:\n%s", got)
	}
}

func TestTranscriptEntriesRestoreOrderedAssistantPartsWithoutCompatibilityDuplicates(t *testing.T) {
	started := time.Unix(400, 0)
	finished := started.Add(1500 * time.Millisecond)
	msg := message.Message{
		Role:    message.RoleAssistant,
		Content: "compatibility answer",
		AssistantParts: []message.AssistantPart{
			{
				Type:   message.AssistantPartReasoning,
				Status: message.AssistantPartCompleted,
				Reasoning: &message.ReasoningPart{
					Text:       "ordered reasoning",
					StartedAt:  &started,
					FinishedAt: &finished,
				},
			},
			{
				Type:   message.AssistantPartText,
				Status: message.AssistantPartCompleted,
				Text:   &message.AssistantTextPart{Text: "ordered answer"},
			},
			{
				Type:   message.AssistantPartToolCall,
				Status: message.AssistantPartCompleted,
				ToolCall: &message.ToolCall{
					ID: "call_1", Name: "Read", Input: []byte(`{"file_path":"README.md"}`),
				},
			},
		},
		ToolUse: &message.ToolCall{ID: "call_1", Name: "Read", Input: []byte(`{"file_path":"README.md"}`)},
	}

	entries := transcriptEntriesFromMessage(msg, started, "")
	if len(entries) != 3 {
		t.Fatalf("entries = %#v, want exactly reasoning/text/tool", entries)
	}
	if entries[0].kind != entryReasoning || entries[0].body != "ordered reasoning" || entries[0].reasoningFinishedAt == nil {
		t.Fatalf("reasoning entry = %#v", entries[0])
	}
	if entries[1].kind != entryAssistant || entries[1].body != "ordered answer" {
		t.Fatalf("text entry = %#v", entries[1])
	}
	if entries[2].kind != entryTool || entries[2].toolUseID != "call_1" {
		t.Fatalf("tool entry = %#v", entries[2])
	}
	for _, entry := range entries {
		if strings.Contains(entry.body, "compatibility answer") {
			t.Fatalf("compatibility projection was duplicated: %#v", entries)
		}
	}
}
