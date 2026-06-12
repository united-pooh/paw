package subagent

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

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

type recordingModel struct {
	mu     sync.Mutex
	rounds []fakeRound
	calls  [][]message.Message
}

func (m *recordingModel) StreamMessage(ctx context.Context, messages []message.Message) (<-chan model.StreamEvent, error) {
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

func newTestManager(t *testing.T, modelStreamer *recordingModel, cfg settings.Config, notifier Notifier) (*Manager, *session.JSONLStore, string) {
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

	syncOut, err := tool.Run(context.Background(), json.RawMessage(`{"prompt":"sync prompt","description":"focus"}`))
	if err != nil {
		t.Fatalf("sync Run() error = %v", err)
	}
	if !strings.Contains(syncOut, `"content": "sync answer"`) || !strings.Contains(syncOut, `"context_mode": "empty"`) {
		t.Fatalf("sync output = %q", syncOut)
	}

	backgroundOut, err := tool.Run(context.Background(), json.RawMessage(`{"prompt":"background prompt","run_mode":"background"}`))
	if err != nil {
		t.Fatalf("background Run() error = %v", err)
	}
	var launched TaskSnapshot
	if err := json.Unmarshal([]byte(backgroundOut), &launched); err != nil {
		t.Fatalf("json.Unmarshal(backgroundOut) error = %v", err)
	}
	completed := waitForTask(t, manager, launched.ID, TaskCompleted)
	if completed.Content != "background answer" {
		t.Fatalf("completed background task = %#v", completed)
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
