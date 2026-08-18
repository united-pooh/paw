package bubble

import (
	"strings"
	"testing"
	"time"

	"paw/internal/ui"
	"paw/internal/ui/bubble/viewportx"
)

// TestRunningToolElapsedAdvancesAcrossFrames 回归：running tool 的 elapsed
// 计时必须随帧驱动自动更新，不能被严格 revision cache 短路冻结。
// 历史 bug：refreshRunningToolProgress 每秒调用 refreshViewport，但
// running tool 的 elapsed 由 runtime index 驱动显式失效；只有鼠标 hover
// 才偶然刷新的旧行为会让计时冻结。
func TestRunningToolElapsedAdvancesAcrossFrames(t *testing.T) {
	vp := viewportx.New(100, 20)
	vp.Width = 100
	m := appModel{
		viewport: vp,
		transcript: []transcriptEntry{{
			kind: entryTool, title: "tool", toolName: "bash", toolStatus: "running",
			body: "bash running", toolStartedAt: time.Now().Add(-5 * time.Second),
		}},
	}
	base := time.Now()

	// 第一帧：渲染 5.00s
	m.cursorFrameAt = base
	first := m.renderTranscriptContent()
	if !strings.Contains(first, "5.00s") {
		t.Fatalf("first frame elapsed = %q, want 5.00s", first)
	}

	// 第二帧：推进 3 秒，模拟 cursorFrameMsg 分支调用 refreshRunningToolProgress
	m.cursorFrameAt = base.Add(3 * time.Second)
	m.refreshRunningToolProgress(base.Add(3 * time.Second))
	second := m.renderTranscriptContent()
	if !strings.Contains(second, "8.00s") {
		t.Fatalf("second frame elapsed = %q, want 8.00s (cache must not freeze progress)", second)
	}

	// 同一秒内重复帧：不应额外 touch（版本稳定，无抖动刷新）
	versionBefore := m.transcript[0].version
	m.cursorFrameAt = base.Add(3*time.Second + 500*time.Millisecond)
	m.refreshRunningToolProgress(base.Add(3*time.Second + 500*time.Millisecond))
	if m.transcript[0].version != versionBefore {
		t.Fatalf("same-second frame touched entry: version %d -> %d", versionBefore, m.transcript[0].version)
	}
}

// TestTaskWaitProgressAdvances 回归：TaskWait 状态行计时同样不被缓存冻结。
func TestTaskWaitProgressAdvances(t *testing.T) {
	vp := viewportx.New(100, 20)
	vp.Width = 100
	started := time.Now().Add(-2 * time.Second)
	m := appModel{
		viewport: vp,
		transcript: []transcriptEntry{{
			kind: entrySystem, title: "", body: "worker 正在运行 2s",
			taskWaitRunning: true, taskWaitNames: []string{"测试"},
			toolName: "TaskWait", toolStatus: "running",
			createdAt: started, toolStartedAt: started,
		}},
	}
	base := time.Now()
	m.cursorFrameAt = base
	first := m.renderTranscriptContent()
	if !strings.Contains(first, "2s") {
		t.Fatalf("first frame = %q, want 2s", first)
	}
	m.cursorFrameAt = base.Add(4 * time.Second)
	m.refreshRunningToolProgress(base.Add(4 * time.Second))
	second := m.renderTranscriptContent()
	if !strings.Contains(second, "6s") {
		t.Fatalf("second frame = %q, want 6s", second)
	}
}

func TestToolEventsRefreshViewportOnce(t *testing.T) {
	m := newTestModel(&fakeRunner{})
	beforeCall := m.transcriptRefreshCount
	m.recordToolCallEntry("call-1", "Read", []byte(`{"file_path":"README.md"}`), false, false, nil)
	if got := m.transcriptRefreshCount - beforeCall; got != 1 {
		t.Fatalf("tool call viewport refreshes = %d, want 1", got)
	}

	beforeResult := m.transcriptRefreshCount
	m.recordToolResultEntry("call-1", "Read", "ok", "content", false, false, false, nil)
	if got := m.transcriptRefreshCount - beforeResult; got != 1 {
		t.Fatalf("tool result viewport refreshes = %d, want 1", got)
	}
	if _, ok := m.toolRuntime.byID["call-1"]; ok {
		t.Fatal("completed tool remained in runtime ID index")
	}
	if len(m.toolRuntime.running) != 0 {
		t.Fatalf("completed tool remained in running set: %#v", m.toolRuntime.running)
	}
}

func TestToolCallUpdateFlushesBufferedAssistantAndRefreshesViewportOnce(t *testing.T) {
	m := newTestModel(&fakeRunner{})
	next, _ := m.Update(assistantDeltaMsg("before tool"))
	m = next.(appModel)
	m.lastTranscriptRefreshAt = time.Now().Add(-time.Second)
	before := m.transcriptRefreshCount

	next, _ = m.Update(toolCallMsg(ui.ToolCallEvent{
		ID:    "call-1",
		Name:  "Read",
		Input: []byte(`{"file_path":"README.md"}`),
	}))
	m = next.(appModel)

	if got := m.transcriptRefreshCount - before; got != 1 {
		t.Fatalf("tool call event viewport refreshes = %d, want 1", got)
	}
	if assistant := lastEntryOfKind(t, m.transcript, entryAssistant); assistant.body != "before tool" {
		t.Fatalf("assistant tail = %q, want flushed content", assistant.body)
	}
}

func TestResultOnlyMutationRefreshesViewportOnce(t *testing.T) {
	m := newTestModel(&fakeRunner{})
	before := m.transcriptRefreshCount

	m.recordToolResultEntry("orphan", "Write", "ok", "updated", false, true, true, nil)

	if got := m.transcriptRefreshCount - before; got != 1 {
		t.Fatalf("result-only mutation viewport refreshes = %d, want 1", got)
	}
}

func TestFirstRunningToolDoesNotRebuildHistoricalRuntimeIndex(t *testing.T) {
	m := newTestModel(&fakeRunner{})
	for index := 0; index < 5_000; index++ {
		m.appendTranscriptEntry(transcriptEntry{kind: entryAssistant, title: "assistant", body: "history"})
	}
	m.toolRuntimeRebuildVisits = 0

	m.recordToolCallEntry("tail-tool", "Read", []byte(`{"file_path":"README.md"}`), false, false, nil)

	if got := m.toolRuntimeRebuildVisits; got != 0 {
		t.Fatalf("first running tool rebuilt %d historical entries, want 0", got)
	}
}

func TestRunningToolProgressDoesNotRefreshBeforeDisplayedSecondChanges(t *testing.T) {
	m := newTestModel(&fakeRunner{})
	started := time.Now()
	m.cursorFrameAt = started
	m.recordToolCallEntry("call-1", "Read", []byte(`{"file_path":"README.md"}`), false, false, nil)
	version := m.transcript[len(m.transcript)-1].version
	refreshes := m.transcriptRefreshCount

	m.refreshRunningToolProgress(started.Add(500 * time.Millisecond))

	if got := m.transcript[len(m.transcript)-1].version; got != version {
		t.Fatalf("sub-second progress touched tool version %d -> %d", version, got)
	}
	if got := m.transcriptRefreshCount - refreshes; got != 0 {
		t.Fatalf("sub-second progress viewport refreshes = %d, want 0", got)
	}
}

func TestRunningToolProgressUsesRuntimeIndex(t *testing.T) {
	started := time.Now()
	entries := make([]transcriptEntry, 5_001)
	for i := 0; i < len(entries)-1; i++ {
		entries[i] = transcriptEntry{kind: entryAssistant, title: "assistant", body: "history"}
	}
	entries[len(entries)-1] = transcriptEntry{
		kind: entryTool, title: "tool", toolUseID: "tail-tool", toolName: "Read",
		toolStatus: "running", body: "running", toolStartedAt: started,
	}
	m := newTestModel(&fakeRunner{})
	m.replaceTranscript(entries)
	m.cursorFrameAt = started
	m.refreshViewport()

	if len(m.toolRuntime.running) != 1 {
		t.Fatalf("running index size = %d, want 1", len(m.toolRuntime.running))
	}
	historyVersion := m.transcript[0].version
	refreshes := m.transcriptRefreshCount
	m.cursorFrameAt = started.Add(2 * time.Second)
	m.refreshRunningToolProgress(m.cursorFrameAt)

	if got := m.toolProgressVisits; got != 1 {
		t.Fatalf("progress tick visited %d entries, want running set size 1", got)
	}
	if m.transcript[0].version != historyVersion {
		t.Fatalf("historical entry was touched: %d -> %d", historyVersion, m.transcript[0].version)
	}
	if got := m.transcriptRefreshCount - refreshes; got != 1 {
		t.Fatalf("progress viewport refreshes = %d, want 1", got)
	}
}
