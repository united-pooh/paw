package loop

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"paw/internal/model"
	"paw/internal/session"
	"paw/internal/tool"
)

// newStateCompactionRunner 构造模式 B runner：5 轮 transcript + 大 history。
func newStateCompactionRunner(t *testing.T) (*Runner, *fakeModel, *session.JSONLStore, string) {
	t.Helper()
	root := t.TempDir()
	store, err := session.NewJSONLStore(filepath.Join(root, ".paw"))
	if err != nil {
		t.Fatal(err)
	}
	sessionID := "state-compact"
	seedTurnedSession(t, store, sessionID, 5)

	modelClient := &fakeModel{rounds: []fakeRound{{events: []model.StreamEvent{{Delta: "done"}, {Done: true}}}}}
	runner := NewRunnerWithInstructionRoot(modelClient, &fakeUI{}, tool.NewRegistry(), store, sessionID, root)
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
	runner := NewRunnerWithInstructionRoot(modelClient, &fakeUI{}, tool.NewRegistry(), store, sessionID, root)
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
	runner := NewRunnerWithInstructionRoot(modelClient, &fakeUI{}, tool.NewRegistry(), store, sessionID, root)
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
