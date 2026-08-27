package sessionactor

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"paw/internal/actor"
	"paw/internal/loop"
	"paw/internal/message"
	"paw/internal/model"
	"paw/internal/session"
	"paw/internal/tool"
	toolfile "paw/internal/tool/file"
	"paw/internal/ui"
)

type scriptedModel struct{}

type capturingRichModel struct {
	messages []message.Message
}

func (m *capturingRichModel) StreamMessage(_ context.Context, messages []message.Message, _ []model.ToolDefinition) (<-chan model.StreamEvent, error) {
	m.messages = message.CloneMessages(messages)
	events := make(chan model.StreamEvent, 2)
	events <- model.StreamEvent{Delta: "hello"}
	events <- model.StreamEvent{Done: true}
	close(events)
	return events, nil
}

func (*scriptedModel) StreamMessage(context.Context, []message.Message, []model.ToolDefinition) (<-chan model.StreamEvent, error) {
	events := make(chan model.StreamEvent, 2)
	events <- model.StreamEvent{Delta: "hello"}
	events <- model.StreamEvent{Done: true}
	close(events)
	return events, nil
}

type countingModel struct{ calls atomic.Int64 }

func (m *countingModel) StreamMessage(context.Context, []message.Message, []model.ToolDefinition) (<-chan model.StreamEvent, error) {
	m.calls.Add(1)
	events := make(chan model.StreamEvent, 2)
	events <- model.StreamEvent{Delta: "retried"}
	events <- model.StreamEvent{Done: true}
	close(events)
	return events, nil
}

type toolCallModel struct {
	call  message.ToolCall
	calls atomic.Int64
}

func (m *toolCallModel) StreamMessage(context.Context, []message.Message, []model.ToolDefinition) (<-chan model.StreamEvent, error) {
	m.calls.Add(1)
	events := make(chan model.StreamEvent, 1)
	events <- model.StreamEvent{ToolCalls: []message.ToolCall{m.call}, Done: true, FinishReason: model.FinishReasonToolCalls}
	close(events)
	return events, nil
}

type countingTool struct{ calls int }

func (t *countingTool) Name() string                 { return "Count" }
func (t *countingTool) Description() string          { return "count" }
func (t *countingTool) InputSchema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (t *countingTool) Run(context.Context, json.RawMessage) (string, error) {
	t.calls++
	return "unexpected", nil
}

type recordingUI struct {
	mu     sync.Mutex
	events []string
}

type permissionPrompter struct {
	decision loop.PermissionDecision
	calls    atomic.Int64
}

func (p *permissionPrompter) PromptPermission(context.Context, loop.PermissionRequest) (loop.PermissionDecision, error) {
	p.calls.Add(1)
	return p.decision, nil
}

type blockingPermissionPrompter struct {
	started  chan loop.PermissionRequest
	decision chan loop.PermissionDecision
}

type crashingPermissionPrompter struct{ started chan struct{} }

func (p crashingPermissionPrompter) PromptPermission(context.Context, loop.PermissionRequest) (loop.PermissionDecision, error) {
	if p.started != nil {
		select {
		case p.started <- struct{}{}:
		default:
		}
	}
	panic("simulated crash")
}

func (p *blockingPermissionPrompter) PromptPermission(ctx context.Context, request loop.PermissionRequest) (loop.PermissionDecision, error) {
	select {
	case p.started <- request:
	case <-ctx.Done():
		return "", ctx.Err()
	}
	select {
	case decision := <-p.decision:
		return decision, nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func (u *recordingUI) append(event string) {
	u.mu.Lock()
	u.events = append(u.events, event)
	u.mu.Unlock()
}
func (u *recordingUI) OnAssistantDelta(text string) error { u.append("assistant:" + text); return nil }
func (u *recordingUI) OnToolCall(event ui.ToolCallEvent) error {
	u.append("tool-call:" + event.Name)
	return nil
}
func (u *recordingUI) OnToolResult(event ui.ToolResultEvent) error {
	u.append("tool-result:" + event.Name)
	return nil
}
func (u *recordingUI) OnDone() error { u.append("done"); return nil }

func TestHostRunsDurableTurnThroughSessionActor(t *testing.T) {
	store, err := session.NewJSONLStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewJSONLStore: %v", err)
	}
	output := &recordingUI{}
	engine := loop.NewEngine(&scriptedModel{}, output, tool.NewRegistry(), store, "s1")
	host, err := NewHost(engine, store, "s1")
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}
	defer host.Close()

	result, err := host.RunTurn(context.Background(), "hi")
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	if result.Content != "hello" {
		t.Fatalf("result = %+v, want hello", result)
	}
	history, err := store.LoadResolvedHistory(context.Background(), "s1")
	if err != nil {
		t.Fatalf("LoadResolvedHistory: %v", err)
	}
	if len(history) != 2 || history[0].Content != "hi" || history[1].Content != "hello" {
		t.Fatalf("history = %+v", history)
	}
	envelopes, _, err := store.LoadEnvelopes(context.Background(), "s1")
	if err != nil {
		t.Fatalf("LoadEnvelopes: %v", err)
	}
	foundRuntime := false
	for _, event := range envelopes {
		if event.Kind == "runtime" {
			foundRuntime = true
		}
		if event.Type == "assistant.delta" || event.Type == "turn.done" {
			t.Fatalf("ephemeral display event was persisted: %+v", event)
		}
	}
	if !foundRuntime {
		t.Fatalf("session actor runtime events missing: %+v", envelopes)
	}
	output.mu.Lock()
	defer output.mu.Unlock()
	want := []string{"assistant:hello", "done"}
	if len(output.events) != len(want) || output.events[0] != want[0] || output.events[1] != want[1] {
		t.Fatalf("display events = %#v, want %#v", output.events, want)
	}
}

func TestHostNewSessionCreatesEmptyIsolatedConversation(t *testing.T) {
	store, err := session.NewJSONLStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewJSONLStore: %v", err)
	}
	modelClient := &capturingRichModel{}
	host, err := NewHost(loop.NewEngine(modelClient, &recordingUI{}, tool.NewRegistry(), store, "s1"), store, "s1")
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}
	defer host.Close()

	if _, err := host.RunTurn(context.Background(), "old prompt"); err != nil {
		t.Fatalf("old RunTurn: %v", err)
	}
	newID, loaded, err := host.NewSession(context.Background())
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if newID == "" || newID == "s1" || host.CurrentSessionID() != newID {
		t.Fatalf("new/current session = %q/%q", newID, host.CurrentSessionID())
	}
	if len(loaded.Messages) != 0 || loaded.Recovery != nil {
		t.Fatalf("new session load result = %#v, want empty", loaded)
	}
	if exists, err := store.Exists(context.Background(), newID); err != nil || !exists {
		t.Fatalf("new session root exists=%v err=%v", exists, err)
	}

	if _, err := host.RunTurn(context.Background(), "fresh prompt"); err != nil {
		t.Fatalf("fresh RunTurn: %v", err)
	}
	for _, msg := range modelClient.messages {
		if strings.Contains(msg.Content, "old prompt") {
			t.Fatalf("new model request retained old context: %#v", modelClient.messages)
		}
	}
	oldHistory, err := store.LoadResolvedHistory(context.Background(), "s1")
	if err != nil {
		t.Fatalf("old history: %v", err)
	}
	newHistory, err := store.LoadResolvedHistory(context.Background(), newID)
	if err != nil {
		t.Fatalf("new history: %v", err)
	}
	if len(oldHistory) != 2 || len(newHistory) != 2 || newHistory[0].Content != "fresh prompt" {
		t.Fatalf("old/new history = %#v / %#v", oldHistory, newHistory)
	}
}

// ForkSession 以当前会话的完整上下文创建分支：fork 立即可见父会话历史，
// 之后分支上的新 turn 只写子会话，父会话历史保持不变。
func TestHostForkSessionSharesContextAndDiverges(t *testing.T) {
	store, err := session.NewJSONLStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewJSONLStore: %v", err)
	}
	modelClient := &capturingRichModel{}
	host, err := NewHost(loop.NewEngine(modelClient, &recordingUI{}, tool.NewRegistry(), store, "s1"), store, "s1")
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}
	defer host.Close()

	if _, err := host.RunTurn(context.Background(), "seed prompt"); err != nil {
		t.Fatalf("seed RunTurn: %v", err)
	}
	forkID, loaded, err := host.ForkSession(context.Background(), "s1")
	if err != nil {
		t.Fatalf("ForkSession: %v", err)
	}
	if forkID == "" || forkID == "s1" || host.CurrentSessionID() != forkID {
		t.Fatalf("fork/current session = %q/%q", forkID, host.CurrentSessionID())
	}
	if len(loaded.Messages) != 2 || loaded.Messages[0].Content != "seed prompt" {
		t.Fatalf("forked load result = %#v, want parent context", loaded)
	}
	meta, err := store.GetMeta(context.Background(), forkID)
	if err != nil {
		t.Fatalf("fork meta: %v", err)
	}
	if meta.ParentSessionID != "s1" {
		t.Fatalf("fork parent = %q, want s1", meta.ParentSessionID)
	}

	if _, err := host.RunTurn(context.Background(), "branch prompt"); err != nil {
		t.Fatalf("branch RunTurn: %v", err)
	}
	parentHistory, err := store.LoadResolvedHistory(context.Background(), "s1")
	if err != nil {
		t.Fatalf("parent history: %v", err)
	}
	branchHistory, err := store.LoadResolvedHistory(context.Background(), forkID)
	if err != nil {
		t.Fatalf("branch history: %v", err)
	}
	if len(parentHistory) != 2 {
		t.Fatalf("parent history changed after fork: %#v", parentHistory)
	}
	if len(branchHistory) != 4 || branchHistory[0].Content != "seed prompt" || branchHistory[2].Content != "branch prompt" {
		t.Fatalf("branch history = %#v, want parent prefix + new turn", branchHistory)
	}
}

func TestHostPreservesRichImageDataThroughSessionActor(t *testing.T) {
	store, err := session.NewJSONLStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewJSONLStore: %v", err)
	}
	model := &capturingRichModel{}
	host, err := NewHost(loop.NewEngine(model, &recordingUI{}, tool.NewRegistry(), store, "s1"), store, "s1")
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}
	defer host.Close()

	input := message.Message{
		Role:    message.RoleUser,
		Content: "describe [Image 1]",
		Parts: []message.ContentPart{
			{Type: message.ContentPartText, Text: "describe "},
			{Type: message.ContentPartImage, Image: &message.ImagePart{
				MIMEType: "image/png",
				Data:     []byte("png"),
			}},
		},
	}
	if _, err := host.RunRichTurn(context.Background(), input); err != nil {
		t.Fatalf("RunRichTurn: %v", err)
	}
	var imageData []byte
	for _, msg := range model.messages {
		for _, part := range msg.Parts {
			if part.Type == message.ContentPartImage && part.Image != nil {
				imageData = part.Image.Data
			}
		}
	}
	if string(imageData) != "png" {
		t.Fatalf("model image data = %q, want %q; messages = %#v", imageData, "png", model.messages)
	}
}

func TestHostStateFoldsTranscriptWithoutActorMailbox(t *testing.T) {
	store, err := session.NewJSONLStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewJSONLStore: %v", err)
	}
	host, err := NewHost(loop.NewEngine(&scriptedModel{}, &recordingUI{}, tool.NewRegistry(), store, "s1"), store, "s1")
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}
	if err := host.ActivateGoal(context.Background(), "goal-1", "paused"); err != nil {
		t.Fatalf("ActivateGoal: %v", err)
	}
	host.Close()
	state, err := host.State(context.Background(), "s1")
	if err != nil {
		t.Fatalf("State after actor shutdown: %v", err)
	}
	if state.ActiveGoalID != "goal-1" || state.GoalStatus != "paused" {
		t.Fatalf("state = %#v", state)
	}
}

func TestPermissionDecisionIsDurableAndIdempotent(t *testing.T) {
	store, err := session.NewJSONLStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	host, err := NewHost(loop.NewEngine(&scriptedModel{}, &recordingUI{}, tool.NewRegistry(), store, "s1"), store, "s1")
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close()
	prompter := &permissionPrompter{decision: loop.PermissionAllowOnce}
	host.SetPermissionPrompter(prompter)
	request := loop.PermissionRequest{SessionID: "s1", TurnID: "turn-1", ToolCallID: "read-1", CanonicalPath: "/tmp/outside.txt"}
	for range 2 {
		decision, err := host.Decide(context.Background(), request)
		if err != nil || decision != loop.PermissionAllowOnce {
			t.Fatalf("Decide = %q, %v", decision, err)
		}
	}
	if prompter.calls.Load() != 1 {
		t.Fatalf("prompter calls = %d, want 1", prompter.calls.Load())
	}
	state, err := host.State(context.Background(), "s1")
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Permissions) != 1 || state.Permissions[0].Decision != loop.PermissionAllowOnce {
		t.Fatalf("permissions = %#v", state.Permissions)
	}
	host.system.Drain()
	envelopes, _, err := store.LoadEnvelopes(context.Background(), "s1")
	if err != nil {
		t.Fatal(err)
	}
	foundDecisionInbox := false
	for _, envelope := range envelopes {
		if envelope.Kind != "runtime" || envelope.Type != "sys.inbox.received" {
			continue
		}
		var payload struct {
			Msg struct {
				Kind string `json:"kind"`
			} `json:"msg"`
		}
		if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		foundDecisionInbox = foundDecisionInbox || payload.Msg.Kind == msgPermissionDecision
	}
	if !foundDecisionInbox {
		t.Fatal("permission decision was not delivered through the durable actor inbox")
	}
}

func TestRepublishPendingPermissionWaitsForPrompter(t *testing.T) {
	store, err := session.NewJSONLStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	host, err := NewHost(loop.NewEngine(&scriptedModel{}, &recordingUI{}, tool.NewRegistry(), store, "s1"), store, "s1")
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close()
	request := loop.PermissionRequest{SessionID: "s1", TurnID: "turn-1", ToolCallID: "read-1", CanonicalPath: "/tmp/outside.txt"}
	id := permissionID(request)
	if err := host.appendDomain(context.Background(), "s1", EventPermissionRequested, permissionRecord{ID: id, Request: request}); err != nil {
		t.Fatal(err)
	}

	host.RepublishPendingPermissions("s1")
	time.Sleep(20 * time.Millisecond)
	state, err := host.State(context.Background(), "s1")
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Permissions) != 1 || state.Permissions[0].Decision != "" {
		t.Fatalf("permission changed without a prompter: %#v", state.Permissions)
	}
}

func TestRestartRepublishesPendingPermissionOnce(t *testing.T) {
	store, err := session.NewJSONLStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	request := loop.PermissionRequest{SessionID: "s1", TurnID: "turn-1", ToolCallID: "read-1", CanonicalPath: "/tmp/outside.txt"}
	id := permissionID(request)
	first, err := NewHost(loop.NewEngine(&scriptedModel{}, &recordingUI{}, tool.NewRegistry(), store, "s1"), store, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if err := first.appendDomain(context.Background(), "s1", EventPermissionRequested, permissionRecord{ID: id, Request: request}); err != nil {
		t.Fatal(err)
	}
	first.Close()

	second, err := NewHost(loop.NewEngine(&scriptedModel{}, &recordingUI{}, tool.NewRegistry(), store, "s1"), store, "s1")
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	prompter := &blockingPermissionPrompter{
		started:  make(chan loop.PermissionRequest, 2),
		decision: make(chan loop.PermissionDecision, 1),
	}
	second.SetPermissionPrompter(prompter)
	if _, err := second.LoadSession(context.Background(), "s1"); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-prompter.started:
		if got != request {
			t.Fatalf("request = %#v, want %#v", got, request)
		}
	case <-time.After(time.Second):
		t.Fatal("pending permission was not republished")
	}
	second.RepublishPendingPermissions("s1")
	select {
	case duplicate := <-prompter.started:
		t.Fatalf("pending permission was republished twice: %#v", duplicate)
	case <-time.After(20 * time.Millisecond):
	}
	prompter.decision <- loop.PermissionAllowOnce
	deadline := time.Now().Add(time.Second)
	for {
		state, stateErr := second.State(context.Background(), "s1")
		if stateErr != nil {
			t.Fatal(stateErr)
		}
		if len(state.Permissions) == 1 && state.Permissions[0].Decision == loop.PermissionAllowOnce {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("permission decision was not persisted: %#v", state.Permissions)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestForkDoesNotInheritParentModesOrPermission(t *testing.T) {
	store, err := session.NewJSONLStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := store.CreateRoot(ctx, session.CreateRootRequest{SessionID: "parent"}); err != nil {
		t.Fatal(err)
	}
	host, err := NewHost(loop.NewEngine(&scriptedModel{}, &recordingUI{}, tool.NewRegistry(), store, "parent"), store, "parent")
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close()
	if err := host.ActivateGoal(ctx, "goal-parent", "paused"); err != nil {
		t.Fatal(err)
	}
	if err := host.SavePlan(ctx, "plan-parent", "paused", map[string]string{"id": "plan-parent"}); err != nil {
		t.Fatal(err)
	}
	request := loop.PermissionRequest{SessionID: "parent", TurnID: "turn-parent", ToolCallID: "read-parent", CanonicalPath: "/tmp/outside.txt"}
	if err := host.appendDomain(ctx, "parent", EventPermissionRequested, permissionRecord{ID: permissionID(request), Request: request}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Fork(ctx, session.ForkRequest{SessionID: "child", ParentSessionID: "parent", ForkFromSeq: -1}); err != nil {
		t.Fatal(err)
	}

	result, err := host.LoadSession(ctx, "child")
	if err != nil {
		t.Fatal(err)
	}
	if result.Modes == nil {
		t.Fatal("child mode snapshot is nil")
	}
	if *result.Modes != (loop.SessionModeSnapshot{}) {
		t.Fatalf("child inherited parent modes: %#v", result.Modes)
	}
	state, err := host.State(ctx, "child")
	if err != nil {
		t.Fatal(err)
	}
	if state.ActiveGoalID != "" || state.ActivePlanID != "" || len(state.Permissions) != 0 {
		t.Fatalf("child state = %#v", state)
	}
}

func TestLoadSessionReportsPendingPermissionWithoutExecuting(t *testing.T) {
	store, err := session.NewJSONLStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	host, err := NewHost(loop.NewEngine(&scriptedModel{}, &recordingUI{}, tool.NewRegistry(), store, "s1"), store, "s1")
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close()
	request := loop.PermissionRequest{SessionID: "s1", TurnID: "turn-1", ToolCallID: "read-1", CanonicalPath: "/tmp/outside.txt"}
	id := permissionID(request)
	if err := host.appendDomain(context.Background(), "s1", EventPermissionRequested, permissionRecord{ID: id, Request: request}); err != nil {
		t.Fatal(err)
	}
	result, err := host.LoadSession(context.Background(), "s1")
	if err != nil {
		t.Fatal(err)
	}
	if result.Modes == nil || result.Modes.PendingPermissionID != id {
		t.Fatalf("modes = %#v", result.Modes)
	}
}

func TestLoadSessionDoesNotRetryToolStartedWithoutResult(t *testing.T) {
	store, err := session.NewJSONLStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	call := message.ToolCall{ID: "write-1", Name: "Write", Input: []byte(`{"file_path":"x"}`)}
	if err := store.BeginTurn(ctx, "s1", "turn-1", message.Message{Role: message.RoleUser, Content: "write"}); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendAssistant(ctx, "s1", "turn-1", message.Message{Role: message.RoleAssistant, ToolUse: &call}); err != nil {
		t.Fatal(err)
	}
	host, err := NewHost(loop.NewEngine(&scriptedModel{}, &recordingUI{}, tool.NewRegistry(), store, "s1"), store, "s1")
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close()
	if err := host.ToolStarted(ctx, loop.ToolStart{SessionID: "s1", TurnID: "turn-1", ToolCallID: "write-1", ToolName: "Write", CallIndex: 0}); err != nil {
		t.Fatal(err)
	}
	result, err := host.LoadSession(ctx, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if result.Recovery == nil || len(result.Recovery.CompletedToolResults) != 0 {
		t.Fatalf("recovery = %#v", result.Recovery)
	}
	found := false
	for _, msg := range result.Messages {
		if msg.ToolResult != nil && msg.ToolResult.ToolUseID == "write-1" && msg.ToolResult.IsError {
			found = true
		}
	}
	if !found {
		t.Fatalf("interrupted result missing from %#v", result.Messages)
	}
	state, err := host.State(ctx, "s1")
	if err != nil || len(state.StartedTools) != 0 {
		t.Fatalf("state=%#v err=%v", state, err)
	}
}

func TestRecoveredTurnDoesNotReplayMeaningfulProviderOutput(t *testing.T) {
	store, err := session.NewJSONLStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := store.BeginTurn(ctx, "s1", "turn-1", message.Message{Role: message.RoleUser, Content: "hello"}); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendPartialAssistant(ctx, "s1", "turn-1", message.Message{Role: message.RoleAssistant, Content: "partial"}); err != nil {
		t.Fatal(err)
	}
	model := &countingModel{}
	host, err := NewHost(loop.NewEngine(model, &recordingUI{}, tool.NewRegistry(), store, "s1"), store, "s1")
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close()
	_, err = host.execute("s1", "replay", turnRequest{Input: message.Message{Role: message.RoleUser, Content: "hello"}, Timing: loop.TurnTiming{TurnID: "turn-1"}})
	if err == nil || model.calls.Load() != 0 {
		t.Fatalf("err=%v model calls=%d", err, model.calls.Load())
	}
}

func TestRecoveredTurnMayRetryBeforeProviderOutput(t *testing.T) {
	store, err := session.NewJSONLStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := store.BeginTurn(ctx, "s1", "turn-1", message.Message{Role: message.RoleUser, Content: "hello"}); err != nil {
		t.Fatal(err)
	}
	if err := store.FailTurn(ctx, "s1", "turn-1", errors.New("provider disconnected before output")); err != nil {
		t.Fatal(err)
	}
	model := &countingModel{}
	host, err := NewHost(loop.NewEngine(model, &recordingUI{}, tool.NewRegistry(), store, "s1"), store, "s1")
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close()
	execution, err := host.execute("s1", "replay", turnRequest{Input: message.Message{Role: message.RoleUser, Content: "hello"}, Timing: loop.TurnTiming{TurnID: "turn-1"}})
	if err != nil || model.calls.Load() != 1 || execution.Message.Content != "retried" {
		t.Fatalf("execution=%#v err=%v model calls=%d", execution, err, model.calls.Load())
	}
	history, err := store.LoadResolvedHistory(ctx, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 2 || history[0].Content != "hello" || history[1].Content != "retried" {
		t.Fatalf("history = %#v", history)
	}
}

func TestLoadSessionActivatesDurableInboxTurn(t *testing.T) {
	store, err := session.NewJSONLStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	firstModel := &countingModel{}
	first, err := NewHost(loop.NewEngine(firstModel, &recordingUI{}, tool.NewRegistry(), store, "s1"), store, "s1")
	if err != nil {
		t.Fatal(err)
	}
	var crashed atomic.Bool
	hit := make(chan struct{}, 1)
	first.system.SetJournalHook(func(stage string, id actor.ActorID) {
		if stage == actor.StageInboxReceived && id == actorID("s1") && crashed.CompareAndSwap(false, true) {
			hit <- struct{}{}
			panic("simulated crash")
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_, _ = first.RunTurnWithTiming(ctx, "hello", "turn-1", time.Now())
		close(done)
	}()
	select {
	case <-hit:
	case <-time.After(time.Second):
		t.Fatal("durable inbox crash point was not reached")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("crashed inbox turn did not stop")
	}
	if !crashed.Load() || firstModel.calls.Load() != 0 {
		t.Fatalf("crashed=%v first model calls=%d", crashed.Load(), firstModel.calls.Load())
	}
	first.Close()

	secondModel := &countingModel{}
	second, err := NewHost(loop.NewEngine(secondModel, &recordingUI{}, tool.NewRegistry(), store, "s1"), store, "s1")
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if _, err := second.LoadSession(context.Background(), "s1"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	var history []message.Message
	for time.Now().Before(deadline) {
		history, err = store.LoadResolvedHistory(context.Background(), "s1")
		if err != nil {
			t.Fatal(err)
		}
		if len(history) == 2 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if secondModel.calls.Load() != 1 {
		envelopes, _, _ := store.LoadEnvelopes(context.Background(), "s1")
		t.Fatalf("recovered model calls = %d, want 1; envelopes=%#v", secondModel.calls.Load(), envelopes)
	}
	if len(history) != 2 || history[0].Content != "hello" || history[1].Content != "retried" {
		t.Fatalf("history = %#v", history)
	}
}

func TestRestartRepublishesPermissionAndContinuesDurableToolTurn(t *testing.T) {
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
	firstModel := &toolCallModel{call: call}
	firstRegistry := tool.NewRegistry()
	firstRegistry.Register(&toolfile.ReadTool{Root: workspace})
	first, err := NewHost(loop.NewEngine(firstModel, &recordingUI{}, firstRegistry, store, "s1"), store, "s1")
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{}, 1)
	first.SetPermissionPrompter(crashingPermissionPrompter{started: started})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_, _ = first.RunTurnWithTiming(ctx, "read", "turn-1", time.Now())
		close(done)
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("permission prompt did not start")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("crashed permission turn did not stop")
	}
	first.Close()
	state, err := first.State(context.Background(), "s1")
	if err != nil {
		t.Fatal(err)
	}
	if firstModel.calls.Load() != 1 || len(state.Permissions) != 1 || state.Permissions[0].Decision != "" {
		t.Fatalf("first calls=%d permissions=%#v", firstModel.calls.Load(), state.Permissions)
	}

	secondModel := &countingModel{}
	secondRegistry := tool.NewRegistry()
	secondRegistry.Register(&toolfile.ReadTool{Root: workspace})
	second, err := NewHost(loop.NewEngine(secondModel, &recordingUI{}, secondRegistry, store, "s1"), store, "s1")
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	prompter := &permissionPrompter{decision: loop.PermissionAllowOnce}
	second.SetPermissionPrompter(prompter)
	if _, err := second.LoadSession(context.Background(), "s1"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for secondModel.calls.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if secondModel.calls.Load() != 1 || prompter.calls.Load() != 1 {
		envelopes, _, _ := store.LoadEnvelopes(context.Background(), "s1")
		t.Fatalf("recovered model calls=%d prompt calls=%d envelopes=%#v", secondModel.calls.Load(), prompter.calls.Load(), envelopes)
	}
	records, err := store.LoadResolvedJournalRecords(context.Background(), "s1")
	if err != nil {
		t.Fatal(err)
	}
	foundReadResult := false
	for _, record := range records {
		if record.Kind == session.JournalToolResult && record.ToolResult != nil && record.ToolResult.ToolUseID == "read-1" {
			foundReadResult = !record.ToolResult.IsError && strings.Contains(record.ToolResult.Content, "outside")
		}
	}
	if !foundReadResult {
		t.Fatalf("approved Read result missing from %#v", records)
	}
}

func TestRecoveredToolResultContinuesWithoutRepeatingTool(t *testing.T) {
	store, err := session.NewJSONLStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	call := message.ToolCall{ID: "count-1", Name: "Count", Input: json.RawMessage(`{}`)}
	if err := store.BeginTurn(ctx, "s1", "turn-1", message.Message{Role: message.RoleUser, Content: "count"}); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendAssistant(ctx, "s1", "turn-1", message.Message{Role: message.RoleAssistant, ToolUse: &call}); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendToolResult(ctx, "s1", "turn-1", 0, message.ToolResult{ToolUseID: "count-1", Content: "already done"}); err != nil {
		t.Fatal(err)
	}
	model := &countingModel{}
	registry := tool.NewRegistry()
	counter := &countingTool{}
	registry.Register(counter)
	host, err := NewHost(loop.NewEngine(model, &recordingUI{}, registry, store, "s1"), store, "s1")
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close()
	execution, err := host.execute("s1", "replay", turnRequest{Input: message.Message{Role: message.RoleUser, Content: "count"}, Timing: loop.TurnTiming{TurnID: "turn-1"}})
	if err != nil || execution.Message.Content != "retried" || model.calls.Load() != 1 || counter.calls != 0 {
		t.Fatalf("execution=%#v err=%v model=%d tool=%d", execution, err, model.calls.Load(), counter.calls)
	}
}

func TestRecoveredPendingReadDecisionContinuesExactCall(t *testing.T) {
	workspace := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	store, err := session.NewJSONLStore(filepath.Join(workspace, ".paw"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	call := message.ToolCall{ID: "read-1", Name: "Read", Input: json.RawMessage(`{"file_path":"` + outside + `"}`)}
	if err := store.BeginTurn(ctx, "s1", "turn-1", message.Message{Role: message.RoleUser, Content: "read"}); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendAssistant(ctx, "s1", "turn-1", message.Message{Role: message.RoleAssistant, ToolUse: &call}); err != nil {
		t.Fatal(err)
	}
	model := &countingModel{}
	registry := tool.NewRegistry()
	registry.Register(&toolfile.ReadTool{Root: workspace})
	host, err := NewHost(loop.NewEngine(model, &recordingUI{}, registry, store, "s1"), store, "s1")
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close()
	canonical, err := filepath.EvalSymlinks(outside)
	if err != nil {
		t.Fatal(err)
	}
	request := loop.PermissionRequest{SessionID: "s1", TurnID: "turn-1", ToolCallID: "read-1", CanonicalPath: canonical}
	id := permissionID(request)
	if err := host.system.Suspend(actorID("s1"), "pending"); err != nil {
		t.Fatal(err)
	}
	if err := host.appendDomain(ctx, "s1", EventPermissionRequested, permissionRecord{ID: id, Request: request}); err != nil {
		t.Fatal(err)
	}
	host.SetPermissionPrompter(&permissionPrompter{decision: loop.PermissionAllowOnce})
	if decision, err := host.Decide(ctx, request); err != nil || decision != loop.PermissionAllowOnce {
		t.Fatalf("Decide=%s err=%v", decision, err)
	}
	execution, err := host.execute("s1", "replay", turnRequest{Input: message.Message{Role: message.RoleUser, Content: "read"}, Timing: loop.TurnTiming{TurnID: "turn-1"}})
	if err != nil || execution.Message.Content != "retried" || model.calls.Load() != 1 {
		t.Fatalf("execution=%#v err=%v model=%d", execution, err, model.calls.Load())
	}
}

func actorID(sessionID string) actor.ActorID {
	return actor.ActorID{Type: actorType, Key: sessionID}
}
