package plan

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"paw/internal/loop"
)

type sessionHost interface {
	GoalTurnExecutor() loop.TurnExecutor
	CurrentSessionID() string
	SavePlanFor(context.Context, string, string, string, any) error
	ClearPlanFor(context.Context, string) error
	ActivePlan(context.Context, string) (string, string, json.RawMessage, error)
}

// SessionController adapts the plan Runtime to Bubble Tea and binds it to the
// session currently selected by the Host.
type SessionController struct {
	host   sessionHost
	store  DocStore
	filter loop.ToolFilter

	mu        sync.Mutex
	runtime   *Runtime
	sessionID string
	activeID  PlanID
	notify    func(PlanDoc)
	stopped   func(reason string)
}

func NewSessionController(host sessionHost, plansDir, storeDir string) *SessionController {
	var docStore DocStore = NewFileStore(plansDir)
	if storeDir != "" {
		if esStore, err := NewEventStore(plansDir, storeDir); err == nil {
			docStore = esStore
		}
	}
	c := &SessionController{host: host, store: docStore, filter: ModeFilter(plansDir)}
	if host != nil {
		c.bindRuntime(host.CurrentSessionID())
	}
	return c
}

func (c *SessionController) bindRuntime(sessionID string) {
	runtime := NewRuntime(RuntimeConfig{
		Store:     c.store,
		Executor:  c.host.GoalTurnExecutor(),
		SessionID: sessionID,
		Filter:    c.filter,
		Snapshot: func(ctx context.Context, snapshot Session) error {
			return c.host.SavePlanFor(ctx, sessionID, string(snapshot.ID), string(snapshot.Status), snapshot)
		},
		ClearSnapshot: func(ctx context.Context) error {
			return c.host.ClearPlanFor(ctx, sessionID)
		},
		Events: func(e Event) {
			switch e.Type {
			case EventPaused, EventFailed, EventCancelled:
				c.mu.Lock()
				stopped := c.stopped
				current := c.sessionID == sessionID
				c.mu.Unlock()
				if current && stopped != nil {
					stopped(fmt.Sprintf("%s", e.Status))
				}
			}
		},
		OnFinalized: func(doc PlanDoc) {
			c.mu.Lock()
			current := c.sessionID == sessionID
			if current {
				c.activeID = ""
			}
			notify := c.notify
			c.mu.Unlock()
			if current && notify != nil {
				notify(doc)
			}
		},
	})
	c.runtime = runtime
	c.sessionID = sessionID
	c.activeID = ""
}

func (c *SessionController) SetNotify(fn func(PlanDoc)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.notify = fn
}

func (c *SessionController) SetStopped(fn func(reason string)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stopped = fn
}

func (c *SessionController) Finalize(ctx context.Context, id PlanID, path string) (PlanDoc, error) {
	c.mu.Lock()
	runtime := c.runtime
	c.mu.Unlock()
	if runtime == nil {
		return PlanDoc{}, fmt.Errorf("plan runtime is unavailable")
	}
	return runtime.Finalize(ctx, id, path)
}

func (c *SessionController) Start(requirement string) (string, error) {
	requirement = strings.TrimSpace(requirement)
	if requirement == "" {
		return "", fmt.Errorf("plan requirement is empty")
	}
	if err := c.ensureCurrentSession(); err != nil {
		return "", err
	}
	c.mu.Lock()
	runtime := c.runtime
	c.mu.Unlock()
	session, err := runtime.Start(context.Background(), requirement)
	if err != nil {
		return "", err
	}
	c.mu.Lock()
	c.activeID = session.ID
	c.mu.Unlock()
	return string(session.ID), nil
}

func (c *SessionController) Rebind(sessionID string) error {
	if c == nil || c.host == nil {
		return fmt.Errorf("plan runtime is unavailable")
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return fmt.Errorf("session id is empty")
	}
	id, _, raw, err := c.host.ActivePlan(context.Background(), sessionID)
	if err != nil {
		return err
	}
	c.mu.Lock()
	c.bindRuntime(sessionID)
	runtime := c.runtime
	c.mu.Unlock()
	if id == "" {
		return nil
	}
	var snapshot Session
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		return fmt.Errorf("restore plan snapshot: %w", err)
	}
	if string(snapshot.ID) != id || snapshot.SessionID != sessionID {
		return fmt.Errorf("plan snapshot does not belong to session %s", sessionID)
	}
	if err := runtime.Restore(snapshot); err != nil {
		return err
	}
	c.mu.Lock()
	c.activeID = snapshot.ID
	c.mu.Unlock()
	return nil
}

func (c *SessionController) ensureCurrentSession() error {
	if c == nil || c.host == nil {
		return fmt.Errorf("plan runtime is unavailable")
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

func (c *SessionController) Resume() error {
	if err := c.ensureCurrentSession(); err != nil {
		return err
	}
	c.mu.Lock()
	id, runtime := c.activeID, c.runtime
	c.mu.Unlock()
	if id == "" {
		return fmt.Errorf("no active plan session")
	}
	return runtime.Resume(context.Background(), id)
}

func (c *SessionController) Status() string {
	c.mu.Lock()
	id, runtime := c.activeID, c.runtime
	c.mu.Unlock()
	if id == "" || runtime == nil {
		return "no active plan session"
	}
	s, err := runtime.Status(context.Background(), id)
	if err != nil {
		return err.Error()
	}
	var b strings.Builder
	fmt.Fprintf(&b, "id: %s\nsession: %s\nstatus: %s\nrequirement: %s\nturns: %d/%d\ncontinuations: %d/%d", s.ID, s.SessionID, s.Status, s.Requirement, s.TurnsUsed, s.MaxTurns, s.Continuations, s.MaxContinuations)
	if s.PauseReason != "" {
		b.WriteString("\npause reason: " + string(s.PauseReason))
	}
	if s.LastDecision != "" {
		b.WriteString("\nlast decision: " + s.LastDecision)
	}
	return b.String()
}

func (c *SessionController) List() string {
	c.mu.Lock()
	runtime := c.runtime
	c.mu.Unlock()
	if runtime == nil {
		return "plan runtime is unavailable"
	}
	docs, err := runtime.List(context.Background())
	if err != nil {
		return err.Error()
	}
	if len(docs) == 0 {
		return "no plans yet"
	}
	lines := make([]string, 0, len(docs))
	for _, doc := range docs {
		lines = append(lines, fmt.Sprintf("- %s status=%s title=%s path=%s", doc.ID, doc.Status, doc.Title, doc.Path))
	}
	return strings.Join(lines, "\n")
}

func (c *SessionController) Show(id string) string {
	c.mu.Lock()
	runtime := c.runtime
	c.mu.Unlock()
	if runtime == nil {
		return "plan runtime is unavailable"
	}
	doc, found, err := runtime.Show(context.Background(), PlanID(strings.TrimSpace(id)))
	if err != nil {
		return err.Error()
	}
	if !found {
		return "plan not found: " + id
	}
	return fmt.Sprintf("id: %s\nsession: %s\nstatus: %s\ntitle: %s\npath: %s\n\n%s", doc.ID, doc.SessionID, doc.Status, doc.Title, doc.Path, doc.Content)
}

func (c *SessionController) Cancel() error {
	c.mu.Lock()
	id, runtime := c.activeID, c.runtime
	c.mu.Unlock()
	if id == "" || runtime == nil {
		return fmt.Errorf("no active plan session")
	}
	if err := runtime.Cancel(context.Background(), id); err != nil {
		return err
	}
	c.mu.Lock()
	c.activeID = ""
	c.mu.Unlock()
	return nil
}

var _ interface {
	Start(string) (string, error)
	Status() string
	List() string
	Show(string) string
	Resume() error
	Cancel() error
	Rebind(string) error
} = (*SessionController)(nil)
