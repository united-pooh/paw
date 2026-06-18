package subagent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"gocode/internal/loop"
	"gocode/internal/message"
	"gocode/internal/model"
	"gocode/internal/session"
	"gocode/internal/settings"
	"gocode/internal/ui"
)

type fakeRound struct {
	events []model.StreamEvent
	err    error
}

type blockingLauncher struct {
	mu      sync.Mutex
	started []WorkerRequest
}

func (l *blockingLauncher) Start(ctx context.Context, req WorkerRequest) (Process, error) {
	l.mu.Lock()
	l.started = append(l.started, req)
	l.mu.Unlock()
	return newBlockingProcess(req), nil
}

type blockingProcess struct {
	req  WorkerRequest
	once sync.Once
	done chan struct{}
}

func newBlockingProcess(req WorkerRequest) *blockingProcess {
	return &blockingProcess{req: req, done: make(chan struct{})}
}

func (p *blockingProcess) PID() int {
	return 4242
}

func (p *blockingProcess) Wait() (WorkerResult, error) {
	<-p.done
	return WorkerResult{
		TaskID:    p.req.TaskID,
		SessionID: p.req.SessionID,
		Error:     "stopped",
		ExitCode:  -1,
	}, context.Canceled
}

func (p *blockingProcess) Stop() error {
	p.once.Do(func() { close(p.done) })
	return nil
}

type recordingModel struct {
	mu     sync.Mutex
	rounds []fakeRound
	calls  [][]message.Message
}

func (m *recordingModel) StreamMessage(ctx context.Context, messages []message.Message, _ []model.ToolDefinition) (<-chan model.StreamEvent, error) {
	m.mu.Lock()
	index := len(m.calls)
	copied := append([]message.Message(nil), messages...)
	m.calls = append(m.calls, copied)
	m.mu.Unlock()

	if index >= len(m.rounds) {
		return nil, context.Canceled
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

func (m *recordingModel) callsSnapshot() [][]message.Message {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([][]message.Message, len(m.calls))
	for i := range m.calls {
		out[i] = append([]message.Message(nil), m.calls[i]...)
	}
	return out
}

type blockingStreamModel struct {
	firstSent   chan struct{}
	releaseDone chan struct{}
}

func newBlockingStreamModel() *blockingStreamModel {
	return &blockingStreamModel{
		firstSent:   make(chan struct{}),
		releaseDone: make(chan struct{}),
	}
}

func (m *blockingStreamModel) StreamMessage(ctx context.Context, _ []message.Message, _ []model.ToolDefinition) (<-chan model.StreamEvent, error) {
	ch := make(chan model.StreamEvent)
	go func() {
		defer close(ch)
		if !sendTestModelEvent(ctx, ch, model.StreamEvent{Delta: "early step\nEND_STEP\n"}) {
			return
		}
		close(m.firstSent)
		select {
		case <-ctx.Done():
			return
		case <-m.releaseDone:
		}
		_ = sendTestModelEvent(ctx, ch, model.StreamEvent{Done: true})
	}()
	return ch, nil
}

func sendTestModelEvent(ctx context.Context, ch chan<- model.StreamEvent, ev model.StreamEvent) bool {
	select {
	case ch <- ev:
		return true
	case <-ctx.Done():
		return false
	}
}

type fakeSettingsProvider struct {
	cfg settings.Config
}

func (p fakeSettingsProvider) CurrentSettings() settings.Config {
	return p.cfg
}

type fakeNotifier struct {
	mu     sync.Mutex
	events []ui.SystemEvent
	ch     chan ui.SystemEvent
}

func newFakeNotifier() *fakeNotifier {
	return &fakeNotifier{ch: make(chan ui.SystemEvent, 4)}
}

func (n *fakeNotifier) OnSystemMessage(event ui.SystemEvent) error {
	n.mu.Lock()
	n.events = append(n.events, event)
	n.mu.Unlock()
	select {
	case n.ch <- event:
	default:
	}
	return nil
}

func (n *fakeNotifier) wait(t *testing.T) ui.SystemEvent {
	t.Helper()
	select {
	case event := <-n.ch:
		return event
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for system notification")
		return ui.SystemEvent{}
	}
}

type fakeContextSink struct {
	mu     sync.Mutex
	inputs []string
	ch     chan string
}

func newFakeContextSink() *fakeContextSink {
	return &fakeContextSink{ch: make(chan string, 4)}
}

func (s *fakeContextSink) SubmitSupplement(input string) bool {
	s.mu.Lock()
	s.inputs = append(s.inputs, input)
	s.mu.Unlock()
	select {
	case s.ch <- input:
	default:
	}
	return true
}

func (s *fakeContextSink) wait(t *testing.T) string {
	t.Helper()
	select {
	case input := <-s.ch:
		return input
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for context supplement")
		return ""
	}
}

func newTestManager(t *testing.T, modelStreamer loop.ModelStreamer, cfg settings.Config, notifier Notifier) (*Manager, *session.JSONLStore, string) {
	t.Helper()
	root := t.TempDir()
	store, err := session.NewJSONLStore(filepath.Join(root, ".ccagent"))
	if err != nil {
		t.Fatalf("NewJSONLStore() error = %v", err)
	}
	return NewManager(Config{
		Model:    modelStreamer,
		Store:    store,
		Root:     root,
		Settings: fakeSettingsProvider{cfg: cfg},
		Notifier: notifier,
	}), store, root
}

func createSessionWithHistory(t *testing.T, store *session.JSONLStore, sessionID string, history ...message.Message) {
	t.Helper()
	ctx := context.Background()
	if _, err := store.CreateRoot(ctx, session.CreateRootRequest{SessionID: sessionID}); err != nil {
		t.Fatalf("CreateRoot(%q) error = %v", sessionID, err)
	}
	if len(history) == 0 {
		return
	}
	if err := store.Append(ctx, sessionID, history...); err != nil {
		t.Fatalf("Append(%q) error = %v", sessionID, err)
	}
}

func waitForTask(t *testing.T, manager *Manager, id string, want TaskStatus) TaskSnapshot {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		task, ok := manager.Status(id)
		if ok && task.Status == want {
			return task
		}
		time.Sleep(10 * time.Millisecond)
	}
	task, _ := manager.Status(id)
	t.Fatalf("task %s did not reach status %s: %#v", id, want, task)
	return TaskSnapshot{}
}

func messageContents(messages []message.Message) []string {
	contents := make([]string, 0, len(messages))
	for _, msg := range messages {
		contents = append(contents, msg.Content)
	}
	return contents
}

func withDefaultPersonas(t *testing.T, personas []persona) {
	t.Helper()
	previous := defaultPersonas
	defaultPersonas = append([]persona(nil), personas...)
	t.Cleanup(func() {
		defaultPersonas = previous
	})
}

func TestRunEmptyContextStartsIndependentRootSession(t *testing.T) {
	modelStreamer := &recordingModel{
		rounds: []fakeRound{
			{events: []model.StreamEvent{{Delta: "child answer"}, {Done: true}}},
		},
	}
	manager, store, root := newTestManager(t, modelStreamer, settings.DefaultConfig(), nil)
	createSessionWithHistory(t, store, "parent",
		message.Message{Role: message.RoleUser, Content: "parent question"},
		message.Message{Role: message.RoleAssistant, Content: "parent answer"},
	)

	result, err := manager.Run(context.Background(), Request{
		ParentSessionID: "parent",
		Prompt:          "child prompt",
		ContextMode:     settings.ContextModeEmpty,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ContextMode != settings.ContextModeEmpty || result.RunMode != settings.RunModeSync {
		t.Fatalf("result = %#v", result)
	}
	if result.TranscriptPath != TranscriptPath(root, result.SessionID) {
		t.Fatalf("TranscriptPath = %q, want %q", result.TranscriptPath, TranscriptPath(root, result.SessionID))
	}

	calls := modelStreamer.callsSnapshot()
	if len(calls) != 1 || len(calls[0]) < 2 {
		t.Fatalf("model calls = %#v", calls)
	}
	systemPrompt := calls[0][0].Content
	if strings.Contains(systemPrompt, "Subagent:") || strings.Contains(systemPrompt, "SubagentStatus:") {
		t.Fatalf("system prompt should not expose recursive subagent tools: %q", systemPrompt)
	}
	for _, msg := range calls[0][1:] {
		if strings.Contains(msg.Content, "parent question") || strings.Contains(msg.Content, "parent answer") {
			t.Fatalf("empty-context call unexpectedly inherited parent history: %#v", calls[0])
		}
	}
	if got := calls[0][1].Content; got != "child prompt" {
		t.Fatalf("child prompt = %q, want child prompt", got)
	}

	history, err := store.LoadResolvedHistory(context.Background(), result.SessionID)
	if err != nil {
		t.Fatalf("LoadResolvedHistory(%q) error = %v", result.SessionID, err)
	}
	if got := messageContents(history); len(got) != 2 || got[0] != "child prompt" || got[1] != "child answer" {
		t.Fatalf("resolved history = %#v", got)
	}
}

func TestStreamEmitsDeltaBeforeModelDone(t *testing.T) {
	modelStreamer := newBlockingStreamModel()
	manager, _, _ := newTestManager(t, modelStreamer, settings.DefaultConfig(), nil)

	stream, err := manager.Stream(context.Background(), Request{
		Prompt:      "stream prompt",
		ContextMode: settings.ContextModeEmpty,
	})
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}

	select {
	case <-modelStreamer.firstSent:
	case <-time.After(time.Second):
		t.Fatal("model did not send first delta")
	}
	select {
	case ev := <-stream.Events:
		if ev.Delta != "early step\nEND_STEP\n" || ev.Done {
			t.Fatalf("first stream event = %#v", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("Stream() did not emit delta before model Done")
	}
	if task, ok := manager.Status(stream.SessionID); !ok || task.Status != TaskRunning {
		t.Fatalf("task status before Done = %#v/%v, want running", task, ok)
	}

	close(modelStreamer.releaseDone)
	var sawDone bool
	for ev := range stream.Events {
		if ev.Err != nil {
			t.Fatalf("stream error = %v", ev.Err)
		}
		if ev.Done {
			sawDone = true
		}
	}
	if !sawDone {
		t.Fatal("stream did not emit Done")
	}
	completed := waitForTask(t, manager, stream.SessionID, TaskCompleted)
	if completed.Content != "early step\nEND_STEP" {
		t.Fatalf("completed content = %q", completed.Content)
	}
}

func TestStreamReusesRequestedSessionAndDoesNotDuplicateBootstrapPrompt(t *testing.T) {
	modelStreamer := &recordingModel{
		rounds: []fakeRound{
			{events: []model.StreamEvent{{Delta: "first step\nEND_STEP\n"}, {Done: true}}},
			{events: []model.StreamEvent{{Delta: "second step\nEND_STEP\n"}, {Done: true}}},
		},
	}
	manager, store, _ := newTestManager(t, modelStreamer, settings.DefaultConfig(), nil)
	ctx := context.Background()
	sessionID := "streamma-a-session"

	first, err := manager.Stream(ctx, Request{
		SessionID: sessionID,
		Prompt:    "StreamMA logical agent bootstrap.\n\nOriginal problem:\nMake a thing.\n",
		SystemPrompt: "streamma_agent_id=a\nstreamma_role=tester\n" +
			"The final non-whitespace line of every assistant message must be exactly END_STEP.",
		ContextMode: settings.ContextModeEmpty,
	})
	if err != nil {
		t.Fatalf("first Stream() error = %v", err)
	}
	drainSubagentStream(t, first.Events)
	waitForTask(t, manager, sessionID, TaskCompleted)

	second, err := manager.Stream(ctx, Request{
		SessionID:   sessionID,
		Prompt:      "StreamMA incremental invocation.\n\nNew inbound step from planner:\nUse the existing ctx_a.\n",
		ContextMode: settings.ContextModeEmpty,
	})
	if err != nil {
		t.Fatalf("second Stream() error = %v", err)
	}
	if second.SessionID != sessionID {
		t.Fatalf("second SessionID = %q, want %q", second.SessionID, sessionID)
	}
	drainSubagentStream(t, second.Events)
	waitForTask(t, manager, sessionID, TaskCompleted)

	history, err := store.LoadResolvedHistory(ctx, sessionID)
	if err != nil {
		t.Fatalf("LoadResolvedHistory() error = %v", err)
	}
	if len(history) != 4 {
		t.Fatalf("history len = %d, want 4 messages: %#v", len(history), history)
	}
	if !strings.Contains(history[0].Content, "Original problem:") {
		t.Fatalf("first user prompt missing bootstrap problem: %#v", history[0])
	}
	if strings.Contains(history[2].Content, "Original problem:") {
		t.Fatalf("second user prompt duplicated bootstrap problem: %#v", history[2])
	}
	if !strings.Contains(history[2].Content, "New inbound step from planner") {
		t.Fatalf("second user prompt missing incremental inbound step: %#v", history[2])
	}
	combined := history[0].Content + "\n" + history[2].Content
	if strings.Count(combined, "Original problem:") != 1 {
		t.Fatalf("bootstrap problem appears %d times in user prompts", strings.Count(combined, "Original problem:"))
	}
	calls := modelStreamer.callsSnapshot()
	if len(calls) == 0 || len(calls[0]) == 0 {
		t.Fatalf("model calls = %#v, want first call with system prompt", calls)
	}
	system := calls[0][0]
	if system.Role != message.RoleSystem || !strings.Contains(system.Content, "streamma_agent_id=a") || !strings.Contains(system.Content, "exactly END_STEP") {
		t.Fatalf("first model system prompt = %#v, want StreamMA system supplement", system)
	}
	if strings.Contains(history[0].Content, "streamma_agent_id=a") {
		t.Fatalf("StreamMA system prompt leaked into user history: %#v", history[0])
	}
}

func drainSubagentStream(t *testing.T, events <-chan model.StreamEvent) {
	t.Helper()
	for ev := range events {
		if ev.Err != nil {
			t.Fatalf("stream error = %v", ev.Err)
		}
	}
}

func TestLaunchAssignsUniqueRunningPersonas(t *testing.T) {
	withDefaultPersonas(t, []persona{
		{Name: "角色A", Color: "#111111"},
		{Name: "角色B", Color: "#222222"},
	})

	root := t.TempDir()
	store, err := session.NewJSONLStore(filepath.Join(root, ".ccagent"))
	if err != nil {
		t.Fatalf("NewJSONLStore() error = %v", err)
	}
	launcher := &blockingLauncher{}
	manager := NewManager(Config{
		Store:    store,
		Root:     root,
		Settings: fakeSettingsProvider{cfg: settings.DefaultConfig()},
		Launcher: launcher,
	})

	first, err := manager.Launch(context.Background(), Request{Prompt: "first"})
	if err != nil {
		t.Fatalf("first Launch() error = %v", err)
	}
	second, err := manager.Launch(context.Background(), Request{Prompt: "second"})
	if err != nil {
		t.Fatalf("second Launch() error = %v", err)
	}
	if first.Name == "" || second.Name == "" || first.Color == "" || second.Color == "" {
		t.Fatalf("launched personas = %#v / %#v", first, second)
	}
	if first.Name == second.Name {
		t.Fatalf("running subagents reused persona %q", first.Name)
	}

	if _, err := manager.Launch(context.Background(), Request{Prompt: "third"}); err == nil || !strings.Contains(err.Error(), "no idle subagent names") {
		t.Fatalf("third Launch() error = %v, want exhausted persona pool", err)
	}

	if _, err := manager.Stop(context.Background(), first.ID); err != nil {
		t.Fatalf("Stop(first) error = %v", err)
	}
	if _, err := manager.Stop(context.Background(), second.ID); err != nil {
		t.Fatalf("Stop(second) error = %v", err)
	}
}

func TestCompletedPersonaIsIdle(t *testing.T) {
	withDefaultPersonas(t, []persona{{Name: "可复用角色", Color: "#333333"}})
	modelStreamer := &recordingModel{
		rounds: []fakeRound{
			{events: []model.StreamEvent{{Delta: "first answer"}, {Done: true}}},
			{events: []model.StreamEvent{{Delta: "second answer"}, {Done: true}}},
		},
	}
	manager, _, _ := newTestManager(t, modelStreamer, settings.DefaultConfig(), nil)

	first, err := manager.Run(context.Background(), Request{Prompt: "first"})
	if err != nil {
		t.Fatalf("first Run() error = %v", err)
	}
	if first.AgentName != "可复用角色" || first.AgentColor != "#333333" {
		t.Fatalf("first result persona = %#v", first)
	}

	second, err := manager.Run(context.Background(), Request{Prompt: "second"})
	if err != nil {
		t.Fatalf("second Run() error = %v", err)
	}
	if second.AgentName != first.AgentName || second.AgentColor != first.AgentColor {
		t.Fatalf("second result persona = %#v, want reuse of completed persona %#v", second, first)
	}
}

func TestRunForkContextInheritsCommittedParentHistory(t *testing.T) {
	modelStreamer := &recordingModel{
		rounds: []fakeRound{
			{events: []model.StreamEvent{{Delta: "child answer"}, {Done: true}}},
		},
	}
	manager, store, _ := newTestManager(t, modelStreamer, settings.DefaultConfig(), nil)
	createSessionWithHistory(t, store, "parent",
		message.Message{Role: message.RoleUser, Content: "parent question"},
		message.Message{Role: message.RoleAssistant, Content: "parent answer"},
	)

	result, err := manager.Run(context.Background(), Request{
		ParentSessionID: "parent",
		Prompt:          "child prompt",
		ContextMode:     settings.ContextModeFork,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	calls := modelStreamer.callsSnapshot()
	if len(calls) != 1 || len(calls[0]) < 4 {
		t.Fatalf("model calls = %#v", calls)
	}
	gotCallContents := messageContents(calls[0][1:])
	wantCallContents := []string{"parent question", "parent answer", "child prompt"}
	for i, want := range wantCallContents {
		if gotCallContents[i] != want {
			t.Fatalf("model call contents = %#v, want %#v", gotCallContents, wantCallContents)
		}
	}

	history, err := store.LoadResolvedHistory(context.Background(), result.SessionID)
	if err != nil {
		t.Fatalf("LoadResolvedHistory(%q) error = %v", result.SessionID, err)
	}
	gotHistoryContents := messageContents(history)
	wantHistoryContents := []string{"parent question", "parent answer", "child prompt", "child answer"}
	for i, want := range wantHistoryContents {
		if gotHistoryContents[i] != want {
			t.Fatalf("resolved history = %#v, want %#v", gotHistoryContents, wantHistoryContents)
		}
	}
}

func TestRunForkContextCreatesEmptyParentWhenMissing(t *testing.T) {
	modelStreamer := &recordingModel{
		rounds: []fakeRound{
			{events: []model.StreamEvent{{Delta: "child answer"}, {Done: true}}},
		},
	}
	manager, store, _ := newTestManager(t, modelStreamer, settings.DefaultConfig(), nil)

	result, err := manager.Run(context.Background(), Request{
		ParentSessionID: "lazy-parent",
		Prompt:          "child prompt",
		ContextMode:     settings.ContextModeFork,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if exists, err := store.Exists(context.Background(), "lazy-parent"); err != nil {
		t.Fatalf("Exists(lazy-parent) error = %v", err)
	} else if !exists {
		t.Fatalf("lazy parent should be created before fork")
	}
	history, err := store.LoadResolvedHistory(context.Background(), result.SessionID)
	if err != nil {
		t.Fatalf("LoadResolvedHistory(%q) error = %v", result.SessionID, err)
	}
	if got := messageContents(history); len(got) != 2 || got[0] != "child prompt" || got[1] != "child answer" {
		t.Fatalf("resolved history = %#v, want child-only history", got)
	}
}

func TestLaunchTracksStatusListAndNotification(t *testing.T) {
	modelStreamer := &recordingModel{
		rounds: []fakeRound{
			{events: []model.StreamEvent{{Delta: "background answer"}, {Done: true}}},
		},
	}
	notifier := newFakeNotifier()
	manager, _, root := newTestManager(t, modelStreamer, settings.DefaultConfig(), notifier)

	task, err := manager.Launch(context.Background(), Request{
		Prompt:      "background prompt",
		Description: "background work",
	})
	if err != nil {
		t.Fatalf("Launch() error = %v", err)
	}
	if task.Status != TaskRunning || task.RunMode != settings.RunModeBackground || task.ContextMode != settings.ContextModeEmpty {
		t.Fatalf("initial task = %#v", task)
	}
	if task.TranscriptPath != TranscriptPath(root, task.SessionID) {
		t.Fatalf("TranscriptPath = %q, want %q", task.TranscriptPath, TranscriptPath(root, task.SessionID))
	}
	if snapshot, ok := manager.Status(task.ID); !ok || snapshot.Status != TaskRunning {
		t.Fatalf("initial Status() = %#v/%v", snapshot, ok)
	}

	completed := waitForTask(t, manager, task.ID, TaskCompleted)
	if completed.Content != "background answer" || completed.Error != "" || completed.FinishedAt == nil {
		t.Fatalf("completed task = %#v", completed)
	}
	list := manager.ListTasks()
	if len(list) != 1 || list[0].ID != task.ID || list[0].Status != TaskCompleted {
		t.Fatalf("ListTasks() = %#v", list)
	}

	event := notifier.wait(t)
	if event.Title != "subagent" || !strings.Contains(event.Body, "status=completed") || !strings.Contains(event.Body, "background answer") {
		t.Fatalf("system event = %#v", event)
	}
}

func TestBackgroundCompletionSubmitsParentContext(t *testing.T) {
	longResult := strings.Repeat("x", parentContextResultMaxRunes+20)
	modelStreamer := &recordingModel{
		rounds: []fakeRound{
			{events: []model.StreamEvent{{Delta: longResult}, {Done: true}}},
		},
	}
	sink := newFakeContextSink()
	manager, _, _ := newTestManager(t, modelStreamer, settings.DefaultConfig(), nil)
	manager.contextSink = sink

	task, err := manager.Launch(context.Background(), Request{
		ParentSessionID: "parent-session",
		Prompt:          "background prompt",
		Description:     "collect context",
	})
	if err != nil {
		t.Fatalf("Launch() error = %v", err)
	}
	completed := waitForTask(t, manager, task.ID, TaskCompleted)
	if completed.Content != longResult {
		t.Fatalf("completed.Content length = %d, want %d", len(completed.Content), len(longResult))
	}

	update := sink.wait(t)
	for _, want := range []string{
		"Background subagent completed.",
		"id: " + task.ID,
		"description: collect context",
		"status: completed",
		"result:",
		"[truncated; full result: " + completed.OutputPath + "]",
	} {
		if !strings.Contains(update, want) {
			t.Fatalf("context update = %q, want %q", update, want)
		}
	}
	_, afterResult, ok := strings.Cut(update, "result:\n")
	if !ok {
		t.Fatalf("context update = %q, want result section", update)
	}
	resultSection, _, ok := strings.Cut(afterResult, "\n[truncated; full result:")
	if !ok {
		t.Fatalf("context update = %q, want truncated marker after result", update)
	}
	if len([]rune(resultSection)) != parentContextResultMaxRunes {
		t.Fatalf("context result rune length = %d, want %d", len([]rune(resultSection)), parentContextResultMaxRunes)
	}
}

func TestSyncCompletionDoesNotSubmitParentContext(t *testing.T) {
	modelStreamer := &recordingModel{
		rounds: []fakeRound{
			{events: []model.StreamEvent{{Delta: "sync answer"}, {Done: true}}},
		},
	}
	sink := newFakeContextSink()
	manager, _, _ := newTestManager(t, modelStreamer, settings.DefaultConfig(), nil)
	manager.contextSink = sink

	if _, err := manager.Run(context.Background(), Request{
		ParentSessionID: "parent-session",
		Prompt:          "sync prompt",
	}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	select {
	case update := <-sink.ch:
		t.Fatalf("unexpected context update for sync task: %q", update)
	default:
	}
}

func TestSubagentToolsProvideSyncAndStatusAccess(t *testing.T) {
	modelStreamer := &recordingModel{
		rounds: []fakeRound{
			{events: []model.StreamEvent{{Delta: "sync answer"}, {Done: true}}},
			{events: []model.StreamEvent{{Delta: "background answer"}, {Done: true}}},
		},
	}
	manager, _, _ := newTestManager(t, modelStreamer, settings.DefaultConfig(), nil)

	tool := NewTool(manager, "parent-session")
	if tool.Name() != "Subagent" {
		t.Fatalf("tool.Name() = %q, want Subagent", tool.Name())
	}
	schema := string(tool.InputSchema())
	for _, want := range []string{`"required":["prompt"]`, `"context_mode"`, `"run_mode"`, `"description"`} {
		if !strings.Contains(schema, want) {
			t.Fatalf("InputSchema() = %q, want %q", schema, want)
		}
	}

	if safeTool, ok := tool.(interface{ IsConcurrencySafe(json.RawMessage) bool }); !ok || !safeTool.IsConcurrencySafe(json.RawMessage(`{"prompt":"background prompt"}`)) || safeTool.IsConcurrencySafe(json.RawMessage(`{"prompt":"sync prompt","run_mode":"sync"}`)) {
		t.Fatalf("Subagent concurrency safety should be true for default background and false for explicit sync")
	}

	syncOut, err := tool.Run(context.Background(), json.RawMessage(`{"prompt":"sync prompt","description":"focus","run_mode":"sync"}`))
	if err != nil {
		t.Fatalf("sync Run() error = %v", err)
	}
	if !strings.Contains(syncOut, `"content": "sync answer"`) || !strings.Contains(syncOut, `"context_mode": "empty"`) {
		t.Fatalf("sync output = %q", syncOut)
	}

	backgroundOut, err := tool.Run(context.Background(), json.RawMessage(`{"prompt":"background prompt"}`))
	if err != nil {
		t.Fatalf("background Run() error = %v", err)
	}
	var launched TaskSnapshot
	if err := json.Unmarshal([]byte(backgroundOut), &launched); err != nil {
		t.Fatalf("json.Unmarshal(backgroundOut) error = %v", err)
	}
	if launched.ParentSessionID != "parent-session" {
		t.Fatalf("launched.ParentSessionID = %q, want parent-session", launched.ParentSessionID)
	}
	completed := waitForTask(t, manager, launched.ID, TaskCompleted)
	if completed.Content != "background answer" {
		t.Fatalf("completed background task = %#v", completed)
	}
	if completed.ParentSessionID != "parent-session" {
		t.Fatalf("completed.ParentSessionID = %q, want parent-session", completed.ParentSessionID)
	}

	statusTool := NewStatusTool(manager)
	listOut, err := statusTool.Run(context.Background(), nil)
	if err != nil {
		t.Fatalf("status list Run() error = %v", err)
	}
	if !strings.Contains(listOut, launched.ID) || !strings.Contains(listOut, `"status": "completed"`) {
		t.Fatalf("status list output = %q", listOut)
	}

	singleOut, err := statusTool.Run(context.Background(), json.RawMessage(`{"id":"`+launched.ID+`"}`))
	if err != nil {
		t.Fatalf("status single Run() error = %v", err)
	}
	if !strings.Contains(singleOut, `"id": "`+launched.ID+`"`) || !strings.Contains(singleOut, `"content": "background answer"`) {
		t.Fatalf("status single output = %q", singleOut)
	}
}

func TestLaunchPersistsTaskRegistry(t *testing.T) {
	modelStreamer := &recordingModel{
		rounds: []fakeRound{
			{events: []model.StreamEvent{{Delta: "persisted answer"}, {Done: true}}},
		},
	}
	manager, store, root := newTestManager(t, modelStreamer, settings.DefaultConfig(), nil)

	task, err := manager.Launch(context.Background(), Request{
		Prompt:      "persist this",
		Description: "persist test",
	})
	if err != nil {
		t.Fatalf("Launch() error = %v", err)
	}
	completed := waitForTask(t, manager, task.ID, TaskCompleted)
	if completed.OutputPath == "" || completed.ExitCode == nil || *completed.ExitCode != 0 {
		t.Fatalf("completed task output metadata = %#v", completed)
	}
	if _, err := os.Stat(completed.OutputPath); err != nil {
		t.Fatalf("output path %q stat error = %v", completed.OutputPath, err)
	}

	reader := NewManager(Config{Store: store, Root: root, Settings: fakeSettingsProvider{cfg: settings.DefaultConfig()}})
	loaded, ok := reader.Status(task.ID)
	if !ok {
		t.Fatalf("Status(%q) from registry not found", task.ID)
	}
	if loaded.Status != TaskCompleted || loaded.Content != "persisted answer" || loaded.OutputPath != completed.OutputPath {
		t.Fatalf("loaded task = %#v, want completed persisted task", loaded)
	}
}

func TestStopStopsRunningWorkerAndPersistsStoppedStatus(t *testing.T) {
	root := t.TempDir()
	store, err := session.NewJSONLStore(filepath.Join(root, ".ccagent"))
	if err != nil {
		t.Fatalf("NewJSONLStore() error = %v", err)
	}
	launcher := &blockingLauncher{}
	manager := NewManager(Config{
		Store:    store,
		Root:     root,
		Settings: fakeSettingsProvider{cfg: settings.DefaultConfig()},
		Launcher: launcher,
	})

	task, err := manager.Launch(context.Background(), Request{Prompt: "wait"})
	if err != nil {
		t.Fatalf("Launch() error = %v", err)
	}
	if task.PID != 4242 || task.OutputPath == "" || task.Depth != 1 {
		t.Fatalf("launched task = %#v", task)
	}

	stopped, err := manager.Stop(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if stopped.Status != TaskStopped || stopped.ExitCode == nil || *stopped.ExitCode != -1 {
		t.Fatalf("stopped task = %#v", stopped)
	}
	loaded, ok := manager.Status(task.ID)
	if !ok || loaded.Status != TaskStopped {
		t.Fatalf("Status() after stop = %#v/%v", loaded, ok)
	}
	if _, err := os.Stat(stopped.OutputPath); err != nil {
		t.Fatalf("stopped output path %q stat error = %v", stopped.OutputPath, err)
	}
}

func TestProcessLauncherParsesWorkerJSON(t *testing.T) {
	launcher := &ProcessLauncher{
		Command: os.Args[0],
		Args:    []string{"-test.run=TestSubagentWorkerHelperProcess"},
		Env:     []string{"GOCODE_SUBAGENT_HELPER=1"},
	}
	proc, err := launcher.Start(context.Background(), WorkerRequest{
		TaskID:    "task-1",
		SessionID: "session-1",
		Prompt:    "hello",
	})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	result, err := proc.Wait()
	if err != nil {
		t.Fatalf("Wait() error = %v result=%#v", err, result)
	}
	if result.TaskID != "task-1" || result.SessionID != "session-1" || result.Content != "worker: hello" || result.ExitCode != 0 {
		t.Fatalf("worker result = %#v", result)
	}
}

func TestSubagentWorkerHelperProcess(t *testing.T) {
	if os.Getenv("GOCODE_SUBAGENT_HELPER") != "1" {
		return
	}
	var req WorkerRequest
	if err := json.NewDecoder(os.Stdin).Decode(&req); err != nil {
		_, _ = os.Stderr.WriteString(err.Error())
		os.Exit(2)
	}
	_ = json.NewEncoder(os.Stdout).Encode(WorkerResult{
		TaskID:    req.TaskID,
		SessionID: req.SessionID,
		Content:   "worker: " + req.Prompt,
		ExitCode:  0,
	})
	os.Exit(0)
}
