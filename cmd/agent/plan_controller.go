package main

import (
	"context"
	"fmt"
	"paw/internal/loop"
	"paw/internal/plan"
	"strings"
	"sync"
)

// sessionPlanController adapts the plan Runtime to the small Bubble Tea
// controller interface. Plans are fully independent of Goals: the controller
// only needs a turn executor (the shared runner) and the plans directory.
type sessionPlanController struct {
	runtime   *plan.Runtime
	sessionID string
	mu        sync.Mutex
	activeID  plan.PlanID
	notify    func(plan.PlanDoc)
	stopped   func(reason string)
}

func newSessionPlanController(sessionID string, runner *loop.Runner, plansDir string) *sessionPlanController {
	c := &sessionPlanController{sessionID: sessionID}
	c.runtime = plan.NewRuntime(plan.RuntimeConfig{
		Store:    plan.NewFileStore(plansDir),
		Executor: runner.GoalTurnExecutor(),
		Filter:   plan.ModeFilter(plansDir),
		Events: func(e plan.Event) {
			switch e.Type {
			case plan.EventPaused, plan.EventFailed, plan.EventCancelled:
				if c.stopped != nil {
					c.stopped(fmt.Sprintf("%s", e.Status))
				}
			}
		},
		OnFinalized: func(doc plan.PlanDoc) {
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
func (c *sessionPlanController) SetNotify(fn func(plan.PlanDoc)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.notify = fn
}

// SetStopped wires the non-final end callback (TUI releases working state).
func (c *sessionPlanController) SetStopped(fn func(reason string)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stopped = fn
}

// Finalize approves the plan document; used as the plan_finalize tool hook.
func (c *sessionPlanController) Finalize(ctx context.Context, id plan.PlanID, path string) (plan.PlanDoc, error) {
	return c.runtime.Finalize(ctx, id, path)
}

func (c *sessionPlanController) Start(requirement string) (string, error) {
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

func (c *sessionPlanController) Status() string {
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

func (c *sessionPlanController) List() string {
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

func (c *sessionPlanController) Show(id string) string {
	doc, found, err := c.runtime.Show(context.Background(), plan.PlanID(strings.TrimSpace(id)))
	if err != nil {
		return err.Error()
	}
	if !found {
		return "plan not found: " + id
	}
	return fmt.Sprintf("id: %s\nstatus: %s\ntitle: %s\npath: %s\n\n%s", doc.ID, doc.Status, doc.Title, doc.Path, doc.Content)
}

func (c *sessionPlanController) Cancel() error {
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

var _ interface {
	Start(string) (string, error)
	Status() string
	List() string
	Show(string) string
	Cancel() error
} = (*sessionPlanController)(nil)
