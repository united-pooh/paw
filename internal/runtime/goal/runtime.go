package goal

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"paw/internal/message"
	"paw/internal/runtime/loop"
	"paw/internal/todo"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type TodoSource func() (todo.Snapshot, bool)

type goalExecutionContextKey struct{}

type RuntimeConfig struct {
	Store       GoalStore
	Executor    loop.TurnExecutor
	Todo        TodoSource
	Policy      Policy
	Events      EventSink
	Now         func() time.Time
	Evidence    EvidenceStore
	Checkpoints CheckpointStore
	Inputs      GoalInputQueue
	Bind        func(context.Context, GoalSnapshot) error
	Clear       func(context.Context) error
	History     HistorySource
}

type Runtime struct {
	store          GoalStore
	executor       loop.TurnExecutor
	todo           TodoSource
	policy         Policy
	events         EventSink
	now            func() time.Time
	evidence       EvidenceStore
	checkpoints    CheckpointStore
	inputs         GoalInputQueue
	bind           func(context.Context, GoalSnapshot) error
	clear          func(context.Context) error
	mu             sync.Mutex
	active         bool
	closing        bool
	notifying      bool
	pendingStop    *Event
	activeID       GoalID
	cancel         context.CancelFunc
	sequence       uint64
	history        HistorySource
	metricExcluded map[string]bool
	nextInput      string
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
	return &Runtime{
		store: store, executor: cfg.Executor, todo: cfg.Todo, policy: policy.Normalize(),
		events: cfg.Events, now: nowFn, evidence: cfg.Evidence,
		checkpoints: cfg.Checkpoints, inputs: cfg.Inputs, bind: cfg.Bind, clear: cfg.Clear, history: cfg.History,
	}
}

func (r *Runtime) Start(ctx context.Context, goal Goal) (GoalSnapshot, error) {
	if r == nil {
		return GoalSnapshot{}, errors.New("goal runtime is nil")
	}
	if strings.TrimSpace(goal.Objective) == "" {
		return GoalSnapshot{}, errors.New("goal objective is empty")
	}
	r.mu.Lock()
	if r.active || r.notifying {
		r.mu.Unlock()
		return GoalSnapshot{}, ErrGoalActive
	}
	if goal.ID == "" {
		var bytes [16]byte
		if _, err := rand.Read(bytes[:]); err != nil {
			r.mu.Unlock()
			return GoalSnapshot{}, err
		}
		bytes[6] = (bytes[6] & 0x0f) | 0x40
		bytes[8] = (bytes[8] & 0x3f) | 0x80
		goal.ID = GoalID(fmt.Sprintf("goal-%x-%x-%x-%x-%x", bytes[0:4], bytes[4:6], bytes[6:8], bytes[8:10], bytes[10:16]))
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
	if err := goal.Transition(GoalRunning, ""); err != nil {
		r.mu.Unlock()
		return GoalSnapshot{}, err
	}
	if err := r.store.Create(ctx, goal); err != nil {
		r.mu.Unlock()
		return GoalSnapshot{}, err
	}
	if err := r.updateGoal(ctx, goal); err != nil {
		r.mu.Unlock()
		return GoalSnapshot{}, err
	}
	r.active, r.activeID, r.closing = true, goal.ID, false
	r.nextInput = ""
	r.mu.Unlock()
	r.emit(Event{Type: EventStarted, GoalID: goal.ID, Snapshot: goal.Snapshot(), At: r.now()})
	r.checkpointBestEffort(context.Background(), goal.ID, Decision{Reason: "goal started"})
	go r.runAsync(goal.ID)
	return goal.Snapshot(), nil
}

func (r *Runtime) runAsync(id GoalID) {
	ctx, cancel := context.WithCancel(context.Background())
	r.mu.Lock()
	if r.activeID == id {
		r.cancel = cancel
		if r.closing {
			cancel()
		}
	}
	r.mu.Unlock()
	_ = r.Run(ctx, id)
	cancel()
}

func (r *Runtime) Run(ctx context.Context, id GoalID) error {
	if r == nil {
		return errors.New("goal runtime is nil")
	}
	ctx = context.WithValue(ctx, goalExecutionContextKey{}, id)
	goal, ok, err := r.store.Get(ctx, id)
	if err != nil {
		return err
	}
	if !ok {
		return ErrGoalNotFound
	}
	r.mu.Lock()
	if !r.active {
		r.active, r.activeID, r.closing = true, id, false
	}
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		var stopped *Event
		if r.activeID == id {
			r.active, r.activeID, r.cancel = false, "", nil
			stopped, r.pendingStop = r.pendingStop, nil
			r.notifying = stopped != nil
		}
		r.mu.Unlock()
		if stopped != nil {
			r.emit(*stopped)
		}
	}()
	if goal.Status != GoalRunning {
		return nil
	}
	if r.executor == nil {
		return r.finishWithError(ctx, goal, errors.New("goal executor is nil"))
	}
	if ctx.Err() != nil {
		return r.finishWithError(ctx, goal, ctx.Err())
	}
	goal.CurrentTaskID = fmt.Sprintf("task-%d", atomic.AddUint64(&r.sequence, 1))
	goal.UpdatedAt = r.now()
	if err := r.updateGoal(ctx, goal); err != nil {
		return err
	}
	r.emit(Event{Type: EventTaskStarted, GoalID: id, TaskID: goal.CurrentTaskID, Snapshot: goal.Snapshot(), At: r.now()})
	policy := r.policy
	policy.Budget = goal.Budget
	evaluator := NewEvaluatorWithConfig(EvaluatorConfig{Policy: policy, Now: r.now})
	adapter := &completionAdapter{runtime: r, goalID: id, evaluator: evaluator, startTurns: goal.TurnsUsed, startTools: goal.ToolCallsUsed}
	r.mu.Lock()
	historySource := r.history
	supplement := r.nextInput
	r.nextInput = ""
	r.mu.Unlock()
	if historySource != nil {
		history, historyErr := historySource(ctx)
		if historyErr != nil {
			return r.finishWithError(ctx, goal, historyErr)
		}
		r.mu.Lock()
		r.metricExcluded = historyCalls(history)
		r.mu.Unlock()
	}
	input := message.Message{Role: message.RoleUser, Content: goalPrompt(goal, supplement)}
	if queued, ok := r.dequeueInput(context.Background(), id); ok && strings.TrimSpace(queued.Content) != "" {
		input.Content += "\n用户排队补充（同一目标）：\n" + queued.Content
	}
	task := &loop.Task{ID: goal.CurrentTaskID, Input: input, Status: loop.TaskRunning}
	_, runErr := (loop.TaskOrchestrator{Executor: r.executor, Evaluator: adapter, Events: r.taskEvents}).Run(ctx, task)
	if ctx.Err() != nil && runErr == nil {
		runErr = ctx.Err()
	}
	if runErr != nil {
		latest, found, getErr := r.store.Get(context.Background(), id)
		if getErr == nil && found && latest.Status != GoalRunning {
			return nil
		}
		if getErr == nil && found {
			goal = latest
		}
		return r.finishWithError(ctx, goal, runErr)
	}
	goal, found, getErr := r.store.Get(context.Background(), id)
	if getErr != nil {
		return getErr
	}
	if !found {
		return ErrGoalNotFound
	}
	decision := adapter.last
	switch decision.Action {
	case ActionComplete:
		if err := goal.Transition(GoalCompleted, ""); err != nil {
			return err
		}
		goal.LastDecision = decision.Reason
		if err := r.updateGoal(context.Background(), goal); err != nil {
			return err
		}
		r.emit(Event{Type: EventCompleted, GoalID: id, Decision: decision, Snapshot: goal.Snapshot(), At: r.now()})
		r.checkpointBestEffort(context.Background(), id, decision)
	case ActionFailed:
		if err := goal.Transition(GoalFailed, decision.PauseReason); err != nil {
			return err
		}
		goal.LastDecision = decision.Reason
		if err := r.updateGoal(context.Background(), goal); err != nil {
			return err
		}
		r.emit(Event{Type: EventFailed, GoalID: id, Decision: decision, Snapshot: goal.Snapshot(), At: r.now()})
		r.checkpointBestEffort(context.Background(), id, decision)
	case ActionBlocked:
		if err := goal.Transition(GoalBlocked, decision.PauseReason); err != nil {
			return err
		}
		goal.LastDecision = decision.Reason
		if err := r.updateGoal(context.Background(), goal); err != nil {
			return err
		}
		r.emit(Event{Type: EventBlocked, GoalID: id, Decision: decision, Snapshot: goal.Snapshot(), At: r.now()})
		r.checkpointBestEffort(context.Background(), id, decision)
	default:
		if err := goal.Transition(GoalPaused, decision.PauseReason); err != nil {
			return err
		}
		goal.LastDecision = decision.Reason
		if err := r.updateGoal(context.Background(), goal); err != nil {
			return err
		}
		r.emit(Event{Type: EventPaused, GoalID: id, Decision: decision, Snapshot: goal.Snapshot(), At: r.now()})
		r.checkpointBestEffort(context.Background(), id, decision)
	}
	return nil
}

func (r *Runtime) finishWithError(ctx context.Context, goal Goal, err error) error {
	r.mu.Lock()
	r.closing = true
	r.mu.Unlock()
	reason := ClassifyError(err)
	if transitionErr := goal.Transition(GoalPaused, reason); transitionErr != nil {
		return transitionErr
	}
	goal.LastDecision = err.Error()
	if updateErr := r.updateGoal(context.Background(), goal); updateErr != nil {
		return updateErr
	}
	r.emit(Event{Type: EventPaused, GoalID: goal.ID, Snapshot: goal.Snapshot(), Err: err, At: r.now()})
	r.checkpointBestEffort(context.Background(), goal.ID, Decision{Action: ActionPause, Reason: err.Error(), PauseReason: reason})
	return err
}

func (r *Runtime) Resume(ctx context.Context, id GoalID) error {
	return r.resume(ctx, id, "")
}

func (r *Runtime) ResumeWithInput(ctx context.Context, id GoalID, input string) error {
	return r.resume(ctx, id, strings.TrimSpace(input))
}

func (r *Runtime) resume(ctx context.Context, id GoalID, input string) error {
	r.mu.Lock()
	if r.active || r.notifying {
		r.mu.Unlock()
		return ErrGoalActive
	}
	goal, ok, err := r.store.Get(ctx, id)
	if err != nil {
		r.mu.Unlock()
		return err
	}
	if !ok {
		r.mu.Unlock()
		return ErrGoalNotFound
	}
	if goal.Status != GoalPaused && goal.Status != GoalBlocked {
		r.mu.Unlock()
		return fmt.Errorf("goal %q is not resumable from %s", id, goal.Status)
	}
	if err := goal.Transition(GoalRunning, ""); err != nil {
		r.mu.Unlock()
		return err
	}
	if err := r.updateGoal(ctx, goal); err != nil {
		r.mu.Unlock()
		return err
	}
	if input != "" {
		if r.nextInput != "" {
			r.nextInput += "\n"
		}
		r.nextInput += input
	}
	r.active, r.activeID, r.closing = true, id, false
	r.mu.Unlock()
	r.emit(Event{Type: EventResumed, GoalID: id, Snapshot: goal.Snapshot(), At: r.now()})
	r.checkpointBestEffort(context.Background(), id, Decision{Reason: "goal resumed"})
	go r.runAsync(id)
	return nil
}

func (r *Runtime) submitRunning(id GoalID, input string, steer func(string) bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.active || r.activeID != id || r.closing {
		return fmt.Errorf("goal is stopping; retry the input after it has paused")
	}
	goal, ok, err := r.store.Get(context.Background(), id)
	if err != nil {
		return err
	}
	if !ok || goal.Status != GoalRunning {
		return fmt.Errorf("goal is not running; retry the input after it has paused")
	}
	if steer != nil && steer(input) {
		return nil
	}
	if r.nextInput != "" {
		r.nextInput += "\n"
	}
	r.nextInput += input
	return nil
}

func (r *Runtime) Pause(ctx context.Context, id GoalID) error {
	return r.stop(ctx, id, GoalPaused, PauseUserInputRequired, EventPaused)
}

func (r *Runtime) Cancel(ctx context.Context, id GoalID) error {
	return r.stop(ctx, id, GoalCancelled, "", EventCancelled)
}

func (r *Runtime) stop(ctx context.Context, id GoalID, status GoalStatus, reason PauseReason, eventType EventType) error {
	r.mu.Lock()
	if r.notifying {
		r.mu.Unlock()
		return ErrGoalActive
	}
	goal, ok, err := r.store.Get(ctx, id)
	if err != nil {
		r.mu.Unlock()
		return err
	}
	if !ok {
		r.mu.Unlock()
		return ErrGoalNotFound
	}
	if goal.Status.Terminal() || (status == GoalPaused && goal.Status != GoalRunning) {
		r.mu.Unlock()
		return nil
	}
	if err := goal.Transition(status, reason); err != nil {
		r.mu.Unlock()
		return err
	}
	if err := r.updateGoal(ctx, goal); err != nil {
		r.mu.Unlock()
		return err
	}
	event := Event{Type: eventType, GoalID: id, Snapshot: goal.Snapshot(), At: r.now()}
	active := r.active && r.activeID == id
	var cancel context.CancelFunc
	if active {
		r.closing = true
		r.pendingStop = &event
		cancel = r.cancel
	} else {
		r.notifying = true
	}
	r.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if !active {
		r.emit(event)
	}
	if status == GoalPaused {
		r.checkpointBestEffort(context.Background(), id, Decision{Action: ActionPause, Reason: "goal paused", PauseReason: reason})
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

// SaveCheckpoint persists the latest resumable execution state when a
// checkpoint store is configured. It is intentionally best-effort at call
// sites, while this method exposes persistence errors to callers.
func (r *Runtime) SaveCheckpoint(ctx context.Context, id GoalID, decision Decision) error {
	if r == nil || r.checkpoints == nil {
		return nil
	}
	g, ok, err := r.store.Get(ctx, id)
	if err != nil {
		return err
	}
	if !ok {
		return ErrGoalNotFound
	}
	var snapshot todo.Snapshot
	if r.todo != nil {
		snapshot, _ = r.todo()
	}
	checkpoint := GoalCheckpoint{
		GoalID: id, SessionID: g.SessionID, Status: g.Status,
		Objective: g.Objective, ContinuationUsed: g.ContinuationUsed,
		NoProgressCount: g.NoProgressCount, LastDecision: decision.Reason,
		PauseReason: g.PauseReason, TodoSnapshot: snapshot, CreatedAt: r.now(),
	}
	if r.evidence != nil {
		items, listErr := r.evidence.ListByGoal(ctx, id)
		if listErr != nil {
			return listErr
		}
		for _, item := range items {
			checkpoint.EvidenceIDs = append(checkpoint.EvidenceIDs, item.ID)
		}
	}
	if err := r.checkpoints.Save(ctx, checkpoint); err != nil {
		return err
	}
	r.emit(Event{Type: EventCheckpointSaved, GoalID: id, Snapshot: g.Snapshot(), Decision: decision, At: r.now()})
	return nil
}

// Recover loads the durable goal state and leaves it paused. Recovery never
// starts model execution; callers must explicitly invoke Resume.
func (r *Runtime) Recover(ctx context.Context, id GoalID) (GoalSnapshot, error) {
	if r == nil {
		return GoalSnapshot{}, errors.New("goal runtime is nil")
	}
	g, ok, err := r.store.Get(ctx, id)
	if err != nil {
		return GoalSnapshot{}, err
	}
	if !ok {
		return GoalSnapshot{}, ErrGoalNotFound
	}
	if g.Status == GoalRunning || g.Status == GoalPlanning || g.Status == GoalReplanning {
		if err := g.Transition(GoalPaused, PauseUserInputRequired); err != nil {
			return GoalSnapshot{}, err
		}
		if r.checkpoints != nil {
			if checkpoint, found, checkpointErr := r.checkpoints.Latest(ctx, id); checkpointErr != nil {
				return GoalSnapshot{}, checkpointErr
			} else if found {
				g.LastDecision = checkpoint.LastDecision
				g.PauseReason = checkpoint.PauseReason
			}
		}
	}
	if err := r.updateGoal(ctx, g); err != nil {
		return GoalSnapshot{}, err
	}
	return g.Snapshot(), nil
}

func (r *Runtime) updateGoal(ctx context.Context, goal Goal) error {
	if err := r.store.Update(ctx, goal); err != nil {
		return err
	}
	if goal.Status.Terminal() {
		if r.clear != nil {
			return r.clear(ctx)
		}
		return nil
	}
	if r.bind != nil {
		return r.bind(ctx, goal.Snapshot())
	}
	return nil
}

// Retry resumes a paused or blocked goal explicitly.
func (r *Runtime) Retry(ctx context.Context, id GoalID) error {
	return r.Resume(ctx, id)
}

func (r *Runtime) EvidenceForGoal(ctx context.Context, id GoalID) ([]Evidence, error) {
	if r == nil || r.evidence == nil {
		return nil, nil
	}
	return r.evidence.ListByGoal(ctx, id)
}
func (r *Runtime) ReadyToResume(ctx context.Context, id GoalID) (bool, error) {
	g, err := r.Status(ctx, id)
	if err != nil {
		return false, err
	}
	return g.Status == GoalPaused || g.Status == GoalBlocked, nil
}

func (r *Runtime) dequeueInput(ctx context.Context, id GoalID) (GoalInput, bool) {
	if r == nil || r.inputs == nil {
		return GoalInput{}, false
	}
	input, ok, err := r.inputs.Dequeue(ctx, id)
	if err != nil || !ok {
		return GoalInput{}, false
	}
	// Control inputs are consumed at the boundary. Steer/clarify/resume
	// content is used as the next model input; pause/cancel are handled by the
	// explicit lifecycle methods and are therefore not injected into prompts.
	switch input.Kind {
	case GoalInputSteer, GoalInputClarify, GoalInputResume:
		return input, true
	default:
		return GoalInput{}, false
	}
}

func (r *Runtime) checkpointBestEffort(ctx context.Context, id GoalID, decision Decision) {
	if r == nil || r.checkpoints == nil {
		return
	}
	_ = r.SaveCheckpoint(ctx, id, decision)
}

func (r *Runtime) EnqueueInput(ctx context.Context, input GoalInput) error {
	if r == nil || r.inputs == nil {
		return errors.New("goal input queue is unavailable")
	}
	if err := r.inputs.Enqueue(ctx, input); err != nil {
		return err
	}
	r.emit(Event{Type: EventInputReceived, GoalID: input.GoalID, At: r.now()})
	return nil
}
func (r *Runtime) emit(e Event) {
	switch e.Type {
	case EventPaused, EventBlocked, EventCompleted, EventFailed, EventCancelled:
		r.mu.Lock()
		if r.active && r.activeID == e.GoalID {
			r.pendingStop = &e
			r.mu.Unlock()
			return
		}
		r.notifying = true
		r.mu.Unlock()
		defer func() {
			r.mu.Lock()
			r.notifying = false
			r.mu.Unlock()
		}()
	}
	if e.At.IsZero() {
		e.At = r.now()
	}
	if r.events != nil {
		r.events(e)
	}
}
func (r *Runtime) taskEvents(e loop.TaskEvent) {
	r.mu.Lock()
	goalID := r.activeID
	r.mu.Unlock()
	if e.Type == loop.TaskEventTurnDone {
		if goalID != "" {
			if g, ok, err := r.store.Get(context.Background(), goalID); err == nil && ok {
				g.TurnsUsed++
				g.ToolCallsUsed += e.Execution.SuccessfulToolCalls
				g.UpdatedAt = r.now()
				if err := r.updateGoal(context.Background(), g); err == nil {
					r.checkpointBestEffort(context.Background(), goalID, Decision{Reason: fmt.Sprintf("turn %d completed", e.TurnNumber)})
				}
			}
		}
		r.emit(Event{Type: EventTurnDone, GoalID: goalID, TaskID: e.TaskID, TurnNumber: e.TurnNumber, At: r.now()})
	}
	if e.Type == loop.TaskEventContinued {
		if goalID != "" {
			if g, ok, err := r.store.Get(context.Background(), goalID); err == nil && ok {
				g.ContinuationUsed++
				g.UpdatedAt = r.now()
				_ = r.updateGoal(context.Background(), g)
			}
		}
		r.emit(Event{Type: EventContinued, GoalID: goalID, TaskID: e.TaskID, TurnNumber: e.TurnNumber, At: r.now()})
	}
}

type completionAdapter struct {
	runtime      *Runtime
	goalID       GoalID
	evaluator    *Evaluator
	last         Decision
	startTurns   int
	startTools   int
	lastProgress string
}

func (a *completionAdapter) EvaluateCompletion(execution loop.TurnExecution, used, noProgress int) loop.CompletionEvaluation {
	var snap todo.Snapshot
	has := false
	if a.runtime.todo != nil {
		snap, has = a.runtime.todo()
	}
	observation := Observation{
		GoalID: a.goalID, Assistant: execution.Message, Todo: snap, HasTodo: has,
		ContinuationUsed: used, NoProgressCount: noProgress,
	}
	if goal, ok, err := a.runtime.store.Get(context.Background(), a.goalID); err == nil && ok {
		observation.TurnsUsed = goal.TurnsUsed - a.startTurns
		observation.ToolCallsUsed = goal.ToolCallsUsed - a.startTools
		observation.Metric = goal.Metric
		if len(goal.Measurements) > 0 {
			latest := goal.Measurements[len(goal.Measurements)-1]
			observation.Measurement = &latest
		}
		observation.HasAcceptanceCriteria = len(goal.AcceptanceCriteria) > 0
		// Acceptance criteria are deliberately conservative until a verifier
		// records an explicit passed evidence item.
		observation.AcceptancePassed = !observation.HasAcceptanceCriteria
		observation.Verification = append([]VerificationSpec(nil), goal.Verification...)
		if a.runtime.evidence != nil {
			if evidence, listErr := a.runtime.evidence.ListByGoal(context.Background(), a.goalID); listErr == nil {
				observation.Evidence = evidence
			}
		}
	}
	if history, err := a.runtime.metricHistory(context.Background()); err == nil {
		a.runtime.mu.Lock()
		excluded := make(map[string]bool, len(a.runtime.metricExcluded))
		for id, value := range a.runtime.metricExcluded {
			excluded[id] = value
		}
		a.runtime.mu.Unlock()
		observation.ProgressToken = experimentProgress(history, excluded)
		observation.HadToolActivity = observation.ProgressToken != a.lastProgress
		a.lastProgress = observation.ProgressToken
		if observation.Metric != nil && observation.Measurement != nil && !excluded[observation.Measurement.ToolCallID] {
			_, digest, verifyErr := extractMetric(*observation.Metric, observation.Measurement.ToolCallID, history)
			observation.MeasurementValid = verifyErr == nil && digest == observation.Measurement.Digest && measurementFresh(observation.Measurement.ToolCallID, history)
		}
	} else {
		a.runtime.mu.Lock()
		hasHistory := a.runtime.history != nil
		a.runtime.mu.Unlock()
		if hasHistory {
			observation.ToolError = fmt.Errorf("trusted goal evidence history unavailable: %w", err)
		}
		observation.HadToolActivity = execution.SuccessfulToolCalls > 0
		if observation.HadToolActivity {
			observation.ProgressToken = fmt.Sprintf("turn-%d", observation.TurnsUsed)
		}
	}
	d := a.evaluator.Evaluate(observation)
	a.runtime.mu.Lock()
	queued := a.runtime.nextInput
	if queued != "" {
		if d.Action == ActionComplete {
			observation.MeasurementValid = false
			d = a.evaluator.Evaluate(observation)
		}
		if d.Action == ActionContinue || d.Action == ActionCompact {
			d.NextPrompt.Content += "\n用户补充（同一目标）：\n" + queued
			a.runtime.nextInput = ""
		}
	}
	if d.Action != ActionContinue && d.Action != ActionCompact {
		a.runtime.closing = true
	}
	a.runtime.mu.Unlock()
	a.last = d
	if g, ok, err := a.runtime.store.Get(context.Background(), a.goalID); err == nil && ok {
		g.NoProgressCount = d.NoProgress
		g.LastDecision = d.Reason
		g.UpdatedAt = a.runtime.now()
		_ = a.runtime.updateGoal(context.Background(), g)
	}
	action := loop.CompletionComplete
	if d.Action == ActionContinue || d.Action == ActionCompact {
		action = loop.CompletionContinue
	}
	if d.Action == ActionBlocked {
		action = loop.CompletionBlocked
	}
	if d.Action == ActionPause || d.Action == ActionFailed {
		action = loop.CompletionPause
	}
	return loop.CompletionEvaluation{Decision: loop.CompletionDecision{Action: action, Reason: d.Reason}, HasSignal: true, NoProgress: d.NoProgress, NextInput: d.NextPrompt}
}
