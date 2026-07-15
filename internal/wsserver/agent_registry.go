package wsserver

import (
	"context"
	"crypto/sha256"
	"fmt"
	"sort"
	"sync"
	"time"

	"codex-agent-go/internal/loop"
	"codex-agent-go/internal/subagent"
)

// AgentStatus is the lifecycle state of a persona slot.
type AgentStatus string

const (
	AgentStatusIdle    AgentStatus = "idle"
	AgentStatusPending AgentStatus = "pending"
	AgentStatusRunning AgentStatus = "running"
	AgentStatusDone    AgentStatus = "done"
	AgentStatusFailed  AgentStatus = "failed"
	AgentStatusStopped AgentStatus = "stopped"
)

// RunnerFactory creates a new Runner for a persona's conversation session.
// sessionID is the persona's stable UUID (derived from the persona name).
type RunnerFactory func(ctx context.Context, sessionID string) (*loop.Runner, error)

// personaSlot holds the state of a single persona.
type personaSlot struct {
	id         string
	name       string
	color      string
	status     AgentStatus
	runner     *loop.Runner // independent conversation; retained after task completion
	startedAt  *time.Time
	finishedAt *time.Time
	taskID     string
	seenTasks  map[string]struct{}
	index      int // original order in defaultPersonas, for stable idle sorting
}

// AgentRegistry manages all 40 persona slots and routes user input to their Runners.
type AgentRegistry struct {
	mu      sync.RWMutex
	slots   []*personaSlot
	byID    map[string]*personaSlot
	byName  map[string]*personaSlot
	factory RunnerFactory
}

// statusPriority maps AgentStatus to sort priority (lower = higher in list).
var statusPriority = map[AgentStatus]int{
	AgentStatusRunning: 0,
	AgentStatusPending: 1,
	AgentStatusDone:    2,
	AgentStatusFailed:  2,
	AgentStatusStopped: 2,
	AgentStatusIdle:    3,
}

// personaID generates a stable UUID v4-format string from a persona name.
// The same name always produces the same ID regardless of restart.
func personaID(name string) string {
	h := sha256.Sum256([]byte("paw-persona:" + name))
	b := h[:16]
	b[6] = (b[6] & 0x0f) | 0x40 // set version 4
	b[8] = (b[8] & 0x3f) | 0x80 // set variant bits
	return fmt.Sprintf("%x-%x-%x-%x-%x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// NewAgentRegistry initialises all 40 persona slots in idle state.
// factory is called by Activate to create a Runner when a persona becomes active.
func NewAgentRegistry(factory RunnerFactory) *AgentRegistry {
	personas := subagent.Personas()
	r := &AgentRegistry{
		byID:    make(map[string]*personaSlot, len(personas)),
		byName:  make(map[string]*personaSlot, len(personas)),
		factory: factory,
	}
	for i, p := range personas {
		id := personaID(p.Name)
		slot := &personaSlot{
			id:        id,
			name:      p.Name,
			color:     p.Color,
			status:    AgentStatusIdle,
			seenTasks: make(map[string]struct{}),
			index:     i,
		}
		r.slots = append(r.slots, slot)
		r.byID[id] = slot
		r.byName[p.Name] = slot
	}
	return r
}

// Activate transitions the named persona to running and creates its Runner via factory.
// Returns the persona's stable ID and ok=true on success.
// Returns ok=false if the name is unknown or the factory returns an error (slot is rolled back).
func (r *AgentRegistry) Activate(ctx context.Context, name string) (id string, ok bool) {
	return r.ActivateTask(ctx, name, "")
}

// ActivateTask binds an activation to one task generation. Duplicate or stale
// task IDs cannot replace a running generation or overwrite its terminal state.
func (r *AgentRegistry) ActivateTask(ctx context.Context, name, taskID string) (id string, ok bool) {
	r.mu.Lock()
	slot, exists := r.byName[name]
	if !exists {
		r.mu.Unlock()
		return "", false
	}
	if slot.status == AgentStatusPending || slot.status == AgentStatusRunning {
		r.mu.Unlock()
		return slot.id, false
	}
	if taskID != "" {
		if _, seen := slot.seenTasks[taskID]; seen {
			r.mu.Unlock()
			return slot.id, false
		}
		slot.seenTasks[taskID] = struct{}{}
	}
	previousStatus := slot.status
	previousTaskID := slot.taskID
	previousStartedAt := slot.startedAt
	previousFinishedAt := slot.finishedAt
	// Use pending while factory creates the runner.
	slot.status = AgentStatusPending
	slot.taskID = taskID
	now := time.Now().UTC()
	slot.startedAt = &now
	slot.finishedAt = nil
	id = slot.id
	factory := r.factory
	r.mu.Unlock()

	// Create runner outside the lock — factory may be slow.
	runner, err := factory(ctx, id)
	if err != nil {
		r.mu.Lock()
		if slot.status == AgentStatusPending && slot.taskID == taskID {
			slot.status = previousStatus
			slot.taskID = previousTaskID
			slot.startedAt = previousStartedAt
			slot.finishedAt = previousFinishedAt
			delete(slot.seenTasks, taskID)
		}
		r.mu.Unlock()
		return "", false
	}

	r.mu.Lock()
	if slot.status != AgentStatusPending || slot.taskID != taskID {
		r.mu.Unlock()
		return id, false
	}
	slot.status = AgentStatusRunning // Now truly running with a valid runner.
	slot.runner = runner
	r.mu.Unlock()
	return id, true
}

// Deactivate preserves the legacy completed-task behavior.
func (r *AgentRegistry) Deactivate(name string) {
	r.FinishTask(name, "", AgentStatusDone)
}

// Finish transitions the named persona to an exact terminal state.
func (r *AgentRegistry) Finish(name string, status AgentStatus) bool {
	return r.FinishTask(name, "", status)
}

// FinishTask accepts the first terminal transition for the active task
// generation and retains the independent conversation Runner.
func (r *AgentRegistry) FinishTask(name, taskID string, status AgentStatus) bool {
	if !isTerminalAgentStatus(status) {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	slot, ok := r.byName[name]
	if !ok {
		return false
	}
	if slot.taskID != taskID || isTerminalAgentStatus(slot.status) {
		return false
	}
	slot.status = status
	now := time.Now().UTC()
	slot.finishedAt = &now
	return true
}

func isTerminalAgentStatus(status AgentStatus) bool {
	switch status {
	case AgentStatusDone, AgentStatusFailed, AgentStatusStopped:
		return true
	default:
		return false
	}
}

// RouteInput delivers text to the named agent's independent conversation.
// Returns an error if the conversation has never been activated.
func (r *AgentRegistry) RouteInput(ctx context.Context, agentID string, text string) error {
	r.mu.RLock()
	slot, ok := r.byID[agentID]
	if !ok || slot.runner == nil {
		r.mu.RUnlock()
		return fmt.Errorf("agent %s: conversation unavailable", agentID)
	}
	runner := slot.runner
	r.mu.RUnlock()

	_, err := runner.RunTurn(ctx, text)
	return err
}

// Snapshot returns a sorted copy of all agent states.
// Order: running (newest first) > pending > terminal > idle (original persona order).
func (r *AgentRegistry) Snapshot() []loop.AgentInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()

	infos := make([]loop.AgentInfo, len(r.slots))
	for i, slot := range r.slots {
		infos[i] = loop.AgentInfo{
			ID:                    slot.id,
			Name:                  slot.name,
			Color:                 slot.color,
			Status:                string(slot.status),
			TaskID:                slot.taskID,
			StartedAt:             slot.startedAt,
			FinishedAt:            slot.finishedAt,
			ConversationAvailable: slot.runner != nil,
		}
	}

	sort.SliceStable(infos, func(i, j int) bool {
		pi := statusPriority[AgentStatus(infos[i].Status)]
		pj := statusPriority[AgentStatus(infos[j].Status)]
		if pi != pj {
			return pi < pj
		}
		// Within running/pending/done: most recently started first.
		si, sj := infos[i].StartedAt, infos[j].StartedAt
		if si != nil && sj != nil {
			return si.After(*sj)
		}
		// Idle: SliceStable preserves the original slot order (slot.index order).
		return false
	})

	return infos
}
