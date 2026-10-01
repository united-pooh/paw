package plan

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"paw/internal/message"
	"paw/internal/runtime/loop"
)

type conversationExecutor struct {
	count atomic.Int32
	run   func(context.Context, message.Message) error
}

func (e *conversationExecutor) ExecuteTurn(ctx context.Context, input message.Message, _ *loop.TurnTiming) (loop.TurnExecution, error) {
	e.count.Add(1)
	if e.run != nil {
		if err := e.run(ctx, input); err != nil {
			return loop.TurnExecution{}, err
		}
	}
	return loop.TurnExecution{Message: message.Message{Role: message.RoleAssistant, Content: "请说明需要保留的行为。"}}, nil
}

func waitPlanIdle(t *testing.T, runtime *Runtime, id PlanID, want SessionStatus) Session {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		runtime.mu.Lock()
		snapshot, active := runtime.memSessions[id].Snapshot(), runtime.active || runtime.closing
		runtime.mu.Unlock()
		if !active {
			if snapshot.Status != want {
				t.Fatalf("idle plan status = %s, want %s; decision=%s", snapshot.Status, want, snapshot.LastDecision)
			}
			return snapshot
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("plan did not become idle")
	return Session{}
}

func writeReadyPlan(ctx context.Context, runtime *Runtime, input message.Message) error {
	const marker = "计划文件必须写入："
	_, tail, ok := strings.Cut(input.Content, marker)
	if !ok {
		return nil
	}
	path := strings.TrimSpace(strings.SplitN(tail, "\n", 2)[0])
	if err := os.WriteFile(path, []byte("# Plan\n\nReady for review"), 0o644); err != nil {
		return err
	}
	_, err := runtime.Finalize(ctx, PlanID(strings.TrimSuffix(filepath.Base(path), ".md")), path)
	return err
}

func TestPlanPlainQuestionWaitsWithoutAutomaticContinuation(t *testing.T) {
	executor := &conversationExecutor{}
	runtime := newTestRuntime(executor, newTestStore(t))
	snapshot, err := runtime.Start(context.Background(), "讨论登录方案，先不要执行")
	if err != nil {
		t.Fatal(err)
	}
	waitPlanIdle(t, runtime, snapshot.ID, SessionAwaitingInput)
	if executor.count.Load() != 1 || runtime.ActiveID() != snapshot.ID {
		t.Fatalf("plain question must await the next user answer: calls=%d active=%s", executor.count.Load(), runtime.ActiveID())
	}
}

func TestPlanFinalizeSavesReadyWithoutApprovingExecution(t *testing.T) {
	var notifications atomic.Int32
	executor := &conversationExecutor{}
	runtime := NewRuntime(RuntimeConfig{
		Store: newTestStore(t), Executor: executor,
		OnFinalized: func(doc PlanDoc) {
			if doc.Status != PlanReady {
				t.Errorf("ready notification status = %s", doc.Status)
			}
			notifications.Add(1)
		},
	})
	executor.run = func(ctx context.Context, input message.Message) error { return writeReadyPlan(ctx, runtime, input) }
	snapshot, err := runtime.Start(context.Background(), "只保存方案")
	if err != nil {
		t.Fatal(err)
	}
	waitPlanIdle(t, runtime, snapshot.ID, SessionReady)
	doc, ok, err := runtime.Show(context.Background(), snapshot.ID)
	if err != nil || !ok || doc.Status != PlanReady || notifications.Load() != 1 {
		t.Fatalf("finalize must only save ready: doc=%+v present=%v err=%v notifications=%d", doc, ok, err, notifications.Load())
	}
}
