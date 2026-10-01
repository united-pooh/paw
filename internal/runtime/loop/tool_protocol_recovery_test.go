package loop

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"paw/internal/capability/model"
	"paw/internal/capability/tool"
	"paw/internal/message"
	"paw/internal/storage/session"
	"paw/internal/tokentracer"
)

func protocolFailureRound() fakeRound {
	return fakeRound{events: []model.StreamEvent{{Delta: malformedToolProtocolFixture}, {Done: true}}}
}

func protocolNativeRound(id, name string) fakeRound {
	return fakeRound{events: []model.StreamEvent{{ToolCalls: []message.ToolCall{{ID: id, Name: name, Input: json.RawMessage(`{}`)}}, Done: true}}}
}

func captureProtocolLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var logs bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(previous) })
	return &logs
}

func TestToolProtocolRecoveryReissuesNativeCallWithoutReplayingTools(t *testing.T) {
	logs := captureProtocolLogs(t)
	client := &fakeModel{rounds: []fakeRound{
		protocolNativeRound("completed-write", "Write"),
		{events: []model.StreamEvent{{Thinking: "checking the result"}, {Delta: "Checking next step.\n"}, {Delta: malformedToolProtocolFixture}, {Done: true}}},
		protocolNativeRound("fresh-read", "Read"),
		{events: []model.StreamEvent{{Delta: "finished"}, {Done: true}}},
	}}
	root := t.TempDir()
	write := &mutationProbeTool{name: "Write", path: filepath.Join(root, "work.txt"), writeOnRun: "done", output: "write already completed"}
	read := &mutationProbeTool{name: "Read", output: "verified"}
	registry := tool.NewRegistry()
	registry.Register(write)
	registry.Register(read)
	store, err := session.NewJSONLStore(filepath.Join(root, "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	output := &fakeUI{}
	runner := NewEngineWithInstructionRoot(client, output, registry, store, "protocol-retry", root)
	tracer := tokentracer.New("protocol recovery")
	runner.SetTokenTracer(tracer)
	result, err := runner.RunTurn(context.Background(), "write and verify")
	if err != nil || result.Content != "finished" {
		t.Fatalf("result=%q err=%v", result.Content, err)
	}
	if write.runCalls != 1 || read.runCalls != 1 {
		t.Fatalf("executions write=%d read=%d", write.runCalls, read.runCalls)
	}
	if len(client.calls) != 4 || output.doneCount != 1 {
		t.Fatalf("requests=%d done=%d", len(client.calls), output.doneCount)
	}
	request, _ := json.Marshal(client.calls[2])
	for _, want := range []string{"[Paw tool protocol recovery]", "write already completed", "原生工具调用"} {
		if !strings.Contains(string(request), want) {
			t.Fatalf("recovery request missing %q: %s", want, request)
		}
	}
	for _, unwanted := range []string{"DSML", "checking the result", "Checking next step."} {
		if strings.Contains(string(request), unwanted) {
			t.Fatalf("untrusted partial included in request: %s", request)
		}
	}
	if len(output.systemEvents()) != 0 || strings.Contains(strings.Join(output.deltas, ""), "DSML") {
		t.Fatalf("visible protocol recovery: %#v", output)
	}
	snapshot, err := store.LoadSnapshot(context.Background(), "protocol-retry")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Recovery != nil {
		t.Fatalf("recovered turn persisted as failed: %#v", snapshot.Recovery)
	}
	history, _ := json.Marshal(snapshot.ActiveHistory)
	if strings.Contains(string(history), "Paw tool protocol recovery") || strings.Contains(string(history), "DSML") {
		t.Fatalf("ephemeral protocol stored in history: %s", history)
	}
	for _, want := range []string{`"status":"failure"`, `"status":"retry"`, `"status":"recovered"`} {
		if !strings.Contains(logs.String(), want) {
			t.Fatalf("missing %s in logs: %s", want, logs.String())
		}
	}
	if strings.Contains(logs.String(), "printf fixture") {
		t.Fatalf("raw tool input leaked to log: %s", logs.String())
	}
	var outcomes []string
	for _, event := range tracer.Snapshot().Events {
		if event.Type == "tool_protocol_recovery" {
			outcomes = append(outcomes, event.Data["status"].(string))
		}
	}
	if !reflect.DeepEqual(outcomes, []string{"failure", "retry", "recovered"}) {
		t.Fatalf("trace outcomes = %q", outcomes)
	}
}

func TestToolProtocolRecoveryBudgetAndSuccessReset(t *testing.T) {
	for _, succeeds := range []bool{false, true} {
		t.Run(map[bool]string{false: "exhaustion", true: "reset after complete response"}[succeeds], func(t *testing.T) {
			logs := captureProtocolLogs(t)
			client := &fakeModel{}
			for range 10 {
				client.rounds = append(client.rounds, protocolFailureRound())
			}
			if succeeds {
				client.rounds = append(client.rounds, protocolNativeRound("read", "Read"))
				for range 10 {
					client.rounds = append(client.rounds, protocolFailureRound())
				}
				client.rounds = append(client.rounds, fakeRound{events: []model.StreamEvent{{Delta: "finished"}, {Done: true}}})
			} else {
				client.rounds = append(client.rounds, protocolFailureRound())
			}
			registry := tool.NewRegistry()
			registry.Register(&fakeTool{name: "Read", output: "completed read"})
			output := &fakeUI{}
			runner := NewEngineWithInstructionRoot(client, output, registry, nil, "budget", t.TempDir())
			result, err := runner.RunTurn(context.Background(), "finish task")
			if succeeds {
				if err != nil || result.Content != "finished" || len(client.calls) != 22 {
					t.Fatalf("result=%q err=%v requests=%d", result.Content, err, len(client.calls))
				}
			} else {
				var protocolErr *ToolProtocolError
				if !errors.As(err, &protocolErr) || len(client.calls) != 11 {
					t.Fatalf("err=%v requests=%d", err, len(client.calls))
				}
				if !strings.Contains(logs.String(), `"status":"exhausted"`) {
					t.Fatalf("exhaustion not logged: %s", logs.String())
				}
			}
		})
	}
}

type canceledProtocolModel struct {
	calls  int
	cancel context.CancelFunc
}

func (m *canceledProtocolModel) StreamMessage(_ context.Context, _ []message.Message, _ []model.ToolDefinition) (<-chan model.StreamEvent, error) {
	m.calls++
	m.cancel()
	events := make(chan model.StreamEvent, 2)
	events <- model.StreamEvent{Delta: malformedToolProtocolFixture}
	events <- model.StreamEvent{Done: true}
	close(events)
	return events, nil
}

func TestToolProtocolRecoveryRespectsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := &canceledProtocolModel{cancel: cancel}
	runner := NewEngineWithInstructionRoot(client, &fakeUI{}, tool.NewRegistry(), nil, "cancel", t.TempDir())
	_, err := runner.RunTurn(ctx, "finish task")
	if !errors.Is(err, context.Canceled) || client.calls != 1 {
		t.Fatalf("error=%v requests=%d", err, client.calls)
	}
}

func TestToolProtocolRecoveryWorksForPersistedToolTurn(t *testing.T) {
	root := t.TempDir()
	store, err := session.NewJSONLStore(filepath.Join(root, "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	user := message.Message{Role: message.RoleUser, Content: "write and verify"}
	assistant := buildAssistantToolCallMessage([]message.ToolCall{{ID: "done", Name: "Write", Input: json.RawMessage(`{}`)}})
	completed := message.ToolResult{ToolUseID: "done", Content: "already written"}
	if err := store.BeginTurn(ctx, "restore", "turn", user); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendAssistant(ctx, "restore", "turn", assistant); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendToolResult(ctx, "restore", "turn", 0, completed); err != nil {
		t.Fatal(err)
	}
	write := &mutationProbeTool{name: "Write", output: "must not execute again"}
	registry := tool.NewRegistry()
	registry.Register(write)
	client := &fakeModel{rounds: []fakeRound{protocolFailureRound(), {events: []model.StreamEvent{{Delta: "finished"}, {Done: true}}}}}
	runner := NewEngineWithInstructionRoot(client, &fakeUI{}, registry, store, "restore", root)
	result, err := runner.ContinueRecoveredTurn(ctx, RecoveredTurn{TurnID: "turn", History: []message.Message{user, assistant, buildToolResultsMessage([]message.ToolResult{completed})}, Assistant: assistant, CompletedResult: []message.ToolResult{completed}}, nil)
	if err != nil || result.Message.Content != "finished" || write.runCalls != 0 {
		t.Fatalf("result=%q err=%v executions=%d", result.Message.Content, err, write.runCalls)
	}
}

func TestToolProtocolRecoveryHandlesNativeArgumentError(t *testing.T) {
	logs := captureProtocolLogs(t)
	client := &fakeModel{rounds: []fakeRound{
		{events: []model.StreamEvent{{Thinking: "prepare call"}, {Err: &model.ToolArgumentsError{Provider: "synthetic", CallID: "broken", Tool: "Read"}}}},
		protocolNativeRound("replacement", "Read"),
		{events: []model.StreamEvent{{Delta: "finished"}, {Done: true}}},
	}}
	read := &mutationProbeTool{name: "Read", output: "read result"}
	registry := tool.NewRegistry()
	registry.Register(read)
	output := &fakeUI{}
	runner := NewEngineWithInstructionRoot(client, output, registry, nil, "native", t.TempDir())
	result, err := runner.RunTurn(context.Background(), "read")
	if err != nil || result.Content != "finished" || read.runCalls != 1 || output.doneCount != 1 {
		t.Fatalf("result=%q err=%v executions=%d done=%d", result.Content, err, read.runCalls, output.doneCount)
	}
	if !strings.Contains(logs.String(), `"format":"native_arguments"`) {
		t.Fatalf("missing native protocol log: %s", logs.String())
	}
}

func TestToolProtocolRecoveryRejectsMixedNativeAndMalformedTextBatch(t *testing.T) {
	client := &fakeModel{rounds: []fakeRound{
		{events: []model.StreamEvent{{Delta: malformedToolProtocolFixture}, {ToolCalls: []message.ToolCall{{ID: "unsafe", Name: "Write", Input: json.RawMessage(`{}`)}}, Done: true}}},
		{events: []model.StreamEvent{{Delta: "finished"}, {Done: true}}},
	}}
	write := &mutationProbeTool{name: "Write", output: "must not execute"}
	registry := tool.NewRegistry()
	registry.Register(write)
	runner := NewEngineWithInstructionRoot(client, &fakeUI{}, registry, nil, "mixed", t.TempDir())
	result, err := runner.RunTurn(context.Background(), "finish task")
	if err != nil || result.Content != "finished" || write.runCalls != 0 {
		t.Fatalf("result=%q err=%v executions=%d", result.Content, err, write.runCalls)
	}
}

func TestToolProtocolRecoveryClosesReasoningWithoutEndingTurn(t *testing.T) {
	client := &fakeModel{rounds: []fakeRound{
		{events: []model.StreamEvent{
			{AssistantPart: &model.AssistantPartEvent{Lifecycle: model.AssistantPartLifecycleStart, Type: "reasoning", BlockIndex: 0}},
			{AssistantPart: &model.AssistantPartEvent{Lifecycle: model.AssistantPartLifecycleDelta, Type: "reasoning", BlockIndex: 0, Delta: "prepare call"}},
			{Delta: malformedToolProtocolFixture}, {Done: true},
		}},
		{events: []model.StreamEvent{{Delta: "finished"}, {Done: true}}},
	}}
	output := &orderedDisplayUI{}
	runner := NewEngineWithInstructionRoot(client, output, tool.NewRegistry(), nil, "reasoning", t.TempDir())
	_, err := runner.RunTurn(context.Background(), "finish task")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"reasoning-start:0:false", "reasoning-delta:0:prepare call", "reasoning-end:0", "assistant:finished", "done"}
	if !reflect.DeepEqual(output.events, want) {
		t.Fatalf("events = %#v, want %#v", output.events, want)
	}
}

func TestToolProtocolRecoveryHintDoesNotOverwriteHistory(t *testing.T) {
	backing := []message.Message{{Role: message.RoleUser, Content: "task"}, {Role: message.RoleAssistant, Content: "reserved"}}
	recovery := toolProtocolRecovery{attempts: 1}
	history := recovery.requestHistory(backing[:1])
	if len(history) != 2 || history[1].Content != toolProtocolRecoveryInstruction || backing[1].Content != "reserved" {
		t.Fatalf("history=%#v backing=%#v", history, backing)
	}
}
