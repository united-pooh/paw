package main

import (
	"context"
	"fmt"
	"paw/internal/goal"
	"paw/internal/loop"
	"paw/internal/todo"
	"strings"
	"sync"
)

// sessionGoalController adapts the Goal Runtime to the small Bubble Tea
// controller interface. It deliberately keeps the active Goal ID in memory:
// MVP Goals are session-scoped and explicit resume is required.
type sessionGoalController struct {
	runtime   *goal.Runtime
	sessionID string
	mu        sync.Mutex
	activeID  goal.GoalID
	stopped   func(reason string)
}

func newSessionGoalController(sessionID string, runner *loop.Runner, broker *todo.Broker) *sessionGoalController {
	var source goal.TodoSource
	if broker != nil {
		source = broker.Latest
	}
	c := &sessionGoalController{sessionID: sessionID}
	c.runtime = goal.NewRuntime(goal.RuntimeConfig{
		Executor: runner.GoalTurnExecutor(),
		Todo:     source,
		Events: func(e goal.Event) {
			switch e.Type {
			// 会话结束（完成/失败/取消）或暂停：goal 不再占用前台工作态，
			// 通知 UI 释放 goalWorking（否则 header 永久显示 working）。
			case goal.EventCompleted, goal.EventFailed, goal.EventCancelled, goal.EventPaused:
				if c.stopped != nil {
					c.stopped(string(e.Type))
				}
			}
		},
	})
	return c
}

// SetStopped wires the goal-session end callback (TUI releases working state).
func (c *sessionGoalController) SetStopped(fn func(reason string)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stopped = fn
}

func (c *sessionGoalController) Start(objective string) (string, error) {
	if c == nil || c.runtime == nil {
		return "", fmt.Errorf("goal runtime is unavailable")
	}
	objective = strings.TrimSpace(objective)
	if objective == "" {
		return "", fmt.Errorf("goal objective is empty")
	}
	snapshot, err := c.runtime.Start(context.Background(), goal.Goal{SessionID: c.sessionID, Objective: objective})
	if err != nil {
		return "", err
	}
	c.mu.Lock()
	c.activeID = snapshot.ID
	c.mu.Unlock()
	return string(snapshot.ID), nil
}

func (c *sessionGoalController) current() (goal.GoalSnapshot, error) {
	if c == nil || c.runtime == nil {
		return goal.GoalSnapshot{}, fmt.Errorf("goal runtime is unavailable")
	}
	c.mu.Lock()
	id := c.activeID
	c.mu.Unlock()
	if id == "" {
		return goal.GoalSnapshot{}, fmt.Errorf("no active goal")
	}
	return c.runtime.Status(context.Background(), id)
}

func (c *sessionGoalController) Status() string {
	snapshot, err := c.current()
	if err != nil {
		return err.Error()
	}
	body := fmt.Sprintf("id: %s\nobjective: %s\nstatus: %s\ncontinuations: %d/%d\nno-progress: %d", snapshot.ID, snapshot.Objective, snapshot.Status, snapshot.ContinuationUsed, snapshot.Budget.MaxContinuations, snapshot.NoProgressCount)
	if snapshot.PauseReason != "" {
		body += "\npause reason: " + string(snapshot.PauseReason)
	}
	if snapshot.LastDecision != "" {
		body += "\nlast decision: " + snapshot.LastDecision
	}
	return body
}

func (c *sessionGoalController) Pause() error {
	s, err := c.current()
	if err != nil {
		return err
	}
	return c.runtime.Pause(context.Background(), s.ID)
}
func (c *sessionGoalController) Resume() error {
	s, err := c.current()
	if err != nil {
		return err
	}
	return c.runtime.Resume(context.Background(), s.ID)
}
func (c *sessionGoalController) Cancel() error {
	s, err := c.current()
	if err != nil {
		return err
	}
	return c.runtime.Cancel(context.Background(), s.ID)
}

func (c *sessionGoalController) Budget() string {
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
} = (*sessionGoalController)(nil)
