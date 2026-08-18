package goal

import (
	"context"
	"fmt"
	"paw/internal/loop"
	"paw/internal/todo"
	"strings"
	"sync"
)

// SessionController adapts the Goal Runtime to the small Bubble Tea
// controller interface. It deliberately keeps the active Goal ID in memory:
// MVP Goals are session-scoped and explicit resume is required.
type SessionController struct {
	runtime   *Runtime
	sessionID string
	mu        sync.Mutex
	activeID  GoalID
	stopped   func(reason string)
}

// NewSessionController builds the session-scoped goal controller.
// storeDir is the session store root; an empty value falls back to the
// in-memory store (headless, no persistence; evidence/checkpoint stay off).
func NewSessionController(sessionID string, runner *loop.Runner, broker *todo.Broker, storeDir string) *SessionController {
	var source TodoSource
	if broker != nil {
		source = broker.Latest
	}
	c := &SessionController{sessionID: sessionID}
	// 事件溯源存储：goal 状态（含 evidence/checkpoint 子状态）持久化到
	// <storeDir>/goals/ 事件流；storeDir 为空时回退到内存存储。
	var docStore GoalStore
	var evidenceStore EvidenceStore
	var checkpointStore CheckpointStore
	if storeDir != "" {
		if esStore, err := NewEventStore(storeDir); err == nil {
			docStore = esStore
			evidenceStore = esStore.EvidenceStore()
			checkpointStore = esStore.CheckpointStore()
		}
	}
	c.runtime = NewRuntime(RuntimeConfig{
		Store:       docStore,
		Executor:    runner.GoalTurnExecutor(),
		Todo:        source,
		Evidence:    evidenceStore,
		Checkpoints: checkpointStore,
		Events: func(e Event) {
			switch e.Type {
			// 会话结束（完成/失败/取消）或暂停：goal 不再占用前台工作态，
			// 通知 UI 释放 goalWorking（否则 header 永久显示 working）。
			case EventCompleted, EventFailed, EventCancelled, EventPaused:
				if c.stopped != nil {
					c.stopped(string(e.Type))
				}
			}
		},
	})
	return c
}

// SetStopped wires the goal-session end callback (TUI releases working state).
func (c *SessionController) SetStopped(fn func(reason string)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stopped = fn
}

// Start launches a goal session for the trimmed objective and remembers its ID.
func (c *SessionController) Start(objective string) (string, error) {
	if c == nil || c.runtime == nil {
		return "", fmt.Errorf("goal runtime is unavailable")
	}
	objective = strings.TrimSpace(objective)
	if objective == "" {
		return "", fmt.Errorf("goal objective is empty")
	}
	snapshot, err := c.runtime.Start(context.Background(), Goal{SessionID: c.sessionID, Objective: objective})
	if err != nil {
		return "", err
	}
	c.mu.Lock()
	c.activeID = snapshot.ID
	c.mu.Unlock()
	return string(snapshot.ID), nil
}

// current resolves the active goal snapshot, failing when no goal is active.
func (c *SessionController) current() (GoalSnapshot, error) {
	if c == nil || c.runtime == nil {
		return GoalSnapshot{}, fmt.Errorf("goal runtime is unavailable")
	}
	c.mu.Lock()
	id := c.activeID
	c.mu.Unlock()
	if id == "" {
		return GoalSnapshot{}, fmt.Errorf("no active goal")
	}
	return c.runtime.Status(context.Background(), id)
}

// Status renders the active goal snapshot as a text block.
func (c *SessionController) Status() string {
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

// Pause suspends the active goal session.
func (c *SessionController) Pause() error {
	s, err := c.current()
	if err != nil {
		return err
	}
	return c.runtime.Pause(context.Background(), s.ID)
}

// Resume continues a paused goal session.
func (c *SessionController) Resume() error {
	s, err := c.current()
	if err != nil {
		return err
	}
	return c.runtime.Resume(context.Background(), s.ID)
}

// Cancel aborts the active goal session.
func (c *SessionController) Cancel() error {
	s, err := c.current()
	if err != nil {
		return err
	}
	return c.runtime.Cancel(context.Background(), s.ID)
}

// Budget renders the active goal budget counters and deadline.
func (c *SessionController) Budget() string {
	s, err := c.current()
	if err != nil {
		return err.Error()
	}
	return fmt.Sprintf("turns: %d/%d\ntool calls: %d/%d\ncontinuations: %d/%d\nno-progress: %d/%d\ndeadline: %s", s.TurnsUsed, s.Budget.MaxTurns, s.ToolCallsUsed, s.Budget.MaxToolCalls, s.ContinuationUsed, s.Budget.MaxContinuations, s.NoProgressCount, s.Budget.MaxNoProgress, s.Budget.Deadline.Format("2006-01-02T15:04:05Z07:00"))
}

// Compile-time assertion: satisfies the Bubble Tea goal controller interface
// (Start/Status/Pause/Resume/Cancel/Budget) without importing the UI package.
var _ interface {
	Start(string) (string, error)
	Status() string
	Pause() error
	Resume() error
	Cancel() error
	Budget() string
} = (*SessionController)(nil)
