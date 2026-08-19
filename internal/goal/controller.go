package goal

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"paw/internal/loop"
	"paw/internal/todo"
)

type sessionHost interface {
	GoalTurnExecutor() loop.TurnExecutor
	CurrentSessionID() string
	ActivateGoalFor(context.Context, string, string, string) error
	ClearGoalFor(context.Context, string) error
	ActiveGoal(context.Context, string) (string, string, error)
}

// SessionController adapts the Goal Runtime to Bubble Tea and resolves the
// active Goal from the Host's current session binding.
type SessionController struct {
	host        sessionHost
	store       GoalStore
	todo        TodoSource
	evidence    EvidenceStore
	checkpoints CheckpointStore

	mu        sync.Mutex
	runtime   *Runtime
	sessionID string
	activeID  GoalID
	stopped   func(reason string)
}

func NewSessionController(host sessionHost, broker *todo.Broker, storeDir string) *SessionController {
	var source TodoSource
	if broker != nil {
		source = broker.Latest
	}
	c := &SessionController{host: host, todo: source}
	if storeDir != "" {
		if esStore, err := NewEventStore(storeDir); err == nil {
			c.store = esStore
			c.evidence = esStore.EvidenceStore()
			c.checkpoints = esStore.CheckpointStore()
		}
	}
	if c.store == nil {
		c.store = NewMemoryStore()
	}
	if host != nil {
		c.bindRuntime(host.CurrentSessionID())
	}
	return c
}

func (c *SessionController) bindRuntime(sessionID string) {
	c.runtime = NewRuntime(RuntimeConfig{
		Store:       c.store,
		Executor:    c.host.GoalTurnExecutor(),
		Todo:        c.todo,
		Evidence:    c.evidence,
		Checkpoints: c.checkpoints,
		Bind: func(ctx context.Context, snapshot GoalSnapshot) error {
			return c.host.ActivateGoalFor(ctx, sessionID, string(snapshot.ID), string(snapshot.Status))
		},
		Clear: func(ctx context.Context) error {
			return c.host.ClearGoalFor(ctx, sessionID)
		},
		Events: func(e Event) {
			terminal := e.Type == EventCompleted || e.Type == EventFailed || e.Type == EventCancelled
			stoppedEvent := terminal || e.Type == EventPaused
			c.mu.Lock()
			current := c.sessionID == sessionID
			if current && terminal {
				c.activeID = ""
			}
			stopped := c.stopped
			c.mu.Unlock()
			if current && stoppedEvent && stopped != nil {
				stopped(string(e.Type))
			}
		},
	})
	c.sessionID = sessionID
	c.activeID = ""
}

func (c *SessionController) SetStopped(fn func(reason string)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stopped = fn
}

func (c *SessionController) Start(objective string) (string, error) {
	if c == nil || c.host == nil {
		return "", fmt.Errorf("goal runtime is unavailable")
	}
	objective = strings.TrimSpace(objective)
	if objective == "" {
		return "", fmt.Errorf("goal objective is empty")
	}
	if err := c.ensureCurrentSession(); err != nil {
		return "", err
	}
	c.mu.Lock()
	runtime, sessionID := c.runtime, c.sessionID
	c.mu.Unlock()
	snapshot, err := runtime.Start(context.Background(), Goal{SessionID: sessionID, Objective: objective})
	if err != nil {
		return "", err
	}
	c.mu.Lock()
	c.activeID = snapshot.ID
	c.mu.Unlock()
	return string(snapshot.ID), nil
}

func (c *SessionController) Rebind(sessionID string) error {
	if c == nil || c.host == nil {
		return fmt.Errorf("goal runtime is unavailable")
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return fmt.Errorf("session id is empty")
	}
	id, _, err := c.host.ActiveGoal(context.Background(), sessionID)
	if err != nil {
		return err
	}
	if id == "" {
		goals, listErr := c.store.List(context.Background(), sessionID)
		if listErr != nil {
			return listErr
		}
		candidates := make([]GoalSnapshot, 0, len(goals))
		for _, snapshot := range goals {
			if !snapshot.Status.Terminal() {
				candidates = append(candidates, snapshot)
			}
		}
		sort.Slice(candidates, func(i, j int) bool {
			if candidates[i].UpdatedAt.Equal(candidates[j].UpdatedAt) {
				return candidates[i].ID < candidates[j].ID
			}
			return candidates[i].UpdatedAt.Before(candidates[j].UpdatedAt)
		})
		if len(candidates) > 0 {
			id = string(candidates[len(candidates)-1].ID)
		}
	}
	c.mu.Lock()
	c.bindRuntime(sessionID)
	runtime := c.runtime
	c.mu.Unlock()
	if id == "" {
		return nil
	}
	snapshot, err := runtime.Recover(context.Background(), GoalID(id))
	if err != nil {
		return err
	}
	if snapshot.SessionID != sessionID {
		return fmt.Errorf("goal %s does not belong to session %s", id, sessionID)
	}
	if snapshot.Status.Terminal() {
		return c.host.ClearGoalFor(context.Background(), sessionID)
	}
	c.mu.Lock()
	c.activeID = snapshot.ID
	c.mu.Unlock()
	return nil
}

func (c *SessionController) ensureCurrentSession() error {
	if c == nil || c.host == nil {
		return fmt.Errorf("goal runtime is unavailable")
	}
	current := c.host.CurrentSessionID()
	c.mu.Lock()
	bound := c.sessionID
	c.mu.Unlock()
	if current == bound {
		return nil
	}
	return c.Rebind(current)
}

func (c *SessionController) current() (GoalSnapshot, error) {
	if err := c.ensureCurrentSession(); err != nil {
		return GoalSnapshot{}, err
	}
	c.mu.Lock()
	id, runtime := c.activeID, c.runtime
	c.mu.Unlock()
	if id == "" {
		return GoalSnapshot{}, fmt.Errorf("no active goal")
	}
	return runtime.Status(context.Background(), id)
}

func (c *SessionController) Status() string {
	snapshot, err := c.current()
	if err != nil {
		return err.Error()
	}
	body := fmt.Sprintf("id: %s\nsession: %s\nobjective: %s\nstatus: %s\ncontinuations: %d/%d\nno-progress: %d", snapshot.ID, snapshot.SessionID, snapshot.Objective, snapshot.Status, snapshot.ContinuationUsed, snapshot.Budget.MaxContinuations, snapshot.NoProgressCount)
	if snapshot.PauseReason != "" {
		body += "\npause reason: " + string(snapshot.PauseReason)
	}
	if snapshot.LastDecision != "" {
		body += "\nlast decision: " + snapshot.LastDecision
	}
	return body
}

func (c *SessionController) Pause() error {
	s, err := c.current()
	if err != nil {
		return err
	}
	c.mu.Lock()
	runtime := c.runtime
	c.mu.Unlock()
	return runtime.Pause(context.Background(), s.ID)
}

func (c *SessionController) Resume() error {
	s, err := c.current()
	if err != nil {
		return err
	}
	c.mu.Lock()
	runtime := c.runtime
	c.mu.Unlock()
	return runtime.Resume(context.Background(), s.ID)
}

func (c *SessionController) Cancel() error {
	s, err := c.current()
	if err != nil {
		return err
	}
	c.mu.Lock()
	runtime := c.runtime
	c.mu.Unlock()
	if err := runtime.Cancel(context.Background(), s.ID); err != nil {
		return err
	}
	c.mu.Lock()
	c.activeID = ""
	c.mu.Unlock()
	return nil
}

func (c *SessionController) Budget() string {
	s, err := c.current()
	if err != nil {
		return err.Error()
	}
	return fmt.Sprintf("turns: %d/%d\ntool calls: %d/%d\ncontinuations: %d/%d\nno-progress: %d/%d\ndeadline: %s", s.TurnsUsed, s.Budget.MaxTurns, s.ToolCallsUsed, s.Budget.MaxToolCalls, s.ContinuationUsed, s.Budget.MaxContinuations, s.NoProgressCount, s.Budget.MaxNoProgress, s.Budget.Deadline.Format("2006-01-02T15:04:05Z07:00"))
}

var _ interface {
	Start(string) (string, error)
	Status() string
	Pause() error
	Resume() error
	Cancel() error
	Budget() string
	Rebind(string) error
} = (*SessionController)(nil)
