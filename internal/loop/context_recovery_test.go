package loop

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"paw/internal/message"
	"paw/internal/model"
	"paw/internal/tool"
	"paw/internal/ui"
)

type countingRecoveryTool struct {
	calls int
	onRun func()
}

func (*countingRecoveryTool) Name() string        { return "Read" }
func (*countingRecoveryTool) Description() string { return "read a file" }
func (*countingRecoveryTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object"}`)
}
func (tool *countingRecoveryTool) Run(context.Context, json.RawMessage) (string, error) {
	tool.calls++
	if tool.onRun != nil {
		tool.onRun()
	}
	return "module paw", nil
}

func TestRunTurnRecoversContextLimitWithinToolRoundWithoutRerunningTool(t *testing.T) {
	contextErr := &model.ProviderHTTPError{
		StatusCode: 400,
		Code:       "context_length_exceeded",
		Message:    "input exceeds the model context length",
	}
	modelClient := &fakeModel{rounds: []fakeRound{
		{events: []model.StreamEvent{{
			ToolCalls:    []message.ToolCall{{ID: "call-1", Name: "Read", Input: json.RawMessage(`{"file_path":"go.mod"}`)}},
			ProviderData: json.RawMessage(`{"transport":"openai-responses","version":1,"output_items":[{"type":"function_call","call_id":"call-1","name":"Read","arguments":"{\"file_path\":\"go.mod\"}"}]}`),
			FinishReason: model.FinishReasonToolCalls,
			Done:         true,
		}}},
		{err: contextErr},
		{events: []model.StreamEvent{{Delta: "## Goal\nFinish the current task.", Done: true}}},
		{events: []model.StreamEvent{{Delta: "finished after recovery", Done: true}}},
	}}
	output := &fakeUI{}
	readTool := &countingRecoveryTool{}
	registry := tool.NewRegistry()
	registry.Register(readTool)
	runner := NewRunnerWithInstructionRoot(modelClient, output, registry, nil, "context-recovery", t.TempDir())
	runner.SetContextLimitTokens(1_000_000)
	runner.setHistory(contextRecoveryHistory())
	readTool.onRun = func() {
		runner.SubmitSupplement("keep the public API stable")
		runner.SubmitSupplement("do not rerun the tool")
	}

	msg, err := runner.RunTurn(context.Background(), "read go.mod and finish")
	if err != nil {
		t.Fatalf("RunTurn() error = %v", err)
	}
	if msg.Content != "finished after recovery" {
		t.Fatalf("message content = %q", msg.Content)
	}
	if readTool.calls != 1 {
		t.Fatalf("tool calls = %d, want 1", readTool.calls)
	}
	if len(modelClient.calls) != 4 {
		t.Fatalf("model calls = %d, want tool round + failed request + summary + retry", len(modelClient.calls))
	}
	if !messagesContainAdjacentToolPair(modelClient.calls[3], "call-1") {
		t.Fatalf("retried request lost recent function-call/output pair: %#v", modelClient.calls[3])
	}
	if !messagesContainProviderData(modelClient.calls[3], "call-1") {
		t.Fatalf("retried request lost Responses provider data: %#v", modelClient.calls[3])
	}
	if got := runner.ContextStats(1_000_000, "").UsedTokens; got <= 0 {
		t.Fatalf("context token estimate = %d, want updated estimate", got)
	}
	events := output.systemEvents()
	if countSystemEventsByTitle(events, "context-recovery") != 1 {
		t.Fatalf("system events = %#v, want one context-recovery notice", events)
	}
	for _, event := range events {
		if event.Title == "context-recovery" && strings.Contains(event.Body, "%!") {
			t.Fatalf("context-recovery notice contains formatting artifact: %q", event.Body)
		}
	}
}

func TestRunModelTurnKeepsSkillContextWhenRequestIsRejectedBeforeStream(t *testing.T) {
	contextErr := &model.ProviderHTTPError{
		StatusCode: 400,
		Code:       "context_length_exceeded",
		Message:    "context length exceeded",
	}
	modelClient := &fakeModel{rounds: []fakeRound{
		{err: contextErr},
		{events: []model.StreamEvent{{Delta: "recovered", Done: true}}},
	}}
	runner := NewRunner(modelClient, &fakeUI{}, tool.NewRegistry(), nil, "skill-context-retry")
	turn := &TurnState{SkillContext: "Always preserve the exact tool protocol."}
	history := []message.Message{{Role: message.RoleUser, Content: "continue"}}

	if _, err := runner.runModelTurn(context.Background(), history, turn); !errors.Is(err, contextErr) {
		t.Fatalf("first runModelTurn() error = %v, want context error", err)
	}
	if turn.PlanEmitted {
		t.Fatal("PlanEmitted = true after request rejection, want retry to retain full skill context")
	}
	if _, err := runner.runModelTurn(context.Background(), history, turn); err != nil {
		t.Fatalf("second runModelTurn() error = %v", err)
	}
	if len(modelClient.calls) != 2 {
		t.Fatalf("model calls = %d, want 2", len(modelClient.calls))
	}
	for i, call := range modelClient.calls {
		if len(call) == 0 || !strings.Contains(call[0].Content, turn.SkillContext) {
			t.Fatalf("model call %d lost skill context: %#v", i, call)
		}
	}
}

func TestRunTurnRoundZeroRecoveryDoesNotPinStaleToolPair(t *testing.T) {
	contextErr := &model.ProviderHTTPError{
		StatusCode: 400,
		Code:       "context_length_exceeded",
		Message:    "context length exceeded",
	}
	modelClient := &fakeModel{rounds: []fakeRound{
		{err: contextErr},
		{events: []model.StreamEvent{{Delta: "summary", Done: true}}},
		{events: []model.StreamEvent{{Delta: "recovered", Done: true}}},
	}}
	runner := NewRunnerWithInstructionRoot(modelClient, &fakeUI{}, tool.NewRegistry(), nil, "round-zero-stale-pair", t.TempDir())
	runner.SetContextLimitTokens(1_000_000)
	runner.setHistory([]message.Message{
		{Role: message.RoleUser, Content: "original task"},
		buildAssistantToolCallMessage([]message.ToolCall{{ID: "stale-call", Name: "Read", Input: json.RawMessage(`{}`)}}),
		buildToolResultMessage("stale-call", "old result", false),
		{Role: message.RoleAssistant, Content: strings.Repeat("old reasoning ", 80)},
		{Role: message.RoleUser, Content: "newer constraint"},
	})

	msg, err := runner.RunTurn(context.Background(), "continue without tools")
	if err != nil {
		t.Fatalf("RunTurn() error = %v", err)
	}
	if msg.Content != "recovered" {
		t.Fatalf("message content = %q, want recovered", msg.Content)
	}
	if len(modelClient.calls) != 3 {
		t.Fatalf("model calls = %d, want failed round + summary + retry", len(modelClient.calls))
	}
}

func TestRunTurnDoesNotRecoverContextLimitTwiceInOneModelRound(t *testing.T) {
	firstErr := &model.ProviderHTTPError{StatusCode: 413, Message: "prompt too large"}
	secondErr := &model.ProviderHTTPError{StatusCode: 400, Code: "context_too_large", Message: "context too large"}
	modelClient := &fakeModel{rounds: []fakeRound{
		{events: []model.StreamEvent{{
			ToolCalls:    []message.ToolCall{{ID: "call-1", Name: "Read", Input: json.RawMessage(`{}`)}},
			FinishReason: model.FinishReasonToolCalls,
			Done:         true,
		}}},
		{err: firstErr},
		{events: []model.StreamEvent{{Delta: "summary", Done: true}}},
		{err: secondErr},
	}}
	output := &fakeUI{}
	readTool := &countingRecoveryTool{}
	registry := tool.NewRegistry()
	registry.Register(readTool)
	runner := NewRunnerWithInstructionRoot(modelClient, output, registry, nil, "context-recovery-twice", t.TempDir())
	runner.SetContextLimitTokens(1_000_000)
	runner.setHistory(contextRecoveryHistory())

	_, err := runner.RunTurn(context.Background(), "read once")
	if !errors.Is(err, secondErr) {
		t.Fatalf("RunTurn() error = %v, want second provider error", err)
	}
	if len(modelClient.calls) != 4 {
		t.Fatalf("model calls = %d, want exactly one summary and one retry", len(modelClient.calls))
	}
	if readTool.calls != 1 {
		t.Fatalf("tool calls = %d, want 1", readTool.calls)
	}
	if countSystemEventsByTitle(output.systemEvents(), "context-recovery") != 1 {
		t.Fatalf("system events = %#v, want one recovery notice", output.systemEvents())
	}
}

func TestRunTurnDoesNotRetryUnknownBadRequest(t *testing.T) {
	badRequest := &model.ProviderHTTPError{
		StatusCode: 400,
		Code:       "invalid_request_error",
		Message:    "missing field output",
	}
	modelClient := &fakeModel{rounds: []fakeRound{{err: badRequest}}}
	output := &fakeUI{}
	runner := NewRunnerWithInstructionRoot(modelClient, output, tool.NewRegistry(), nil, "unknown-400", t.TempDir())

	_, err := runner.RunTurn(context.Background(), "hello")
	if !errors.Is(err, badRequest) {
		t.Fatalf("RunTurn() error = %v, want original bad request", err)
	}
	if len(modelClient.calls) != 1 {
		t.Fatalf("model calls = %d, want 1", len(modelClient.calls))
	}
	if countSystemEventsByTitle(output.systemEvents(), "context-recovery") != 0 {
		t.Fatalf("unexpected recovery notice: %#v", output.systemEvents())
	}
}

func TestRunTurnExplainsUnavailableContextRecoveryAndPreservesOriginalError(t *testing.T) {
	contextErr := &model.ProviderHTTPError{
		StatusCode: 400,
		Code:       "input_too_large",
		Message:    "input is too large",
	}
	modelClient := &fakeModel{rounds: []fakeRound{{err: contextErr}}}
	runner := NewRunnerWithInstructionRoot(modelClient, &fakeUI{}, tool.NewRegistry(), nil, "unavailable-recovery", t.TempDir())

	_, err := runner.RunTurn(context.Background(), "only one message exists")
	if !errors.Is(err, contextErr) {
		t.Fatalf("RunTurn() error = %v, want original provider error discoverable", err)
	}
	if err == nil || !strings.Contains(err.Error(), "context recovery unavailable") {
		t.Fatalf("RunTurn() error = %v, want recovery unavailable explanation", err)
	}
	if len(modelClient.calls) != 1 {
		t.Fatalf("model calls = %d, want no retry when history cannot compact", len(modelClient.calls))
	}
}

func TestRunTurnDoesNotRecoverContextErrorAfterPartialSSEOutput(t *testing.T) {
	contextErr := &model.ProviderHTTPError{
		StatusCode: 400,
		Code:       "context_length_exceeded",
		Message:    "context length exceeded",
	}
	modelClient := &fakeModel{rounds: []fakeRound{{events: []model.StreamEvent{
		{Delta: "partial response"},
		{Err: contextErr},
	}}}}
	output := &fakeUI{}
	runner := NewRunnerWithInstructionRoot(modelClient, output, tool.NewRegistry(), nil, "partial-context-error", t.TempDir())
	runner.SetContextLimitTokens(1_000_000)
	runner.setHistory(contextRecoveryHistory())

	_, err := runner.RunTurn(context.Background(), "continue")
	if !errors.Is(err, contextErr) {
		t.Fatalf("RunTurn() error = %v, want original stream error", err)
	}
	if len(modelClient.calls) != 1 {
		t.Fatalf("model calls = %d, want partial stream not replayed", len(modelClient.calls))
	}
	if got := strings.Join(output.deltas, ""); got != "partial response" {
		t.Fatalf("visible output = %q, want original partial response once", got)
	}
	if output.doneCount != 1 {
		t.Fatalf("done count = %d, want 1", output.doneCount)
	}
	if countSystemEventsByTitle(output.systemEvents(), "context-recovery") != 0 {
		t.Fatalf("unexpected recovery notice: %#v", output.systemEvents())
	}
}

func TestRunTurnDoesNotRecoverContextErrorAfterPartialSSEToolCall(t *testing.T) {
	contextErr := &model.ProviderHTTPError{
		StatusCode: 400,
		Code:       "context_length_exceeded",
		Message:    "context length exceeded",
	}
	modelClient := &fakeModel{rounds: []fakeRound{{events: []model.StreamEvent{
		{
			ToolCalls:    []message.ToolCall{{ID: "partial-call", Name: "Read", Input: json.RawMessage(`{"file_path":"go.mod"}`)}},
			ProviderData: json.RawMessage(`{"transport":"openai-responses","version":1,"output_items":[{"type":"function_call","call_id":"partial-call","name":"Read","arguments":"{}"}]}`),
		},
		{Err: contextErr},
	}}}}
	output := &fakeUI{}
	readTool := &countingRecoveryTool{}
	registry := tool.NewRegistry()
	registry.Register(readTool)
	runner := NewRunnerWithInstructionRoot(modelClient, output, registry, nil, "partial-tool-context-error", t.TempDir())
	runner.SetContextLimitTokens(1_000_000)
	runner.setHistory(contextRecoveryHistory())

	_, err := runner.RunTurn(context.Background(), "continue")
	if !errors.Is(err, contextErr) {
		t.Fatalf("RunTurn() error = %v, want original stream error", err)
	}
	if len(modelClient.calls) != 1 {
		t.Fatalf("model calls = %d, want partial tool-call stream not replayed", len(modelClient.calls))
	}
	if readTool.calls != 0 {
		t.Fatalf("tool calls = %d, want incomplete tool call not executed", readTool.calls)
	}
	if len(output.toolCalls) != 0 || len(output.toolResults) != 0 {
		t.Fatalf("tool UI events = calls %#v results %#v, want none", output.toolCalls, output.toolResults)
	}
	if countSystemEventsByTitle(output.systemEvents(), "context-recovery") != 0 {
		t.Fatalf("unexpected recovery notice: %#v", output.systemEvents())
	}
}

func TestRunTurnDoesNotRecoverContextErrorAfterPartialSSEThinking(t *testing.T) {
	contextErr := &model.ProviderHTTPError{
		StatusCode: 400,
		Code:       "context_length_exceeded",
		Message:    "context length exceeded",
	}
	modelClient := &fakeModel{rounds: []fakeRound{
		{events: []model.StreamEvent{
			{Thinking: "partial reasoning"},
			{Err: contextErr},
		}},
		{events: []model.StreamEvent{{Delta: "summary", Done: true}}},
		{events: []model.StreamEvent{{Delta: "incorrect replay", Done: true}}},
	}}
	output := &fakeUI{}
	runner := NewRunnerWithInstructionRoot(modelClient, output, tool.NewRegistry(), nil, "partial-thinking-context-error", t.TempDir())
	runner.SetContextLimitTokens(1_000_000)
	runner.setHistory(contextRecoveryHistory())

	_, err := runner.RunTurn(context.Background(), "continue")
	if !errors.Is(err, contextErr) {
		t.Fatalf("RunTurn() error = %v, want original stream error", err)
	}
	if len(modelClient.calls) != 1 {
		t.Fatalf("model calls = %d, want established thinking stream not replayed", len(modelClient.calls))
	}
	if got := strings.Join(output.thinking, ""); got != "partial reasoning" {
		t.Fatalf("thinking output = %q, want original reasoning once", got)
	}
	if countSystemEventsByTitle(output.systemEvents(), "context-recovery") != 0 {
		t.Fatalf("unexpected recovery notice: %#v", output.systemEvents())
	}
}

func TestPartialAssistantMessagePreservesToolCallsAndProviderData(t *testing.T) {
	providerData := json.RawMessage(`{"transport":"openai-responses","version":1}`)
	state := &turnState{
		toolCalls:    []message.ToolCall{{ID: "partial-call", Name: "Read", Input: json.RawMessage(`{}`)}},
		providerData: providerData,
	}

	msg := (&Runner{}).partialAssistantMessage(state)
	calls := toolCallsFromMessage(msg)
	if len(calls) != 1 || calls[0].ID != "partial-call" {
		t.Fatalf("partial tool calls = %#v, want partial-call", calls)
	}
	if string(msg.ProviderData) != string(providerData) {
		t.Fatalf("partial ProviderData = %s, want %s", msg.ProviderData, providerData)
	}
}

func contextRecoveryHistory() []message.Message {
	return []message.Message{
		{Role: message.RoleUser, Content: "Original task constraints."},
		{Role: message.RoleAssistant, Content: strings.Repeat("old investigation detail ", 40)},
		{Role: message.RoleUser, Content: "Keep the public API stable."},
		{Role: message.RoleAssistant, Content: strings.Repeat("older implementation detail ", 40)},
	}
}

func messagesContainAdjacentToolPair(messages []message.Message, callID string) bool {
	for i := 0; i+1 < len(messages); i++ {
		calls := toolCallsFromMessage(messages[i])
		results := toolResultsFromMessage(messages[i+1])
		for _, call := range calls {
			if call.ID != callID {
				continue
			}
			for _, result := range results {
				if result.ToolUseID == callID {
					return true
				}
			}
		}
	}
	return false
}

func messagesContainProviderData(messages []message.Message, callID string) bool {
	for _, msg := range messages {
		if strings.Contains(string(msg.ProviderData), callID) {
			return true
		}
	}
	return false
}

func countSystemEventsByTitle(events []ui.SystemEvent, title string) int {
	count := 0
	for _, event := range events {
		if event.Title == title {
			count++
		}
	}
	return count
}
