package plan

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"paw/internal/message"
	"paw/internal/runtime/loop"
)

// RuntimeConfig wires a plan runtime to a turn executor, a document store and
// optional observers. Executor must be safe to call from Run; when it also
// implements loop.ToolFilterApplier (the runner's turn executor does), the
// runtime scopes tools and injects plan instructions for its own turns.
type RuntimeConfig struct {
	Store            DocStore
	Executor         loop.TurnExecutor
	SessionID        string
	Events           EventSink
	Now              func() time.Time
	Filter           loop.ToolFilter
	MaxTurns         int
	MaxContinuations int
	MaxNoProgress    int
	Snapshot         func(context.Context, Session) error
	ClearSnapshot    func(context.Context) error
	// OnFinalized reports a saved, ready document; it never requests execution.
	OnFinalized func(PlanDoc)
}

var (
	ErrPlanActive    = errors.New("a plan session is already running")
	ErrPlanNotFound  = errors.New("plan not found")
	ErrNoActivePlan  = errors.New("no active plan session")
	ErrPlanFinalized = errors.New("plan session is already approved")
	ErrPlanNotReady  = errors.New("plan is not ready; finish reviewing and save it before execution")
)

type Runtime struct {
	store       DocStore
	executor    loop.TurnExecutor
	sessionID   string
	events      EventSink
	now         func() time.Time
	filter      loop.ToolFilter
	onFinalized func(PlanDoc)
	snapshot    func(context.Context, Session) error
	clear       func(context.Context) error

	maxTurns         int
	maxContinuations int
	maxNoProgress    int

	mu          sync.Mutex
	active      bool
	closing     bool
	activeID    PlanID
	cancel      context.CancelFunc
	sequence    uint64
	memSessions map[PlanID]Session
}

func NewRuntime(cfg RuntimeConfig) *Runtime {
	if cfg.Store == nil {
		cfg.Store = NewFileStore(DefaultDirectory)
	}
	nowFn := cfg.Now
	if nowFn == nil {
		nowFn = time.Now
	}
	maxTurns := cfg.MaxTurns
	if maxTurns <= 0 {
		maxTurns = 8
	}
	maxContinuations := cfg.MaxContinuations
	if maxContinuations <= 0 {
		maxContinuations = 4
	}
	maxNoProgress := cfg.MaxNoProgress
	if maxNoProgress <= 0 {
		maxNoProgress = 2
	}
	return &Runtime{
		store: cfg.Store, executor: cfg.Executor, sessionID: cfg.SessionID, events: cfg.Events,
		now: nowFn, filter: cfg.Filter, onFinalized: cfg.OnFinalized,
		snapshot: cfg.Snapshot, clear: cfg.ClearSnapshot,
		maxTurns: maxTurns, maxContinuations: maxContinuations, maxNoProgress: maxNoProgress,
		memSessions: map[PlanID]Session{},
	}
}

// Start begins a plan-authoring session for a requirement. It returns the
// session snapshot; execution runs asynchronously.
func (r *Runtime) Start(ctx context.Context, requirement string) (Session, error) {
	if r == nil {
		return Session{}, errors.New("plan runtime is nil")
	}
	requirement = strings.TrimSpace(requirement)
	if requirement == "" {
		return Session{}, errors.New("plan requirement is empty")
	}
	r.mu.Lock()
	if r.active || r.closing || r.activeID != "" {
		r.mu.Unlock()
		return Session{}, ErrPlanActive
	}
	id, err := r.store.NextID(ctx, Slug(requirement))
	if err != nil {
		r.mu.Unlock()
		return Session{}, err
	}
	session := Session{
		ID:               id,
		SessionID:        r.sessionID,
		Status:           SessionClarifying,
		Requirement:      requirement,
		MaxTurns:         r.maxTurns,
		MaxContinuations: r.maxContinuations,
		MaxNoProgress:    r.maxNoProgress,
		CreatedAt:        r.now(),
		UpdatedAt:        r.now(),
	}
	if err := r.persistSessionLocked(ctx, session); err != nil {
		r.mu.Unlock()
		return Session{}, err
	}
	r.active, r.activeID = true, id
	runCtx, cancel := context.WithCancel(context.Background())
	r.cancel = cancel
	r.mu.Unlock()
	r.emit(Event{Type: EventStarted, PlanID: id, Status: session.Status, At: r.now()})
	go r.runAsync(runCtx, id, requirement)
	return session.Snapshot(), nil
}

func (r *Runtime) runAsync(ctx context.Context, id PlanID, requirement string) {
	_ = r.Run(ctx, id, requirement)
}

// Run answers one user submission, then waits. Queued user submissions are
// consumed before the run ends; they are never synthetic continuations.
func (r *Runtime) Run(ctx context.Context, id PlanID, requirement string) error {
	if r == nil {
		return errors.New("plan runtime is nil")
	}
	if r.executor == nil {
		return r.finishWithError(ctx, id, errors.New("plan executor is nil"))
	}
	r.mu.Lock()
	if _, ok := r.memSessions[id]; !ok {
		r.mu.Unlock()
		return ErrPlanNotFound
	}
	if !r.active {
		r.active, r.activeID = true, id
	}
	r.mu.Unlock()

	// Scope tools and inject plan instructions for the whole session. Restore
	// the previous runner state afterwards.
	applier, scoped := r.executor.(loop.ToolFilterApplier)
	var savedSupplement string
	if scoped {
		savedSupplement = applier.SystemSupplement()
		applier.SetTurnToolFilter(r.filter)
		applier.SetSystemSupplement(Instructions)
	}
	defer func() {
		if scoped {
			applier.SetTurnToolFilter(nil)
			applier.SetSystemSupplement(savedSupplement)
		}
		r.finishRun(id)
	}()
	for {
		if err := ctx.Err(); err != nil {
			return r.finishWithError(context.Background(), id, err)
		}
		r.mu.Lock()
		session := r.memSessions[id]
		session.CurrentTaskID = fmt.Sprintf("plan-task-%d", atomic.AddUint64(&r.sequence, 1))
		session.UpdatedAt = r.now()
		err := r.persistSessionLocked(ctx, session)
		r.mu.Unlock()
		if err != nil {
			return err
		}
		planPath := filepath.Join(r.store.Dir(), string(id)+".md")
		input := message.Message{Role: message.RoleUser, Content: "当前 PLAN 对话的用户输入：\n\n" + requirement +
			"\n\n计划文件必须写入：" + planPath + "\n沿用当前对话继续澄清或修改；需要用户回答时直接提问并等待。保存就绪计划不代表批准执行。"}
		evaluator := &evaluator{runtime: r, id: id}
		var eventErr error
		task := &loop.Task{ID: session.CurrentTaskID, Input: input, Status: loop.TaskRunning}
		_, runErr := (loop.TaskOrchestrator{Executor: r.executor, Evaluator: evaluator, Events: r.taskEvents(id, &eventErr)}).Run(ctx, task)
		if runErr != nil {
			return r.finishWithError(context.Background(), id, runErr)
		}
		if evaluator.err != nil {
			return evaluator.err
		}
		if eventErr != nil {
			return eventErr
		}
		r.mu.Lock()
		latest := r.memSessions[id]
		if latest.Status == SessionPaused || latest.Status.Terminal() || len(latest.PendingInputs) == 0 {
			// Seal admission before releasing the lock; Submit starts a fresh
			// user turn once this run has restored its scoped tools.
			r.closing = true
			r.mu.Unlock()
			return nil
		}
		requirement = latest.PendingInputs[0]
		latest.PendingInputs = latest.PendingInputs[1:]
		if err := r.invalidateReadyLocked(ctx, &latest); err != nil {
			r.mu.Unlock()
			return r.finishWithError(context.Background(), id, err)
		}
		latest.Status = SessionDrafting
		err = r.persistSessionLocked(ctx, latest)
		r.mu.Unlock()
		if err != nil {
			return err
		}
	}
}

func (r *Runtime) finishWithError(ctx context.Context, id PlanID, err error) error {
	session, ok := r.loadSession(id)
	if !ok {
		return err
	}
	if session.Status.Terminal() || session.Status == SessionPaused {
		return err
	}
	_ = session.Transition(SessionFailed, PauseBlocked)
	session.LastDecision = err.Error()
	if storeErr := r.storeSession(ctx, session); storeErr != nil {
		return storeErr
	}
	return err
}

// Finalize saves a ready plan. Only the explicit user Execute entry point can
// approve execution.
func (r *Runtime) Finalize(ctx context.Context, id PlanID, path string) (PlanDoc, error) {
	if r == nil {
		return PlanDoc{}, errors.New("plan runtime is nil")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.active || r.activeID != id {
		return PlanDoc{}, ErrNoActivePlan
	}
	session := r.memSessions[id]
	if session.Status.Terminal() || session.Status == SessionPaused {
		return PlanDoc{}, ErrPlanFinalized
	}

	cleanPath, err := r.validatePath(path)
	if err != nil {
		return PlanDoc{}, err
	}
	expected, err := filepath.Abs(filepath.Join(r.store.Dir(), string(id)+".md"))
	if err != nil || cleanPath != expected {
		return PlanDoc{}, errors.New("plan path must match the current plan id")
	}
	doc, ok, err := r.store.Get(ctx, id)
	if err != nil {
		return PlanDoc{}, err
	}
	data, readErr := os.ReadFile(cleanPath)
	if readErr != nil {
		return PlanDoc{}, fmt.Errorf("read plan file: %w", readErr)
	}
	written, _ := decodeDoc(string(data), string(id), cleanPath)
	if written.Status == PlanApproved {
		return PlanDoc{}, errors.New("plan metadata cannot approve execution")
	}
	if !ok {
		doc = written
	} else {
		doc.Content = written.Content
		if written.Title != "" {
			doc.Title = written.Title
		}
	}
	doc.ID = id
	doc.SessionID = session.SessionID
	doc.Path = cleanPath
	if strings.TrimSpace(doc.Title) == "" {
		doc.Title = session.Requirement
	}
	if strings.TrimSpace(doc.Content) == "" {
		return PlanDoc{}, errors.New("plan document is empty")
	}
	doc.Status = written.Status
	if err := r.store.Update(ctx, doc); err != nil {
		return PlanDoc{}, err
	}
	doc.Status = PlanReady
	if err := r.store.Update(ctx, doc); err != nil {
		return PlanDoc{}, err
	}
	doc, ok, err = r.store.Get(ctx, id)
	if err != nil {
		return PlanDoc{}, fmt.Errorf("read saved plan: %w", err)
	}
	if !ok {
		return PlanDoc{}, ErrPlanNotFound
	}
	if err := session.Transition(SessionReady, ""); err != nil {
		return PlanDoc{}, err
	}
	session.ReadyContentHash = readyContentHash(doc)
	session.LastDecision = "plan saved and ready; awaiting explicit user execution"
	if err := r.persistSessionLocked(ctx, session); err != nil {
		return PlanDoc{}, err
	}
	return doc, nil
}

func (r *Runtime) validatePath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", errors.New("plan path is empty")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	dirAbs, err := filepath.Abs(r.store.Dir())
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(dirAbs, abs)
	if err != nil {
		return "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errors.New("plan path must be inside the plans directory")
	}
	return abs, nil
}

// Resume continues a paused plan session.
func (r *Runtime) Resume(ctx context.Context, id PlanID) error {
	r.mu.Lock()
	if r.active || r.closing {
		r.mu.Unlock()
		return ErrPlanActive
	}
	session, ok := r.memSessions[id]
	if !ok {
		r.mu.Unlock()
		return ErrPlanNotFound
	}
	if session.Status != SessionPaused && session.Status != SessionAwaitingInput && session.Status != SessionReady {
		r.mu.Unlock()
		return fmt.Errorf("plan %q is not resumable from %s", id, session.Status)
	}
	if len(session.PendingInputs) == 0 && (session.Status == SessionReady || session.Status == SessionAwaitingInput || session.ResumeStatus == SessionReady || session.ResumeStatus == SessionAwaitingInput) {
		if session.Status == SessionPaused {
			session.Status = session.ResumeStatus
		}
		session.ResumeStatus, session.PauseReason = "", ""
		if err := r.persistSessionLocked(ctx, session); err != nil {
			r.mu.Unlock()
			return err
		}
		r.closing = true
		r.mu.Unlock()
		r.finishRun(id)
		return nil
	}
	input := "继续当前计划对话；沿用已有内容，需要用户补充时提问并等待。"
	if err := r.invalidateReadyLocked(ctx, &session); err != nil {
		r.mu.Unlock()
		return err
	}
	if len(session.PendingInputs) > 0 {
		input = session.PendingInputs[0]
		session.PendingInputs = session.PendingInputs[1:]
	}
	session.Status = SessionDrafting
	session.ResumeStatus, session.PauseReason = "", ""
	session.LastDecision = "user resumed the plan conversation"
	session.UpdatedAt = r.now()
	if err := r.persistSessionLocked(ctx, session); err != nil {
		r.mu.Unlock()
		return err
	}
	runCtx, cancel := context.WithCancel(context.Background())
	r.active, r.activeID, r.cancel = true, id, cancel
	r.mu.Unlock()
	r.emit(Event{Type: EventResumed, PlanID: id, Status: session.Status, At: r.now()})
	go r.runAsync(runCtx, id, input)
	return nil
}

// Restore attaches a persisted session snapshot without executing model work.
// Non-terminal work is made explicitly resumable from its previous status.
func (r *Runtime) Restore(snapshot Session) error {
	if r == nil {
		return errors.New("plan runtime is nil")
	}
	if snapshot.ID == "" {
		return errors.New("plan id is empty")
	}
	if snapshot.Status.Terminal() {
		return fmt.Errorf("plan %q is terminal at %s", snapshot.ID, snapshot.Status)
	}
	if len(snapshot.PendingInputs) > 0 {
		snapshot.Status = SessionPaused
		snapshot.ResumeStatus = SessionDrafting
		snapshot.PauseReason = PauseUserInputRequired
	}
	if snapshot.Status == SessionAwaitingApprov {
		snapshot.Status = SessionAwaitingInput
	}
	resumeStatus := snapshot.Status
	if resumeStatus == SessionPaused && snapshot.ResumeStatus != "" {
		resumeStatus = snapshot.ResumeStatus
	}
	if resumeStatus == SessionPaused || resumeStatus.Terminal() || resumeStatus == "" {
		resumeStatus = SessionClarifying
	}
	if snapshot.Status != SessionReady && snapshot.Status != SessionAwaitingInput {
		snapshot.ResumeStatus = resumeStatus
	}
	if snapshot.Status != SessionPaused && snapshot.Status != SessionReady && snapshot.Status != SessionAwaitingInput {
		if err := snapshot.Transition(SessionPaused, PauseUserInputRequired); err != nil {
			return err
		}
	}
	r.mu.Lock()
	if r.active || r.closing {
		r.mu.Unlock()
		return ErrPlanActive
	}
	if err := r.persistSessionLocked(context.Background(), snapshot); err != nil {
		r.mu.Unlock()
		return err
	}
	r.activeID = snapshot.ID
	r.mu.Unlock()
	return nil
}

// Cancel stops the active plan session.
func (r *Runtime) Cancel(ctx context.Context, id PlanID) error {
	r.mu.Lock()
	if r.closing && !r.active {
		r.mu.Unlock()
		return ErrPlanActive
	}
	if r.activeID == id && r.cancel != nil {
		r.cancel()
	}
	session, ok := r.memSessions[id]
	if !ok {
		r.mu.Unlock()
		return ErrPlanNotFound
	}
	if !session.Status.Terminal() {
		_ = session.Transition(SessionCancelled, "")
		session.PendingInputs = nil
		if err := r.persistSessionLocked(ctx, session); err != nil {
			r.mu.Unlock()
			return err
		}
		active := r.active
		if !active {
			r.closing = true
		}
		r.mu.Unlock()
		if !active {
			r.finishRun(id)
		}
		return nil
	}
	r.mu.Unlock()
	return nil
}

// Status returns the session snapshot for a plan id.
func (r *Runtime) Status(ctx context.Context, id PlanID) (Session, error) {
	s, ok := r.loadSession(id)
	if !ok {
		return Session{}, ErrPlanNotFound
	}
	return s.Snapshot(), nil
}

// List returns all persisted plan documents.
func (r *Runtime) List(ctx context.Context) ([]PlanDoc, error) {
	return r.store.List(ctx)
}

// Show returns one persisted plan document.
func (r *Runtime) Show(ctx context.Context, id PlanID) (PlanDoc, bool, error) {
	return r.store.Get(ctx, id)
}

// ActiveID returns the currently running plan id, if any.
func (r *Runtime) ActiveID() PlanID {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.activeID
}

func (r *Runtime) loadSession(id PlanID) (Session, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.memSessions[id]
	return s, ok
}

func (r *Runtime) storeSession(ctx context.Context, s Session) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.persistSessionLocked(ctx, s)
}

func (r *Runtime) persistSessionLocked(ctx context.Context, s Session) error {
	if s.Status.Terminal() {
		if r.clear != nil {
			if err := r.clear(ctx); err != nil {
				return err
			}
		}
	} else if r.snapshot != nil {
		if err := r.snapshot(ctx, s.Snapshot()); err != nil {
			return err
		}
	}
	r.memSessions[s.ID] = s
	return nil
}

func (r *Runtime) taskEvents(id PlanID, sinkErr *error) loop.TaskEventSink {
	return func(e loop.TaskEvent) {
		if sinkErr != nil && *sinkErr != nil {
			return
		}
		r.mu.Lock()
		session := r.memSessions[id]
		if e.Type == loop.TaskEventTurnDone {
			session.TurnsUsed++
			session.UpdatedAt = r.now()
			if err := r.persistSessionLocked(context.Background(), session); err != nil {
				if sinkErr != nil {
					*sinkErr = err
				}
				r.mu.Unlock()
				return
			}
			r.mu.Unlock()
			r.emit(Event{Type: EventTurnDone, PlanID: id, Status: session.Status, Decision: session.LastDecision, At: r.now()})
			return
		}
		if e.Type == loop.TaskEventContinued {
			session.Continuations = e.ContinuationUsed
			session.UpdatedAt = r.now()
			if err := r.persistSessionLocked(context.Background(), session); err != nil && sinkErr != nil {
				*sinkErr = err
			}
		}
		r.mu.Unlock()
	}
}

func (r *Runtime) emit(e Event) {
	if e.At.IsZero() {
		e.At = r.now()
	}
	if r.events != nil {
		r.events(e)
	}
}
