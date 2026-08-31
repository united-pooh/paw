package task

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"paw/internal/actor"
	"paw/internal/es"
	"paw/internal/loop"
	coremcp "paw/internal/mcp"
	"paw/internal/message"
	"paw/internal/model"
	"paw/internal/session"
	"paw/internal/settings"
	"paw/internal/tokentracer"
	"paw/internal/ui"
)

type fakeRound struct {
	events []model.StreamEvent
	err    error
}

type blockingLauncher struct {
	mu      sync.Mutex
	started []WorkerRequest
}

type trackingBlockingLauncher struct {
	process *blockingProcess
	pid     int
}

type contextProbeLauncher struct {
	ctx     context.Context
	process *blockingProcess
}

type testWorkerGovernor struct {
	slots chan struct{}
}

func newTestWorkerGovernor(capacity int) *testWorkerGovernor {
	return &testWorkerGovernor{slots: make(chan struct{}, capacity)}
}

func (g *testWorkerGovernor) AcquireWorker(ctx context.Context) (func(), error) {
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case g.slots <- struct{}{}:
		var once sync.Once
		return func() { once.Do(func() { <-g.slots }) }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (l *contextProbeLauncher) Start(ctx context.Context, req WorkerRequest) (Process, error) {
	l.ctx = ctx
	l.process = newBlockingProcess(req)
	return l.process, nil
}

func (l *trackingBlockingLauncher) Start(_ context.Context, req WorkerRequest) (Process, error) {
	l.process = newBlockingProcess(req)
	l.process.pid = l.pid
	return l.process, nil
}

func (l *blockingLauncher) Start(ctx context.Context, req WorkerRequest) (Process, error) {
	l.mu.Lock()
	l.started = append(l.started, req)
	l.mu.Unlock()
	return newBlockingProcess(req), nil
}

type blockingProcess struct {
	req       WorkerRequest
	pid       int
	once      sync.Once
	done      chan struct{}
	partial   WorkerResult
	stopCause error
}

func newBlockingProcess(req WorkerRequest) *blockingProcess {
	return &blockingProcess{req: req, pid: os.Getpid(), done: make(chan struct{})}
}

func (p *blockingProcess) PID() int {
	return p.pid
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
	return p.StopWithCause(context.Canceled)
}

func (p *blockingProcess) StopWithCause(cause error) error {
	p.stopCause = cause
	p.once.Do(func() { close(p.done) })
	return nil
}

func (p *blockingProcess) PartialResult() WorkerResult {
	return p.partial
}

type immediateLauncher struct {
	result func(WorkerRequest) WorkerResult
}

func (l immediateLauncher) Start(ctx context.Context, req WorkerRequest) (Process, error) {
	result := WorkerResult{
		TaskID:    req.TaskID,
		SessionID: req.SessionID,
		ExitCode:  0,
	}
	if l.result != nil {
		result = l.result(req)
	}
	return immediateProcess{result: result}, nil
}

type immediateProcess struct {
	result WorkerResult
}

func (p immediateProcess) PID() int {
	return 5150
}

func (p immediateProcess) Wait() (WorkerResult, error) {
	return p.result, nil
}

func (p immediateProcess) Stop() error {
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
	store, err := session.NewJSONLStore(filepath.Join(root, ".paw"))
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

// 等待目标全部不在注册表投影中时，WaitAny 立即返回（不存在的任务永远不会
// 进入终态），而不是挂满超时——worker/模型误等未知 id 时不再死锁。
func TestWaitAnyReturnsImmediatelyWhenAllTasksNotFound(t *testing.T) {
	manager, _, _ := newTestManager(t, &recordingModel{}, settings.DefaultConfig(), nil)
	start := time.Now()
	result, err := manager.WaitAny(context.Background(), []string{"missing-task"}, 10*time.Minute)
	if err != nil {
		t.Fatalf("WaitAny() error = %v", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("WaitAny blocked %s on unknown ids, want immediate return", elapsed)
	}
	if result.TimedOut {
		t.Fatalf("WaitAny() TimedOut = true, want false for all-not-found")
	}
	if len(result.Tasks) != 1 || !result.Tasks[0].NotFound {
		t.Fatalf("WaitAny() tasks = %#v, want not_found summary", result.Tasks)
	}
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

func TestBaseToolRegistryDoesNotContainUpdateTodo(t *testing.T) {
	registry := newBaseToolRegistry(t.TempDir())
	if _, ok := registry.Get("update_todo"); ok {
		t.Fatal("task base registry unexpectedly contains update_todo")
	}
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
	if strings.Contains(systemPrompt, "Task:") || strings.Contains(systemPrompt, "TaskStatus:") {
		t.Fatalf("system prompt should not expose recursive task tools: %q", systemPrompt)
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

func TestRunPersistsStructuredUsageFromInProcessWorker(t *testing.T) {
	usage := model.Usage{PromptTokens: 80, CompletionTokens: 6, PromptCacheHitTokens: 30}
	modelStreamer := &recordingModel{
		rounds: []fakeRound{
			{events: []model.StreamEvent{{Delta: "usage answer"}, {Usage: &usage}, {Done: true}}},
		},
	}
	manager, _, _ := newTestManager(t, modelStreamer, settings.DefaultConfig(), nil)

	result, err := manager.Run(context.Background(), Request{
		SessionID:   "run-usage-session",
		Prompt:      "run usage prompt",
		ContextMode: settings.ContextModeEmpty,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	task, ok := manager.Status(result.SessionID)
	if !ok {
		t.Fatalf("Status(%q) not found", result.SessionID)
	}
	if task.Usage == nil {
		t.Fatalf("completed task missing usage: %#v", task)
	}
	if task.Usage.Input != 50 || task.Usage.CacheRead != 30 || task.Usage.Output != 6 {
		t.Fatalf("task usage = %#v, want normalized in-process worker usage", task.Usage)
	}
	if task.UsedTokens != 86 {
		t.Fatalf("task UsedTokens = %d, want compatibility total 86", task.UsedTokens)
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

func TestStreamPersistsStructuredUsageFromModel(t *testing.T) {
	usage := model.Usage{PromptTokens: 100, CompletionTokens: 5, PromptCacheHitTokens: 40}
	modelStreamer := &recordingModel{
		rounds: []fakeRound{
			{events: []model.StreamEvent{{Delta: "usage answer"}, {Usage: &usage}, {Done: true}}},
		},
	}
	manager, _, _ := newTestManager(t, modelStreamer, settings.DefaultConfig(), nil)

	stream, err := manager.Stream(context.Background(), Request{
		Prompt:      "stream usage prompt",
		ContextMode: settings.ContextModeEmpty,
	})
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	drainTaskStream(t, stream.Events)
	completed := waitForTask(t, manager, stream.SessionID, TaskCompleted)

	if completed.Usage == nil {
		t.Fatalf("completed task missing usage: %#v", completed)
	}
	if completed.Usage.Input != 60 || completed.Usage.CacheRead != 40 || completed.Usage.Output != 5 {
		t.Fatalf("completed usage = %#v, want normalized model usage", completed.Usage)
	}
	if completed.UsedTokens != 105 {
		t.Fatalf("completed UsedTokens = %d, want 105 compatibility total", completed.UsedTokens)
	}
}

func TestStreamingUICapturesAndForwardsModelUsage(t *testing.T) {
	events := make(chan model.StreamEvent, 1)
	streamUI := &streamingUI{ctx: context.Background(), events: events}
	usage := model.Usage{PromptTokens: 100, CompletionTokens: 7, PromptCacheHitTokens: 40}

	streamUI.OnModelUsage(usage)

	got := streamUI.Usage()
	if got == nil {
		t.Fatal("Usage() = nil, want captured structured usage")
	}
	if got.Input != 60 || got.CacheRead != 40 || got.Output != 7 {
		t.Fatalf("Usage() = %#v, want normalized model usage", got)
	}
	select {
	case ev := <-events:
		if ev.Usage == nil || ev.Usage.PromptTokens != 100 || ev.Usage.CompletionTokens != 7 {
			t.Fatalf("forwarded event = %#v, want original model usage", ev)
		}
	default:
		t.Fatal("OnModelUsage did not forward usage event")
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
	drainTaskStream(t, first.Events)
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
	drainTaskStream(t, second.Events)
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
	if strings.Contains(system.Content, "input_schema=") {
		t.Fatalf("StreamMA system prompt should use compact tool descriptions, got: %q", system.Content)
	}
	if strings.Contains(history[0].Content, "streamma_agent_id=a") {
		t.Fatalf("StreamMA system prompt leaked into user history: %#v", history[0])
	}
}

func drainTaskStream(t *testing.T, events <-chan model.StreamEvent) {
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
	store, err := session.NewJSONLStore(filepath.Join(root, ".paw"))
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
		t.Fatalf("running taskController reused persona %q", first.Name)
	}

	if _, err := manager.Launch(context.Background(), Request{Prompt: "third"}); err == nil || !strings.Contains(err.Error(), "no idle task names") {
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
	if event.Title != "task" || !strings.Contains(event.Body, `<task id="`+task.ID+`"`) || !strings.Contains(event.Body, `state="completed"`) || !strings.Contains(event.Body, "summary: background answer") {
		t.Fatalf("system event = %#v", event)
	}
}

func TestWorkerResultUsagePersistsToTaskSnapshotAndTracerEvent(t *testing.T) {
	root := t.TempDir()
	store, err := session.NewJSONLStore(filepath.Join(root, ".paw"))
	if err != nil {
		t.Fatalf("NewJSONLStore() error = %v", err)
	}
	usage := tokentracer.Usage{Input: 12, CacheRead: 5, CacheCreation: 2, Output: 3}.Normalized()
	tracer := tokentracer.New("usage-test")
	manager := NewManager(Config{
		Store:    store,
		Root:     root,
		Settings: fakeSettingsProvider{cfg: settings.DefaultConfig()},
		Launcher: immediateLauncher{result: func(req WorkerRequest) WorkerResult {
			copied := usage
			return WorkerResult{
				TaskID:     req.TaskID,
				SessionID:  req.SessionID,
				Content:    "done with usage",
				ExitCode:   0,
				UsedTokens: 9999,
				Usage:      &copied,
			}
		}},
		Tracer: tracer,
	})

	if _, err := manager.Run(context.Background(), Request{
		SessionID:   "usage-session",
		Prompt:      "collect structured usage",
		ContextMode: settings.ContextModeEmpty,
	}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	task, ok := manager.Status("usage-session")
	if !ok {
		t.Fatal("Status(usage-session) not found")
	}
	if task.UsedTokens != 9999 {
		t.Fatalf("task UsedTokens = %d, want compatibility value 9999", task.UsedTokens)
	}
	if task.Usage == nil || task.Usage.Input != 12 || task.Usage.CacheRead != 5 || task.Usage.CacheCreation != 2 || task.Usage.Output != 3 {
		t.Fatalf("task Usage = %#v, want structured usage", task.Usage)
	}

	var end tokentracer.Event
	for _, event := range tracer.Snapshot().Events {
		if event.Type == "task_end" {
			end = event
		}
	}
	if end.Type == "" {
		t.Fatal("task_end event not recorded")
	}
	if got, ok := end.Data["used_tokens"].(int); !ok || got != 9999 {
		t.Fatalf("event used_tokens = %#v, want 9999", end.Data["used_tokens"])
	}
	gotUsage, ok := end.Data["usage"].(tokentracer.Usage)
	if !ok {
		t.Fatalf("event usage = %#v, want tokentracer.Usage", end.Data["usage"])
	}
	if gotUsage.Input != 12 || gotUsage.CacheRead != 5 || gotUsage.CacheCreation != 2 || gotUsage.Output != 3 {
		t.Fatalf("event usage = %#v, want structured usage", gotUsage)
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
		"<task id=\"" + task.ID + "\"",
		`state="completed"`,
		"summary: ",
		"[truncated; full result: " + completed.OutputPath + "]",
		"transcript: " + completed.TranscriptPath,
		"output: " + completed.OutputPath,
		"</task>",
	} {
		if !strings.Contains(update, want) {
			t.Fatalf("context update = %q, want %q", update, want)
		}
	}
	_, afterResult, ok := strings.Cut(update, "summary: ")
	if !ok {
		t.Fatalf("context update = %q, want summary section", update)
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

func TestTaskToolsProvideSyncAndStatusAccess(t *testing.T) {
	modelStreamer := &recordingModel{
		rounds: []fakeRound{
			{events: []model.StreamEvent{{Delta: "sync answer"}, {Done: true}}},
			{events: []model.StreamEvent{{Delta: "background answer"}, {Done: true}}},
		},
	}
	manager, _, _ := newTestManager(t, modelStreamer, settings.DefaultConfig(), nil)

	tool := NewTool(manager, "parent-session")
	if tool.Name() != "Task" {
		t.Fatalf("tool.Name() = %q, want Task", tool.Name())
	}
	if description := strings.ToLower(tool.Description()); !strings.Contains(description, "prefer context_mode empty") || !strings.Contains(description, "background work survives ordinary parent turn failures") {
		t.Fatalf("tool.Description() = %q", tool.Description())
	}
	schema := string(tool.InputSchema())
	for _, want := range []string{`"required":["prompt"]`, `"context_mode"`, `"run_mode"`, `"description"`} {
		if !strings.Contains(schema, want) {
			t.Fatalf("InputSchema() = %q, want %q", schema, want)
		}
	}

	if safeTool, ok := tool.(interface{ IsConcurrencySafe(json.RawMessage) bool }); !ok || !safeTool.IsConcurrencySafe(json.RawMessage(`{"prompt":"background prompt"}`)) || safeTool.IsConcurrencySafe(json.RawMessage(`{"prompt":"sync prompt","run_mode":"sync"}`)) {
		t.Fatalf("Task concurrency safety should be true for default background and false for explicit sync")
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
	// Without id, status lists only running tasks; the launched task already
	// completed, so the list must be empty and never carry content.
	if strings.Contains(listOut, launched.ID) || strings.Contains(listOut, "background answer") {
		t.Fatalf("status list output = %q, want running-only tasks without content", listOut)
	}

	singleOut, err := statusTool.Run(context.Background(), json.RawMessage(`{"id":"`+launched.ID+`"}`))
	if err != nil {
		t.Fatalf("status single Run() error = %v", err)
	}
	if !strings.Contains(singleOut, `"id": "`+launched.ID+`"`) || !strings.Contains(singleOut, `"status": "completed"`) {
		t.Fatalf("status single output = %q", singleOut)
	}
	if strings.Contains(singleOut, "background answer") || strings.Contains(singleOut, `"content"`) {
		t.Fatalf("status single output = %q, want no content field", singleOut)
	}
}

func TestWaitAnyReturnsWhenAnyTaskCompletes(t *testing.T) {
	modelStreamer := &recordingModel{
		rounds: []fakeRound{
			{events: []model.StreamEvent{{Delta: "first answer"}, {Done: true}}},
			{events: []model.StreamEvent{{Delta: "second answer"}, {Done: true}}},
			{events: []model.StreamEvent{{Delta: "third answer"}, {Done: true}}},
		},
	}
	manager, _, _ := newTestManager(t, modelStreamer, settings.DefaultConfig(), nil)

	first, err := manager.Launch(context.Background(), Request{Prompt: "first"})
	if err != nil {
		t.Fatalf("first Launch() error = %v", err)
	}
	second, err := manager.Launch(context.Background(), Request{Prompt: "second"})
	if err != nil {
		t.Fatalf("second Launch() error = %v", err)
	}
	third, err := manager.Launch(context.Background(), Request{Prompt: "third"})
	if err != nil {
		t.Fatalf("third Launch() error = %v", err)
	}

	start := time.Now()
	result, err := manager.WaitAny(context.Background(), []string{first.ID, second.ID, third.ID}, 2*time.Second)
	if err != nil {
		t.Fatalf("WaitAny() error = %v", err)
	}
	if result.TimedOut {
		t.Fatalf("WaitAny() timed out, want completion: %#v", result)
	}
	if !result.AnyTerminal {
		t.Fatalf("WaitAny() AnyTerminal = false, want true: %#v", result)
	}
	if time.Since(start) > 1500*time.Millisecond {
		t.Fatalf("WaitAny() blocked for %v, want return as soon as the fastest task completes", time.Since(start))
	}
	if len(result.Tasks) != 3 {
		t.Fatalf("WaitAny() tasks = %#v, want 3 summaries", result.Tasks)
	}
	terminal := 0
	for _, summary := range result.Tasks {
		if isTerminalStatus(summary.Status) {
			terminal++
		}
	}
	if terminal < 1 {
		t.Fatalf("WaitAny() terminal count = %d, want at least 1: %#v", terminal, result.Tasks)
	}
	// Multiple fast workers may finish between the first wakeup and snapshot.
	for _, id := range []string{first.ID, second.ID, third.ID} {
		waitForTask(t, manager, id, TaskCompleted)
	}
}

func TestWaitAnyAlreadyFinishedReturnsImmediately(t *testing.T) {
	modelStreamer := &recordingModel{
		rounds: []fakeRound{
			{events: []model.StreamEvent{{Delta: "done answer"}, {Done: true}}},
		},
	}
	manager, _, _ := newTestManager(t, modelStreamer, settings.DefaultConfig(), nil)

	task, err := manager.Launch(context.Background(), Request{Prompt: "already done"})
	if err != nil {
		t.Fatalf("Launch() error = %v", err)
	}
	waitForTask(t, manager, task.ID, TaskCompleted)

	start := time.Now()
	result, err := manager.WaitAny(context.Background(), []string{task.ID}, 10*time.Second)
	if err != nil {
		t.Fatalf("WaitAny() error = %v", err)
	}
	if result.TimedOut || !result.AnyTerminal {
		t.Fatalf("WaitAny() = %#v, want immediate completion", result)
	}
	if time.Since(start) > time.Second {
		t.Fatalf("WaitAny() on finished task blocked for %v", time.Since(start))
	}
	if len(result.Tasks) != 1 || result.Tasks[0].ID != task.ID || result.Tasks[0].Status != TaskCompleted {
		t.Fatalf("WaitAny() tasks = %#v", result.Tasks)
	}
}

func TestWaitAnyTimeoutReturnsSnapshot(t *testing.T) {
	root := t.TempDir()
	store, err := session.NewJSONLStore(filepath.Join(root, ".paw"))
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

	task, err := manager.Launch(context.Background(), Request{Prompt: "slow"})
	if err != nil {
		t.Fatalf("Launch() error = %v", err)
	}

	result, err := manager.WaitAny(context.Background(), []string{task.ID}, 30*time.Millisecond)
	if err != nil {
		t.Fatalf("WaitAny() error = %v", err)
	}
	if !result.TimedOut {
		t.Fatalf("WaitAny() = %#v, want timed_out=true", result)
	}
	if result.AnyTerminal {
		t.Fatalf("WaitAny() AnyTerminal = true on timeout, want false")
	}
	if len(result.Tasks) != 1 || result.Tasks[0].ID != task.ID || result.Tasks[0].Status != TaskRunning {
		t.Fatalf("WaitAny() timeout snapshot = %#v, want still-running summary", result.Tasks)
	}
	if _, err := manager.Stop(context.Background(), task.ID); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
}

func TestWaitAnyContextCancelReturnsError(t *testing.T) {
	root := t.TempDir()
	store, err := session.NewJSONLStore(filepath.Join(root, ".paw"))
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

	task, err := manager.Launch(context.Background(), Request{Prompt: "slow"})
	if err != nil {
		t.Fatalf("Launch() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(30 * time.Millisecond)
		cancel()
	}()
	if _, err := manager.WaitAny(ctx, []string{task.ID}, 10*time.Second); !errors.Is(err, context.Canceled) {
		t.Fatalf("WaitAny() error = %v, want context.Canceled", err)
	}
	if _, err := manager.Stop(context.Background(), task.ID); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
}

func TestWaitAnyUnknownIDsMarkedNotFound(t *testing.T) {
	modelStreamer := &recordingModel{
		rounds: []fakeRound{
			{events: []model.StreamEvent{{Delta: "real answer"}, {Done: true}}},
		},
	}
	manager, _, _ := newTestManager(t, modelStreamer, settings.DefaultConfig(), nil)

	real, err := manager.Launch(context.Background(), Request{Prompt: "real"})
	if err != nil {
		t.Fatalf("Launch() error = %v", err)
	}
	waitForTask(t, manager, real.ID, TaskCompleted)

	result, err := manager.WaitAny(context.Background(), []string{real.ID, "ghost-task-1", "ghost-task-2"}, time.Second)
	if err != nil {
		t.Fatalf("WaitAny() error = %v", err)
	}
	if result.TimedOut {
		t.Fatalf("WaitAny() = %#v, want completion despite unknown ids", result)
	}
	byID := make(map[string]TaskSummary, len(result.Tasks))
	for _, summary := range result.Tasks {
		byID[summary.ID] = summary
	}
	if byID[real.ID].Status != TaskCompleted {
		t.Fatalf("real task = %#v, want completed", byID[real.ID])
	}
	for _, ghost := range []string{"ghost-task-1", "ghost-task-2"} {
		if byID[ghost].Status != TaskNotFound || !byID[ghost].NotFound {
			t.Fatalf("unknown task %s = %#v, want not_found marker", ghost, byID[ghost])
		}
	}
}

func TestWaitToolRejectsEmptyTaskIDs(t *testing.T) {
	manager, _, _ := newTestManager(t, &recordingModel{}, settings.DefaultConfig(), nil)
	waitTool := NewWaitTool(manager)
	if _, err := waitTool.Run(context.Background(), json.RawMessage(`{"task_ids":[]}`)); err == nil {
		t.Fatal("Wait() with empty task_ids should fail")
	}
	if _, err := waitTool.Run(context.Background(), json.RawMessage(`{}`)); err == nil {
		t.Fatal("Wait() without task_ids should fail")
	}
}

func TestWaitToolAppliesDefaultTimeoutFromSettings(t *testing.T) {
	modelStreamer := &recordingModel{
		rounds: []fakeRound{
			{events: []model.StreamEvent{{Delta: "fast answer"}, {Done: true}}},
		},
	}
	cfg := settings.DefaultConfig()
	cfg.Task.WaitTimeoutMs = 2000
	manager, _, _ := newTestManager(t, modelStreamer, cfg, nil)
	waitTool := NewWaitTool(manager)

	task, err := manager.Launch(context.Background(), Request{Prompt: "fast"})
	if err != nil {
		t.Fatalf("Launch() error = %v", err)
	}
	out, err := waitTool.Run(context.Background(), json.RawMessage(`{"task_ids":["`+task.ID+`"]}`))
	if err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
	if !strings.Contains(out, `"timed_out": false`) || !strings.Contains(out, `"status": "completed"`) {
		t.Fatalf("Wait() output = %q", out)
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

func TestCompletedTaskRecoversFromActorJournalWithoutMetadata(t *testing.T) {
	root := t.TempDir()
	store, err := session.NewJSONLStore(filepath.Join(root, ".paw"))
	if err != nil {
		t.Fatalf("NewJSONLStore() error = %v", err)
	}
	newManager := func() *Manager {
		return NewManager(Config{
			Store:    store,
			Root:     root,
			Settings: fakeSettingsProvider{cfg: settings.DefaultConfig()},
			Launcher: immediateLauncher{},
		})
	}

	manager := newManager()
	result, err := manager.Run(context.Background(), Request{Prompt: "persist through actor journal"})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.SessionID == "" {
		t.Fatal("Run() returned an empty session id")
	}

	metaPath := filepath.Join(root, ".paw", tasksDirName, result.SessionID, "meta.json")
	if err := os.Remove(metaPath); err != nil {
		t.Fatalf("remove compatibility metadata: %v", err)
	}

	restarted := newManager()
	got, ok := restarted.Status(result.SessionID)
	if !ok {
		t.Fatalf("Status(%q) was not recovered from the actor journal", result.SessionID)
	}
	if got.Status != TaskCompleted || got.SessionID != result.SessionID || got.ExitCode == nil || *got.ExitCode != 0 {
		t.Fatalf("recovered task = %#v, want completed task %q", got, result.SessionID)
	}
}

func TestTaskLifecycleEventFlowGolden(t *testing.T) {
	root := t.TempDir()
	store, err := session.NewJSONLStore(filepath.Join(root, ".paw"))
	if err != nil {
		t.Fatalf("NewJSONLStore() error = %v", err)
	}
	manager := NewManager(Config{
		Store:    store,
		Root:     root,
		Settings: fakeSettingsProvider{cfg: settings.DefaultConfig()},
		Launcher: immediateLauncher{},
	})
	result, err := manager.Run(context.Background(), Request{Prompt: "golden lifecycle"})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	journal, err := es.NewJSONLStore(filepath.Join(root, ".paw", "actors"), taskActorType)
	if err != nil {
		t.Fatalf("NewJSONLStore(actor journal) error = %v", err)
	}
	events, _, err := journal.Load(context.Background(), result.SessionID)
	if err != nil {
		t.Fatalf("Load(actor journal) error = %v", err)
	}
	got := make([]string, 0, 3)
	for _, event := range events {
		if event.Kind == es.KindDomain {
			got = append(got, event.Type)
		}
	}
	want := []string{taskEventCreated, taskEventStarted, taskEventCompleted}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("task domain event flow = %v, want %v", got, want)
	}
}

func TestRunningTaskRecoversInterruptedFromActorJournalWithoutMetadata(t *testing.T) {
	root := t.TempDir()
	store, err := session.NewJSONLStore(filepath.Join(root, ".paw"))
	if err != nil {
		t.Fatalf("NewJSONLStore() error = %v", err)
	}
	launcher := &trackingBlockingLauncher{pid: 999999}
	manager := NewManager(Config{
		Store:    store,
		Root:     root,
		Settings: fakeSettingsProvider{cfg: settings.DefaultConfig()},
		Launcher: launcher,
	})
	task, err := manager.Launch(context.Background(), Request{Prompt: "recover crash"})
	if err != nil {
		t.Fatalf("Launch() error = %v", err)
	}
	t.Cleanup(func() { _ = launcher.process.Stop() })
	metaPath := filepath.Join(root, ".paw", tasksDirName, task.ID, "meta.json")
	if err := os.Remove(metaPath); err != nil {
		t.Fatalf("remove compatibility metadata: %v", err)
	}
	manager.actors.system.Stop()

	restarted := NewManager(Config{Store: store, Root: root, Settings: fakeSettingsProvider{cfg: settings.DefaultConfig()}})
	got, ok := restarted.Status(task.ID)
	if !ok || got.Status != TaskInterrupted || got.FinishedAt == nil || got.ExitCode == nil || *got.ExitCode != -1 {
		t.Fatalf("recovered task = %#v / %v, want interrupted crash recovery", got, ok)
	}
}

func TestListTasksRecoversActorRegistryWithoutMetadata(t *testing.T) {
	root := t.TempDir()
	store, err := session.NewJSONLStore(filepath.Join(root, ".paw"))
	if err != nil {
		t.Fatalf("NewJSONLStore() error = %v", err)
	}
	newManager := func() *Manager {
		return NewManager(Config{
			Store:    store,
			Root:     root,
			Settings: fakeSettingsProvider{cfg: settings.DefaultConfig()},
			Launcher: immediateLauncher{},
		})
	}

	manager := newManager()
	result, err := manager.Run(context.Background(), Request{Prompt: "index through registry actor"})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	metaPath := filepath.Join(root, ".paw", tasksDirName, result.SessionID, "meta.json")
	if err := os.Remove(metaPath); err != nil {
		t.Fatalf("remove compatibility metadata: %v", err)
	}

	tasks := newManager().ListTasks()
	if len(tasks) != 1 {
		t.Fatalf("ListTasks() = %#v, want one actor-indexed task", tasks)
	}
	if tasks[0].ID != result.SessionID || tasks[0].Status != TaskCompleted {
		t.Fatalf("ListTasks()[0] = %#v, want completed task %q", tasks[0], result.SessionID)
	}
}

func TestTotalTaskTokensRecoversActorProjectionWithoutMetadata(t *testing.T) {
	root := t.TempDir()
	store, err := session.NewJSONLStore(filepath.Join(root, ".paw"))
	if err != nil {
		t.Fatalf("NewJSONLStore() error = %v", err)
	}
	manager := NewManager(Config{
		Store:    store,
		Root:     root,
		Settings: fakeSettingsProvider{cfg: settings.DefaultConfig()},
		Launcher: immediateLauncher{result: func(req WorkerRequest) WorkerResult {
			return WorkerResult{TaskID: req.TaskID, SessionID: req.SessionID, ExitCode: 0, UsedTokens: 77}
		}},
	})
	result, err := manager.Run(context.Background(), Request{Prompt: "project tokens", ParentSessionID: "parent"})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	metaPath := filepath.Join(root, ".paw", tasksDirName, result.SessionID, "meta.json")
	if err := os.Remove(metaPath); err != nil {
		t.Fatalf("remove compatibility metadata: %v", err)
	}

	restarted := NewManager(Config{Store: store, Root: root, Settings: fakeSettingsProvider{cfg: settings.DefaultConfig()}})
	if got := restarted.TotalTaskTokens("parent"); got != 77 {
		t.Fatalf("TotalTaskTokens(parent) = %d, want 77 from actor projection", got)
	}
}

func TestRunningTaskIsIndexedBeforeWorkerCompletion(t *testing.T) {
	root := t.TempDir()
	store, err := session.NewJSONLStore(filepath.Join(root, ".paw"))
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
	task, err := manager.Launch(context.Background(), Request{Prompt: "index while running"})
	if err != nil {
		t.Fatalf("Launch() error = %v", err)
	}
	t.Cleanup(func() { _, _ = manager.Stop(context.Background(), task.ID) })

	metaPath := filepath.Join(root, ".paw", tasksDirName, task.ID, "meta.json")
	if err := os.Remove(metaPath); err != nil {
		t.Fatalf("remove compatibility metadata: %v", err)
	}
	restarted := NewManager(Config{Store: store, Root: root, Settings: fakeSettingsProvider{cfg: settings.DefaultConfig()}})
	tasks := restarted.ListTasks()
	if len(tasks) != 1 || tasks[0].ID != task.ID || tasks[0].Status != TaskRunning || tasks[0].PID != os.Getpid() {
		t.Fatalf("ListTasks() = %#v, want running actor-indexed task %#v", tasks, task)
	}
}

func TestStopUsesTaskActorWhenCompatibilityMetadataIsMissing(t *testing.T) {
	root := t.TempDir()
	store, err := session.NewJSONLStore(filepath.Join(root, ".paw"))
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
	task, err := manager.Launch(context.Background(), Request{Prompt: "stop through task actor"})
	if err != nil {
		t.Fatalf("Launch() error = %v", err)
	}
	t.Cleanup(func() { _, _ = manager.Stop(context.Background(), task.ID) })
	metaPath := filepath.Join(root, ".paw", tasksDirName, task.ID, "meta.json")
	if err := os.Remove(metaPath); err != nil {
		t.Fatalf("remove compatibility metadata: %v", err)
	}

	restarted := NewManager(Config{Store: store, Root: root, Settings: fakeSettingsProvider{cfg: settings.DefaultConfig()}})
	stopped, err := restarted.Stop(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if stopped.Status != TaskStopped || stopped.ExitCode == nil || *stopped.ExitCode != -1 {
		t.Fatalf("Stop() = %#v, want stopped actor task", stopped)
	}
	got, ok := restarted.Status(task.ID)
	if !ok || got.Status != TaskStopped {
		t.Fatalf("Status(%q) = (%#v, %v), want stopped", task.ID, got, ok)
	}
	data, err := os.ReadFile(metaPath)
	if err != nil {
		t.Fatalf("read recreated compatibility metadata: %v", err)
	}
	if !strings.Contains(string(data), `"status": "stopped"`) {
		t.Fatalf("compatibility metadata = %s, want stopped status", data)
	}
}

func TestStopStopsRunningWorkerAndPersistsStoppedStatus(t *testing.T) {
	root := t.TempDir()
	store, err := session.NewJSONLStore(filepath.Join(root, ".paw"))
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
	if task.PID != os.Getpid() || task.OutputPath == "" || task.Depth != 1 {
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

func TestStopPreservesPartialWorkerResultAndCause(t *testing.T) {
	root := t.TempDir()
	store, err := session.NewJSONLStore(filepath.Join(root, ".paw"))
	if err != nil {
		t.Fatal(err)
	}
	launcher := &trackingBlockingLauncher{pid: os.Getpid()}
	manager := NewManager(Config{Store: store, Root: root, Settings: fakeSettingsProvider{cfg: settings.DefaultConfig()}, Launcher: launcher})
	task, err := manager.Launch(context.Background(), Request{Prompt: "partial"})
	if err != nil {
		t.Fatal(err)
	}
	usage := tokentracer.Usage{Input: 30, Output: 7}.Normalized()
	launcher.process.partial = WorkerResult{
		TaskID: task.ID, SessionID: task.SessionID, Content: "partial answer", Usage: &usage,
	}

	stopped, err := manager.Stop(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stopped.Content != "partial answer" || stopped.UsedTokens != 37 || stopped.Usage == nil {
		t.Fatalf("stopped task = %#v", stopped)
	}
	if launcher.process.stopCause == nil || launcher.process.stopCause.Error() != "stopped" {
		t.Fatalf("stop cause = %v", launcher.process.stopCause)
	}
	data, err := os.ReadFile(stopped.OutputPath)
	if err != nil {
		t.Fatal(err)
	}
	var output WorkerResult
	if err := json.Unmarshal(data, &output); err != nil {
		t.Fatal(err)
	}
	if output.Content != "partial answer" || output.UsedTokens != 37 || output.Usage == nil || output.Error != "stopped" {
		t.Fatalf("saved output = %#v", output)
	}
}

func TestStopOwnedTasksOnlyInterruptsExactParentTurn(t *testing.T) {
	root := t.TempDir()
	store, err := session.NewJSONLStore(filepath.Join(root, ".paw"))
	if err != nil {
		t.Fatal(err)
	}
	manager := NewManager(Config{Store: store, Root: root, Settings: fakeSettingsProvider{cfg: settings.DefaultConfig()}, Launcher: &blockingLauncher{}})

	a1, err := manager.Launch(context.Background(), Request{ParentSessionID: "parent", ParentTurnID: "turn-a", Prompt: "a1"})
	if err != nil {
		t.Fatal(err)
	}
	a2, err := manager.Launch(context.Background(), Request{ParentSessionID: "parent", ParentTurnID: "turn-a", Prompt: "a2"})
	if err != nil {
		t.Fatal(err)
	}
	b1, err := manager.Launch(context.Background(), Request{ParentSessionID: "parent", ParentTurnID: "turn-b", Prompt: "b1"})
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := manager.Launch(context.Background(), Request{ParentSessionID: "parent", Prompt: "legacy"})
	if err != nil {
		t.Fatal(err)
	}

	manager.StopOwnedTasks(context.Background(), "parent", "turn-a", "interrupted: parent failed")
	for _, id := range []string{a1.ID, a2.ID} {
		task, ok := manager.Status(id)
		if !ok || task.Status != TaskInterrupted || !strings.Contains(task.Error, "parent failed") {
			t.Fatalf("owned task %s = %#v/%v", id, task, ok)
		}
	}
	for _, id := range []string{b1.ID, legacy.ID} {
		task, ok := manager.Status(id)
		if !ok || task.Status != TaskRunning {
			t.Fatalf("unowned task %s = %#v/%v, want running", id, task, ok)
		}
	}
	if len(manager.RunningTasks()) != 2 {
		t.Fatalf("RunningTasks() = %#v, want turn-b and legacy only", manager.RunningTasks())
	}
	// Repeated cleanup is idempotent and must not change another owner's tasks.
	manager.StopOwnedTasks(context.Background(), "parent", "turn-a", "second cleanup")
	if task, _ := manager.Status(a1.ID); task.Error != "interrupted: parent failed" {
		t.Fatalf("repeated cleanup rewrote terminal task: %#v", task)
	}
	_, _ = manager.Stop(context.Background(), b1.ID)
	_, _ = manager.Stop(context.Background(), legacy.ID)
}

func TestStopOwnedTasksUsesActorOwnershipAfterRestartWithoutMetadata(t *testing.T) {
	root := t.TempDir()
	store, err := session.NewJSONLStore(filepath.Join(root, ".paw"))
	if err != nil {
		t.Fatalf("NewJSONLStore() error = %v", err)
	}
	manager := NewManager(Config{
		Store:    store,
		Root:     root,
		Settings: fakeSettingsProvider{cfg: settings.DefaultConfig()},
		Launcher: &blockingLauncher{},
	})
	launch := func(turn, prompt string) TaskSnapshot {
		t.Helper()
		task, launchErr := manager.Launch(context.Background(), Request{
			Prompt:          prompt,
			ParentSessionID: "parent",
			ParentTurnID:    turn,
		})
		if launchErr != nil {
			t.Fatalf("Launch(%q) error = %v", prompt, launchErr)
		}
		return task
	}
	a1 := launch("turn-a", "a1")
	a2 := launch("turn-a", "a2")
	b1 := launch("turn-b", "b1")
	t.Cleanup(func() {
		manager.StopOwnedTasks(context.Background(), "parent", "turn-a", "cleanup")
		manager.StopOwnedTasks(context.Background(), "parent", "turn-b", "cleanup")
	})
	for _, task := range []TaskSnapshot{a1, a2, b1} {
		metaPath := filepath.Join(root, ".paw", tasksDirName, task.ID, "meta.json")
		if err := os.Remove(metaPath); err != nil {
			t.Fatalf("remove metadata for %s: %v", task.ID, err)
		}
	}

	restarted := NewManager(Config{Store: store, Root: root, Settings: fakeSettingsProvider{cfg: settings.DefaultConfig()}})
	restarted.StopOwnedTasks(context.Background(), "parent", "turn-a", "parent failed")
	for _, task := range []TaskSnapshot{a1, a2} {
		got, ok := restarted.Status(task.ID)
		if !ok || got.Status != TaskInterrupted || got.Error != "parent failed" {
			t.Fatalf("Status(%s) = (%#v, %v), want interrupted actor task", task.ID, got, ok)
		}
	}
	got, ok := restarted.Status(b1.ID)
	if !ok || got.Status != TaskRunning {
		t.Fatalf("Status(%s) = (%#v, %v), want untouched running task", b1.ID, got, ok)
	}
}

func TestManagerGovernorBlocksSecondWorkerUntilFirstReleases(t *testing.T) {
	root := t.TempDir()
	store, err := session.NewJSONLStore(filepath.Join(root, ".paw"))
	if err != nil {
		t.Fatal(err)
	}
	governor := newTestWorkerGovernor(1)
	launcher := &blockingLauncher{}
	manager := NewManager(Config{
		Store: store, Root: root, Settings: fakeSettingsProvider{cfg: settings.DefaultConfig()},
		Launcher: launcher, Governor: governor,
	})
	first, err := manager.Launch(context.Background(), Request{Prompt: "first"})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := manager.Run(ctx, Request{Prompt: "second"}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("second Run() error = %v, want governor context deadline", err)
	}
	launcher.mu.Lock()
	started := len(launcher.started)
	launcher.mu.Unlock()
	if started != 1 {
		t.Fatalf("launcher starts = %d, want only first worker", started)
	}

	if _, err := manager.Stop(context.Background(), first.ID); err != nil {
		t.Fatal(err)
	}
	third, err := manager.Launch(context.Background(), Request{Prompt: "third"})
	if err != nil {
		t.Fatalf("third Launch() error = %v", err)
	}
	_, _ = manager.Stop(context.Background(), third.ID)
}

func TestLaunchDetachesBackgroundWorkerFromParentCancellation(t *testing.T) {
	root := t.TempDir()
	store, err := session.NewJSONLStore(filepath.Join(root, ".paw"))
	if err != nil {
		t.Fatal(err)
	}
	launcher := &contextProbeLauncher{}
	manager := NewManager(Config{Store: store, Root: root, Settings: fakeSettingsProvider{cfg: settings.DefaultConfig()}, Launcher: launcher})
	ctx, cancel := context.WithCancel(context.Background())

	task, err := manager.Launch(ctx, Request{Prompt: "detached"})
	if err != nil {
		t.Fatalf("Launch() error = %v", err)
	}
	cancel()
	if launcher.ctx == nil || launcher.ctx.Err() != nil {
		t.Fatalf("background worker context after parent cancel = %v, want live detached context", launcher.ctx)
	}
	if got, ok := manager.Status(task.ID); !ok || got.Status != TaskRunning {
		t.Fatalf("Status(%s) = (%#v, %v), want running", task.ID, got, ok)
	}
	_, _ = manager.Stop(context.Background(), task.ID)
}

func TestTaskToolPropagatesTurnOwnerFromContext(t *testing.T) {
	root := t.TempDir()
	store, err := session.NewJSONLStore(filepath.Join(root, ".paw"))
	if err != nil {
		t.Fatal(err)
	}
	manager := NewManager(Config{Store: store, Root: root, Settings: fakeSettingsProvider{cfg: settings.DefaultConfig()}, Launcher: &blockingLauncher{}})
	tool := NewTool(manager, "fallback-session")
	ctx := loop.WithTurnOwner(context.Background(), "parent-session", "turn-42")
	out, err := tool.Run(ctx, json.RawMessage(`{"prompt":"owned"}`))
	if err != nil {
		t.Fatal(err)
	}
	var task TaskSnapshot
	if err := json.Unmarshal([]byte(out), &task); err != nil {
		t.Fatal(err)
	}
	if task.ParentSessionID != "parent-session" || task.ParentTurnID != "turn-42" {
		t.Fatalf("task owner = %q/%q", task.ParentSessionID, task.ParentTurnID)
	}
	_, _ = manager.Stop(context.Background(), task.ID)
}

type roleAwareLauncher struct {
	result func(WorkerRequest) WorkerResult
}

func (l roleAwareLauncher) Start(ctx context.Context, req WorkerRequest) (Process, error) {
	result := WorkerResult{
		TaskID:    req.TaskID,
		SessionID: req.SessionID,
		ExitCode:  0,
	}
	if l.result != nil {
		result = l.result(req)
	}
	return roleAwareProcess{result: result}, nil
}

type roleAwareProcess struct {
	result WorkerResult
}

func (p roleAwareProcess) PID() int                     { return 6161 }
func (p roleAwareProcess) Wait() (WorkerResult, error)  { return p.result, nil }
func (p roleAwareProcess) Stop() error                  { return nil }
func (p roleAwareProcess) WorkerRole() (string, string) { return "高松灯", "#CC0033" }

// TestWorkerRoleOverridesTaskName 验证具名池 worker 的角色覆盖任务名/色
// （任务剥离 persona；无具名 worker 的路径仍走 assignPersona 兜底）。
func TestWorkerRoleOverridesTaskName(t *testing.T) {
	modelStreamer := &recordingModel{
		rounds: []fakeRound{
			{events: []model.StreamEvent{{Delta: "worker answer"}, {Done: true}}},
		},
	}
	manager, _, _ := newTestManager(t, modelStreamer, settings.DefaultConfig(), nil)
	manager.launcher = roleAwareLauncher{}

	result, err := manager.Run(context.Background(), Request{Prompt: "worker role"})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.AgentName != "高松灯" || result.AgentColor != "#CC0033" {
		t.Fatalf("AgentName/AgentColor = %q/%q, want worker role 高松灯/#CC0033", result.AgentName, result.AgentColor)
	}
}

func TestProcessLauncherParsesWorkerJSON(t *testing.T) {
	launcher := &ProcessLauncher{
		Command: os.Args[0],
		Args:    []string{"-test.run=TestTaskWorkerHelperProcess"},
		Env:     []string{"PAW_task_HELPER=1"},
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

func TestTaskWorkerHelperProcess(t *testing.T) {
	if os.Getenv("PAW_task_HELPER") != "1" {
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

func TestProcessLauncherBrokeredWorkerProtocol(t *testing.T) {
	broker := &testMCPBroker{snapshot: coremcp.Snapshot{Tools: []coremcp.ToolSpec{{Name: "codegraph__explore"}}}}
	launcher := &ProcessLauncher{
		Command: os.Args[0],
		Args:    []string{"-test.run=TestTaskFramedWorkerHelperProcess"},
		Env:     []string{"PAW_task_FRAMED_HELPER=1"},
		Broker:  broker,
	}
	proc, err := launcher.Start(context.Background(), WorkerRequest{TaskID: "task-2", SessionID: "session-2"})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	updater, ok := proc.(interface{ UpdateMCPSnapshot(coremcp.Snapshot) error })
	if !ok {
		t.Fatal("brokered process does not support snapshot updates")
	}
	if err := updater.UpdateMCPSnapshot(coremcp.Snapshot{Version: 2}); err != nil {
		t.Fatalf("UpdateMCPSnapshot() error = %v", err)
	}
	result, err := proc.Wait()
	if err != nil {
		t.Fatalf("Wait() error = %v result=%#v", err, result)
	}
	if result.Content != "parent:codegraph__explore" || broker.name != "codegraph__explore" {
		t.Fatalf("result=%#v broker=%#v", result, broker)
	}
}

type testMCPBroker struct {
	snapshot coremcp.Snapshot
	name     string
}

func (b *testMCPBroker) Snapshot() coremcp.Snapshot { return b.snapshot.Clone() }
func (b *testMCPBroker) Call(_ context.Context, name string, _ json.RawMessage) (string, error) {
	b.name = name
	return "parent:" + name, nil
}

func TestTaskFramedWorkerHelperProcess(t *testing.T) {
	if os.Getenv("PAW_task_FRAMED_HELPER") != "1" {
		return
	}
	decoder := json.NewDecoder(os.Stdin)
	encoder := json.NewEncoder(os.Stdout)
	var start WorkerMessage
	if err := decoder.Decode(&start); err != nil {
		os.Exit(2)
	}
	if start.Type != WorkerMessageStart {
		os.Exit(3)
	}
	_ = encoder.Encode(WorkerMessage{Type: WorkerMessageMCPCall, RequestID: "call-1", Tool: "codegraph__explore", Input: json.RawMessage(`{"query":"main"}`)})
	var result WorkerMessage
	gotResult := false
	gotSnapshot := false
	for !gotResult || !gotSnapshot {
		var message WorkerMessage
		if err := decoder.Decode(&message); err != nil {
			os.Exit(4)
		}
		switch message.Type {
		case WorkerMessageMCPResult:
			result = message
			gotResult = true
		case WorkerMessageSnapshot:
			if message.Snapshot.Version != 2 {
				os.Exit(5)
			}
			gotSnapshot = true
		}
	}
	_ = encoder.Encode(NewWorkerResultMessage(WorkerResult{
		TaskID:    start.TaskID,
		SessionID: start.SessionID,
		Content:   result.Content,
		ExitCode:  0,
	}))
	os.Exit(0)
}

func newStoreWithManager(t *testing.T, root string) (*Manager, *session.JSONLStore) {
	t.Helper()
	store, err := session.NewJSONLStore(filepath.Join(root, ".paw"))
	if err != nil {
		t.Fatalf("NewJSONLStore() error = %v", err)
	}
	manager := NewManager(Config{
		Store:    store,
		Root:     root,
		Settings: fakeSettingsProvider{cfg: settings.DefaultConfig()},
	})
	return manager, store
}

func TestActiveTasksTracksHostedProcessLifecycle(t *testing.T) {
	root := t.TempDir()
	store, err := session.NewJSONLStore(filepath.Join(root, ".paw"))
	if err != nil {
		t.Fatalf("NewJSONLStore() error = %v", err)
	}
	manager := NewManager(Config{
		Store:    store,
		Root:     root,
		Settings: fakeSettingsProvider{cfg: settings.DefaultConfig()},
		Launcher: &blockingLauncher{},
	})
	defer manager.Close()

	launched, err := manager.Launch(context.Background(), Request{Prompt: "active task"})
	if err != nil {
		t.Fatalf("Launch() error = %v", err)
	}
	if active := manager.ActiveTasks(); len(active) != 1 || active[0].ID != launched.ID {
		t.Fatalf("ActiveTasks() = %#v, want launched task", active)
	}
	if _, err := manager.Stop(context.Background(), launched.ID); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if active := manager.ActiveTasks(); len(active) != 0 {
		t.Fatalf("ActiveTasks() after stop = %#v, want empty", active)
	}
}

func TestActiveTasksExcludesRunningProjectionWithoutHostedProcess(t *testing.T) {
	root := t.TempDir()
	store, err := session.NewJSONLStore(filepath.Join(root, ".paw"))
	if err != nil {
		t.Fatalf("NewJSONLStore() error = %v", err)
	}
	manager := NewManager(Config{
		Store:    store,
		Root:     root,
		Settings: fakeSettingsProvider{cfg: settings.DefaultConfig()},
	})
	defer manager.Close()

	stale := TaskSnapshot{
		ID:        "stale-running",
		SessionID: "stale-running",
		Status:    TaskRunning,
		StartedAt: time.Now().UTC(),
	}
	if err := manager.actors.record(context.Background(), taskEventStarted, stale); err != nil {
		t.Fatalf("record stale running projection: %v", err)
	}
	if tasks := manager.ListTasks(); len(tasks) != 1 || tasks[0].Status != TaskRunning {
		t.Fatalf("ListTasks() = %#v, want stale running projection", tasks)
	}
	if active := manager.ActiveTasks(); len(active) != 0 {
		t.Fatalf("ActiveTasks() = %#v, want no task without a hosted process", active)
	}
}

func TestWorkerManagerCanSkipStartupOrphanReconciliation(t *testing.T) {
	root := t.TempDir()
	registry := newTaskRegistry(root)
	running := TaskSnapshot{
		ID:        "parent-task",
		SessionID: "parent-task",
		Status:    TaskRunning,
		PID:       999999,
		StartedAt: time.Now().UTC(),
	}
	if err := registry.saveTask(context.Background(), running); err != nil {
		t.Fatalf("saveTask(running) error = %v", err)
	}
	store, err := session.NewJSONLStore(filepath.Join(root, ".paw"))
	if err != nil {
		t.Fatalf("NewJSONLStore() error = %v", err)
	}

	worker := NewManager(Config{
		Store:                              store,
		Root:                               root,
		Settings:                           fakeSettingsProvider{cfg: settings.DefaultConfig()},
		DisableStartupOrphanReconciliation: true,
	})
	defer worker.Close()

	got, ok := worker.Status(running.ID)
	if !ok || got.Status != TaskRunning {
		t.Fatalf("worker manager task = %#v / %v, want untouched running task", got, ok)
	}
}

func TestReconcileOrphansMarksDeadPIDInterrupted(t *testing.T) {
	root := t.TempDir()
	registry := newTaskRegistry(root)
	now := time.Now().UTC()

	orphan := TaskSnapshot{
		ID:        "orphan-1",
		SessionID: "orphan-1",
		Status:    TaskRunning,
		PID:       999999, // 几乎不可能存在的 pid
		StartedAt: now,
	}
	if err := registry.saveTask(context.Background(), orphan); err != nil {
		t.Fatalf("saveTask(orphan) error = %v", err)
	}
	finished := TaskSnapshot{
		ID:         "done-1",
		SessionID:  "done-1",
		Status:     TaskCompleted,
		PID:        999998,
		StartedAt:  now.Add(-time.Minute),
		FinishedAt: &now,
	}
	if err := registry.saveTask(context.Background(), finished); err != nil {
		t.Fatalf("saveTask(finished) error = %v", err)
	}

	manager, _ := newStoreWithManager(t, root)

	task, ok := manager.Status("orphan-1")
	if !ok {
		t.Fatalf("Status(orphan-1) = false, want true")
	}
	if task.Status != TaskInterrupted {
		t.Fatalf("orphan status = %s, want interrupted", task.Status)
	}
	if task.FinishedAt == nil || task.Error == "" {
		t.Fatalf("orphan task = %#v, want finished_at and error set", task)
	}
	if !isTerminalStatus(task.Status) {
		t.Fatalf("interrupted must be a terminal status")
	}

	// 终态任务不受孤儿回收影响。
	if task, ok := manager.Status("done-1"); !ok || task.Status != TaskCompleted {
		t.Fatalf("done-1 = %#v / %v, want completed", task, ok)
	}

	// RunningTasks 不再包含孤儿。
	for _, task := range manager.RunningTasks() {
		if task.ID == "orphan-1" {
			t.Fatalf("RunningTasks() includes orphan %#v", task)
		}
	}
}

func seedStaleRunningRegistryProjection(t *testing.T, manager *Manager, running TaskSnapshot) {
	t.Helper()
	if err := manager.actors.record(context.Background(), taskEventStarted, running); err != nil {
		t.Fatalf("record running task: %v", err)
	}
	if _, changed, err := manager.actors.stop(context.Background(), running.ID, TaskInterrupted, "worker exited"); err != nil || !changed {
		t.Fatalf("interrupt task = (%v, %v), want changed terminal task", changed, err)
	}
	if err := manager.actors.system.Tell(context.Background(), taskRegistryActorID, actor.Msg{
		Kind:       taskRegistryUpsert,
		Payload:    taskRegistryUpdate{Task: running},
		Durability: actor.Durable,
	}); err != nil {
		t.Fatalf("seed stale registry projection: %v", err)
	}
	manager.actors.system.Drain()
}

func TestReconcileOrphansRepairsStaleRunningRegistryProjection(t *testing.T) {
	root := t.TempDir()
	store, err := session.NewJSONLStore(filepath.Join(root, ".paw"))
	if err != nil {
		t.Fatalf("NewJSONLStore() error = %v", err)
	}
	manager := NewManager(Config{
		Store:    store,
		Root:     root,
		Settings: fakeSettingsProvider{cfg: settings.DefaultConfig()},
	})
	defer manager.Close()

	running := TaskSnapshot{
		ID:        "stale-registry",
		SessionID: "stale-registry",
		Status:    TaskRunning,
		PID:       999999,
		StartedAt: time.Now().UTC(),
	}
	seedStaleRunningRegistryProjection(t, manager, running)
	if tasks := manager.ListTasks(); len(tasks) != 1 || tasks[0].Status != TaskRunning {
		t.Fatalf("ListTasks() before reconcile = %#v, want stale running projection", tasks)
	}

	manager.reconcileOrphanedTasks(context.Background())

	tasks := manager.ListTasks()
	if len(tasks) != 1 || tasks[0].Status != TaskInterrupted || tasks[0].Error != "worker exited" {
		t.Fatalf("ListTasks() after reconcile = %#v, want authoritative interrupted task", tasks)
	}
}

func TestWaitAnyRepairsStaleRunningRegistryProjection(t *testing.T) {
	root := t.TempDir()
	store, err := session.NewJSONLStore(filepath.Join(root, ".paw"))
	if err != nil {
		t.Fatalf("NewJSONLStore() error = %v", err)
	}
	manager := NewManager(Config{
		Store:    store,
		Root:     root,
		Settings: fakeSettingsProvider{cfg: settings.DefaultConfig()},
	})
	defer manager.Close()

	running := TaskSnapshot{
		ID:        "stale-wait",
		SessionID: "stale-wait",
		Status:    TaskRunning,
		PID:       999999,
		StartedAt: time.Now().UTC(),
	}
	seedStaleRunningRegistryProjection(t, manager, running)

	unrelated := TaskSnapshot{
		ID:        "unrelated-orphan",
		SessionID: "unrelated-orphan",
		Status:    TaskRunning,
		PID:       999998,
		StartedAt: time.Now().UTC(),
	}
	if err := manager.actors.record(context.Background(), taskEventStarted, unrelated); err != nil {
		t.Fatalf("record unrelated task: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	result, err := manager.WaitAny(ctx, []string{running.ID}, time.Hour)
	if err != nil {
		t.Fatalf("WaitAny() error = %v", err)
	}
	if result.TimedOut || len(result.Tasks) != 1 || result.Tasks[0].Status != TaskInterrupted {
		t.Fatalf("WaitAny() = %#v, want immediate authoritative interrupted task", result)
	}
	if got, ok := manager.Status(unrelated.ID); !ok || got.Status != TaskRunning {
		t.Fatalf("unrelated task = %#v / %v, want WaitAny to leave it untouched", got, ok)
	}
}

func TestReconcileOrphansKeepsLivePIDRunning(t *testing.T) {
	root := t.TempDir()
	registry := newTaskRegistry(root)
	live := TaskSnapshot{
		ID:        "live-1",
		SessionID: "live-1",
		Status:    TaskRunning,
		PID:       os.Getpid(), // 本进程存活 → 可能是其他实例正在运行的任务
		StartedAt: time.Now().UTC(),
	}
	if err := registry.saveTask(context.Background(), live); err != nil {
		t.Fatalf("saveTask(live) error = %v", err)
	}

	manager, _ := newStoreWithManager(t, root)

	task, ok := manager.Status("live-1")
	if !ok {
		t.Fatalf("Status(live-1) = false, want true")
	}
	if task.Status != TaskRunning {
		t.Fatalf("live task status = %s, want running (must not be reaped)", task.Status)
	}
}

func TestReconcileOrphansKeepsTaskOwnedByLiveManagerWhenWorkerPIDIsPending(t *testing.T) {
	root := t.TempDir()
	store, err := session.NewJSONLStore(filepath.Join(root, ".paw"))
	if err != nil {
		t.Fatalf("NewJSONLStore() error = %v", err)
	}
	launcher := &trackingBlockingLauncher{}
	owner := NewManager(Config{
		Store:    store,
		Root:     root,
		Settings: fakeSettingsProvider{cfg: settings.DefaultConfig()},
		Launcher: launcher,
	})
	t.Cleanup(func() {
		if launcher.process != nil {
			_ = launcher.process.Stop()
		}
		_ = owner.Close()
	})

	started, err := owner.Launch(context.Background(), Request{Prompt: "queued task"})
	if err != nil {
		t.Fatalf("Launch() error = %v", err)
	}
	if started.PID <= 0 {
		t.Fatalf("started PID = %d, want live owner fallback while worker PID is pending", started.PID)
	}

	observer := NewManager(Config{
		Store:    store,
		Root:     root,
		Settings: fakeSettingsProvider{cfg: settings.DefaultConfig()},
	})
	defer observer.Close()
	got, ok := observer.Status(started.ID)
	if !ok || got.Status != TaskRunning {
		t.Fatalf("observer task = %#v / %v, want running task owned by live manager", got, ok)
	}
}

func TestReconcileOrphansKeepsQueuedProcessPoolTask(t *testing.T) {
	root := t.TempDir()
	store, err := session.NewJSONLStore(filepath.Join(root, ".paw"))
	if err != nil {
		t.Fatalf("NewJSONLStore() error = %v", err)
	}
	launcher := &ProcessPoolLauncher{
		Command:       os.Args[0],
		Args:          []string{"-test.run=TestTaskPoolBlockingHelperProcess"},
		Env:           []string{"PAW_task_POOL_BLOCKING_HELPER=1"},
		MaxWorkers:    1,
		QueueCapacity: 2,
	}
	owner := NewManager(Config{
		Store:    store,
		Root:     root,
		Settings: fakeSettingsProvider{cfg: settings.DefaultConfig()},
		Launcher: launcher,
	})
	t.Cleanup(func() {
		_ = owner.Close()
		_ = launcher.Close()
	})

	first, err := owner.Launch(context.Background(), Request{Prompt: "occupy worker"})
	if err != nil {
		t.Fatalf("first Launch() error = %v", err)
	}
	second, err := owner.Launch(context.Background(), Request{Prompt: "queued task"})
	if err != nil {
		t.Fatalf("second Launch() error = %v", err)
	}
	t.Cleanup(func() {
		_, _ = owner.Stop(context.Background(), first.ID)
		_, _ = owner.Stop(context.Background(), second.ID)
	})
	if second.PID <= 0 {
		t.Fatalf("queued task PID = %d, want live owner fallback", second.PID)
	}

	observer := NewManager(Config{
		Store:    store,
		Root:     root,
		Settings: fakeSettingsProvider{cfg: settings.DefaultConfig()},
	})
	defer observer.Close()
	got, ok := observer.Status(second.ID)
	if !ok || got.Status != TaskRunning {
		t.Fatalf("observer queued task = %#v / %v, want running", got, ok)
	}
}

func TestReconcileOrphansSkipsActiveHostedTask(t *testing.T) {
	root := t.TempDir()
	store, err := session.NewJSONLStore(filepath.Join(root, ".paw"))
	if err != nil {
		t.Fatalf("NewJSONLStore() error = %v", err)
	}
	manager := NewManager(Config{
		Store:    store,
		Root:     root,
		Settings: fakeSettingsProvider{cfg: settings.DefaultConfig()},
		Launcher: &blockingLauncher{},
	})
	task, err := manager.Launch(context.Background(), Request{Prompt: "active host task"})
	if err != nil {
		t.Fatalf("Launch() error = %v", err)
	}
	t.Cleanup(func() { _, _ = manager.Stop(context.Background(), task.ID) })

	manager.reconcileOrphanedTasks(context.Background())

	if got, ok := manager.Status(task.ID); !ok || got.Status != TaskRunning {
		t.Fatalf("active hosted task = %#v / %v, want running", got, ok)
	}
}

func TestWaitAnyInterruptedTaskReturnsImmediately(t *testing.T) {
	root := t.TempDir()
	registry := newTaskRegistry(root)
	interrupted := TaskSnapshot{
		ID:        "interrupted-1",
		SessionID: "interrupted-1",
		Status:    TaskInterrupted,
		StartedAt: time.Now().UTC().Add(-time.Hour),
	}
	if err := registry.saveTask(context.Background(), interrupted); err != nil {
		t.Fatalf("saveTask(interrupted) error = %v", err)
	}

	manager, _ := newStoreWithManager(t, root)

	start := time.Now()
	result, err := manager.WaitAny(context.Background(), []string{"interrupted-1"}, time.Hour)
	if err != nil {
		t.Fatalf("WaitAny() error = %v", err)
	}
	if time.Since(start) > time.Second {
		t.Fatalf("WaitAny() took %v, want immediate return for terminal task", time.Since(start))
	}
	if result.TimedOut {
		t.Fatalf("WaitAny() timed out, want immediate terminal result")
	}
	if len(result.Tasks) != 1 || result.Tasks[0].Status != TaskInterrupted {
		t.Fatalf("WaitAny() tasks = %#v, want interrupted", result.Tasks)
	}
}

type failingStartLauncher struct {
	err error
}

func (l failingStartLauncher) Start(context.Context, WorkerRequest) (Process, error) {
	return nil, l.err
}

func TestSubscribeTaskUpdatesNotifiesAfterCompletion(t *testing.T) {
	modelStreamer := &recordingModel{rounds: []fakeRound{{events: []model.StreamEvent{{Delta: "done"}, {Done: true}}}}}
	manager, _, _ := newTestManager(t, modelStreamer, settings.DefaultConfig(), nil)
	updates, cancel := manager.SubscribeTaskUpdates()
	defer cancel()

	task, err := manager.Launch(context.Background(), Request{Prompt: "finish"})
	if err != nil {
		t.Fatalf("Launch() error = %v", err)
	}
	select {
	case <-updates:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for completion update")
	}
	completed, ok := manager.Status(task.ID)
	if !ok || completed.Status != TaskCompleted {
		t.Fatalf("Status() = %#v/%v, want completed", completed, ok)
	}
}

func TestSubscribeTaskUpdatesNotifiesAfterStop(t *testing.T) {
	root := t.TempDir()
	store, err := session.NewJSONLStore(filepath.Join(root, ".paw"))
	if err != nil {
		t.Fatal(err)
	}
	manager := NewManager(Config{Store: store, Root: root, Settings: fakeSettingsProvider{cfg: settings.DefaultConfig()}, Launcher: &blockingLauncher{}})
	updates, cancel := manager.SubscribeTaskUpdates()
	defer cancel()

	task, err := manager.Launch(context.Background(), Request{Prompt: "stop"})
	if err != nil {
		t.Fatalf("Launch() error = %v", err)
	}
	if _, err := manager.Stop(context.Background(), task.ID); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	select {
	case <-updates:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for stop update")
	}
	stopped, ok := manager.Status(task.ID)
	if !ok || stopped.Status != TaskStopped {
		t.Fatalf("Status() = %#v/%v, want stopped", stopped, ok)
	}
}

func TestSubscribeTaskUpdatesNotifiesAfterStartFailure(t *testing.T) {
	root := t.TempDir()
	store, err := session.NewJSONLStore(filepath.Join(root, ".paw"))
	if err != nil {
		t.Fatal(err)
	}
	manager := NewManager(Config{
		Store: store, Root: root, Settings: fakeSettingsProvider{cfg: settings.DefaultConfig()},
		Launcher: failingStartLauncher{err: errors.New("launcher unavailable")},
	})
	updates, cancel := manager.SubscribeTaskUpdates()
	defer cancel()

	if _, err := manager.Launch(context.Background(), Request{Prompt: "fail to start"}); err == nil {
		t.Fatal("Launch() error = nil, want launcher failure")
	}
	select {
	case <-updates:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for start-failure update")
	}
	var failed bool
	for _, task := range manager.ListTasks() {
		if task.Status == TaskFailed {
			failed = true
		}
	}
	if !failed {
		t.Fatalf("ListTasks() = %#v, want failed task", manager.ListTasks())
	}
}

func TestSubscribeTaskUpdatesCancelIsIdempotentAndStopsDelivery(t *testing.T) {
	manager, _, _ := newTestManager(t, &recordingModel{}, settings.DefaultConfig(), nil)
	updates, cancel := manager.SubscribeTaskUpdates()
	cancel()
	cancel()

	select {
	case _, ok := <-updates:
		if ok {
			t.Fatal("unsubscribed update channel received a notification")
		}
	default:
	}
}

func TestManagerCloseStopsRunningTasksAndClosesSubscriptions(t *testing.T) {
	root := t.TempDir()
	store, err := session.NewJSONLStore(filepath.Join(root, ".paw"))
	if err != nil {
		t.Fatalf("NewJSONLStore() error = %v", err)
	}
	manager := NewManager(Config{
		Store:    store,
		Root:     root,
		Settings: fakeSettingsProvider{cfg: settings.DefaultConfig()},
		Launcher: &blockingLauncher{},
	})
	updates, cancel := manager.SubscribeTaskUpdates()
	defer cancel()
	task, err := manager.Launch(context.Background(), Request{Prompt: "close manager"})
	if err != nil {
		t.Fatalf("Launch() error = %v", err)
	}

	if err := manager.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := manager.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
	for range updates {
	}

	got, ok, err := newTaskRegistry(root).loadTask(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("loadTask(%q) error = %v", task.ID, err)
	}
	if !ok || got.Status != TaskInterrupted {
		t.Fatalf("closed task = %#v / %v, want interrupted", got, ok)
	}
}
