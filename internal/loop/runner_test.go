package loop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"paw/internal/message"
	"paw/internal/model"
	"paw/internal/session"
	"paw/internal/skill"
	"paw/internal/streamma"
	"paw/internal/tokentracer"
	"paw/internal/tool"
	toolfile "paw/internal/tool/file"
	"paw/internal/ui"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeRound struct {
	events []model.StreamEvent
	err    error
}

type fakeModel struct {
	rounds []fakeRound
	calls  [][]message.Message
	tools  [][]model.ToolDefinition
}

func (m *fakeModel) StreamMessage(ctx context.Context, messages []message.Message, tools []model.ToolDefinition) (<-chan model.StreamEvent, error) {
	copied := append([]message.Message(nil), messages...)
	m.calls = append(m.calls, copied)
	m.tools = append(m.tools, append([]model.ToolDefinition(nil), tools...))

	index := len(m.calls) - 1
	if index >= len(m.rounds) {
		return nil, errors.New("unexpected model call")
	}

	round := m.rounds[index]
	if round.err != nil {
		return nil, round.err
	}

	ch := make(chan model.StreamEvent, len(round.events))
	for _, ev := range round.events {
		ch <- ev
	}
	close(ch)
	return ch, nil
}

type blockingModel struct {
	streams []chan model.StreamEvent
	started chan int
	calls   [][]message.Message
}

func (m *blockingModel) StreamMessage(ctx context.Context, messages []message.Message, _ []model.ToolDefinition) (<-chan model.StreamEvent, error) {
	copied := append([]message.Message(nil), messages...)
	m.calls = append(m.calls, copied)

	index := len(m.calls) - 1
	if index >= len(m.streams) {
		return nil, errors.New("unexpected model call")
	}
	if m.started != nil {
		m.started <- index
	}
	return m.streams[index], nil
}

type fakeUI struct {
	systemMu    sync.Mutex
	deltas      []string
	thinking    []string
	toolCalls   []ui.ToolCallEvent
	toolResults []ui.ToolResultEvent
	system      []ui.SystemEvent
	doneCount   int
}

func (u *fakeUI) OnAssistantDelta(text string) error {
	u.deltas = append(u.deltas, text)
	return nil
}

func (u *fakeUI) OnThinkingDelta(text string) error {
	u.thinking = append(u.thinking, text)
	return nil
}

func (u *fakeUI) OnToolCall(event ui.ToolCallEvent) error {
	u.toolCalls = append(u.toolCalls, event)
	return nil
}

func (u *fakeUI) OnToolResult(event ui.ToolResultEvent) error {
	u.toolResults = append(u.toolResults, event)
	return nil
}

func (u *fakeUI) OnDone() error {
	u.doneCount++
	return nil
}

func (u *fakeUI) OnSystemMessage(event ui.SystemEvent) error {
	u.systemMu.Lock()
	u.system = append(u.system, event)
	u.systemMu.Unlock()
	return nil
}

func (u *fakeUI) systemEvents() []ui.SystemEvent {
	u.systemMu.Lock()
	defer u.systemMu.Unlock()
	return append([]ui.SystemEvent(nil), u.system...)
}

type mutationCaptureUI struct {
	calls   []ui.ToolCallEvent
	results []ui.ToolResultEvent
	onCall  func(ui.ToolCallEvent)
}

func (*mutationCaptureUI) OnAssistantDelta(string) error { return nil }
func (u *mutationCaptureUI) OnToolCall(event ui.ToolCallEvent) error {
	u.calls = append(u.calls, event)
	if u.onCall != nil {
		u.onCall(event)
	}
	return nil
}
func (u *mutationCaptureUI) OnToolResult(event ui.ToolResultEvent) error {
	u.results = append(u.results, event)
	return nil
}
func (*mutationCaptureUI) OnDone() error               { return nil }
func (*mutationCaptureUI) ConsumesFileMutations() bool { return true }

type nonMutationUI struct {
	calls   []ui.ToolCallEvent
	results []ui.ToolResultEvent
}

func (*nonMutationUI) OnAssistantDelta(string) error { return nil }
func (u *nonMutationUI) OnToolCall(event ui.ToolCallEvent) error {
	u.calls = append(u.calls, event)
	return nil
}
func (u *nonMutationUI) OnToolResult(event ui.ToolResultEvent) error {
	u.results = append(u.results, event)
	return nil
}
func (*nonMutationUI) OnDone() error { return nil }

func promptTextForTest(messages []message.Message) string {
	var builder strings.Builder
	for _, msg := range messages {
		builder.WriteString(string(msg.Role))
		builder.WriteString(":\n")
		builder.WriteString(msg.Content)
		builder.WriteByte('\n')
	}
	return builder.String()
}

func writeLoopTestSkill(t *testing.T, root, name, body string) string {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, skill.SkillFileName)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRunnerWorkspaceRoot(t *testing.T) {
	root := t.TempDir()
	runner := NewRunnerWithInstructionRoot(nil, nil, nil, nil, "", root)

	if got := runner.WorkspaceRoot(); got != root {
		t.Fatalf("WorkspaceRoot() = %q, want %q", got, root)
	}
}

func TestRunToolCallEmitsCompleteEditMutationSnapshot(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "a.txt")
	before := "x\nkeep\nx\n"
	if err := os.WriteFile(path, []byte(before), 0o644); err != nil {
		t.Fatal(err)
	}
	state := toolfile.NewReadStateStore()
	state.Record(path, []byte(before))
	registry := tool.NewRegistry()
	registry.Register(&toolfile.EditTool{Root: root, ReadState: state})
	capture := &mutationCaptureUI{}
	runner := &Runner{ui: capture, registry: registry, workRoot: root}
	tracer := tokentracer.New("mutation-test")
	runner.SetTokenTracer(tracer)

	result, err := runner.runToolCall(context.Background(), message.ToolCall{
		ID:    "edit-1",
		Name:  "Edit",
		Input: []byte(`{"file_path":"a.txt","old_string":"x","new_string":"y","replace_all":true}`),
	})
	if err != nil || result.IsError {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if result.Content != "edited a.txt (2 replacements)" {
		t.Fatalf("model-visible content = %q", result.Content)
	}
	if len(capture.calls) != 1 || capture.calls[0].FileMutation == nil {
		t.Fatalf("calls = %+v", capture.calls)
	}
	if !capture.calls[0].FileMutationKnown || !capture.calls[0].IsFileMutation {
		t.Fatalf("call mutation metadata = %+v", capture.calls[0])
	}
	if got := capture.calls[0].FileMutation; got.Before != before || !got.BeforeExists || got.After != "" || got.AfterExists {
		t.Fatalf("call snapshot = %+v", got)
	}
	if len(capture.results) != 1 || capture.results[0].FileMutation == nil {
		t.Fatalf("results = %+v", capture.results)
	}
	if !capture.results[0].FileMutationKnown || !capture.results[0].IsFileMutation {
		t.Fatalf("result mutation metadata = %+v", capture.results[0])
	}
	snapshot := capture.results[0].FileMutation
	if snapshot.Before != before || snapshot.After != "y\nkeep\ny\n" || !snapshot.BeforeExists || !snapshot.AfterExists {
		t.Fatalf("snapshot = %+v", snapshot)
	}
	if strings.Contains(result.Content, before) || strings.Contains(result.Content, snapshot.After) {
		t.Fatalf("model-visible result leaked file contents: %q", result.Content)
	}
	foundTraceResult := false
	for _, event := range tracer.Snapshot().Events {
		if event.Type != "tool_result" {
			continue
		}
		foundTraceResult = true
		if event.Data["content"] != result.Content {
			t.Fatalf("trace tool_result content = %#v, want %q", event.Data["content"], result.Content)
		}
		if _, ok := event.Data["file_mutation"]; ok {
			t.Fatalf("trace tool_result leaked mutation snapshot: %#v", event.Data)
		}
		if strings.Contains(fmt.Sprint(event.Data), before) || strings.Contains(fmt.Sprint(event.Data), snapshot.After) {
			t.Fatalf("trace tool_result leaked file contents: %#v", event.Data)
		}
	}
	if !foundTraceResult {
		t.Fatal("missing tool_result trace event")
	}
}

func TestRunToolCallWriteEmptySnapshotPreservesExistence(t *testing.T) {
	for _, test := range []struct {
		name         string
		createBefore bool
	}{
		{name: "new empty file"},
		{name: "existing empty file", createBefore: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "empty.txt")
			if test.createBefore {
				if err := os.WriteFile(path, nil, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			registry := tool.NewRegistry()
			registry.Register(&toolfile.WriteTool{Root: root, ReadState: toolfile.NewReadStateStore()})
			capture := &mutationCaptureUI{}
			runner := &Runner{ui: capture, registry: registry, workRoot: root}

			result, err := runner.runToolCall(context.Background(), message.ToolCall{
				ID: "write-empty", Name: "Write", Input: []byte(`{"file_path":"empty.txt","content":""}`),
			})
			if err != nil || result.IsError {
				t.Fatalf("result=%+v err=%v", result, err)
			}
			if len(capture.calls) != 1 || capture.calls[0].FileMutation == nil {
				t.Fatalf("calls = %+v", capture.calls)
			}
			if got := capture.calls[0].FileMutation; got.Before != "" || got.BeforeExists != test.createBefore || got.After != "" || got.AfterExists {
				t.Fatalf("call snapshot = %+v, want BeforeExists=%v and no After", got, test.createBefore)
			}
			if len(capture.results) != 1 || capture.results[0].FileMutation == nil {
				t.Fatalf("results = %+v", capture.results)
			}
			if got := capture.results[0].FileMutation; got.Before != "" || got.BeforeExists != test.createBefore || got.After != "" || !got.AfterExists {
				t.Fatalf("result snapshot = %+v, want BeforeExists=%v AfterExists=true with empty contents", got, test.createBefore)
			}
		})
	}
}

func TestRunToolCallAbsoluteMutationPathsRespectWorkspace(t *testing.T) {
	root := t.TempDir()
	inside := filepath.Join(root, "inside.txt")
	before := "before\n"
	if err := os.WriteFile(inside, []byte(before), 0o644); err != nil {
		t.Fatal(err)
	}
	state := toolfile.NewReadStateStore()
	state.Record(inside, []byte(before))
	registry := tool.NewRegistry()
	registry.Register(&toolfile.EditTool{Root: root, ReadState: state})

	t.Run("inside workspace", func(t *testing.T) {
		capture := &mutationCaptureUI{}
		runner := &Runner{ui: capture, registry: registry, workRoot: root}
		input, err := json.Marshal(map[string]any{"file_path": inside, "old_string": "before", "new_string": "after"})
		if err != nil {
			t.Fatal(err)
		}
		result, err := runner.runToolCall(context.Background(), message.ToolCall{ID: "edit-absolute", Name: "Edit", Input: input})
		if err != nil || result.IsError {
			t.Fatalf("result=%+v err=%v", result, err)
		}
		if len(capture.results) != 1 || capture.results[0].FileMutation == nil {
			t.Fatalf("results = %+v", capture.results)
		}
		if got := capture.results[0].FileMutation; got.Before != before || got.After != "after\n" || !got.BeforeExists || !got.AfterExists {
			t.Fatalf("absolute-path snapshot = %+v", got)
		}
	})

	t.Run("outside workspace", func(t *testing.T) {
		outside := filepath.Join(t.TempDir(), "outside.txt")
		if err := os.WriteFile(outside, []byte("outside\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		capture := &mutationCaptureUI{}
		runner := &Runner{ui: capture, registry: registry, workRoot: root}
		input, err := json.Marshal(map[string]any{"file_path": outside, "old_string": "outside", "new_string": "changed"})
		if err != nil {
			t.Fatal(err)
		}
		result, err := runner.runToolCall(context.Background(), message.ToolCall{ID: "edit-outside", Name: "Edit", Input: input})
		if err != nil {
			t.Fatalf("runToolCall: %v", err)
		}
		if !result.IsError || !strings.Contains(result.Content, "path escapes allowed roots") {
			t.Fatalf("result = %+v, want workspace escape error", result)
		}
		if len(capture.calls) != 1 || capture.calls[0].FileMutation != nil || len(capture.results) != 1 || capture.results[0].FileMutation != nil {
			t.Fatalf("outside path captured mutation: calls=%+v results=%+v", capture.calls, capture.results)
		}
		if !capture.calls[0].FileMutationKnown || !capture.calls[0].IsFileMutation || !capture.results[0].FileMutationKnown || !capture.results[0].IsFileMutation {
			t.Fatalf("outside path lost resolved capability metadata: calls=%+v results=%+v", capture.calls, capture.results)
		}
	})
}

func TestRunToolCallDoesNotAttachMutationOnEditFailure(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "a.txt")
	if err := os.WriteFile(path, []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	registry := tool.NewRegistry()
	registry.Register(&toolfile.EditTool{Root: root, ReadState: toolfile.NewReadStateStore()})
	capture := &mutationCaptureUI{}
	runner := &Runner{ui: capture, registry: registry, workRoot: root}

	result, err := runner.runToolCall(context.Background(), message.ToolCall{
		ID:    "edit-fail",
		Name:  "Edit",
		Input: []byte(`{"file_path":"a.txt","old_string":"x","new_string":"y"}`),
	})
	if err != nil {
		t.Fatalf("runToolCall: %v", err)
	}
	if !result.IsError || !strings.Contains(result.Content, "use Read first") {
		t.Fatalf("result = %+v", result)
	}
	if len(capture.results) != 1 || capture.results[0].FileMutation != nil {
		t.Fatalf("result events = %+v", capture.results)
	}
}

func TestRunToolCallSkipsSnapshotForUIWithoutMutationConsumer(t *testing.T) {
	root := t.TempDir()
	registry := tool.NewRegistry()
	registry.Register(&toolfile.WriteTool{Root: root, ReadState: toolfile.NewReadStateStore()})
	capture := &nonMutationUI{}
	runner := &Runner{ui: capture, registry: registry, workRoot: root}

	result, err := runner.runToolCall(context.Background(), message.ToolCall{
		ID:    "write-1",
		Name:  "Write",
		Input: []byte(`{"file_path":"new.txt","content":"hello\n"}`),
	})
	if err != nil || result.IsError {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if len(capture.calls) != 1 || capture.calls[0].FileMutation != nil {
		t.Fatalf("call events = %+v", capture.calls)
	}
	if len(capture.results) != 1 || capture.results[0].FileMutation != nil {
		t.Fatalf("result events = %+v", capture.results)
	}
}

func TestRunToolCallDoesNotTreatSameNameToolAsBuiltinMutation(t *testing.T) {
	registry := tool.NewRegistry()
	registry.Register(&fakeTool{name: "Edit", output: "fake"})
	capture := &mutationCaptureUI{}
	runner := &Runner{ui: capture, registry: registry, workRoot: t.TempDir()}

	result, err := runner.runToolCall(context.Background(), message.ToolCall{
		ID: "fake", Name: "Edit", Input: []byte(`{"file_path":"outside"}`),
	})
	if err != nil || result.Content != "fake" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if len(capture.results) != 1 || capture.results[0].FileMutation != nil {
		t.Fatalf("same-name non-capability tool got snapshot: %+v", capture.results)
	}
	if !capture.calls[0].FileMutationKnown || capture.calls[0].IsFileMutation || !capture.results[0].FileMutationKnown || capture.results[0].IsFileMutation {
		t.Fatalf("same-name non-capability metadata calls=%+v results=%+v", capture.calls, capture.results)
	}
}

func TestRunToolCallDoesNotTreatSameNameMCPToolAsBuiltinMutation(t *testing.T) {
	registry := tool.NewRegistry()
	registry.Register(&fakeTool{name: "mcp__Edit", output: "mcp fake"})
	capture := &mutationCaptureUI{}
	runner := &Runner{ui: capture, registry: registry, workRoot: t.TempDir()}

	result, err := runner.runToolCall(context.Background(), message.ToolCall{
		ID: "mcp-fake", Name: "mcp__Edit", Input: []byte(`{"file_path":"outside"}`),
	})
	if err != nil || result.Content != "mcp fake" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if len(capture.results) != 1 || capture.results[0].FileMutation != nil {
		t.Fatalf("same-name MCP tool got snapshot: %+v", capture.results)
	}
	if !capture.calls[0].FileMutationKnown || capture.calls[0].IsFileMutation || !capture.results[0].FileMutationKnown || capture.results[0].IsFileMutation {
		t.Fatalf("same-name MCP metadata calls=%+v results=%+v", capture.calls, capture.results)
	}
}

func TestRunToolCallDoesNotInspectMutationTargetForNonConsumer(t *testing.T) {
	probe := &mutationProbeTool{name: "Probe", path: filepath.Join(t.TempDir(), "missing"), output: "ok"}
	registry := tool.NewRegistry()
	registry.Register(probe)
	capture := &nonMutationUI{}
	runner := &Runner{ui: capture, registry: registry}

	result, err := runner.runToolCall(context.Background(), message.ToolCall{ID: "probe", Name: "Probe", Input: []byte(`{}`)})
	if err != nil || result.IsError || result.Content != "ok" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if probe.targetCalls != 0 {
		t.Fatalf("FileMutationTarget calls = %d, want 0", probe.targetCalls)
	}
}

func TestRunToolCallSnapshotFailureDoesNotChangeToolSuccess(t *testing.T) {
	path := filepath.Join(t.TempDir(), "remove-after-run.txt")
	if err := os.WriteFile(path, []byte("before"), 0o644); err != nil {
		t.Fatal(err)
	}
	probe := &mutationProbeTool{
		name: "Probe", path: path, beforeExists: true, output: "concise success", removeOnRun: true,
	}
	registry := tool.NewRegistry()
	registry.Register(probe)
	capture := &mutationCaptureUI{}
	runner := &Runner{ui: capture, registry: registry}

	result, err := runner.runToolCall(context.Background(), message.ToolCall{ID: "probe", Name: "Probe", Input: []byte(`{}`)})
	if err != nil || result.IsError || result.Content != "concise success" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if len(capture.results) != 1 || capture.results[0].FileMutation != nil {
		t.Fatalf("result events = %+v, want successful result without incomplete snapshot", capture.results)
	}
	if !capture.results[0].FileMutationKnown || !capture.results[0].IsFileMutation {
		t.Fatalf("after-capture failure lost mutation metadata: %+v", capture.results[0])
	}
}

func TestRunToolCallBindsMutationAndRunToResolvedTool(t *testing.T) {
	root := t.TempDir()
	originalPath := filepath.Join(root, "original.txt")
	replacementPath := filepath.Join(root, "replacement.txt")
	if err := os.WriteFile(originalPath, []byte("original before"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(replacementPath, []byte("replacement before"), 0o644); err != nil {
		t.Fatal(err)
	}
	original := &mutationProbeTool{
		name: "Probe", path: originalPath, beforeExists: true, output: "original result", writeOnRun: "original after",
	}
	replacement := &mutationProbeTool{
		name: "Probe", path: replacementPath, beforeExists: true, output: "replacement result", writeOnRun: "replacement after",
	}
	registry := tool.NewRegistry()
	registry.Register(original)
	capture := &mutationCaptureUI{}
	capture.onCall = func(ui.ToolCallEvent) { registry.Register(replacement) }
	runner := &Runner{ui: capture, registry: registry}

	result, err := runner.runToolCall(context.Background(), message.ToolCall{ID: "probe", Name: "Probe", Input: []byte(`{}`)})
	if err != nil || result.IsError || result.Content != "original result" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if original.targetCalls != 1 || original.runCalls != 1 {
		t.Fatalf("original targetCalls=%d runCalls=%d, want 1/1", original.targetCalls, original.runCalls)
	}
	if replacement.targetCalls != 0 || replacement.runCalls != 0 {
		t.Fatalf("replacement targetCalls=%d runCalls=%d, want 0/0", replacement.targetCalls, replacement.runCalls)
	}
	if len(capture.results) != 1 || capture.results[0].FileMutation == nil {
		t.Fatalf("results = %+v", capture.results)
	}
	if got := capture.results[0].FileMutation; got.Before != "original before" || got.After != "original after" || !got.BeforeExists || !got.AfterExists {
		t.Fatalf("snapshot = %+v, want original tool mutation", got)
	}
	data, err := os.ReadFile(replacementPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "replacement before" {
		t.Fatalf("replacement file = %q, want unchanged", data)
	}
}

type fakeStreamMASubagentRunner struct {
	mu                   sync.Mutex
	requests             []StreamMASubagentRequest
	omitBoundaryFor      map[string]bool
	plannerFirstStepSent chan struct{}
	releasePlanner       chan struct{}
	builderStarted       chan struct{}
}

func (r *fakeStreamMASubagentRunner) StreamSubagent(ctx context.Context, req StreamMASubagentRequest) (StreamMASubagentStream, error) {
	r.mu.Lock()
	r.requests = append(r.requests, req)
	r.mu.Unlock()
	sessionID := strings.TrimSpace(req.SessionID)
	if sessionID == "" {
		sessionID = req.AgentID + "-session"
	}
	if req.AgentID == "planner" && r.plannerFirstStepSent != nil {
		return StreamMASubagentStream{Events: r.streamBlockingPlanner(ctx), SessionID: sessionID}, nil
	}
	if req.AgentID == "builder" && r.builderStarted != nil {
		closeOnce(r.builderStarted)
	}

	content := req.AgentID + " completed step\nEND_STEP\n"
	if r.omitBoundaryFor[req.AgentID] {
		content = req.AgentID + " completed step\n"
	}
	switch req.AgentID {
	case "finalizer":
		content = "final StreamMA answer\nEND_STEP\n"
		if r.omitBoundaryFor[req.AgentID] {
			content = "final StreamMA answer\n"
		}
	}
	return StreamMASubagentStream{Events: streamMAEvents(ctx, content), SessionID: sessionID}, nil
}

func (r *fakeStreamMASubagentRunner) Requests() []StreamMASubagentRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]StreamMASubagentRequest(nil), r.requests...)
}

func (r *fakeStreamMASubagentRunner) streamBlockingPlanner(ctx context.Context) <-chan model.StreamEvent {
	ch := make(chan model.StreamEvent)
	go func() {
		defer close(ch)
		if !sendLoopTestStreamEvent(ctx, ch, model.StreamEvent{Delta: "planner early step\nEND_STEP\n"}) {
			return
		}
		closeOnce(r.plannerFirstStepSent)
		select {
		case <-ctx.Done():
			return
		case <-r.releasePlanner:
		}
		if !sendLoopTestStreamEvent(ctx, ch, model.StreamEvent{Delta: "planner late step\nEND_STEP\n"}) {
			return
		}
		_ = sendLoopTestStreamEvent(ctx, ch, model.StreamEvent{Done: true})
	}()
	return ch
}

func streamMAEvents(ctx context.Context, content string) <-chan model.StreamEvent {
	ch := make(chan model.StreamEvent)
	go func() {
		defer close(ch)
		usage := model.Usage{PromptTokens: 100, CompletionTokens: 10, PromptCacheHitTokens: 40, PromptCacheMissTokens: 60}
		if !sendLoopTestStreamEvent(ctx, ch, model.StreamEvent{Usage: &usage}) {
			return
		}
		if !sendLoopTestStreamEvent(ctx, ch, model.StreamEvent{Delta: content}) {
			return
		}
		_ = sendLoopTestStreamEvent(ctx, ch, model.StreamEvent{Done: true})
	}()
	return ch
}

func sendLoopTestStreamEvent(ctx context.Context, ch chan<- model.StreamEvent, ev model.StreamEvent) bool {
	select {
	case ch <- ev:
		return true
	case <-ctx.Done():
		return false
	}
}

func closeOnce(ch chan struct{}) {
	defer func() {
		_ = recover()
	}()
	close(ch)
}

type fakeTool struct {
	name   string
	output string
	err    error
	input  json.RawMessage
	schema json.RawMessage
	safe   bool
}

type mutationProbeTool struct {
	name         string
	path         string
	beforeExists bool
	output       string
	targetCalls  int
	runCalls     int
	removeOnRun  bool
	writeOnRun   string
}

func (t *mutationProbeTool) Name() string               { return t.name }
func (*mutationProbeTool) Description() string          { return "mutation probe" }
func (*mutationProbeTool) InputSchema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (t *mutationProbeTool) FileMutationTarget(json.RawMessage) (tool.FileMutationTarget, error) {
	t.targetCalls++
	return tool.FileMutationTarget{Path: t.path, BeforeExists: t.beforeExists}, nil
}
func (t *mutationProbeTool) Run(context.Context, json.RawMessage) (string, error) {
	t.runCalls++
	if t.removeOnRun {
		if err := os.Remove(t.path); err != nil {
			return "", err
		}
	}
	if t.writeOnRun != "" {
		if err := os.WriteFile(t.path, []byte(t.writeOnRun), 0o644); err != nil {
			return "", err
		}
	}
	return t.output, nil
}

type blockingTool struct {
	name    string
	output  string
	started chan struct{}
	release chan struct{}
	safe    bool
}

type registryReplacingTool struct {
	name        string
	output      string
	registry    *tool.Registry
	replacement tool.Tool
	safetyCalls int
	runCalls    int
}

func (t *registryReplacingTool) Name() string      { return t.name }
func (*registryReplacingTool) Description() string { return "registry replacing tool" }
func (*registryReplacingTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object"}`)
}
func (t *registryReplacingTool) IsConcurrencySafe(json.RawMessage) bool {
	t.safetyCalls++
	t.registry.Register(t.replacement)
	return false
}
func (t *registryReplacingTool) Run(context.Context, json.RawMessage) (string, error) {
	t.runCalls++
	return t.output, nil
}

type fakeStore struct {
	history   []message.Message
	loadCalls int
	appends   [][]message.Message
}

func (s *fakeStore) Append(ctx context.Context, sessionID string, msgs ...message.Message) error {
	copied := append([]message.Message(nil), msgs...)
	s.appends = append(s.appends, copied)
	return nil
}

func (s *fakeStore) LoadResolvedHistory(ctx context.Context, sessionID string) ([]message.Message, error) {
	s.loadCalls++
	return append([]message.Message(nil), s.history...), nil
}

func (t *fakeTool) Name() string {
	return t.name
}

func (t *fakeTool) Description() string {
	return "fake tool"
}

func (t *fakeTool) InputSchema() json.RawMessage {
	if t.schema != nil {
		return t.schema
	}
	return nil
}

func (t *fakeTool) Run(ctx context.Context, input json.RawMessage) (string, error) {
	if t.err != nil {
		return "", t.err
	}
	t.input = append(json.RawMessage(nil), input...)
	return t.output, nil
}

func (t *fakeTool) IsConcurrencySafe(input json.RawMessage) bool {
	return t.safe
}

func (t *blockingTool) Name() string {
	return t.name
}

func (t *blockingTool) Description() string {
	return "blocking fake tool"
}

func (t *blockingTool) InputSchema() json.RawMessage {
	return nil
}

func (t *blockingTool) Run(ctx context.Context, input json.RawMessage) (string, error) {
	if t.started != nil {
		close(t.started)
	}
	if t.release != nil {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-t.release:
		}
	}
	return t.output, nil
}

func (t *blockingTool) IsConcurrencySafe(input json.RawMessage) bool {
	return t.safe
}

func messagesContainContent(messages []message.Message, want string) bool {
	for _, msg := range messages {
		if strings.Contains(msg.Content, want) {
			return true
		}
	}
	return false
}

func TestRunTurnStreamsAndReturnsFinalMessage(t *testing.T) {
	ui := &fakeUI{}
	model := &fakeModel{
		rounds: []fakeRound{
			{
				events: []model.StreamEvent{
					{Delta: "he"},
					{Delta: "llo"},
					{Done: true},
				},
			},
		},
	}
	runner := NewRunner(model, ui, tool.NewRegistry(), nil, "")

	msg, err := runner.RunTurn(context.Background(), "hello")
	if err != nil {
		t.Fatalf("RunTurn() error = %v", err)
	}
	if msg.Role != message.RoleAssistant {
		t.Fatalf("msg.Role = %q, want %q", msg.Role, message.RoleAssistant)
	}
	if msg.Content != "hello" {
		t.Fatalf("msg.Content = %q, want %q", msg.Content, "hello")
	}
	if got := len(ui.deltas); got != 2 {
		t.Fatalf("len(ui.deltas) = %d, want 2", got)
	}
	if ui.deltas[0] != "he" || ui.deltas[1] != "llo" {
		t.Fatalf("ui.deltas = %#v, want [\"he\", \"llo\"]", ui.deltas)
	}
	if ui.doneCount != 1 {
		t.Fatalf("ui.doneCount = %d, want 1", ui.doneCount)
	}
}

func TestRunTurnRecordsTokenTracerUsage(t *testing.T) {
	usage := model.Usage{
		PromptTokens:     100,
		CompletionTokens: 5,
		PromptTokensDetails: model.TokenDetails{
			CachedTokens: 20,
		},
	}
	streamer := &fakeModel{rounds: []fakeRound{{
		events: []model.StreamEvent{
			{Usage: &usage},
			{Delta: "hello"},
			{Done: true},
		},
	}}}
	runner := NewRunner(streamer, &fakeUI{}, tool.NewRegistry(), &fakeStore{}, "trace-session")
	tracer := tokentracer.New("test")
	runner.SetTokenTracer(tracer)

	if _, err := runner.RunTurn(context.Background(), "hello"); err != nil {
		t.Fatalf("RunTurn() error = %v", err)
	}

	snapshot := tracer.Snapshot()
	if snapshot.Pipeline.Calls != 1 {
		t.Fatalf("pipeline calls = %d, want 1", snapshot.Pipeline.Calls)
	}
	total := snapshot.Pipeline.Total
	if total.Input != 80 || total.CacheRead != 20 || total.Output != 5 || total.TotalContext != 100 {
		t.Fatalf("pipeline total = %#v, want input=80 cache=20 output=5 total_context=100", total)
	}
	if len(snapshot.Pipeline.Stages) != 1 || snapshot.Pipeline.Stages[0].Status != "completed" {
		t.Fatalf("stages = %#v, want one completed stage", snapshot.Pipeline.Stages)
	}
	if !tokenTracerEventsContain(snapshot.Events, "api_call") || !tokenTracerEventsContain(snapshot.Events, "assistant_delta") {
		t.Fatalf("events = %#v, want api_call and assistant_delta", snapshot.Events)
	}
}

func TestRunTurnInjectsMentionedSkillContextForOneTurn(t *testing.T) {
	root := t.TempDir()
	writeLoopTestSkill(t, root, "design", `---
description: Design discipline
---
Design body line.`)
	ui := &fakeUI{}
	model := &fakeModel{
		rounds: []fakeRound{
			{events: []model.StreamEvent{{Delta: "first"}, {Done: true}}},
			{events: []model.StreamEvent{{Delta: "second"}, {Done: true}}},
		},
	}
	runner := NewRunner(model, ui, tool.NewRegistry(), nil, "")
	runner.SetSkillRegistry(skill.NewRegistry([]string{root}))

	if _, err := runner.RunTurn(context.Background(), "use $design"); err != nil {
		t.Fatalf("RunTurn(skill) error = %v", err)
	}
	for _, event := range ui.systemEvents() {
		if event.Title == "skills" && strings.HasPrefix(event.Body, "loaded ") {
			t.Fatalf("unexpected skill loaded notification: %#v", event)
		}
	}
	if _, err := runner.RunTurn(context.Background(), "plain follow-up"); err != nil {
		t.Fatalf("RunTurn(follow-up) error = %v", err)
	}
	if got := promptTextForTest(model.calls[0]); !strings.Contains(got, "Selected skill instructions") || !strings.Contains(got, "Design body line.") {
		t.Fatalf("first prompt = %q, want skill context", got)
	}
	if got := promptTextForTest(model.calls[1]); strings.Contains(got, "Design body line.") {
		t.Fatalf("second prompt = %q, should not carry skill context", got)
	}
}

func TestRunTurnStreamMAWorkersReceiveSkillContext(t *testing.T) {
	root := t.TempDir()
	writeLoopTestSkill(t, root, "design", "# Design\nStreamMA skill body.")
	output := &fakeUI{}
	subagents := &fakeStreamMASubagentRunner{}
	store := &fakeStore{}
	runner := NewRunner(&fakeModel{}, output, tool.NewRegistry(), store, "streamma-skill-session")
	runner.SetSkillRegistry(skill.NewRegistry([]string{root}))
	runner.SetStreamMASubagentRunner(subagents)

	if _, err := runner.RunTurn(context.Background(), "/streamma explain $design"); err != nil {
		t.Fatalf("RunTurn(streamma skill) error = %v", err)
	}
	requests := subagents.Requests()
	if len(requests) == 0 {
		t.Fatalf("requests = nil, want StreamMA subagent requests")
	}
	if !strings.Contains(requests[0].SystemPrompt, "StreamMA skill body.") {
		t.Fatalf("first StreamMA system prompt = %q, want skill context", requests[0].SystemPrompt)
	}
}

func TestRunTurnStreamMACommandUsesRuntime(t *testing.T) {
	output := &fakeUI{}
	streamer := &fakeModel{}
	subagents := &fakeStreamMASubagentRunner{}
	registry := tool.NewRegistry()
	registry.Register(&fakeTool{name: "read"})
	store := &fakeStore{}
	runner := NewRunner(streamer, output, registry, store, "streamma-session")
	runner.SetStreamMASubagentRunner(subagents)

	msg, err := runner.RunTurn(context.Background(), "/streamma 制作一个临时游戏")
	if err != nil {
		t.Fatalf("RunTurn() error = %v", err)
	}
	if msg.Role != message.RoleAssistant || msg.Content != "final StreamMA answer" {
		t.Fatalf("msg = %#v, want final StreamMA answer", msg)
	}
	if got := strings.Join(output.deltas, ""); got != "final StreamMA answer" {
		t.Fatalf("deltas = %q, want final answer", got)
	}
	if output.doneCount != 1 {
		t.Fatalf("doneCount = %d, want 1", output.doneCount)
	}
	systemEvents := output.systemEvents()
	if len(systemEvents) == 0 || systemEvents[0].Title != "streamma" {
		t.Fatalf("system events = %#v, want streamma events", systemEvents)
	}
	if !strings.Contains(systemEvents[0].Body, "subagent-backed") {
		t.Fatalf("first system event = %#v, want subagent-backed graph notice", systemEvents[0])
	}
	if len(streamer.calls) != 0 {
		t.Fatalf("model calls = %d, want direct model unused when StreamMA subagents are configured", len(streamer.calls))
	}
	requests := subagents.Requests()
	if len(requests) < 4 {
		t.Fatalf("subagent requests = %d, want multiple StreamMA workers", len(requests))
	}
	seen := map[string]bool{}
	for _, req := range requests {
		seen[req.AgentID] = true
		if !strings.Contains(req.Prompt, "END_STEP") {
			t.Fatalf("subagent prompt for %s missing END_STEP contract: %q", req.AgentID, req.Prompt)
		}
		if req.RunID == "" || req.SessionKey == "" || req.InvocationIndex == 0 {
			t.Fatalf("subagent request missing streamma metadata: %#v", req)
		}
	}
	for _, want := range []string{"planner", "scout", "builder", "verifier", "finalizer"} {
		if !seen[want] {
			t.Fatalf("subagent ids = %#v, missing %s", seen, want)
		}
	}
	if len(store.appends) != 1 || len(store.appends[0]) != 2 {
		t.Fatalf("store appends = %#v, want user+assistant", store.appends)
	}
	if store.appends[0][0].Content != "/streamma 制作一个临时游戏" ||
		store.appends[0][1].Content != "final StreamMA answer" {
		t.Fatalf("stored messages = %#v", store.appends[0])
	}
}

func TestRunTurnStreamMAReusesLogicalAgentSessionAndIncrementalPrompt(t *testing.T) {
	output := &fakeUI{}
	subagents := &fakeStreamMASubagentRunner{}
	runner := NewRunner(&fakeModel{}, output, tool.NewRegistry(), &fakeStore{}, "streamma-session")
	runner.SetStreamMASubagentRunner(subagents)

	if _, err := runner.RunTurn(context.Background(), "/streamma 制作一个临时游戏"); err != nil {
		t.Fatalf("RunTurn() error = %v", err)
	}

	var builder []StreamMASubagentRequest
	for _, req := range subagents.Requests() {
		if req.AgentID == "builder" {
			builder = append(builder, req)
		}
	}
	if len(builder) < 2 {
		t.Fatalf("builder requests = %d, want repeated logical agent invocations: %#v", len(builder), subagents.Requests())
	}
	if builder[0].SessionID != "" {
		t.Fatalf("first builder request SessionID = %q, want empty before session assignment", builder[0].SessionID)
	}
	for i := 1; i < len(builder); i++ {
		if builder[i].SessionID != "builder-session" {
			t.Fatalf("builder request %d SessionID = %q, want reused builder-session", i, builder[i].SessionID)
		}
		if builder[i].SessionKey != builder[0].SessionKey {
			t.Fatalf("builder session key changed: first=%q next=%q", builder[0].SessionKey, builder[i].SessionKey)
		}
		if strings.Contains(builder[i].Prompt, "Original problem:") {
			t.Fatalf("incremental builder prompt repeated base problem: %q", builder[i].Prompt)
		}
		if !strings.Contains(builder[i].Prompt, "StreamMA incremental invocation") {
			t.Fatalf("builder prompt %d missing incremental marker: %q", i, builder[i].Prompt)
		}
	}
}

func TestRunTurnStreamMATraceEmitsRuntimeEvents(t *testing.T) {
	output := &fakeUI{}
	subagents := &fakeStreamMASubagentRunner{}
	runner := NewRunner(&fakeModel{}, output, tool.NewRegistry(), &fakeStore{}, "streamma-session")
	runner.SetStreamMASubagentRunner(subagents)

	if _, err := runner.RunTurn(context.Background(), "/streamma-trace 制作一个临时游戏"); err != nil {
		t.Fatalf("RunTurn() error = %v", err)
	}

	var sawStarted, sawStep, sawUsage bool
	for _, event := range output.systemEvents() {
		if event.Title != "streamma-trace" {
			continue
		}
		if strings.Contains(event.Body, "subagent.started agent=planner") &&
			strings.Contains(event.Body, "invocation=1") &&
			strings.Contains(event.Body, "session=planner-session") {
			sawStarted = true
		}
		if strings.Contains(event.Body, "type=agent.step.committed") && strings.Contains(event.Body, "producer=planner") {
			sawStep = true
		}
		if strings.Contains(event.Body, "subagent.finished") &&
			strings.Contains(event.Body, "cache_hit=40") &&
			strings.Contains(event.Body, "cache_miss=60") {
			sawUsage = true
		}
	}
	if !sawStarted || !sawStep || !sawUsage {
		t.Fatalf("trace events = %#v, want planner start, committed step, and usage", output.system)
	}
}

func TestRunTurnStreamMARecordsTokenTracerEvents(t *testing.T) {
	runner := NewRunner(&fakeModel{}, &fakeUI{}, tool.NewRegistry(), &fakeStore{}, "streamma-session")
	runner.SetStreamMASubagentRunner(&fakeStreamMASubagentRunner{})
	tracer := tokentracer.New("streamma-test")
	runner.SetTokenTracer(tracer)

	if _, err := runner.RunTurn(context.Background(), "/streamma 制作一个临时游戏"); err != nil {
		t.Fatalf("RunTurn() error = %v", err)
	}

	snapshot := tracer.Snapshot()
	if snapshot.Pipeline.Calls == 0 || snapshot.Pipeline.Total.TotalContext == 0 {
		t.Fatalf("snapshot total = %#v, want StreamMA usage", snapshot.Pipeline.Total)
	}
	if !tokenTracerEventsContain(snapshot.Events, "streamma.agent.step.committed") {
		t.Fatalf("events = %#v, want streamma step events", snapshot.Events)
	}
	if !tokenTracerEventsContain(snapshot.Events, "streamma.subagent_start") {
		t.Fatalf("events = %#v, want streamma subagent start events", snapshot.Events)
	}
}

func tokenTracerEventsContain(events []tokentracer.Event, eventType string) bool {
	for _, event := range events {
		if event.Type == eventType {
			return true
		}
	}
	return false
}

func TestStreamMAConversationContextIsByteCapped(t *testing.T) {
	history := []message.Message{
		{Role: message.RoleUser, Content: "old " + strings.Repeat("甲", streamMAConversationContextMaxBytes)},
		{Role: message.RoleAssistant, Content: "recent answer"},
	}

	context := streamMAConversationContext(history)
	if got := len([]byte(context)); got > streamMAConversationContextMaxBytes {
		t.Fatalf("context bytes = %d, want <= %d", got, streamMAConversationContextMaxBytes)
	}
	if !strings.Contains(context, "recent answer") {
		t.Fatalf("context = %q, want recent history retained", context)
	}
	if !strings.Contains(context, "earlier conversation truncated") {
		t.Fatalf("context = %q, want truncation marker", context)
	}
}

func TestBuildStreamMAIncrementalPromptTruncatesLargeInboundStep(t *testing.T) {
	prompt := buildStreamMAIncrementalPrompt(streamma.AgentInvocation{
		RunID:           "run-1",
		InvocationIndex: 2,
		AgentID:         "builder",
		Role:            "implementation_builder",
		InboundFrom:     "planner",
		InboundStep: &streamma.StepPacket{
			Content: streamma.StepContent{Text: strings.Repeat("x", streamMAInboundStepMaxBytes+2048)},
		},
		Boundary:        streamma.DefaultBoundary,
		RequireBoundary: true,
	}, false)

	if !strings.Contains(prompt, "truncated to fit StreamMA request budget") {
		t.Fatalf("prompt missing truncation marker")
	}
	if got := len([]byte(prompt)); got > streamMAInboundStepMaxBytes+2048 {
		t.Fatalf("prompt bytes = %d, want bounded prompt", got)
	}
}

func TestRunnerCompactToolPromptOmitsSystemSchemasButKeepsNativeTools(t *testing.T) {
	streamer := &fakeModel{rounds: []fakeRound{{events: []model.StreamEvent{{Delta: "ok"}, {Done: true}}}}}
	registry := tool.NewRegistry()
	registry.Register(&fakeTool{
		name:   "Huge",
		schema: json.RawMessage(`{"type":"object","properties":{"payload":{"type":"string","description":"` + strings.Repeat("schema detail ", 80) + `"}}}`),
	})
	runner := NewRunner(streamer, &fakeUI{}, registry, &fakeStore{}, "session-1")
	runner.SetCompactToolPrompt(true)

	if _, err := runner.RunTurn(context.Background(), "hello"); err != nil {
		t.Fatalf("RunTurn() error = %v", err)
	}
	if len(streamer.calls) != 1 || len(streamer.calls[0]) == 0 {
		t.Fatalf("model calls = %#v", streamer.calls)
	}
	systemPrompt := streamer.calls[0][0].Content
	if strings.Contains(systemPrompt, "input_schema=") || strings.Contains(systemPrompt, "schema detail") {
		t.Fatalf("compact system prompt leaked schema: %q", systemPrompt)
	}
	if !strings.Contains(systemPrompt, "Huge: fake tool") {
		t.Fatalf("compact system prompt missing brief tool description: %q", systemPrompt)
	}
	if len(streamer.tools) != 1 || len(streamer.tools[0]) != 1 || !strings.Contains(string(streamer.tools[0][0].InputSchema), "schema detail") {
		t.Fatalf("native tools = %#v, want schema preserved out-of-band", streamer.tools)
	}
}

func TestRunTurnStreamMARejectsRecoveredSubagentStep(t *testing.T) {
	output := &fakeUI{}
	subagents := &fakeStreamMASubagentRunner{
		omitBoundaryFor: map[string]bool{"planner": true, "scout": true},
	}
	runner := NewRunner(&fakeModel{}, output, tool.NewRegistry(), &fakeStore{}, "streamma-session")
	runner.SetStreamMASubagentRunner(subagents)

	_, err := runner.RunTurn(context.Background(), "/streamma 制作一个临时游戏")
	if err == nil || !strings.Contains(err.Error(), "stream ended before exact boundary") {
		t.Fatalf("RunTurn() error = %v, want strict boundary failure", err)
	}
	for _, req := range subagents.Requests() {
		if req.AgentID == "builder" {
			t.Fatalf("builder should not start from recovered upstream step: %#v", subagents.Requests())
		}
	}
}

func TestRunTurnStreamMAFansOutSubagentStepBeforeDone(t *testing.T) {
	output := &fakeUI{}
	streamer := &fakeModel{}
	subagents := &fakeStreamMASubagentRunner{
		plannerFirstStepSent: make(chan struct{}),
		releasePlanner:       make(chan struct{}),
		builderStarted:       make(chan struct{}),
	}
	runner := NewRunner(streamer, output, tool.NewRegistry(), &fakeStore{}, "streamma-session")
	runner.SetStreamMASubagentRunner(subagents)

	done := make(chan error, 1)
	go func() {
		_, err := runner.RunTurn(context.Background(), "/streamma 制作一个临时游戏")
		done <- err
	}()

	select {
	case <-subagents.plannerFirstStepSent:
	case <-time.After(time.Second):
		t.Fatal("planner did not emit its first StreamMA step")
	}
	select {
	case <-subagents.builderStarted:
	case <-time.After(time.Second):
		t.Fatal("builder did not start from planner END_STEP before planner Done")
	}

	close(subagents.releasePlanner)

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("RunTurn() error = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("RunTurn() did not finish after planner release")
	}
}

func TestRunTurnStreamMARequiresSubagentBackend(t *testing.T) {
	output := &fakeUI{}
	runner := NewRunner(&fakeModel{}, output, tool.NewRegistry(), nil, "")

	_, err := runner.RunTurn(context.Background(), "/streamma explain the design")
	if err == nil || !strings.Contains(err.Error(), "requires subagent backend") {
		t.Fatalf("RunTurn() error = %v, want subagent backend error", err)
	}
}

func TestRunTurnStreamMADisabledRejectsCommandBeforeRuntime(t *testing.T) {
	output := &fakeUI{}
	subagents := &fakeStreamMASubagentRunner{}
	runner := NewRunner(&fakeModel{}, output, tool.NewRegistry(), nil, "")
	runner.SetStreamMASubagentRunner(subagents)
	runner.SetStreamMAEnabled(false)

	_, err := runner.RunTurn(context.Background(), "/streamma-trace explain the design")
	if err == nil || !strings.Contains(err.Error(), "streamma is disabled") {
		t.Fatalf("RunTurn() error = %v, want disabled error", err)
	}
	if got := len(subagents.Requests()); got != 0 {
		t.Fatalf("subagent requests = %d, want 0 when StreamMA is disabled", got)
	}
}

func TestRunTurnCreatesLazySessionOnCommit(t *testing.T) {
	ui := &fakeUI{}
	model := &fakeModel{
		rounds: []fakeRound{{
			events: []model.StreamEvent{
				{Delta: "hello back"},
				{Done: true},
			},
		}},
	}
	store, err := session.NewJSONLStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewJSONLStore() error = %v", err)
	}
	runner := NewRunner(model, ui, tool.NewRegistry(), store, "lazy-session")

	exists, err := store.Exists(context.Background(), "lazy-session")
	if err != nil {
		t.Fatalf("Exists() error = %v", err)
	}
	if exists {
		t.Fatalf("lazy session exists before RunTurn")
	}

	if _, err := runner.RunTurn(context.Background(), "hello"); err != nil {
		t.Fatalf("RunTurn() error = %v", err)
	}
	exists, err = store.Exists(context.Background(), "lazy-session")
	if err != nil {
		t.Fatalf("Exists() after RunTurn error = %v", err)
	}
	if !exists {
		t.Fatalf("lazy session should exist after successful RunTurn")
	}
	history, err := store.LoadResolvedHistory(context.Background(), "lazy-session")
	if err != nil {
		t.Fatalf("LoadResolvedHistory() error = %v", err)
	}
	if len(history) != 2 || history[0].Content != "hello" || history[1].Content != "hello back" {
		t.Fatalf("history = %#v, want user prompt and assistant reply", history)
	}
}

func TestRunTurnUsesPromptBuilderWithProjectInstructions(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, projectInstructionFile), []byte("prefer concise answers"), 0o644); err != nil {
		t.Fatal(err)
	}
	ui := &fakeUI{}
	model := &fakeModel{
		rounds: []fakeRound{
			{
				events: []model.StreamEvent{
					{Delta: "ok"},
					{Done: true},
				},
			},
		},
	}
	runner := NewRunnerWithInstructionRoot(model, ui, tool.NewRegistry(), nil, "", root)

	if _, err := runner.RunTurn(context.Background(), "hello"); err != nil {
		t.Fatalf("RunTurn() error = %v", err)
	}
	if len(model.calls) != 1 || len(model.calls[0]) == 0 {
		t.Fatalf("model.calls = %#v", model.calls)
	}
	system := model.calls[0][0]
	if system.Role != message.RoleSystem {
		t.Fatalf("first role = %q, want system", system.Role)
	}
	for _, want := range []string{"Project instructions from AGENTS.md", "prefer concise answers", "Answer with plain text."} {
		if !strings.Contains(system.Content, want) {
			t.Fatalf("system prompt = %q, want %q", system.Content, want)
		}
	}
}

func TestRunTurnReturnsSyncModelError(t *testing.T) {
	ui := &fakeUI{}
	model := &fakeModel{
		rounds: []fakeRound{
			{err: errors.New("boom")},
		},
	}
	runner := NewRunner(model, ui, tool.NewRegistry(), nil, "")

	_, err := runner.RunTurn(context.Background(), "hello")
	if err == nil || err.Error() != "boom" {
		t.Fatalf("RunTurn() error = %v, want boom", err)
	}
	if len(ui.deltas) != 0 {
		t.Fatalf("len(ui.deltas) = %d, want 0", len(ui.deltas))
	}
	if ui.doneCount != 0 {
		t.Fatalf("ui.doneCount = %d, want 0", ui.doneCount)
	}
}

func TestRunTurnFlushesOnStreamErrorAfterOutput(t *testing.T) {
	ui := &fakeUI{}
	model := &fakeModel{
		rounds: []fakeRound{
			{
				events: []model.StreamEvent{
					{Delta: "he"},
					{Err: errors.New("boom")},
				},
			},
		},
	}
	runner := NewRunner(model, ui, tool.NewRegistry(), nil, "")

	_, err := runner.RunTurn(context.Background(), "hello")
	if err == nil || err.Error() != "boom" {
		t.Fatalf("RunTurn() error = %v, want boom", err)
	}
	if got := len(ui.deltas); got != 1 || ui.deltas[0] != "he" {
		t.Fatalf("ui.deltas = %#v, want [\"he\"]", ui.deltas)
	}
	if ui.doneCount != 1 {
		t.Fatalf("ui.doneCount = %d, want 1", ui.doneCount)
	}
}

func TestRunTurnAllowsEmptyAssistantContent(t *testing.T) {
	ui := &fakeUI{}
	model := &fakeModel{
		rounds: []fakeRound{
			{
				events: []model.StreamEvent{
					{Done: true},
				},
			},
		},
	}
	runner := NewRunner(model, ui, tool.NewRegistry(), nil, "")

	msg, err := runner.RunTurn(context.Background(), "hello")
	if err != nil {
		t.Fatalf("RunTurn() error = %v", err)
	}
	if msg.Role != message.RoleAssistant {
		t.Fatalf("msg.Role = %q, want %q", msg.Role, message.RoleAssistant)
	}
	if msg.Content != "" {
		t.Fatalf("msg.Content = %q, want empty", msg.Content)
	}
	if ui.doneCount != 1 {
		t.Fatalf("ui.doneCount = %d, want 1", ui.doneCount)
	}
}

func TestRunnerForwardsThinkingEventsToUI(t *testing.T) {
	streamer := &fakeModel{rounds: []fakeRound{
		{events: []model.StreamEvent{
			{Thinking: "plan"},
			{Delta: "answer"},
			{Done: true},
		}},
	}}
	output := &fakeUI{}
	runner := NewRunner(streamer, output, tool.NewRegistry(), nil, "")

	if _, err := runner.RunTurn(context.Background(), "hello"); err != nil {
		t.Fatalf("RunTurn() error = %v", err)
	}
	if got := strings.Join(output.thinking, ""); got != "plan" {
		t.Fatalf("thinking = %q, want plan", got)
	}
	if got := strings.Join(output.deltas, ""); got != "answer" {
		t.Fatalf("deltas = %q, want answer", got)
	}
}

func TestContextStatsUsesOnlyRealUsageAndKeepsLastKnownDuringNextTurn(t *testing.T) {
	ui := &fakeUI{}
	first := make(chan model.StreamEvent, 1)
	first <- model.StreamEvent{
		Usage: &model.Usage{PromptTokens: 100, CompletionTokens: 5, PromptCacheHitTokens: 12},
		Done:  true,
	}
	close(first)
	second := make(chan model.StreamEvent, 1)
	streamer := &blockingModel{
		streams: []chan model.StreamEvent{first, second},
		started: make(chan int, 2),
	}
	runner := NewRunner(streamer, ui, tool.NewRegistry(), nil, "")

	if _, err := runner.RunTurn(context.Background(), "first"); err != nil {
		t.Fatalf("first RunTurn() error = %v", err)
	}
	if got := <-streamer.started; got != 0 {
		t.Fatalf("first turn started index = %d, want 0", got)
	}
	firstStats := runner.ContextStats(1024, "")
	if firstStats.UsedTokens != 105 || firstStats.CacheTokens != 12 {
		t.Fatalf("first ContextStats() = %#v, want real usage 105/cache 12", firstStats)
	}

	errCh := make(chan error, 1)
	go func() {
		_, err := runner.RunTurn(context.Background(), "second")
		errCh <- err
	}()
	if got := <-streamer.started; got != 1 {
		t.Fatalf("second turn started index = %d, want 1", got)
	}

	stats := runner.ContextStats(1024, "draft")
	if stats.UsedTokens != 105 || stats.CacheTokens != 12 {
		t.Fatalf("ContextStats() = %#v, want last real usage without draft estimate", stats)
	}

	second <- model.StreamEvent{Done: true}
	close(second)
	if err := <-errCh; err != nil {
		t.Fatalf("second RunTurn() error = %v", err)
	}
}

func TestContextStatsAccumulatesSessionUsageAcrossModelRequests(t *testing.T) {
	ui := &fakeUI{}
	model := &fakeModel{
		rounds: []fakeRound{
			{
				events: []model.StreamEvent{
					{Usage: &model.Usage{InputTokens: 100, CacheReadInputTokens: 40}},
					{Usage: &model.Usage{OutputTokens: 5}},
					{Done: true},
				},
			},
			{
				events: []model.StreamEvent{
					{Usage: &model.Usage{PromptTokens: 150, CompletionTokens: 7, TotalTokens: 157, PromptCacheHitTokens: 30}},
					{Done: true},
				},
			},
		},
	}
	runner := NewRunner(model, ui, tool.NewRegistry(), nil, "")

	if _, err := runner.RunTurn(context.Background(), "first"); err != nil {
		t.Fatalf("first RunTurn() error = %v", err)
	}
	if _, err := runner.RunTurn(context.Background(), "second"); err != nil {
		t.Fatalf("second RunTurn() error = %v", err)
	}

	stats := runner.ContextStats(1024, "")
	if stats.UsedTokens != 157 || stats.CacheTokens != 30 {
		t.Fatalf("ContextStats() = %#v, want latest context usage 157/cache 30", stats)
	}
}

func TestContextStatsDoesNotDoubleCountCumulativeStreamUsageUpdates(t *testing.T) {
	ui := &fakeUI{}
	model := &fakeModel{
		rounds: []fakeRound{
			{
				events: []model.StreamEvent{
					{Usage: &model.Usage{InputTokens: 100, CacheReadInputTokens: 10}},
					{Usage: &model.Usage{OutputTokens: 2}},
					{Usage: &model.Usage{OutputTokens: 5}},
					{Done: true},
				},
			},
		},
	}
	runner := NewRunner(model, ui, tool.NewRegistry(), nil, "")

	if _, err := runner.RunTurn(context.Background(), "hello"); err != nil {
		t.Fatalf("RunTurn() error = %v", err)
	}

	stats := runner.ContextStats(1024, "")
	if stats.UsedTokens != 115 || stats.CacheTokens != 10 {
		t.Fatalf("ContextStats() = %#v, want one request usage 115/cache 10", stats)
	}
}

func TestContextStatsDoesNotEstimateWhenUsageIsUnknown(t *testing.T) {
	runner := NewRunner(&fakeModel{}, &fakeUI{}, tool.NewRegistry(), nil, "")

	stats := runner.ContextStats(1024, "draft prompt")
	if stats.UsedTokens != 0 || stats.CacheTokens != 0 || stats.LimitTokens != 1024 {
		t.Fatalf("ContextStats() = %#v, want zero usage until provider reports usage", stats)
	}
}

func TestContextStatsUsesActualUsageWhenKnown(t *testing.T) {
	tests := []struct {
		name      string
		usage     model.Usage
		wantUsed  int
		wantCache int
	}{
		{
			name: "anthropic",
			usage: model.Usage{
				InputTokens:              100,
				CacheCreationInputTokens: 4,
				CacheReadInputTokens:     40,
				OutputTokens:             6,
			},
			wantUsed:  150,
			wantCache: 40,
		},
		{
			name: "openai-compatible",
			usage: model.Usage{
				PromptTokens:          100,
				CompletionTokens:      2,
				TotalTokens:           102,
				PromptCacheHitTokens:  40,
				PromptCacheMissTokens: 60,
			},
			wantUsed:  102,
			wantCache: 40,
		},
		{
			name: "openai-nested-cache-details",
			usage: model.Usage{
				PromptTokens:     100,
				CompletionTokens: 2,
				TotalTokens:      102,
				PromptTokensDetails: model.TokenDetails{
					CachedTokens: 32,
				},
			},
			wantUsed:  102,
			wantCache: 32,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runner := NewRunner(&fakeModel{}, &fakeUI{}, tool.NewRegistry(), nil, "")
			runner.usage = tt.usage
			runner.usageKnown = true

			stats := runner.ContextStats(1024, "draft should not be estimated")
			if stats.UsedTokens != tt.wantUsed {
				t.Fatalf("UsedTokens = %d, want %d", stats.UsedTokens, tt.wantUsed)
			}
			if stats.CacheTokens != tt.wantCache {
				t.Fatalf("CacheTokens = %d, want %d", stats.CacheTokens, tt.wantCache)
			}
		})
	}
}

func TestLoadSessionRebuildsContextUsageFromActiveHistory(t *testing.T) {
	history := []message.Message{
		{Role: message.RoleUser, Content: "restore this conversation"},
		{Role: message.RoleAssistant, Content: strings.Repeat("restored answer ", 20)},
	}
	store := &fakeStore{history: history}
	runner := NewRunner(&fakeModel{}, &fakeUI{}, tool.NewRegistry(), store, "old-session")
	runner.usage = model.Usage{TotalTokens: 9999, PromptCacheHitTokens: 100}
	runner.usageKnown = true

	if _, err := runner.LoadSession(context.Background(), "restored-session"); err != nil {
		t.Fatalf("LoadSession() error = %v", err)
	}

	want := estimateMessageTokens(append([]message.Message{
		buildSystemMessage(runner.buildSystemPrompt()),
	}, history...))
	stats := runner.ContextStats(100000, "")
	if stats.UsedTokens != want || stats.CacheTokens != 0 {
		t.Fatalf("ContextStats() = %#v, want rebuilt usage %d/cache 0", stats, want)
	}
	if stats.SessionUsedTokens != 0 {
		t.Fatalf("SessionUsedTokens = %d, want unknown cumulative usage reset to 0", stats.SessionUsedTokens)
	}
}

func TestResetHistoryClearsContextUsage(t *testing.T) {
	runner := NewRunner(&fakeModel{}, &fakeUI{}, tool.NewRegistry(), nil, "")
	runner.usage = model.Usage{PromptTokens: 100, CompletionTokens: 20, PromptCacheHitTokens: 10}
	runner.usageKnown = true
	runner.sessionUsage = model.Usage{PromptTokens: 200, CompletionTokens: 40, PromptCacheHitTokens: 20}
	runner.sessionUsageKnown = true

	runner.ResetHistory()

	stats := runner.ContextStats(1024, "")
	if stats.UsedTokens != 0 || stats.CacheTokens != 0 {
		t.Fatalf("ContextStats() = %#v, want reset usage", stats)
	}
}

func TestRunTurnExecutesToolAndReturnsFinalAnswer(t *testing.T) {
	ui := &fakeUI{}
	model := &fakeModel{
		rounds: []fakeRound{
			{
				events: []model.StreamEvent{
					{Delta: `{"type":"tool_use","id":"call_1","name":"Read","input":{"file_path":"go.mod"}}`},
					{Done: true},
				},
			},
			{
				events: []model.StreamEvent{
					{Delta: "go.mod loaded"},
					{Done: true},
				},
			},
		},
	}
	registry := tool.NewRegistry()
	registry.Register(&fakeTool{name: "Read", output: "module paw"})
	runner := NewRunner(model, ui, registry, nil, "")

	msg, err := runner.RunTurn(context.Background(), "read go.mod")
	if err != nil {
		t.Fatalf("RunTurn() error = %v", err)
	}
	if msg.Content != "go.mod loaded" {
		t.Fatalf("msg.Content = %q, want %q", msg.Content, "go.mod loaded")
	}
	if got := len(model.calls); got != 2 {
		t.Fatalf("len(model.calls) = %d, want 2", got)
	}
	if got := len(ui.deltas); got != 1 || ui.deltas[0] != "go.mod loaded" {
		t.Fatalf("ui.deltas = %#v, want [\"go.mod loaded\"]", ui.deltas)
	}
	if ui.doneCount != 1 {
		t.Fatalf("ui.doneCount = %d, want 1", ui.doneCount)
	}
	if got := len(ui.toolCalls); got != 1 {
		t.Fatalf("len(ui.toolCalls) = %d, want 1", got)
	}
	if ui.toolCalls[0].Name != "Read" {
		t.Fatalf("ui.toolCalls[0].Name = %q, want %q", ui.toolCalls[0].Name, "Read")
	}
	if got := len(ui.toolResults); got != 1 {
		t.Fatalf("len(ui.toolResults) = %d, want 1", got)
	}
	if ui.toolResults[0].Name != "Read" || ui.toolResults[0].IsError {
		t.Fatalf("ui.toolResults[0] = %#v, want successful Read result", ui.toolResults[0])
	}

	foundToolResult := false
	for _, msg := range model.calls[1] {
		if strings.Contains(msg.Content, "TOOL_RESULT:") && strings.Contains(msg.Content, "module paw") {
			foundToolResult = true
		}
	}
	if !foundToolResult {
		t.Fatalf("second round messages do not include tool result: %#v", model.calls[1])
	}
}

func TestRunTurnJournalRetainsToolHistoryAfterModelFailure(t *testing.T) {
	store, err := session.NewJSONLStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	failingModel := &fakeModel{rounds: []fakeRound{
		{events: []model.StreamEvent{
			{Delta: `{"type":"tool_use","id":"call_1","name":"Read","input":{"file_path":"go.mod"}}`},
			{Done: true},
		}},
		{err: errors.New("模型接口返回异常状态 502")},
	}}
	registry := tool.NewRegistry()
	registry.Register(&fakeTool{name: "Read", output: "module paw"})
	runner := NewRunner(failingModel, &fakeUI{}, registry, store, "session-1")

	if _, err := runner.RunTurn(context.Background(), "inspect"); err == nil {
		t.Fatal("RunTurn() error = nil, want model failure")
	}
	snapshot, err := store.LoadSnapshot(context.Background(), "session-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Messages) != 3 || snapshot.Recovery == nil {
		t.Fatalf("snapshot=%#v", snapshot)
	}
	if snapshot.Messages[0].Content != "inspect" || snapshot.Messages[1].ToolUse == nil || snapshot.Messages[2].ToolResult == nil {
		t.Fatalf("persisted messages=%#v", snapshot.Messages)
	}

	recoveryModel := &fakeModel{rounds: []fakeRound{{events: []model.StreamEvent{
		{Delta: "continued"},
		{Done: true},
	}}}}
	runner.model = recoveryModel
	if _, err := runner.RunTurn(context.Background(), "continue"); err != nil {
		t.Fatal(err)
	}
	if len(recoveryModel.calls) != 1 || !messagesContainContent(recoveryModel.calls[0], "module paw") || !messagesContainContent(recoveryModel.calls[0], "Paw recovery") {
		t.Fatalf("recovery model call=%#v", recoveryModel.calls)
	}
	finalSnapshot, err := store.LoadSnapshot(context.Background(), "session-1")
	if err != nil {
		t.Fatal(err)
	}
	if finalSnapshot.Recovery != nil {
		t.Fatalf("successful recovery left pending state=%#v", finalSnapshot.Recovery)
	}
}

func TestRunTurnJournalDoesNotPersistPartialAssistantOutput(t *testing.T) {
	store, err := session.NewJSONLStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	failingModel := &fakeModel{rounds: []fakeRound{{events: []model.StreamEvent{
		{Delta: "partial answer"},
		{Err: errors.New("502")},
	}}}}
	runner := NewRunner(failingModel, &fakeUI{}, tool.NewRegistry(), store, "session-1")
	if _, err := runner.RunTurn(context.Background(), "hello"); err == nil {
		t.Fatal("RunTurn() error = nil, want stream failure")
	}
	history, err := store.LoadResolvedHistory(context.Background(), "session-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 1 || history[0].Content != "hello" {
		t.Fatalf("history=%#v, want only user message", history)
	}
}

func TestRunTurnExecutesMultipleToolUsesAndCarriesAllResults(t *testing.T) {
	ui := &fakeUI{}
	model := &fakeModel{
		rounds: []fakeRound{
			{
				events: []model.StreamEvent{
					{Delta: `{"type":"tool_use","id":"call_read","name":"Read","input":{"file_path":"go.mod"}}`},
					{Delta: `{"type":"tool_use","id":"call_grep","name":"Grep","input":{"pattern":"module"}}`},
					{Done: true},
				},
			},
			{
				events: []model.StreamEvent{
					{Delta: "tools complete"},
					{Done: true},
				},
			},
		},
	}
	registry := tool.NewRegistry()
	registry.Register(&fakeTool{name: "Read", output: "module paw", safe: true})
	registry.Register(&fakeTool{name: "Grep", output: "go.mod:1:module paw", safe: true})
	runner := NewRunner(model, ui, registry, nil, "")

	msg, err := runner.RunTurn(context.Background(), "read and grep")
	if err != nil {
		t.Fatalf("RunTurn() error = %v", err)
	}
	if msg.Content != "tools complete" {
		t.Fatalf("msg.Content = %q, want tools complete", msg.Content)
	}
	if got := len(ui.toolCalls); got != 2 {
		t.Fatalf("len(ui.toolCalls) = %d, want 2", got)
	}
	if got := len(ui.toolResults); got != 2 {
		t.Fatalf("len(ui.toolResults) = %d, want 2", got)
	}
	if len(model.calls) != 2 {
		t.Fatalf("model calls = %d, want 2", len(model.calls))
	}
	for _, want := range []string{"TOOL_RESULTS:", "call_read", "module paw", "call_grep", "go.mod:1:module paw"} {
		if !messagesContainContent(model.calls[1], want) {
			t.Fatalf("second model call messages = %#v, want %q", model.calls[1], want)
		}
	}
}

func TestRunTurnRunsConcurrencySafeToolsInParallel(t *testing.T) {
	ui := &fakeUI{}
	model := &fakeModel{
		rounds: []fakeRound{
			{
				events: []model.StreamEvent{
					{Delta: `[`},
					{Delta: `{"type":"tool_use","id":"call_a","name":"A","input":{}},`},
					{Delta: `{"type":"tool_use","id":"call_b","name":"B","input":{}}]`},
					{Done: true},
				},
			},
			{
				events: []model.StreamEvent{{Delta: "done"}, {Done: true}},
			},
		},
	}
	first := &blockingTool{name: "A", output: "a", started: make(chan struct{}), release: make(chan struct{}), safe: true}
	second := &blockingTool{name: "B", output: "b", started: make(chan struct{}), release: make(chan struct{}), safe: true}
	registry := tool.NewRegistry()
	registry.Register(first)
	registry.Register(second)
	runner := NewRunner(model, ui, registry, nil, "")

	done := make(chan error, 1)
	go func() {
		_, err := runner.RunTurn(context.Background(), "run both")
		done <- err
	}()

	select {
	case <-first.started:
	case <-time.After(time.Second):
		t.Fatal("first tool did not start")
	}
	select {
	case <-second.started:
	case <-time.After(time.Second):
		t.Fatal("second concurrency-safe tool did not start before first released")
	}
	close(first.release)
	close(second.release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("RunTurn() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("RunTurn() timed out")
	}
	if len(ui.deltas) != 1 || ui.deltas[0] != "done" {
		t.Fatalf("ui.deltas = %#v, want only final answer", ui.deltas)
	}
}

func TestRunTurnKeepsUnsafeToolsSerial(t *testing.T) {
	ui := &fakeUI{}
	model := &fakeModel{
		rounds: []fakeRound{
			{
				events: []model.StreamEvent{
					{Delta: `[{"type":"tool_use","id":"call_a","name":"A","input":{}},{"type":"tool_use","id":"call_b","name":"B","input":{}}]`},
					{Done: true},
				},
			},
			{
				events: []model.StreamEvent{{Delta: "done"}, {Done: true}},
			},
		},
	}
	first := &blockingTool{name: "A", output: "a", started: make(chan struct{}), release: make(chan struct{})}
	second := &blockingTool{name: "B", output: "b", started: make(chan struct{}), release: make(chan struct{})}
	registry := tool.NewRegistry()
	registry.Register(first)
	registry.Register(second)
	runner := NewRunner(model, ui, registry, nil, "")

	done := make(chan error, 1)
	go func() {
		_, err := runner.RunTurn(context.Background(), "run both")
		done <- err
	}()

	select {
	case <-first.started:
	case <-time.After(time.Second):
		t.Fatal("first tool did not start")
	}
	select {
	case <-second.started:
		t.Fatal("unsafe second tool started before first released")
	case <-time.After(50 * time.Millisecond):
	}
	close(first.release)
	select {
	case <-second.started:
	case <-time.After(time.Second):
		t.Fatal("second tool did not start after first released")
	}
	close(second.release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("RunTurn() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("RunTurn() timed out")
	}
}

func TestRunTurnInjectsSupplementBeforeNextModelStep(t *testing.T) {
	ui := &fakeUI{}
	model := &fakeModel{
		rounds: []fakeRound{
			{
				events: []model.StreamEvent{
					{Delta: `{"type":"tool_use","id":"call_1","name":"Read","input":{"file_path":"go.mod"}}`},
					{Done: true},
				},
			},
			{
				events: []model.StreamEvent{
					{Delta: "used supplement"},
					{Done: true},
				},
			},
		},
	}
	blocker := &blockingTool{
		name:    "Read",
		output:  "module paw",
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	registry := tool.NewRegistry()
	registry.Register(blocker)
	runner := NewRunner(model, ui, registry, nil, "")

	done := make(chan error, 1)
	go func() {
		_, err := runner.RunTurn(context.Background(), "read go.mod")
		done <- err
	}()

	select {
	case <-blocker.started:
	case <-time.After(time.Second):
		t.Fatal("tool did not start")
	}
	if !runner.SubmitSupplement("prefer the short answer") {
		t.Fatal("SubmitSupplement() = false")
	}
	close(blocker.release)

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("RunTurn() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("RunTurn() timed out")
	}
	if got := runner.PendingSupplementCount(); got != 0 {
		t.Fatalf("PendingSupplementCount() = %d, want 0", got)
	}
	if len(model.calls) != 2 {
		t.Fatalf("model calls = %d, want 2", len(model.calls))
	}
	for _, want := range []string{
		"Supplemental instruction submitted while this turn was running:",
		"prefer the short answer",
	} {
		if !messagesContainContent(model.calls[1], want) {
			t.Fatalf("second model call messages = %#v, want %q", model.calls[1], want)
		}
	}
}

func TestRunTurnCarriesLateSupplementToNextPrompt(t *testing.T) {
	stream := make(chan model.StreamEvent, 2)
	blocking := &blockingModel{
		streams: []chan model.StreamEvent{stream},
		started: make(chan int, 1),
	}
	runner := NewRunner(blocking, &fakeUI{}, tool.NewRegistry(), nil, "")

	done := make(chan error, 1)
	go func() {
		_, err := runner.RunTurn(context.Background(), "first")
		done <- err
	}()

	select {
	case <-blocking.started:
	case <-time.After(time.Second):
		t.Fatal("first model call did not start")
	}
	if !runner.SubmitSupplement("late context") {
		t.Fatal("SubmitSupplement() = false")
	}
	stream <- model.StreamEvent{Delta: "done"}
	stream <- model.StreamEvent{Done: true}
	close(stream)

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("RunTurn() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("RunTurn() timed out")
	}
	if got := runner.PendingSupplementCount(); got != 1 {
		t.Fatalf("PendingSupplementCount() = %d, want 1", got)
	}

	nextModel := &fakeModel{rounds: []fakeRound{{
		events: []model.StreamEvent{
			{Delta: "next done"},
			{Done: true},
		},
	}}}
	runner.model = nextModel
	if _, err := runner.RunTurn(context.Background(), "next"); err != nil {
		t.Fatalf("second RunTurn() error = %v", err)
	}
	if got := runner.PendingSupplementCount(); got != 0 {
		t.Fatalf("PendingSupplementCount() after second turn = %d, want 0", got)
	}
	if len(nextModel.calls) != 1 {
		t.Fatalf("next model calls = %d, want 1", len(nextModel.calls))
	}
	for _, want := range []string{"late context", "next"} {
		if !messagesContainContent(nextModel.calls[0], want) {
			t.Fatalf("next model call messages = %#v, want %q", nextModel.calls[0], want)
		}
	}
}

func TestRunTurnKeepsSupplementWhenModelCallFails(t *testing.T) {
	failingModel := &fakeModel{rounds: []fakeRound{{err: errors.New("boom")}}}
	runner := NewRunner(failingModel, &fakeUI{}, tool.NewRegistry(), nil, "")
	if !runner.SubmitSupplement("do not lose this") {
		t.Fatal("SubmitSupplement() = false")
	}

	if _, err := runner.RunTurn(context.Background(), "first"); err == nil {
		t.Fatal("RunTurn() error = nil, want failure")
	}
	if got := runner.PendingSupplementCount(); got != 1 {
		t.Fatalf("PendingSupplementCount() = %d, want 1", got)
	}

	nextModel := &fakeModel{rounds: []fakeRound{{
		events: []model.StreamEvent{
			{Delta: "recovered"},
			{Done: true},
		},
	}}}
	runner.model = nextModel
	if _, err := runner.RunTurn(context.Background(), "recover"); err != nil {
		t.Fatalf("recovery RunTurn() error = %v", err)
	}
	if got := runner.PendingSupplementCount(); got != 0 {
		t.Fatalf("PendingSupplementCount() after recovery = %d, want 0", got)
	}
	if !messagesContainContent(nextModel.calls[0], "do not lose this") {
		t.Fatalf("recovery messages = %#v, want retained supplement", nextModel.calls[0])
	}
}

func TestRunTurnExecutesToolUseWrappedInMarkdownFence(t *testing.T) {
	ui := &fakeUI{}
	model := &fakeModel{
		rounds: []fakeRound{
			{
				events: []model.StreamEvent{
					{Delta: "```json\n"},
					{Delta: `{"type":"tool_use","id":"call_1","name":"Read","input":{"file_path":"go.mod"}}`},
					{Delta: "\n```"},
					{Done: true},
				},
			},
			{
				events: []model.StreamEvent{
					{Delta: "go.mod loaded"},
					{Done: true},
				},
			},
		},
	}
	registry := tool.NewRegistry()
	registry.Register(&fakeTool{name: "Read", output: "module paw"})
	runner := NewRunner(model, ui, registry, nil, "")

	msg, err := runner.RunTurn(context.Background(), "read go.mod")
	if err != nil {
		t.Fatalf("RunTurn() error = %v", err)
	}
	if msg.Content != "go.mod loaded" {
		t.Fatalf("msg.Content = %q, want %q", msg.Content, "go.mod loaded")
	}
	if got := len(model.calls); got != 2 {
		t.Fatalf("len(model.calls) = %d, want 2", got)
	}
	if len(ui.toolCalls) != 1 || ui.toolCalls[0].Name != "Read" {
		t.Fatalf("ui.toolCalls = %#v, want single Read call", ui.toolCalls)
	}
	if len(ui.deltas) != 1 || ui.deltas[0] != "go.mod loaded" {
		t.Fatalf("ui.deltas = %#v, want only final answer output", ui.deltas)
	}
}

func TestRunTurnExecutesToolUseWrappedInMarkdownFenceWithPreamble(t *testing.T) {
	ui := &fakeUI{}
	model := &fakeModel{
		rounds: []fakeRound{
			{
				events: []model.StreamEvent{
					{Delta: "我将依次使用所有可用的工具来展示它们的功能：\n\n## 1. 首先使用 `LS` 查看当前目录结构\n\n```json\n"},
					{Delta: `{"type":"tool_use","id":"call_22","name":"LS","input":{"path":"."}}`},
					{Delta: "\n```"},
					{Done: true},
				},
			},
			{
				events: []model.StreamEvent{
					{Delta: "list complete"},
					{Done: true},
				},
			},
		},
	}
	registry := tool.NewRegistry()
	registry.Register(&fakeTool{name: "LS", output: ".\n.."})
	runner := NewRunner(model, ui, registry, nil, "")

	msg, err := runner.RunTurn(context.Background(), "尝试使用你所有的工具")
	if err != nil {
		t.Fatalf("RunTurn() error = %v", err)
	}
	if msg.Content != "list complete" {
		t.Fatalf("msg.Content = %q, want %q", msg.Content, "list complete")
	}
	if got := len(model.calls); got != 2 {
		t.Fatalf("len(model.calls) = %d, want 2", got)
	}
	if len(ui.toolCalls) != 1 || ui.toolCalls[0].Name != "LS" {
		t.Fatalf("ui.toolCalls = %#v, want single LS call", ui.toolCalls)
	}
	if len(ui.toolResults) != 1 || ui.toolResults[0].Name != "LS" {
		t.Fatalf("ui.toolResults = %#v, want single LS result", ui.toolResults)
	}
}

func TestRunTurnExecutesInvokeDSMLToolUseWithPreamble(t *testing.T) {
	ui := &fakeUI{}
	model := &fakeModel{
		rounds: []fakeRound{
			{
				events: []model.StreamEvent{
					{Delta: "好的，我来读出所有代码文件的内容。\n\n"},
					{Delta: `<invoke name="Bash">`},
					{Delta: "\n<｜｜DSML｜｜ name=\"command\" string=\"true\">cd . && find . -type f -name '*.go' | sort</｜｜DSML｜｜>\n"},
					{Delta: `</invoke>`},
					{Done: true},
				},
			},
			{
				events: []model.StreamEvent{
					{Delta: "代码文件列表已读取"},
					{Done: true},
				},
			},
		},
	}
	registry := tool.NewRegistry()
	bash := &fakeTool{name: "Bash", output: "internal/loop/runner.go"}
	registry.Register(bash)
	runner := NewRunner(model, ui, registry, nil, "")

	msg, err := runner.RunTurn(context.Background(), "读代码文件内容并cat 输出")
	if err != nil {
		t.Fatalf("RunTurn() error = %v", err)
	}
	if msg.Content != "代码文件列表已读取" {
		t.Fatalf("msg.Content = %q, want final answer", msg.Content)
	}
	if got := len(model.calls); got != 2 {
		t.Fatalf("len(model.calls) = %d, want 2", got)
	}
	if len(ui.toolCalls) != 1 || ui.toolCalls[0].Name != "Bash" {
		t.Fatalf("ui.toolCalls = %#v, want single Bash call", ui.toolCalls)
	}

	var input struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal(bash.input, &input); err != nil {
		t.Fatalf("Bash input unmarshal error = %v; input=%s", err, bash.input)
	}
	if input.Command != "cd . && find . -type f -name '*.go' | sort" {
		t.Fatalf("input.Command = %q", input.Command)
	}
	if len(ui.deltas) != 1 || ui.deltas[0] != "代码文件列表已读取" {
		t.Fatalf("ui.deltas = %#v, want only final answer output", ui.deltas)
	}
}

func TestRunTurnParsesInvokeDSMLNonStringParameters(t *testing.T) {
	ui := &fakeUI{}
	model := &fakeModel{
		rounds: []fakeRound{
			{
				events: []model.StreamEvent{
					{Delta: `<invoke name="Grep">`},
					{Delta: `<parameter name="pattern" string="true">cache|Cache</parameter>`},
					{Delta: `<parameter name="path" string="true">internal</parameter>`},
					{Delta: `<parameter name="max_results" string="false">80</parameter>`},
					{Delta: `</invoke>`},
					{Done: true},
				},
			},
			{
				events: []model.StreamEvent{{Delta: "search complete"}, {Done: true}},
			},
		},
	}
	registry := tool.NewRegistry()
	grep := &fakeTool{name: "Grep", output: "match"}
	registry.Register(grep)
	runner := NewRunner(model, ui, registry, nil, "")

	if _, err := runner.RunTurn(context.Background(), "查找 cache"); err != nil {
		t.Fatalf("RunTurn() error = %v", err)
	}

	var input struct {
		Pattern    string `json:"pattern"`
		Path       string `json:"path"`
		MaxResults int    `json:"max_results"`
	}
	if err := json.Unmarshal(grep.input, &input); err != nil {
		t.Fatalf("Grep input unmarshal error = %v; input=%s", err, grep.input)
	}
	if input.Pattern != "cache|Cache" || input.Path != "internal" || input.MaxResults != 80 {
		t.Fatalf("input = %#v", input)
	}
}

func TestRunTurnParsesInvokeDSMLValueAttribute(t *testing.T) {
	ui := &fakeUI{}
	model := &fakeModel{
		rounds: []fakeRound{
			{
				events: []model.StreamEvent{
					{Delta: `<invoke name="Grep">`},
					{Delta: `<parameter name="pattern">cache</parameter>`},
					{Delta: `<parameter name="max_results" string="false" value="80"></parameter>`},
					{Delta: `</invoke>`},
					{Done: true},
				},
			},
			{
				events: []model.StreamEvent{{Delta: "search complete"}, {Done: true}},
			},
		},
	}
	registry := tool.NewRegistry()
	grep := &fakeTool{name: "Grep", output: "match"}
	registry.Register(grep)
	runner := NewRunner(model, ui, registry, nil, "")

	if _, err := runner.RunTurn(context.Background(), "查找 cache"); err != nil {
		t.Fatalf("RunTurn() error = %v", err)
	}

	var input struct {
		MaxResults int `json:"max_results"`
	}
	if err := json.Unmarshal(grep.input, &input); err != nil {
		t.Fatalf("Grep input unmarshal error = %v; input=%s", err, grep.input)
	}
	if input.MaxResults != 80 {
		t.Fatalf("input.MaxResults = %d, want 80", input.MaxResults)
	}
}

func TestRunTurnCarriesHistoryAcrossTurns(t *testing.T) {
	ui := &fakeUI{}
	model := &fakeModel{
		rounds: []fakeRound{
			{
				events: []model.StreamEvent{
					{Delta: "first answer"},
					{Done: true},
				},
			},
			{
				events: []model.StreamEvent{
					{Delta: "second answer"},
					{Done: true},
				},
			},
		},
	}
	runner := NewRunner(model, ui, tool.NewRegistry(), nil, "")

	if _, err := runner.RunTurn(context.Background(), "first question"); err != nil {
		t.Fatalf("first RunTurn() error = %v", err)
	}
	if _, err := runner.RunTurn(context.Background(), "second question"); err != nil {
		t.Fatalf("second RunTurn() error = %v", err)
	}

	if got := len(model.calls); got != 2 {
		t.Fatalf("len(model.calls) = %d, want 2", got)
	}

	secondCall := model.calls[1]
	foundFirstQuestion := false
	foundFirstAnswer := false
	foundSecondQuestion := false
	for _, msg := range secondCall {
		switch msg.Content {
		case "first question":
			foundFirstQuestion = true
		case "first answer":
			foundFirstAnswer = true
		case "second question":
			foundSecondQuestion = true
		}
	}

	if !foundFirstQuestion || !foundFirstAnswer || !foundSecondQuestion {
		t.Fatalf("second round did not include committed history: %#v", secondCall)
	}
}

func TestResetHistoryPreventsStoreReloadDuringProcess(t *testing.T) {
	ui := &fakeUI{}
	model := &fakeModel{
		rounds: []fakeRound{
			{
				events: []model.StreamEvent{
					{Delta: "fresh answer"},
					{Done: true},
				},
			},
		},
	}
	store := &fakeStore{
		history: []message.Message{
			{Role: message.RoleAssistant, Content: "stale answer"},
		},
	}
	runner := NewRunner(model, ui, tool.NewRegistry(), store, "session-1")

	runner.ResetHistory()

	msg, err := runner.RunTurn(context.Background(), "new question")
	if err != nil {
		t.Fatalf("RunTurn() error = %v", err)
	}
	if msg.Content != "fresh answer" {
		t.Fatalf("msg.Content = %q, want %q", msg.Content, "fresh answer")
	}
	if store.loadCalls != 0 {
		t.Fatalf("store.loadCalls = %d, want 0", store.loadCalls)
	}
	if len(model.calls) != 1 {
		t.Fatalf("len(model.calls) = %d, want 1", len(model.calls))
	}
	for _, callMsg := range model.calls[0] {
		if callMsg.Content == "stale answer" {
			t.Fatalf("unexpected stale history in model call: %#v", model.calls[0])
		}
	}
}

// TestContextStats_精简后三字段正确 验证 ContextStats 只暴露三个字段，且 UsedTokens = input+output。
func TestContextStats_精简后三字段正确(t *testing.T) {
	m := &fakeModel{rounds: []fakeRound{{events: []model.StreamEvent{
		{Usage: &model.Usage{InputTokens: 1000, OutputTokens: 100}},
		{Done: true},
	}}}}
	runner := NewRunner(m, &fakeUI{}, nil, nil, "")
	if _, err := runner.RunTurn(context.Background(), "hi"); err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	stats := runner.ContextStats(200000, "")
	if stats.UsedTokens != 1100 {
		t.Errorf("UsedTokens = %d，期望 1100", stats.UsedTokens)
	}
	if stats.CacheTokens != 0 {
		t.Errorf("CacheTokens = %d，期望 0", stats.CacheTokens)
	}
	if stats.LimitTokens != 200000 {
		t.Errorf("LimitTokens = %d，期望 200000", stats.LimitTokens)
	}
}

// TestContextStats_CacheTokens正确反映命中缓存 验证 CacheHitTokens 进入 CacheTokens。
func TestContextStats_CacheTokens正确反映命中缓存(t *testing.T) {
	m := &fakeModel{rounds: []fakeRound{{events: []model.StreamEvent{
		{Usage: &model.Usage{InputTokens: 500, CacheReadInputTokens: 300, OutputTokens: 50}},
		{Done: true},
	}}}}
	runner := NewRunner(m, &fakeUI{}, nil, nil, "")
	if _, err := runner.RunTurn(context.Background(), "hi"); err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	stats := runner.ContextStats(200000, "")
	// UsedTokens = 500 + 300 + 50 = 850
	if stats.UsedTokens != 850 {
		t.Errorf("UsedTokens = %d，期望 850（500 input + 300 cache_read + 50 output）", stats.UsedTokens)
	}
	if stats.CacheTokens != 300 {
		t.Errorf("CacheTokens = %d，期望 300", stats.CacheTokens)
	}
}

// TestContextStats_无usage时返回零值 验证未收到任何 usage 事件时安全返回零值。
func TestContextStats_无usage时返回零值(t *testing.T) {
	runner := NewRunner(nil, nil, nil, nil, "")
	stats := runner.ContextStats(100000, "")
	if stats.UsedTokens != 0 || stats.CacheTokens != 0 {
		t.Errorf("空 runner ContextStats = %+v，期望 token 均为 0", stats)
	}
	if stats.LimitTokens != 100000 {
		t.Errorf("LimitTokens = %d，期望 100000", stats.LimitTokens)
	}
}

// TestRunTurnExecutesToolCallFormatWithJSONInput 验证 <tool_call><tool name="...">JSON</tool></tool_call> 格式。
func TestRunTurnExecutesToolCallFormatWithJSONInput(t *testing.T) {
	ui := &fakeUI{}
	m := &fakeModel{
		rounds: []fakeRound{
			{
				events: []model.StreamEvent{
					{Delta: "<tool_call>\n"},
					{Delta: `<tool name="Bash">{"command":"ls ."}</tool>`},
					{Delta: "\n</tool_call>"},
					{Done: true},
				},
			},
			{
				events: []model.StreamEvent{
					{Delta: "done"},
					{Done: true},
				},
			},
		},
	}
	registry := tool.NewRegistry()
	bash := &fakeTool{name: "Bash", output: "ok"}
	registry.Register(bash)
	runner := NewRunner(m, ui, registry, nil, "")

	msg, err := runner.RunTurn(context.Background(), "list files")
	if err != nil {
		t.Fatalf("RunTurn() error = %v", err)
	}
	if msg.Content != "done" {
		t.Fatalf("msg.Content = %q, want done", msg.Content)
	}
	if len(ui.toolCalls) != 1 || ui.toolCalls[0].Name != "Bash" {
		t.Fatalf("ui.toolCalls = %#v, want single Bash call", ui.toolCalls)
	}
	var input struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal(bash.input, &input); err != nil {
		t.Fatalf("unmarshal Bash input: %v", err)
	}
	if input.Command != "ls ." {
		t.Fatalf("command = %q, want ls .", input.Command)
	}
}

// TestRunTurnExecutesToolCallNameInputFormat 验证 <tool><tool_call><name>X</name><input>JSON</input> 格式。
func TestRunTurnExecutesToolCallNameInputFormat(t *testing.T) {
	ui := &fakeUI{}
	m := &fakeModel{
		rounds: []fakeRound{
			{
				events: []model.StreamEvent{
					{Delta: "让我读取代码。\n\n"},
					{Delta: "<tool>\n"},
					{Delta: "<tool_call>\n"},
					{Delta: "<name>Bash</name>\n"},
					{Delta: "<input>{\"command\":\"ls .\"}</input>\n"},
					{Delta: "</tool_call>\n</tool>"},
					{Done: true},
				},
			},
			{
				events: []model.StreamEvent{
					{Delta: "done"},
					{Done: true},
				},
			},
		},
	}
	registry := tool.NewRegistry()
	bash := &fakeTool{name: "Bash", output: "ok"}
	registry.Register(bash)
	runner := NewRunner(m, ui, registry, nil, "")

	msg, err := runner.RunTurn(context.Background(), "list files")
	if err != nil {
		t.Fatalf("RunTurn() error = %v", err)
	}
	if msg.Content != "done" {
		t.Fatalf("msg.Content = %q, want done", msg.Content)
	}
	if len(ui.toolCalls) != 1 || ui.toolCalls[0].Name != "Bash" {
		t.Fatalf("ui.toolCalls = %#v, want single Bash call", ui.toolCalls)
	}
	var input struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal(bash.input, &input); err != nil {
		t.Fatalf("unmarshal Bash input: %v", err)
	}
	if input.Command != "ls ." {
		t.Fatalf("command = %q, want ls .", input.Command)
	}
}

// TestRunTurnExecutesTagNameJSONFormat 验证 <toolname>JSON</toolname> 格式（如 <glob>{...}</glob>）。
func TestRunTurnExecutesTagNameJSONFormat(t *testing.T) {
	ui := &fakeUI{}
	m := &fakeModel{
		rounds: []fakeRound{
			{
				events: []model.StreamEvent{
					{Delta: "好的，我来读取当前项目的内容。\n\n"},
					{Delta: "<glob>\n"},
					{Delta: "{\n\"pattern\": \"*\",\n\"path\": \".\"\n}\n"},
					{Delta: "</glob>"},
					{Done: true},
				},
			},
			{
				events: []model.StreamEvent{
					{Delta: "读取完成"},
					{Done: true},
				},
			},
		},
	}
	registry := tool.NewRegistry()
	glob := &fakeTool{name: "glob", output: "file1.go\nfile2.go"}
	registry.Register(glob)
	runner := NewRunner(m, ui, registry, nil, "")

	msg, err := runner.RunTurn(context.Background(), "读取当前项目")
	if err != nil {
		t.Fatalf("RunTurn() error = %v", err)
	}
	if msg.Content != "读取完成" {
		t.Fatalf("msg.Content = %q, want 读取完成", msg.Content)
	}
	if len(ui.toolCalls) != 1 || ui.toolCalls[0].Name != "glob" {
		t.Fatalf("ui.toolCalls = %#v, want single glob call", ui.toolCalls)
	}
	var input struct {
		Pattern string `json:"pattern"`
		Path    string `json:"path"`
	}
	if err := json.Unmarshal(glob.input, &input); err != nil {
		t.Fatalf("unmarshal glob input: %v", err)
	}
	if input.Pattern != "*" || input.Path != "." {
		t.Fatalf("glob input = %+v, want pattern=* path=.", input)
	}
}

// TestRunTurnExecutesToolCallFormatWithXMLParams 验证 <tool_call> 中 XML 参数风格。
func TestRunTurnExecutesToolCallFormatWithXMLParams(t *testing.T) {
	ui := &fakeUI{}
	m := &fakeModel{
		rounds: []fakeRound{
			{
				events: []model.StreamEvent{
					{Delta: "<tool_call>\n<tool name=\"Read\">\n"},
					{Delta: "<file_path>README.md</file_path>\n"},
					{Delta: "</tool_call>"},
					{Done: true},
				},
			},
			{
				events: []model.StreamEvent{
					{Delta: "读取完成"},
					{Done: true},
				},
			},
		},
	}
	registry := tool.NewRegistry()
	readTool := &fakeTool{name: "Read", output: "content"}
	registry.Register(readTool)
	runner := NewRunner(m, ui, registry, nil, "")

	msg, err := runner.RunTurn(context.Background(), "read readme")
	if err != nil {
		t.Fatalf("RunTurn() error = %v", err)
	}
	if msg.Content != "读取完成" {
		t.Fatalf("msg.Content = %q, want 读取完成", msg.Content)
	}
	if len(ui.toolCalls) != 1 || ui.toolCalls[0].Name != "Read" {
		t.Fatalf("ui.toolCalls = %#v, want single Read call", ui.toolCalls)
	}
	var input struct {
		FilePath string `json:"file_path"`
	}
	if err := json.Unmarshal(readTool.input, &input); err != nil {
		t.Fatalf("unmarshal Read input: %v", err)
	}
	if input.FilePath != "README.md" {
		t.Fatalf("file_path = %q, want README.md", input.FilePath)
	}
}

type serialBarrierTool struct {
	name    string
	started chan<- string
	release <-chan struct{}
}

func (t *serialBarrierTool) Name() string        { return t.name }
func (t *serialBarrierTool) Description() string { return t.name }
func (t *serialBarrierTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object"}`)
}
func (t *serialBarrierTool) Run(ctx context.Context, _ json.RawMessage) (string, error) {
	t.started <- t.name
	if t.release != nil {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-t.release:
		}
	}
	return t.name + " done", nil
}

type safeBarrierTool struct{ *serialBarrierTool }

func (*safeBarrierTool) IsConcurrencySafe(json.RawMessage) bool { return true }

func TestToolCallsPreserveBarrierAroundSerialTool(t *testing.T) {
	registry := tool.NewRegistry()
	started := make(chan string, 3)
	beforeRelease := make(chan struct{})
	selectRelease := make(chan struct{})
	registry.Register(&safeBarrierTool{&serialBarrierTool{name: "safe-before", started: started, release: beforeRelease}})
	registry.Register(&serialBarrierTool{name: "Select", started: started, release: selectRelease})
	registry.Register(&safeBarrierTool{&serialBarrierTool{name: "safe-after", started: started}})
	runner := NewRunner(&fakeModel{}, &fakeUI{}, registry, nil, "")
	calls := []message.ToolCall{{ID: "1", Name: "safe-before", Input: json.RawMessage(`{}`)}, {ID: "2", Name: "Select", Input: json.RawMessage(`{}`)}, {ID: "3", Name: "safe-after", Input: json.RawMessage(`{}`)}}
	done := make(chan error, 1)
	go func() { _, err := runner.runToolCalls(context.Background(), calls); done <- err }()
	if got := <-started; got != "safe-before" {
		t.Fatalf("first = %s", got)
	}
	select {
	case got := <-started:
		t.Fatalf("started before barrier: %s", got)
	case <-time.After(30 * time.Millisecond):
	}
	close(beforeRelease)
	if got := <-started; got != "Select" {
		t.Fatalf("second = %s", got)
	}
	select {
	case got := <-started:
		t.Fatalf("started after barrier early: %s", got)
	case <-time.After(30 * time.Millisecond):
	}
	close(selectRelease)
	if got := <-started; got != "safe-after" {
		t.Fatalf("third = %s", got)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestToolCallsSerializeMultipleBarriers(t *testing.T) {
	registry := tool.NewRegistry()
	started := make(chan string, 3)
	one := make(chan struct{})
	two := make(chan struct{})
	registry.Register(&serialBarrierTool{name: "Select-1", started: started, release: one})
	registry.Register(&serialBarrierTool{name: "Select-2", started: started, release: two})
	registry.Register(&safeBarrierTool{&serialBarrierTool{name: "safe-after", started: started}})
	runner := NewRunner(&fakeModel{}, &fakeUI{}, registry, nil, "")
	calls := []message.ToolCall{{ID: "1", Name: "Select-1", Input: json.RawMessage(`{}`)}, {ID: "2", Name: "Select-2", Input: json.RawMessage(`{}`)}, {ID: "3", Name: "safe-after", Input: json.RawMessage(`{}`)}}
	done := make(chan error, 1)
	go func() { _, e := runner.runToolCalls(context.Background(), calls); done <- e }()
	if got := <-started; got != "Select-1" {
		t.Fatal(got)
	}
	close(one)
	if got := <-started; got != "Select-2" {
		t.Fatal(got)
	}
	close(two)
	if got := <-started; got != "safe-after" {
		t.Fatal(got)
	}
	if e := <-done; e != nil {
		t.Fatal(e)
	}
}

func TestToolCallConcurrencySafetyAndRunUseResolvedTool(t *testing.T) {
	registry := tool.NewRegistry()
	replacement := &fakeTool{name: "Swap", output: "replacement result", safe: true}
	original := &registryReplacingTool{
		name: "Swap", output: "original result", registry: registry, replacement: replacement,
	}
	registry.Register(original)
	runner := NewRunner(&fakeModel{}, &fakeUI{}, registry, nil, "")

	results, err := runner.runToolCalls(context.Background(), []message.ToolCall{{ID: "swap", Name: "Swap", Input: json.RawMessage(`{}`)}})
	if err != nil {
		t.Fatalf("runToolCalls: %v", err)
	}
	if results.ToolResult == nil || results.ToolResult.Content != "original result" || results.ToolResult.IsError {
		t.Fatalf("result = %+v", results.ToolResult)
	}
	if original.safetyCalls != 1 || original.runCalls != 1 {
		t.Fatalf("original safetyCalls=%d runCalls=%d, want 1/1", original.safetyCalls, original.runCalls)
	}
	if replacement.input != nil {
		t.Fatalf("replacement ran with input %s", replacement.input)
	}
}
