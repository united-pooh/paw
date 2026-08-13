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

	"paw/internal/loop"
	"paw/internal/message"
)

// RuntimeConfig wires a plan runtime to a turn executor, a document store and
// optional observers. Executor must be safe to call from Run; when it also
// implements loop.ToolFilterApplier (the runner's turn executor does), the
// runtime scopes tools and injects plan instructions for its own turns.
type RuntimeConfig struct {
	Store            DocStore
	Executor         loop.TurnExecutor
	Events           EventSink
	Now              func() time.Time
	Filter           loop.ToolFilter
	MaxTurns         int
	MaxContinuations int
	MaxNoProgress    int
	// OnFinalized is invoked once when a plan document is approved.
	OnFinalized func(PlanDoc)
}

var (
	ErrPlanActive    = errors.New("a plan session is already running")
	ErrPlanNotFound  = errors.New("plan not found")
	ErrNoActivePlan  = errors.New("no active plan session")
	ErrPlanFinalized = errors.New("plan session is already approved")
)

type Runtime struct {
	store       DocStore
	executor    loop.TurnExecutor
	events      EventSink
	now         func() time.Time
	filter      loop.ToolFilter
	onFinalized func(PlanDoc)

	maxTurns         int
	maxContinuations int
	maxNoProgress    int

	mu          sync.Mutex
	active      bool
	activeID    PlanID
	cancel      context.CancelFunc
	sequence    uint64
	memSessions map[PlanID]Session
}

func NewRuntime(cfg RuntimeConfig) *Runtime {
	if cfg.Store == nil {
		cfg.Store = NewFileStore("docs/superpowers/plans")
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
		store: cfg.Store, executor: cfg.Executor, events: cfg.Events,
		now: nowFn, filter: cfg.Filter, onFinalized: cfg.OnFinalized,
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
	if r.active {
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
		Status:           SessionClarifying,
		Requirement:      requirement,
		MaxTurns:         r.maxTurns,
		MaxContinuations: r.maxContinuations,
		MaxNoProgress:    r.maxNoProgress,
		CreatedAt:        r.now(),
		UpdatedAt:        r.now(),
	}
	r.memSessions[id] = session
	r.active, r.activeID = true, id
	r.mu.Unlock()
	r.emit(Event{Type: EventStarted, PlanID: id, Status: session.Status, At: r.now()})
	go r.runAsync(id, requirement)
	return session.Snapshot(), nil
}

func (r *Runtime) runAsync(id PlanID, requirement string) {
	ctx, cancel := context.WithCancel(context.Background())
	r.mu.Lock()
	if r.activeID == id {
		r.cancel = cancel
	}
	r.mu.Unlock()
	_ = r.Run(ctx, id, requirement)
	cancel()
}

// Run executes the plan session until the document is approved, the session
// pauses, or an error occurs.
func (r *Runtime) Run(ctx context.Context, id PlanID, requirement string) error {
	if r == nil {
		return errors.New("plan runtime is nil")
	}
	if r.executor == nil {
		return r.finishWithError(ctx, id, errors.New("plan executor is nil"))
	}
	session, ok := r.loadSession(id)
	if !ok {
		return ErrPlanNotFound
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

	// Scope tools and inject plan instructions for the whole session. Restore
	// the previous runner state afterwards.
	applier, scoped := r.executor.(loop.ToolFilterApplier)
	var savedSupplement string
	if scoped {
		savedSupplement = applier.SystemSupplement()
		applier.SetTurnToolFilter(r.filter)
		applier.SetSystemSupplement(Instructions)
		defer func() {
			applier.SetTurnToolFilter(nil)
			applier.SetSystemSupplement(savedSupplement)
		}()
	}

	session.CurrentTaskID = fmt.Sprintf("plan-task-%d", atomic.AddUint64(&r.sequence, 1))
	session.UpdatedAt = r.now()
	r.storeSession(session)
	planPath := filepath.Join(r.store.Dir(), string(id)+".md")
	input := message.Message{
		Role: message.RoleUser,
		Content: "你现在处于 PLAN MODE。需求如下：\n\n" + requirement +
			"\n\n计划文件必须写入：" + planPath + "\n完成澄清与文档撰写后按流程展示并询问【执行/修改】。",
	}
	evaluator := &evaluator{runtime: r, id: id, session: &session}
	task := &loop.Task{ID: session.CurrentTaskID, Input: input, Status: loop.TaskRunning}
	_, runErr := (loop.TaskOrchestrator{Executor: r.executor, Evaluator: evaluator, Events: r.taskEvents(id)}).Run(ctx, task)
	if runErr != nil {
		return r.finishWithError(ctx, id, runErr)
	}

	latest, _ := r.loadSession(id)
	switch latest.Status {
	case SessionApproved:
		r.emit(Event{Type: EventFinalized, PlanID: id, Status: latest.Status, Decision: latest.LastDecision, At: r.now()})
		if doc, found, getErr := r.store.Get(context.Background(), id); getErr == nil && found {
			if r.onFinalized != nil {
				r.onFinalized(doc)
			}
		}
	case SessionFailed:
		r.emit(Event{Type: EventFailed, PlanID: id, Status: latest.Status, Decision: latest.LastDecision, At: r.now()})
	default:
		r.emit(Event{Type: EventPaused, PlanID: id, Status: latest.Status, Decision: latest.LastDecision, At: r.now()})
	}
	return nil
}

func (r *Runtime) finishWithError(ctx context.Context, id PlanID, err error) error {
	session, ok := r.loadSession(id)
	if !ok {
		return err
	}
	if session.Status.Terminal() {
		return err
	}
	_ = session.Transition(SessionFailed, PauseBlocked)
	session.LastDecision = err.Error()
	r.storeSession(session)
	r.emit(Event{Type: EventFailed, PlanID: id, Status: session.Status, Decision: err.Error(), Err: err, At: r.now()})
	return err
}

// Finalize approves the plan document. It is invoked by the plan_finalize
// tool: the path must live under the store directory. The document file is
// the source of truth; this method writes the front matter marker with
// status=approved.
func (r *Runtime) Finalize(ctx context.Context, id PlanID, path string) (PlanDoc, error) {
	if r == nil {
		return PlanDoc{}, errors.New("plan runtime is nil")
	}
	r.mu.Lock()
	if !r.active || r.activeID != id {
		r.mu.Unlock()
		return PlanDoc{}, ErrNoActivePlan
	}
	session := r.memSessions[id]
	if session.Status == SessionApproved {
		r.mu.Unlock()
		return PlanDoc{}, ErrPlanFinalized
	}
	r.mu.Unlock()

	cleanPath, err := r.validatePath(path)
	if err != nil {
		return PlanDoc{}, err
	}
	doc, ok, err := r.store.Get(ctx, id)
	if err != nil {
		return PlanDoc{}, err
	}
	if !ok {
		data, readErr := os.ReadFile(cleanPath)
		if readErr != nil {
			return PlanDoc{}, fmt.Errorf("plan file not found at %s", cleanPath)
		}
		doc, _ = decodeDoc(string(data), string(id), cleanPath)
	}
	doc.ID = id
	doc.Path = cleanPath
	doc.Status = PlanApproved
	if err := r.store.Update(ctx, doc); err != nil {
		return PlanDoc{}, err
	}
	_ = session.Transition(SessionApproved, "")
	session.LastDecision = "plan approved"
	r.storeSession(session)
	r.emit(Event{Type: EventFinalized, PlanID: id, Status: SessionApproved, Decision: session.LastDecision, At: r.now()})
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
	if r.active {
		r.mu.Unlock()
		return ErrPlanActive
	}
	session, ok := r.memSessions[id]
	r.mu.Unlock()
	if !ok {
		return ErrPlanNotFound
	}
	if session.Status != SessionPaused {
		return fmt.Errorf("plan %q is not resumable from %s", id, session.Status)
	}
	if err := session.Transition(SessionClarifying, ""); err != nil {
		return err
	}
	r.mu.Lock()
	r.memSessions[id] = session
	r.active, r.activeID = true, id
	r.mu.Unlock()
	r.emit(Event{Type: EventResumed, PlanID: id, Status: session.Status, At: r.now()})
	go r.runAsync(id, session.Requirement)
	return nil
}

// Cancel stops the active plan session.
func (r *Runtime) Cancel(ctx context.Context, id PlanID) error {
	r.mu.Lock()
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
		r.memSessions[id] = session
		r.mu.Unlock()
		r.emit(Event{Type: EventCancelled, PlanID: id, Status: session.Status, At: r.now()})
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

func (r *Runtime) storeSession(s Session) {
	r.mu.Lock()
	r.memSessions[s.ID] = s
	r.mu.Unlock()
}

func (r *Runtime) taskEvents(id PlanID) loop.TaskEventSink {
	return func(e loop.TaskEvent) {
		session, _ := r.loadSession(id)
		if e.Type == loop.TaskEventTurnDone {
			session.TurnsUsed++
			session.UpdatedAt = r.now()
			if _, ok, err := r.store.Get(context.Background(), id); err == nil && ok {
				if session.Status != SessionApproved && session.Status != SessionPaused {
					session.Status = SessionAwaitingApprov
				}
			} else if session.Status == SessionClarifying || session.Status == SessionPaused {
				session.Status = SessionDrafting
			}
			session.LastDecision = fmt.Sprintf("turn %d completed", e.TurnNumber)
			r.storeSession(session)
			r.emit(Event{Type: EventTurnDone, PlanID: id, Status: session.Status, Decision: session.LastDecision, At: r.now()})
		}
		if e.Type == loop.TaskEventContinued {
			session.Continuations = e.ContinuationUsed
			session.UpdatedAt = r.now()
			r.storeSession(session)
		}
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
