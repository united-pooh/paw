package loop

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"paw/internal/message"
	"paw/internal/model"
	"paw/internal/session"
	"paw/internal/tool"
)

// newStateCompactionRunner 构造模式 B runner：5 轮 transcript + 大 history。
func newStateCompactionRunner(t *testing.T) (*Engine, *fakeModel, *session.JSONLStore, string) {
	t.Helper()
	root := t.TempDir()
	store, err := session.NewJSONLStore(filepath.Join(root, ".paw"))
	if err != nil {
		t.Fatal(err)
	}
	sessionID := "state-compact"
	seedTurnedSession(t, store, sessionID, 5)

	modelClient := &fakeModel{rounds: []fakeRound{{events: []model.StreamEvent{{Delta: "done"}, {Done: true}}}}}
	runner := NewEngineWithInstructionRoot(modelClient, &fakeUI{}, tool.NewRegistry(), store, sessionID, root)
	runner.SetContextMode("state")
	runner.SetStateCompactionRatio(0.5)
	runner.SetContextLimitTokens(4000)
	runner.SetStateBlockProvider(&fakeStateProvider{block: "## 方向\n状态压缩测试"})
	return runner, modelClient, store, sessionID
}

// TestStateCompactionTriggersAtRatio 覆盖：模式 B 超阈值时历史被状态压缩
// （状态块 + 最近 3 轮 + 刷新指令），早期消息消失，无摘要模型调用。
func TestStateCompactionTriggersAtRatio(t *testing.T) {
	runner, modelClient, store, sessionID := newStateCompactionRunner(t)

	// 绕过冷启动：直接装载全量历史（模拟已运行很久的内存投影）。
	snapshot, err := store.LoadSnapshot(context.Background(), sessionID)
	if err != nil {
		t.Fatal(err)
	}
	runner.setHistory(snapshot.ActiveHistory)

	if _, err := runner.RunTurn(context.Background(), "继续"); err != nil {
		t.Fatal(err)
	}
	if len(modelClient.calls) != 1 {
		t.Fatalf("model calls = %d, want 1 (no summarizer call)", len(modelClient.calls))
	}
	prompt := promptTextForTest(modelClient.calls[0])

	if !strings.Contains(prompt, stateRefreshInstruction[:40]) {
		t.Fatal("refresh instruction missing")
	}
	if !strings.Contains(prompt, "## 方向\n状态压缩测试") {
		t.Fatal("state block missing after compaction")
	}
	// 压缩保留「当前轮（进行中，BeginTurn 已占位）+ 前 2 轮」：
	// 5 轮 A–E + 新 turn 开始后，保留 D/E + 新 turn（空），C 及更早被裁。
	if strings.Contains(prompt, "qa") || strings.Contains(prompt, "qb") || strings.Contains(prompt, "qc") {
		t.Fatal("early turns must be dropped by state compaction")
	}
	for _, want := range []string{"qd", "qe"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("recent turn %q must be kept", want)
		}
	}
	if strings.Contains(prompt, "/secret") {
		t.Fatal("tool input must be cleaned after compaction")
	}

	// 审计事件：state_compacted 已写入 transcript。
	records, err := store.LoadResolvedJournalRecords(context.Background(), sessionID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range records {
		if r.Kind == session.JournalStateCompacted {
			found = true
		}
	}
	if !found {
		kinds := []string{}
		for _, r := range records {
			kinds = append(kinds, string(r.Kind))
		}
		t.Fatalf("state_compacted event must be recorded, kinds=%v", kinds)
	}
}

// TestStateCompactionBelowRatioKeepsHistory 覆盖：低于阈值不压缩。
func TestStateCompactionBelowRatioKeepsHistory(t *testing.T) {
	root := t.TempDir()
	store, err := session.NewJSONLStore(filepath.Join(root, ".paw"))
	if err != nil {
		t.Fatal(err)
	}
	sessionID := "state-below"
	seedTurnedSession(t, store, sessionID, 2)

	modelClient := &fakeModel{rounds: []fakeRound{{events: []model.StreamEvent{{Delta: "done"}, {Done: true}}}}}
	runner := NewEngineWithInstructionRoot(modelClient, &fakeUI{}, tool.NewRegistry(), store, sessionID, root)
	runner.SetContextMode("state")
	runner.SetStateCompactionRatio(0.9)
	runner.SetContextLimitTokens(100000) // 窗口巨大，2 轮远低于阈值

	snapshot, err := store.LoadSnapshot(context.Background(), sessionID)
	if err != nil {
		t.Fatal(err)
	}
	runner.setHistory(snapshot.ActiveHistory)

	if _, err := runner.RunTurn(context.Background(), "继续"); err != nil {
		t.Fatal(err)
	}
	prompt := promptTextForTest(modelClient.calls[0])
	if strings.Contains(prompt, stateRefreshInstruction[:40]) {
		t.Fatal("must not compact below ratio")
	}
	if !strings.Contains(prompt, "qa") || !strings.Contains(prompt, "qb") {
		t.Fatal("full history must remain below ratio")
	}
}

// TestStateCompactionSummaryModeUnchanged 覆盖：summary 模式不受状态压缩
// 影响（现有行为）。
func TestStateCompactionSummaryModeUnchanged(t *testing.T) {
	root := t.TempDir()
	store, err := session.NewJSONLStore(filepath.Join(root, ".paw"))
	if err != nil {
		t.Fatal(err)
	}
	sessionID := "summary-mode"
	seedTurnedSession(t, store, sessionID, 5)

	modelClient := &fakeModel{rounds: []fakeRound{{events: []model.StreamEvent{{Delta: "done"}, {Done: true}}}}}
	runner := NewEngineWithInstructionRoot(modelClient, &fakeUI{}, tool.NewRegistry(), store, sessionID, root)
	runner.SetContextLimitTokens(100000)

	snapshot, err := store.LoadSnapshot(context.Background(), sessionID)
	if err != nil {
		t.Fatal(err)
	}
	runner.setHistory(snapshot.ActiveHistory)

	if _, err := runner.RunTurn(context.Background(), "继续"); err != nil {
		t.Fatal(err)
	}
	prompt := promptTextForTest(modelClient.calls[0])
	if strings.Contains(prompt, stateRefreshInstruction[:40]) {
		t.Fatal("summary mode must not use state refresh instruction")
	}
}

// TestStateModeSnipsToolResultsBelowCompactionRatio 覆盖：模式 B 接入 4 级
// 压力后，snip 档（soft < 压力 < stateCompactionRatio）对工具结果做
// 头尾裁剪，历史结构保留，不触发状态压缩（工具结果处理策略与模式 A 一致）。
func TestStateModeSnipsToolResultsBelowCompactionRatio(t *testing.T) {
	history := []message.Message{
		{Role: message.RoleUser, Content: "task"},
		buildAssistantToolCallMessage([]message.ToolCall{{ID: "old1", Name: "Read"}}),
		buildToolResultMessage("old1", strings.Repeat("large stale result\n", 1500), false),
		{Role: message.RoleAssistant, Content: "recent answer"},
		{Role: message.RoleUser, Content: "continue"},
	}
	estimated := estimateMessageTokens(history)
	modelClient := &fakeModel{}
	// 压力 ≈ 0.65 × limit：落在 snip 档（0.6 与 0.9 之间）。
	limit := int(float64(estimated) / 0.65)
	runner := newPressureTestRunner(t, modelClient, limit)
	runner.SetContextMode("state")
	runner.SetStateCompactionRatio(0.9)

	got, err := runner.maintainStateProjection(context.Background(), history)
	if err != nil {
		t.Fatal(err)
	}
	if got.snippedResults != 1 {
		t.Fatalf("snippedResults = %d, want 1", got.snippedResults)
	}
	if got.prunedResults != 0 || got.compaction != nil {
		t.Fatalf("snip band must not prune or compact: %+v", got)
	}
	if len(got.history) != len(history) {
		t.Fatalf("message structure must be preserved in snip band: %d → %d messages", len(history), len(got.history))
	}
	snipped := false
	checkResult := func(content string) {
		if strings.Contains(content, stateRefreshInstruction[:40]) {
			t.Fatal("state compaction must not trigger in snip band")
		}
		if strings.Contains(content, snippedToolResultMarker) {
			snipped = true
		}
	}
	for _, msg := range got.history {
		if msg.ToolResult != nil {
			checkResult(msg.ToolResult.Content)
		}
		for _, tr := range msg.ToolResults {
			checkResult(tr.Content)
		}
	}
	if !snipped {
		t.Fatal("tool result must be snipped with head+tail marker")
	}
}

// TestStateModePruneArchivesBeforeCompaction 覆盖：模式 B compact 档先
// 归档旧工具结果（prune 前置保真），再执行状态压缩。
func TestStateModePruneArchivesBeforeCompaction(t *testing.T) {
	runner, modelClient, store, sessionID := newStateCompactionRunner(t)
	snapshot, err := store.LoadSnapshot(context.Background(), sessionID)
	if err != nil {
		t.Fatal(err)
	}
	runner.setHistory(snapshot.ActiveHistory)

	if _, err := runner.RunTurn(context.Background(), "继续"); err != nil {
		t.Fatal(err)
	}
	if len(modelClient.calls) != 1 {
		t.Fatalf("model calls = %d, want 1 (no summarizer call)", len(modelClient.calls))
	}
	archiveDir := filepath.Join(runner.workRoot, ".paw", "sessions", sessionID, "compactions")
	entries, err := os.ReadDir(archiveDir)
	if err != nil || len(entries) == 0 {
		t.Fatalf("compaction archive must be written before state compression: %v, entries=%d", err, len(entries))
	}
}
