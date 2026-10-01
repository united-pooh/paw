package plan

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"paw/internal/message"
	"paw/internal/runtime/loop"
)

type planSteerAdmission struct {
	committed chan string
	input     string
	aborted   atomic.Bool
}

func (a *planSteerAdmission) Commit() { a.committed <- a.input }
func (a *planSteerAdmission) Abort()  { a.aborted.Store(true) }

type planSteerExecutor struct {
	conversationExecutor
	admission *planSteerAdmission
}

func (e *planSteerExecutor) PrepareSteer(input string) (loop.SteerAdmission, bool) {
	e.admission.input = input
	return e.admission, true
}

func TestPlanRunningSubmitCommitsSteerOnlyAfterPersistence(t *testing.T) {
	for _, failSave := range []bool{false, true} {
		t.Run(map[bool]string{false: "accepted", true: "save failure"}[failSave], func(t *testing.T) {
			entered, release := make(chan struct{}), make(chan struct{})
			admission := &planSteerAdmission{committed: make(chan string, 1)}
			executor := &planSteerExecutor{admission: admission}
			executor.run = func(context.Context, message.Message) error {
				close(entered)
				<-release
				return nil
			}
			runtime := NewRuntime(RuntimeConfig{
				Store: newTestStore(t), Executor: executor,
				Snapshot: func(_ context.Context, snapshot Session) error {
					if failSave && snapshot.LastDecision == "user updated the plan conversation" {
						return errors.New("snapshot disk failure")
					}
					return nil
				},
			})
			session, err := runtime.Start(context.Background(), "draft")
			if err != nil {
				t.Fatal(err)
			}
			<-entered
			_, err = runtime.Submit(context.Background(), "follow-up")
			if failSave {
				if err == nil || !admission.aborted.Load() || len(admission.committed) != 0 {
					t.Fatalf("failed save admitted input: err=%v aborted=%v commits=%d", err, admission.aborted.Load(), len(admission.committed))
				}
			} else if err != nil || len(admission.committed) != 1 || <-admission.committed != "follow-up" {
				t.Fatalf("running input was not committed: %v", err)
			}
			close(release)
			snapshot := waitPlanIdle(t, runtime, session.ID, SessionAwaitingInput)
			if executor.count.Load() != 1 || len(snapshot.PendingInputs) != 0 {
				t.Fatalf("steer duplicated as another turn: calls=%d pending=%q", executor.count.Load(), snapshot.PendingInputs)
			}
		})
	}
}

type planClosingExecutor struct {
	conversationExecutor
	closing chan struct{}
	release chan struct{}
}

func (e *planClosingExecutor) SetTurnToolFilter(filter loop.ToolFilter) {
	if filter == nil {
		close(e.closing)
		<-e.release
	}
}
func (*planClosingExecutor) SystemSupplement() string   { return "" }
func (*planClosingExecutor) SetSystemSupplement(string) {}

func TestPlanSubmitRejectsClosingWindowWithoutLosingInput(t *testing.T) {
	executor := &planClosingExecutor{closing: make(chan struct{}), release: make(chan struct{})}
	runtime := newTestRuntime(executor, newTestStore(t))
	session, err := runtime.Start(context.Background(), "draft")
	if err != nil {
		t.Fatal(err)
	}
	<-executor.closing
	if _, err := runtime.Submit(context.Background(), "keep this draft"); err == nil {
		t.Fatal("input was acknowledged after the run stopped consuming its queue")
	}
	close(executor.release)
	snapshot := waitPlanIdle(t, runtime, session.ID, SessionAwaitingInput)
	if len(snapshot.PendingInputs) != 0 || executor.count.Load() != 1 {
		t.Fatal("rejected input entered the closed run")
	}
}

type resumeFailStore struct {
	DocStore
	fail atomic.Bool
}

func (s *resumeFailStore) Update(ctx context.Context, doc PlanDoc) error {
	if s.fail.Load() {
		return errors.New("document disk failure")
	}
	return s.DocStore.Update(ctx, doc)
}

func TestPlanResumeFailurePreservesPersistedInput(t *testing.T) {
	for _, failure := range []string{"document", "snapshot"} {
		t.Run(failure, func(t *testing.T) {
			store := newTestStore(t)
			executor := &conversationExecutor{}
			original := newTestRuntime(executor, store)
			executor.run = func(ctx context.Context, input message.Message) error { return writeReadyPlan(ctx, original, input) }
			session, err := original.Start(context.Background(), "Draft")
			if err != nil {
				t.Fatal(err)
			}
			snapshot := waitPlanIdle(t, original, session.ID, SessionReady)
			snapshot.PendingInputs = []string{"Persisted correction"}
			failingStore := &resumeFailStore{DocStore: store}
			var failSnapshot atomic.Bool
			var durable atomic.Pointer[Session]
			observed := make(chan string, 1)
			reopenedExecutor := &conversationExecutor{run: func(_ context.Context, input message.Message) error {
				observed <- input.Content
				return nil
			}}
			reopened := NewRuntime(RuntimeConfig{Store: failingStore, Executor: reopenedExecutor, Snapshot: func(_ context.Context, next Session) error {
				if failSnapshot.Load() && next.Status == SessionDrafting {
					return errors.New("snapshot disk failure")
				}
				durable.Store(&next)
				return nil
			}})
			if err := reopened.Restore(snapshot); err != nil {
				t.Fatal(err)
			}
			failingStore.fail.Store(failure == "document")
			failSnapshot.Store(failure == "snapshot")
			if err := reopened.Resume(context.Background(), snapshot.ID); err == nil {
				t.Fatal("expected injected resume failure")
			}
			current := waitPlanIdle(t, reopened, snapshot.ID, SessionPaused)
			if len(current.PendingInputs) != 1 || len(durable.Load().PendingInputs) != 1 {
				t.Fatalf("failed resume lost accepted input: memory=%q durable=%q", current.PendingInputs, durable.Load().PendingInputs)
			}
			failingStore.fail.Store(false)
			failSnapshot.Store(false)
			if err := reopened.Resume(context.Background(), snapshot.ID); err != nil {
				t.Fatal(err)
			}
			waitPlanIdle(t, reopened, snapshot.ID, SessionAwaitingInput)
			if reopenedExecutor.count.Load() != 1 || !strings.Contains(<-observed, "Persisted correction") {
				t.Fatal("retry did not consume the persisted correction exactly once")
			}
		})
	}
}

func TestPlanReadyNotificationSealsAdmissionUntilDelivered(t *testing.T) {
	for _, action := range []string{"submit", "resume", "execute", "pause", "cancel"} {
		t.Run(action, func(t *testing.T) {
			entered, release := make(chan struct{}), make(chan struct{})
			observed := make(chan string, 1)
			executor := &conversationExecutor{}
			var notifications atomic.Int32
			var runtime *Runtime
			runtime = NewRuntime(RuntimeConfig{Store: newTestStore(t), Executor: executor, OnFinalized: func(doc PlanDoc) {
				if _, err := runtime.Status(context.Background(), doc.ID); err != nil {
					t.Error(err)
				}
				if notifications.Add(1) == 1 {
					close(entered)
					<-release
				}
			}})
			executor.run = func(ctx context.Context, input message.Message) error {
				if executor.count.Load() == 1 {
					return writeReadyPlan(ctx, runtime, input)
				}
				observed <- input.Content
				return nil
			}
			session, err := runtime.Start(context.Background(), "Draft")
			if err != nil {
				t.Fatal(err)
			}
			<-entered
			switch action {
			case "submit":
				_, err = runtime.Submit(context.Background(), "Retry this correction")
			case "resume":
				err = runtime.Resume(context.Background(), session.ID)
			case "execute":
				_, err = runtime.Execute(context.Background(), session.ID)
			case "pause":
				err = runtime.Pause(context.Background(), session.ID)
			case "cancel":
				err = runtime.Cancel(context.Background(), session.ID)
			}
			if err == nil {
				t.Errorf("%s admitted before ready notification finished", action)
			}
			close(release)
			if t.Failed() {
				return
			}
			waitPlanIdle(t, runtime, session.ID, SessionReady)
			if _, err := runtime.Submit(context.Background(), "Retry this correction"); err != nil {
				t.Fatal(err)
			}
			snapshot := waitPlanIdle(t, runtime, session.ID, SessionAwaitingInput)
			if executor.count.Load() != 2 || len(snapshot.PendingInputs) != 0 || !strings.Contains(<-observed, "Retry this correction") {
				t.Fatal("retry was lost or duplicated")
			}
		})
	}
}

func TestPlanCancelledNotificationPrecedesNextPlan(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	runtime := NewRuntime(RuntimeConfig{Store: newTestStore(t), Executor: &conversationExecutor{}, Events: func(event Event) {
		if event.Type == EventCancelled {
			close(entered)
			<-release
		}
	}})
	snapshot := Session{ID: "old-plan", Status: SessionPaused, ResumeStatus: SessionDrafting}
	if err := runtime.Restore(snapshot); err != nil {
		t.Fatal(err)
	}
	cancelled := make(chan error, 1)
	go func() { cancelled <- runtime.Cancel(context.Background(), snapshot.ID) }()
	<-entered
	if _, err := runtime.Submit(context.Background(), "New plan"); err == nil {
		t.Error("new plan started before old cancellation notification finished")
	}
	close(release)
	if err := <-cancelled; err != nil {
		t.Fatal(err)
	}
	if t.Failed() {
		return
	}
	session, err := runtime.Submit(context.Background(), "New plan")
	if err != nil {
		t.Fatal(err)
	}
	if session.ID == snapshot.ID {
		t.Fatal("cancelled plan was reused")
	}
	waitPlanIdle(t, runtime, session.ID, SessionAwaitingInput)
}
