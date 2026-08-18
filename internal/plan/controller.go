package plan

import (
	"context"
	"fmt"
	"paw/internal/loop"
	"strings"
	"sync"
)

// SessionController adapts the plan Runtime to the small Bubble Tea
// controller interface. Plans are fully independent of Goals: the controller
// only needs a turn executor (the shared runner) and the plans directory.
type SessionController struct {
	runtime   *Runtime
	sessionID string
	mu        sync.Mutex
	activeID  PlanID
	notify    func(PlanDoc)
	stopped   func(reason string)
}

// NewSessionController builds the session-scoped plan controller.
// storeDir is the session store root (~/.paw/projects/<project>); an empty
// value falls back to plain file storage without the event-sourced journal.
func NewSessionController(sessionID string, runner *loop.Runner, plansDir, storeDir string) *SessionController {
	c := &SessionController{sessionID: sessionID}
	// 事件溯源存储：文档变更走 plan 事件流（<storeDir>/plans/），.md 文件保持
	// 为投影产物；storeDir 为空时回退到纯文件存储。
	var docStore DocStore = NewFileStore(plansDir)
	if storeDir != "" {
		if esStore, err := NewEventStore(plansDir, storeDir); err == nil {
			docStore = esStore
		}
	}
	c.runtime = NewRuntime(RuntimeConfig{
		Store:    docStore,
		Executor: runner.GoalTurnExecutor(),
		Filter:   ModeFilter(plansDir),
		Events: func(e Event) {
			switch e.Type {
			case EventPaused, EventFailed, EventCancelled:
				if c.stopped != nil {
					c.stopped(fmt.Sprintf("%s", e.Status))
				}
			}
		},
		OnFinalized: func(doc PlanDoc) {
			c.mu.Lock()
			c.activeID = ""
			c.mu.Unlock()
			if c.notify != nil {
				c.notify(doc)
			}
		},
	})
	return c
}

// SetNotify wires the approved-plan callback (TUI switches to chat execution).
func (c *SessionController) SetNotify(fn func(PlanDoc)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.notify = fn
}

// SetStopped wires the non-final end callback (TUI releases working state).
func (c *SessionController) SetStopped(fn func(reason string)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stopped = fn
}

// Finalize approves the plan document; used as the plan_finalize tool hook.
func (c *SessionController) Finalize(ctx context.Context, id PlanID, path string) (PlanDoc, error) {
	return c.runtime.Finalize(ctx, id, path)
}

// Start launches a plan session for the trimmed requirement and remembers its ID.
func (c *SessionController) Start(requirement string) (string, error) {
	requirement = strings.TrimSpace(requirement)
	if requirement == "" {
		return "", fmt.Errorf("plan requirement is empty")
	}
	session, err := c.runtime.Start(context.Background(), requirement)
	if err != nil {
		return "", err
	}
	c.mu.Lock()
	c.activeID = session.ID
	c.mu.Unlock()
	return string(session.ID), nil
}

// Status renders the active plan session status, or a placeholder when idle.
func (c *SessionController) Status() string {
	c.mu.Lock()
	id := c.activeID
	c.mu.Unlock()
	if id == "" {
		return "no active plan session"
	}
	s, err := c.runtime.Status(context.Background(), id)
	if err != nil {
		return err.Error()
	}
	var b strings.Builder
	fmt.Fprintf(&b, "id: %s\nstatus: %s\nrequirement: %s\nturns: %d/%d\ncontinuations: %d/%d", s.ID, s.Status, s.Requirement, s.TurnsUsed, s.MaxTurns, s.Continuations, s.MaxContinuations)
	if s.PauseReason != "" {
		b.WriteString("\npause reason: " + string(s.PauseReason))
	}
	if s.LastDecision != "" {
		b.WriteString("\nlast decision: " + s.LastDecision)
	}
	return b.String()
}

// List renders every stored plan document as a one-line summary.
func (c *SessionController) List() string {
	docs, err := c.runtime.List(context.Background())
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

// Show renders one plan document by ID (trimmed), including its content.
func (c *SessionController) Show(id string) string {
	doc, found, err := c.runtime.Show(context.Background(), PlanID(strings.TrimSpace(id)))
	if err != nil {
		return err.Error()
	}
	if !found {
		return "plan not found: " + id
	}
	return fmt.Sprintf("id: %s\nstatus: %s\ntitle: %s\npath: %s\n\n%s", doc.ID, doc.Status, doc.Title, doc.Path, doc.Content)
}

// Cancel aborts the active plan session, if one exists.
func (c *SessionController) Cancel() error {
	c.mu.Lock()
	id := c.activeID
	c.mu.Unlock()
	if id == "" {
		return fmt.Errorf("no active plan session")
	}
	if err := c.runtime.Cancel(context.Background(), id); err != nil {
		return err
	}
	c.mu.Lock()
	c.activeID = ""
	c.mu.Unlock()
	return nil
}

// Compile-time assertion: satisfies the Bubble Tea plan controller interface
// (Start/Status/List/Show/Cancel) without importing the UI package.
var _ interface {
	Start(string) (string, error)
	Status() string
	List() string
	Show(string) string
	Cancel() error
} = (*SessionController)(nil)
