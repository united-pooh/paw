package goal

import (
	"context"
	"errors"
	"fmt"
	"paw/internal/loop"
	"paw/internal/message"
	"paw/internal/todo"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type TodoSource func() (todo.Snapshot, bool)

type RuntimeConfig struct {
	Store    GoalStore
	Executor loop.TurnExecutor
	Todo     TodoSource
	Policy   Policy
	Events   EventSink
	Now      func() time.Time
}

type Runtime struct {
	store    GoalStore
	executor loop.TurnExecutor
	todo     TodoSource
	policy   Policy
	events   EventSink
	now      func() time.Time
	mu       sync.Mutex
	active   bool
	activeID GoalID
	cancel   context.CancelFunc
	sequence uint64
}

var ErrGoalActive = errors.New("a goal is already running")
var ErrGoalNotFound = errors.New("goal not found")

func NewRuntime(cfg RuntimeConfig) *Runtime {
	store := cfg.Store
	if store == nil {
		store = NewMemoryStore()
	}
	nowFn := cfg.Now
	if nowFn == nil {
		nowFn = time.Now
	}
	policy := cfg.Policy
	if policy.Budget.MaxContinuations == 0 && policy.Budget.MaxNoProgress == 0 {
		policy = DefaultPolicy()
	}
	return &Runtime{store: store, executor: cfg.Executor, todo: cfg.Todo, policy: policy.Normalize(), events: cfg.Events, now: nowFn}
}

func (r *Runtime) Start(ctx context.Context, goal Goal) (GoalSnapshot, error) {
	if r == nil {
		return GoalSnapshot{}, errors.New("goal runtime is nil")
	}
	if strings.TrimSpace(goal.Objective) == "" {
		return GoalSnapshot{}, errors.New("goal objective is empty")
	}
	r.mu.Lock()
	if r.active {
		r.mu.Unlock()
		return GoalSnapshot{}, ErrGoalActive
	}
	if goal.ID == "" {
		goal.ID = GoalID(fmt.Sprintf("goal-%d", atomic.AddUint64(&r.sequence, 1)))
	}
	if goal.Status == "" {
		goal.Status = GoalDraft
	}
	if goal.CreatedAt.IsZero() {
		goal.CreatedAt = r.now()
	}
	goal.Budget = goal.Budget.Normalize()
	if goal.Budget.MaxContinuations == 0 {
		goal.Budget = r.policy.Budget
	}
	if err := r.store.Create(ctx, goal); err != nil {
		r.mu.Unlock()
		return GoalSnapshot{}, err
	}
	if err := goal.Transition(GoalRunning, ""); err != nil {
		r.mu.Unlock()
		return GoalSnapshot{}, err
	}
	if err := r.store.Update(ctx, goal); err != nil {
		r.mu.Unlock()
		return GoalSnapshot{}, err
	}
	r.active, r.activeID = true, goal.ID
	r.mu.Unlock()
	r.emit(Event{Type: EventStarted, GoalID: goal.ID, Snapshot: goal.Snapshot(), At: r.now()})
	go r.runAsync(goal.ID)
	return goal.Snapshot(), nil
}

func (r *Runtime) runAsync(id GoalID) {
	ctx, cancel := context.WithCancel(context.Background())
	r.mu.Lock()
	if r.activeID == id {
		r.cancel = cancel
	}
	r.mu.Unlock()
	_ = r.Run(ctx, id)
	cancel()
}

func (r *Runtime) Run(ctx context.Context, id GoalID) error {
	if r == nil {
		return errors.New("goal runtime is nil")
	}
	goal, ok, err := r.store.Get(ctx, id)
	if err != nil {
		return err
	}
	if !ok {
		return ErrGoalNotFound
	}
	if r.executor == nil {
		return r.finishWithError(ctx, goal, errors.New("goal executor is nil"))
	}
	r.mu.Lock()
	if !r.active {
		r.active, r.activeID = true, id
	}
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		if r.activeID == id {
			r.active, r.activeID, r.cancel = false, "", nil
		}
		r.mu.Unlock()
	}()
	goal.CurrentTaskID = fmt.Sprintf("task-%d", atomic.AddUint64(&r.sequence, 1))
	goal.UpdatedAt = r.now()
	_ = r.store.Update(ctx, goal)
	r.emit(Event{Type: EventTaskStarted, GoalID: id, TaskID: goal.CurrentTaskID, Snapshot: goal.Snapshot(), At: r.now()})
	evaluator := NewEvaluator(r.policy)
	adapter := &completionAdapter{runtime: r, goalID: id, evaluator: evaluator}
	task := &loop.Task{ID: goal.CurrentTaskID, Input: message.Message{Role: message.RoleUser, Content: goal.Objective}, Status: loop.TaskRunning}
	_, runErr := (loop.TaskOrchestrator{Executor: r.executor, Evaluator: adapter, Events: r.taskEvents}).Run(ctx, task)
	if runErr != nil {
		latest, found, getErr := r.store.Get(context.Background(), id)
		if getErr == nil && found && latest.Status.Terminal() {
			return nil
		}
		return r.finishWithError(ctx, goal, runErr)
	}
	goal, _, _ = r.store.Get(context.Background(), id)
	decision := adapter.last
	switch decision.Action {
	case ActionComplete:
		if err := goal.Transition(GoalCompleted, ""); err != nil {
			return err
		}
		goal.LastDecision = decision.Reason
		_ = r.store.Update(context.Background(), goal)
		r.emit(Event{Type: EventCompleted, GoalID: id, Decision: decision, Snapshot: goal.Snapshot(), At: r.now()})
	case ActionBlocked:
		_ = goal.Transition(GoalBlocked, decision.PauseReason)
		goal.LastDecision = decision.Reason
		_ = r.store.Update(context.Background(), goal)
		r.emit(Event{Type: EventBlocked, GoalID: id, Decision: decision, Snapshot: goal.Snapshot(), At: r.now()})
	default:
		_ = goal.Transition(GoalPaused, decision.PauseReason)
		goal.LastDecision = decision.Reason
		_ = r.store.Update(context.Background(), goal)
		r.emit(Event{Type: EventPaused, GoalID: id, Decision: decision, Snapshot: goal.Snapshot(), At: r.now()})
	}
	return nil
}

func (r *Runtime) finishWithError(ctx context.Context, goal Goal, err error) error {
	reason := ClassifyError(err)
	_ = goal.Transition(GoalPaused, reason)
	goal.LastDecision = err.Error()
	_ = r.store.Update(context.Background(), goal)
	r.emit(Event{Type: EventPaused, GoalID: goal.ID, Snapshot: goal.Snapshot(), Err: err, At: r.now()})
	return err
}

func (r *Runtime) Resume(ctx context.Context, id GoalID) error {
	r.mu.Lock()
	if r.active {
		r.mu.Unlock()
		return ErrGoalActive
	}
	r.mu.Unlock()
	goal, ok, err := r.store.Get(ctx, id)
	if err != nil {
		return err
	}
	if !ok {
		return ErrGoalNotFound
	}
	if goal.Status != GoalPaused && goal.Status != GoalBlocked {
		return fmt.Errorf("goal %q is not resumable from %s", id, goal.Status)
	}
	if err := goal.Transition(GoalRunning, ""); err != nil {
		return err
	}
	if err := r.store.Update(ctx, goal); err != nil {
		return err
	}
	r.emit(Event{Type: EventResumed, GoalID: id, Snapshot: goal.Snapshot(), At: r.now()})
	r.mu.Lock()
	r.active = true
	r.activeID = id
	r.mu.Unlock()
	go r.runAsync(id)
	return nil
}

func (r *Runtime) Pause(ctx context.Context, id GoalID) error {
	r.mu.Lock()
	if r.activeID == id && r.cancel != nil {
		r.cancel()
	}
	r.mu.Unlock()
	goal, ok, err := r.store.Get(ctx, id)
	if err != nil {
		return err
	}
	if !ok {
		return ErrGoalNotFound
	}
	if goal.Status == GoalRunning {
		_ = goal.Transition(GoalPaused, PauseUserInputRequired)
		if err := r.store.Update(ctx, goal); err != nil {
			return err
		}
		r.emit(Event{Type: EventPaused, GoalID: id, Snapshot: goal.Snapshot(), At: r.now()})
	}
	return nil
}
func (r *Runtime) Cancel(ctx context.Context, id GoalID) error {
	r.mu.Lock()
	if r.activeID == id && r.cancel != nil {
		r.cancel()
	}
	r.mu.Unlock()
	goal, ok, err := r.store.Get(ctx, id)
	if err != nil {
		return err
	}
	if !ok {
		return ErrGoalNotFound
	}
	if !goal.Status.Terminal() {
		if err := goal.Transition(GoalCancelled, ""); err != nil {
			return err
		}
		if err := r.store.Update(ctx, goal); err != nil {
			return err
		}
		r.emit(Event{Type: EventCancelled, GoalID: id, Snapshot: goal.Snapshot(), At: r.now()})
	}
	return nil
}
func (r *Runtime) Status(ctx context.Context, id GoalID) (GoalSnapshot, error) {
	g, ok, err := r.store.Get(ctx, id)
	if err != nil {
		return GoalSnapshot{}, err
	}
	if !ok {
		return GoalSnapshot{}, ErrGoalNotFound
	}
	return g.Snapshot(), nil
}
func (r *Runtime) emit(e Event) {
	if e.At.IsZero() {
		e.At = r.now()
	}
	if r.events != nil {
		r.events(e)
	}
}
func (r *Runtime) taskEvents(e loop.TaskEvent) {
	if e.Type == loop.TaskEventTurnDone {
		r.emit(Event{Type: EventTurnDone, GoalID: r.activeID, TaskID: e.TaskID, TurnNumber: e.TurnNumber, At: r.now()})
	}
	if e.Type == loop.TaskEventContinued {
		r.emit(Event{Type: EventContinued, GoalID: r.activeID, TaskID: e.TaskID, TurnNumber: e.TurnNumber, At: r.now()})
	}
}

type completionAdapter struct {
	runtime   *Runtime
	goalID    GoalID
	evaluator *Evaluator
	last      Decision
}

func (a *completionAdapter) EvaluateCompletion(assistant message.Message, used, noProgress int) loop.CompletionEvaluation {
	var snap todo.Snapshot
	has := false
	if a.runtime.todo != nil {
		snap, has = a.runtime.todo()
	}
	d := a.evaluator.Evaluate(Observation{GoalID: a.goalID, Assistant: assistant, Todo: snap, HasTodo: has, ContinuationUsed: used, NoProgressCount: noProgress})
	a.last = d
	action := loop.CompletionComplete
	if d.Action == ActionContinue || d.Action == ActionCompact {
		action = loop.CompletionContinue
	}
	if d.Action == ActionBlocked {
		action = loop.CompletionBlocked
	}
	if d.Action == ActionPause {
		action = loop.CompletionPause
	}
	return loop.CompletionEvaluation{Decision: loop.CompletionDecision{Action: action, Reason: d.Reason}, HasSignal: true, NoProgress: noProgress, NextInput: d.NextPrompt}
}
