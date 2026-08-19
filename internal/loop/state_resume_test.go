package loop

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"paw/internal/message"
	"paw/internal/model"
	"paw/internal/session"
	"paw/internal/tool"
)

type fakeStateProvider struct {
	block string
}

func (p *fakeStateProvider) BuildStateContext(ctx context.Context) (string, error) {
	return p.block, nil
}

// seedTurnedSession 写入 n 个完整 turn（user → assistant → tool → result → complete）。
func seedTurnedSession(t *testing.T, store *session.JSONLStore, sessionID string, n int) {
	t.Helper()
	ctx := context.Background()
	for i := 0; i < n; i++ {
		turnID := string(rune('A' + i))
		if err := store.BeginTurn(ctx, sessionID, turnID, message.Message{Role: message.RoleUser, Content: "q" + string(rune('a'+i))}); err != nil {
			t.Fatal(err)
		}
		if _, err := store.AppendAssistantWithSequence(ctx, sessionID, turnID, message.Message{
			Role:     message.RoleAssistant,
			ToolUses: []message.ToolCall{{ID: "c-" + turnID, Name: "Read", Input: []byte(`{"path":"/secret"}`)}},
		}); err != nil {
			t.Fatal(err)
		}
		if err := store.AppendToolResult(ctx, sessionID, turnID, 0, message.ToolResult{ToolUseID: "c-" + turnID, Content: strings.Repeat("r", 2000)}); err != nil {
			t.Fatal(err)
		}
		if err := store.CompleteTurn(ctx, sessionID, turnID); err != nil {
			t.Fatal(err)
		}
	}
}

// TestStateModeResumeInjectsStateBlockAndRecentTurns 覆盖：模式 B 恢复 =
// 状态块 + 最近 3 轮清洗对话，早期消息不送，工具参数被清洗。
func TestStateModeResumeInjectsStateBlockAndRecentTurns(t *testing.T) {
	root := t.TempDir()
	store, err := session.NewJSONLStore(filepath.Join(root, ".paw"))
	if err != nil {
		t.Fatal(err)
	}
	sessionID := "state-resume"
	seedTurnedSession(t, store, sessionID, 5) // 5 轮，恢复只留最后 3 轮

	modelClient := &fakeModel{rounds: []fakeRound{{events: []model.StreamEvent{{Delta: "done"}, {Done: true}}}}}
	runner := NewEngineWithInstructionRoot(modelClient, &fakeUI{}, tool.NewRegistry(), store, sessionID, root)
	runner.SetContextMode("state")
	runner.SetStateBlockProvider(&fakeStateProvider{block: "## 方向\n测试任务"})

	if _, err := runner.RunTurn(context.Background(), "continue"); err != nil {
		t.Fatal(err)
	}
	if len(modelClient.calls) != 1 {
		t.Fatalf("model calls = %d, want 1", len(modelClient.calls))
	}
	prompt := promptTextForTest(modelClient.calls[0])

	if !strings.Contains(prompt, "## 方向\n测试任务") {
		t.Fatal("state block must be injected")
	}
	if !strings.Contains(prompt, stateBlockHeader[:40]) {
		t.Fatal("state block header missing")
	}
	// 早期轮次（A/B）不送：qa/qb 不在 prompt。
	if strings.Contains(prompt, "qa\n") || strings.Contains(prompt, "qb\n") {
		t.Fatal("early turns must not be sent in state mode")
	}
	// 最近轮次在（qc/qd/qe）。
	for _, want := range []string{"qc", "qd", "qe"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("recent turn %q missing from prompt", want)
		}
	}
	// 工具参数被清洗：/secret 不在 prompt。
	if strings.Contains(prompt, "/secret") {
		t.Fatal("tool input must be cleaned in state mode")
	}
	// 工具结果截断：2000 个 r 不全量出现。
	if strings.Contains(prompt, strings.Repeat("r", 1500)) {
		t.Fatal("tool result must be truncated in state mode")
	}
}

// TestSummaryModeResumeSendsFullHistory 覆盖：默认 summary 模式恢复仍送
// 全量历史（现状不变）。
func TestSummaryModeResumeSendsFullHistory(t *testing.T) {
	root := t.TempDir()
	store, err := session.NewJSONLStore(filepath.Join(root, ".paw"))
	if err != nil {
		t.Fatal(err)
	}
	sessionID := "summary-resume"
	seedTurnedSession(t, store, sessionID, 5)

	modelClient := &fakeModel{rounds: []fakeRound{{events: []model.StreamEvent{{Delta: "done"}, {Done: true}}}}}
	runner := NewEngineWithInstructionRoot(modelClient, &fakeUI{}, tool.NewRegistry(), store, sessionID, root)
	runner.SetStateBlockProvider(&fakeStateProvider{block: "## 方向\n不应出现"})

	if _, err := runner.RunTurn(context.Background(), "continue"); err != nil {
		t.Fatal(err)
	}
	prompt := promptTextForTest(modelClient.calls[0])
	if strings.Contains(prompt, "## 方向\n不应出现") {
		t.Fatal("state block must not be injected in summary mode")
	}
	if !strings.Contains(prompt, "qa") || !strings.Contains(prompt, "qe") {
		t.Fatal("summary mode must keep full history")
	}
}

// TestStateModeResumeWithoutProvider 覆盖：模式 B 无 provider 时只有
// 最近 3 轮，不注入状态块。
func TestStateModeResumeWithoutProvider(t *testing.T) {
	root := t.TempDir()
	store, err := session.NewJSONLStore(filepath.Join(root, ".paw"))
	if err != nil {
		t.Fatal(err)
	}
	sessionID := "state-resume-no-provider"
	seedTurnedSession(t, store, sessionID, 4)

	modelClient := &fakeModel{rounds: []fakeRound{{events: []model.StreamEvent{{Delta: "done"}, {Done: true}}}}}
	runner := NewEngineWithInstructionRoot(modelClient, &fakeUI{}, tool.NewRegistry(), store, sessionID, root)
	runner.SetContextMode("state")

	if _, err := runner.RunTurn(context.Background(), "continue"); err != nil {
		t.Fatal(err)
	}
	prompt := promptTextForTest(modelClient.calls[0])
	if strings.Contains(prompt, stateBlockHeader[:40]) {
		t.Fatal("no state block without provider")
	}
	if !strings.Contains(prompt, "qb") || strings.Contains(prompt, "qa") {
		t.Fatalf("recent turns only expected: qa sent? %v, qb sent? %v", strings.Contains(prompt, "qa"), strings.Contains(prompt, "qb"))
	}
}
