package sessionactor

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"paw/internal/es"
	"paw/internal/loop"
	"paw/internal/message"
	"paw/internal/model"
	"paw/internal/session"
	"paw/internal/tool"
	toolfile "paw/internal/tool/file"
)

// 事件流级 golden（spec §9.2）：同一输入产生等价的 Durable 领域事件
// 序列。与 UI 视图级 golden（p5_*.golden）互补，这里断言的是合流后
// transcript 单流中的领域事件 Type 序列。

func domainEventTypes(t *testing.T, store *session.JSONLStore, sessionID string) []string {
	t.Helper()
	envelopes, _, err := store.LoadEnvelopes(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("LoadEnvelopes: %v", err)
	}
	types := make([]string, 0, len(envelopes))
	for _, env := range envelopes {
		if env.Kind == es.KindDomain {
			types = append(types, env.Type)
		}
	}
	return types
}

func assertEventFlow(t *testing.T, got, want []string) {
	t.Helper()
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("domain event flow =\n  got  %v\n  want %v", got, want)
	}
}

// toolThenDoneModel：第一次调用返回一次工作区外 Read 调用，之后直接
// 返回文本完成（驱动"工具轮 → 终答"两段流）。
type toolThenDoneModel struct {
	calls atomic.Int64
	call  message.ToolCall
}

func (m *toolThenDoneModel) StreamMessage(context.Context, []message.Message, []model.ToolDefinition) (<-chan model.StreamEvent, error) {
	if m.calls.Add(1) == 1 {
		events := make(chan model.StreamEvent, 1)
		events <- model.StreamEvent{ToolCalls: []message.ToolCall{m.call}, Done: true, FinishReason: model.FinishReasonToolCalls}
		close(events)
		return events, nil
	}
	events := make(chan model.StreamEvent, 2)
	events <- model.StreamEvent{Delta: "done"}
	events <- model.StreamEvent{Done: true}
	close(events)
	return events, nil
}

type errorPrompter struct{}

func (errorPrompter) PromptPermission(context.Context, loop.PermissionRequest) (loop.PermissionDecision, error) {
	return "", errors.New("prompter unavailable")
}

func outsideReadFixture(t *testing.T) (*session.JSONLStore, string, string, *toolThenDoneModel, *tool.Registry) {
	t.Helper()
	workspace := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	store, err := session.NewJSONLStore(filepath.Join(workspace, ".paw"))
	if err != nil {
		t.Fatal(err)
	}
	call := message.ToolCall{ID: "read-1", Name: "Read", Input: json.RawMessage(`{"file_path":"` + outside + `"}`)}
	model := &toolThenDoneModel{call: call}
	registry := tool.NewRegistry()
	registry.Register(&toolfile.ReadTool{Root: workspace})
	return store, workspace, "s1", model, registry
}

// TestGoldenEventFlowNormalTurn：普通 turn 的事件序列。
func TestGoldenEventFlowNormalTurn(t *testing.T) {
	store, err := session.NewJSONLStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	host, err := NewHost(loop.NewEngine(&scriptedModel{}, &recordingUI{}, tool.NewRegistry(), store, "s1"), store, "s1")
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close()
	if _, err := host.RunTurn(context.Background(), "hi"); err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	host.system.Drain()
	assertEventFlow(t, domainEventTypes(t, store, "s1"), []string{
		session.EventTurnStarted,
		session.EventUserMessage,
		session.EventAssistant,
		session.EventTurnCompleted,
	})
}

// TestGoldenEventFlowPermissionAllowedTurn：工作区外 Read 审批放行后
// 工具照常执行，事件序列覆盖 requested → decided → tool_started →
// tool_result 的完整链路。
func TestGoldenEventFlowPermissionAllowedTurn(t *testing.T) {
	store, _, sessionID, model, registry := outsideReadFixture(t)
	host, err := NewHost(loop.NewEngine(model, &recordingUI{}, registry, store, sessionID), store, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close()
	host.SetPermissionPrompter(&permissionPrompter{decision: loop.PermissionAllowOnce})
	if _, err := host.RunTurn(context.Background(), "read"); err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	host.system.Drain()
	assertEventFlow(t, domainEventTypes(t, store, sessionID), []string{
		session.EventTurnStarted,
		session.EventUserMessage,
		session.EventAssistant,
		EventPermissionRequested,
		EventPermissionDecided,
		EventToolStarted,
		session.EventToolResult,
		session.EventAssistant,
		session.EventTurnCompleted,
	})
}

// TestGoldenEventFlowPrompterFailureInterruptsTurn：审批提示器失败时
// turn 以失败收场（turn_failed + turn_interrupted），且补偿 Resume 使
// 后续 turn 立即可用（不悬到 Ask 超时）。
func TestGoldenEventFlowPrompterFailureInterruptsTurn(t *testing.T) {
	store, _, sessionID, model, registry := outsideReadFixture(t)
	host, err := NewHost(loop.NewEngine(model, &recordingUI{}, registry, store, sessionID), store, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close()
	host.SetPermissionPrompter(errorPrompter{})
	if _, err := host.RunTurn(context.Background(), "read"); err == nil {
		t.Fatal("RunTurn with failing prompter: want error")
	}
	host.system.Drain()
	assertEventFlow(t, domainEventTypes(t, store, sessionID), []string{
		session.EventTurnStarted,
		session.EventUserMessage,
		session.EventAssistant,
		EventPermissionRequested,
		session.EventTurnFailed,
		EventTurnInterrupted,
	})

	// 补偿验证：挂起已解除，第二个 turn（无工具调用）应立即成功。
	result, err := host.RunTurn(context.Background(), "again")
	if err != nil {
		t.Fatalf("RunTurn after prompter failure: %v (suspend compensation missing?)", err)
	}
	if result.Content != "done" {
		t.Fatalf("second turn content = %q, want done", result.Content)
	}
	host.system.Drain()
	full := domainEventTypes(t, store, sessionID)
	tail := full[len(full)-4:]
	assertEventFlow(t, tail, []string{
		session.EventTurnStarted,
		session.EventUserMessage,
		session.EventAssistant,
		session.EventTurnCompleted,
	})
}
