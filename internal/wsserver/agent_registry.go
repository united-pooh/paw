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
)

// RunnerFactory creates a new Runner for a persona's conversation session.
// sessionID is the persona's stable UUID (derived from the persona name).
type RunnerFactory func(ctx context.Context, sessionID string) (*loop.Runner, error)

// personaSlot holds the state of a single persona.
type personaSlot struct {
	id        string
	name      string
	color     string
	status    AgentStatus
	runner    *loop.Runner // nil when idle or done
	startedAt *time.Time
	index     int // original order in defaultPersonas, for stable idle sorting
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
			id:     id,
			name:   p.Name,
			color:  p.Color,
			status: AgentStatusIdle,
			index:  i,
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
	r.mu.Lock()
	slot, exists := r.byName[name]
	if !exists {
		r.mu.Unlock()
		return "", false
	}
	slot.status = AgentStatusRunning
	now := time.Now().UTC()
	slot.startedAt = &now
	id = slot.id
	factory := r.factory
	r.mu.Unlock()

	// Create runner outside the lock — factory may be slow.
	runner, err := factory(ctx, id)
	if err != nil {
		r.mu.Lock()
		slot.status = AgentStatusIdle
		slot.startedAt = nil
		r.mu.Unlock()
		return "", false
	}

	r.mu.Lock()
	slot.runner = runner
	r.mu.Unlock()
	return id, true
}

// Deactivate transitions the named persona to done and releases its Runner.
// No-op if the name is unknown.
func (r *AgentRegistry) Deactivate(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	slot, ok := r.byName[name]
	if !ok {
		return
	}
	slot.status = AgentStatusDone
	slot.runner = nil
}

// RouteInput delivers text to the named agent's Runner.RunTurn().
// Returns an error if agentID is unknown or the slot has no active Runner.
func (r *AgentRegistry) RouteInput(ctx context.Context, agentID string, text string) error {
	r.mu.RLock()
	slot, ok := r.byID[agentID]
	if !ok || slot.runner == nil {
		r.mu.RUnlock()
		return fmt.Errorf("agent %s: not found or not running", agentID)
	}
	runner := slot.runner
	r.mu.RUnlock()

	_, err := runner.RunTurn(ctx, text)
	return err
}

// Snapshot returns a sorted copy of all agent states.
// Order: running (newest first) > pending > done > idle (original persona order).
func (r *AgentRegistry) Snapshot() []loop.AgentInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()

	infos := make([]loop.AgentInfo, len(r.slots))
	for i, slot := range r.slots {
		infos[i] = loop.AgentInfo{
			ID:        slot.id,
			Name:      slot.name,
			Color:     slot.color,
			Status:    string(slot.status),
			StartedAt: slot.startedAt,
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
