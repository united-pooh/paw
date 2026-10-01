package plan

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"paw/internal/message"
)

func TestPlanSubmitContinuesConversationAndInvalidatesReady(t *testing.T) {
	executor := &conversationExecutor{}
	runtime := newTestRuntime(executor, newTestStore(t))
	executor.run = func(ctx context.Context, input message.Message) error {
		if !strings.Contains(input.Content, "再澄清") {
			return writeReadyPlan(ctx, runtime, input)
		}
		return nil
	}
	first, err := runtime.Submit(context.Background(), "先讨论方案")
	if err != nil {
		t.Fatal(err)
	}
	waitPlanIdle(t, runtime, first.ID, SessionReady)
	second, err := runtime.Submit(context.Background(), "再澄清错误处理")
	if err != nil || second.ID != first.ID {
		t.Fatalf("follow-up replaced conversation: first=%s second=%s err=%v", first.ID, second.ID, err)
	}
	waitPlanIdle(t, runtime, first.ID, SessionAwaitingInput)
	if _, err := runtime.Execute(context.Background(), first.ID); err == nil {
		t.Fatal("old ready plan remained executable after a clarification")
	}
	doc, _, _ := runtime.Show(context.Background(), first.ID)
	if doc.Status != PlanDraft {
		t.Fatalf("old ready status = %s, want draft", doc.Status)
	}
	if _, err := runtime.Submit(context.Background(), "把明确后的方案保存"); err != nil {
		t.Fatal(err)
	}
	waitPlanIdle(t, runtime, first.ID, SessionReady)
	approved, err := runtime.Execute(context.Background(), first.ID)
	if err != nil || approved.Status != PlanApproved || runtime.ActiveID() != "" {
		t.Fatalf("explicit execution: doc=%+v err=%v active=%s", approved, err, runtime.ActiveID())
	}
	if executor.count.Load() != 3 {
		t.Fatalf("explicit execution called model: calls=%d", executor.count.Load())
	}
}

func TestPlanQueuedUserInputsRunBeforeStopping(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var mu sync.Mutex
	var inputs []string
	executor := &conversationExecutor{}
	executor.run = func(ctx context.Context, input message.Message) error {
		mu.Lock()
		inputs = append(inputs, input.Content)
		first := len(inputs) == 1
		mu.Unlock()
		if first {
			close(entered)
			select {
			case <-release:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return nil
	}
	runtime := newTestRuntime(executor, newTestStore(t))
	first, err := runtime.Start(context.Background(), "第一条要求")
	if err != nil {
		t.Fatal(err)
	}
	<-entered
	for _, input := range []string{"第二条补充", "第三条修正"} {
		got, err := runtime.Submit(context.Background(), input)
		if err != nil || got.ID != first.ID {
			t.Fatalf("submit %q: id=%s err=%v", input, got.ID, err)
		}
	}
	close(release)
	snapshot := waitPlanIdle(t, runtime, first.ID, SessionAwaitingInput)
	mu.Lock()
	defer mu.Unlock()
	if len(inputs) != 3 || !strings.Contains(inputs[1], "第二条补充") || !strings.Contains(inputs[2], "第三条修正") || len(snapshot.PendingInputs) != 0 {
		t.Fatalf("accepted user inputs were lost or reordered: inputs=%q pending=%q", inputs, snapshot.PendingInputs)
	}
}

func TestPlanPauseKeepsConversationResumable(t *testing.T) {
	entered := make(chan struct{})
	executor := &conversationExecutor{}
	executor.run = func(ctx context.Context, _ message.Message) error {
		if executor.count.Load() == 1 {
			close(entered)
			<-ctx.Done()
			return ctx.Err()
		}
		return nil
	}
	runtime := newTestRuntime(executor, newTestStore(t))
	snapshot, err := runtime.Start(context.Background(), "讨论方案")
	if err != nil {
		t.Fatal(err)
	}
	<-entered
	if err := runtime.Pause(context.Background(), snapshot.ID); err != nil {
		t.Fatal(err)
	}
	waitPlanIdle(t, runtime, snapshot.ID, SessionPaused)
	if runtime.ActiveID() != snapshot.ID {
		t.Fatal("pause detached the current plan")
	}
	if err := runtime.Resume(context.Background(), snapshot.ID); err != nil {
		t.Fatal(err)
	}
	waitPlanIdle(t, runtime, snapshot.ID, SessionAwaitingInput)
}

func TestPlanReadyRestoreRequiresExplicitUserExecution(t *testing.T) {
	store := newTestStore(t)
	executor := &conversationExecutor{}
	runtime := newTestRuntime(executor, store)
	executor.run = func(ctx context.Context, input message.Message) error { return writeReadyPlan(ctx, runtime, input) }
	session, err := runtime.Start(context.Background(), "保存计划")
	if err != nil {
		t.Fatal(err)
	}
	snapshot := waitPlanIdle(t, runtime, session.ID, SessionReady)
	reopenedExecutor := &conversationExecutor{}
	reopened := newTestRuntime(reopenedExecutor, store)
	if err := reopened.Restore(snapshot); err != nil {
		t.Fatal(err)
	}
	if got, _ := reopened.Status(context.Background(), snapshot.ID); got.Status != SessionReady || reopenedExecutor.count.Load() != 0 {
		t.Fatalf("ready restore unexpectedly resumed: %+v", got)
	}
	if _, err := reopened.Execute(context.Background(), snapshot.ID); err != nil {
		t.Fatalf("explicit execution after restore: %v", err)
	}
}

func TestPlanDocumentMetadataCannotApproveExecution(t *testing.T) {
	store := newTestStore(t)
	executor := &conversationExecutor{}
	runtime := newTestRuntime(executor, store)
	executor.run = func(_ context.Context, input message.Message) error {
		_, tail, _ := strings.Cut(input.Content, "计划文件必须写入：")
		path := strings.TrimSpace(strings.SplitN(tail, "\n", 2)[0])
		return os.WriteFile(path, []byte("<!-- paw-plan: id="+strings.TrimSuffix(filepath.Base(path), ".md")+" status=approved -->\n# Plan\n"), 0o644)
	}
	snapshot, err := runtime.Start(context.Background(), "讨论方案")
	if err != nil {
		t.Fatal(err)
	}
	waitPlanIdle(t, runtime, snapshot.ID, SessionAwaitingInput)
	if _, err := runtime.Execute(context.Background(), snapshot.ID); err == nil {
		t.Fatal("editable metadata authorized execution")
	}
}

func TestPlanControllerProvidesConversationalUserActions(t *testing.T) {
	host := &fakePlanSessionHost{sessionID: "session-a", executor: &conversationExecutor{}, plans: map[string]Session{}}
	controller := NewSessionController(host, t.TempDir(), "")
	actions, ok := any(controller).(interface {
		Submit(string) (string, error)
		Execute() (PlanDoc, error)
		Pause() error
	})
	if !ok {
		t.Fatal("Plan controller lacks conversational Submit/Execute/Pause actions")
	}
	id, err := actions.Submit("先讨论需求")
	if err != nil {
		t.Fatal(err)
	}
	waitPlanIdle(t, controller.runtime, PlanID(id), SessionAwaitingInput)
	second, err := actions.Submit("补充一个限制")
	if err != nil || second != id {
		t.Fatalf("controller follow-up changed plan: id=%s err=%v", second, err)
	}
	waitPlanIdle(t, controller.runtime, PlanID(id), SessionAwaitingInput)
	if _, err := actions.Execute(); err == nil {
		t.Fatal("controller executed an unfinished conversation")
	}
}

func TestPlanRawMarkdownRevisionWithEventStore(t *testing.T) {
	store, err := NewEventStore(t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	executor := &conversationExecutor{}
	runtime := NewRuntime(RuntimeConfig{Store: store, Executor: executor, SessionID: "session-a"})
	executor.run = func(ctx context.Context, input message.Message) error {
		_, tail, _ := strings.Cut(input.Content, "计划文件必须写入：")
		path := strings.TrimSpace(strings.SplitN(tail, "\n", 2)[0])
		body := "# First plan\n"
		if executor.count.Load() > 1 {
			body = "# Revised plan\n\nKeep the user's correction.\n"
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			return err
		}
		_, err := runtime.Finalize(ctx, PlanID(strings.TrimSuffix(filepath.Base(path), ".md")), path)
		return err
	}
	snapshot, err := runtime.Submit(context.Background(), "Review a plan")
	if err != nil {
		t.Fatal(err)
	}
	waitPlanIdle(t, runtime, snapshot.ID, SessionReady)
	if _, err := runtime.Submit(context.Background(), "Keep the correction"); err != nil {
		t.Fatal(err)
	}
	waitPlanIdle(t, runtime, snapshot.ID, SessionReady)
	doc, err := runtime.Execute(context.Background(), snapshot.ID)
	if err != nil || doc.Status != PlanApproved || !strings.Contains(doc.Content, "user's correction") {
		t.Fatalf("event store lost the current revision: doc=%+v err=%v", doc, err)
	}
}

func TestPlanQueuedCorrectionInvalidatesEarlierTurnReady(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	executor := &conversationExecutor{}
	runtime := newTestRuntime(executor, newTestStore(t))
	executor.run = func(ctx context.Context, input message.Message) error {
		if executor.count.Load() == 1 {
			close(entered)
			<-release
			return writeReadyPlan(ctx, runtime, input)
		}
		return nil
	}
	session, err := runtime.Start(context.Background(), "Draft")
	if err != nil {
		t.Fatal(err)
	}
	<-entered
	if _, err := runtime.Submit(context.Background(), "Correction"); err != nil {
		t.Fatal(err)
	}
	close(release)
	waitPlanIdle(t, runtime, session.ID, SessionAwaitingInput)
	doc, _, _ := runtime.Show(context.Background(), session.ID)
	if doc.Status != PlanDraft {
		t.Fatalf("queued correction retained old ready document: %s", doc.Status)
	}
}

func TestPlanRestoreWithPendingCorrectionCannotExecuteOldReady(t *testing.T) {
	store := newTestStore(t)
	executor := &conversationExecutor{}
	runtime := newTestRuntime(executor, store)
	executor.run = func(ctx context.Context, input message.Message) error { return writeReadyPlan(ctx, runtime, input) }
	session, err := runtime.Start(context.Background(), "Draft")
	if err != nil {
		t.Fatal(err)
	}
	snapshot := waitPlanIdle(t, runtime, session.ID, SessionReady)
	snapshot.PendingInputs = []string{"Persisted user correction"}
	reopenedExecutor := &conversationExecutor{}
	var observed string
	reopenedExecutor.run = func(_ context.Context, input message.Message) error { observed = input.Content; return nil }
	reopened := newTestRuntime(reopenedExecutor, store)
	if err := reopened.Restore(snapshot); err != nil {
		t.Fatal(err)
	}
	if _, err := reopened.Execute(context.Background(), snapshot.ID); err == nil {
		t.Fatal("restored pending correction did not block old ready plan")
	}
	if err := reopened.Resume(context.Background(), snapshot.ID); err != nil {
		t.Fatal(err)
	}
	waitPlanIdle(t, reopened, snapshot.ID, SessionAwaitingInput)
	if reopenedExecutor.count.Load() != 1 || !strings.Contains(observed, "Persisted user correction") {
		t.Fatalf("restore lost accepted correction: %q", observed)
	}
}
