package goal

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"

	"paw/internal/message"
	"paw/internal/todo"
)

type GoalCheckpoint struct {
	ID               string
	GoalID           GoalID
	SessionID        string
	Status           GoalStatus
	Objective        string
	TodoSnapshot     todo.Snapshot
	EvidenceIDs      []string
	ContinuationUsed int
	NoProgressCount  int
	LastDecision     string
	ProgressHash     string
	PauseReason      PauseReason
	NextInput        message.Message
	CreatedAt        time.Time
}

func (c GoalCheckpoint) Clone() GoalCheckpoint {
	c.EvidenceIDs = append([]string(nil), c.EvidenceIDs...)
	c.TodoSnapshot = c.TodoSnapshot.Clone()
	c.NextInput = cloneMessage(c.NextInput)
	return c
}

func cloneMessage(m message.Message) message.Message {
	m.Parts = append([]message.ContentPart(nil), m.Parts...)
	for i := range m.Parts {
		if m.Parts[i].Image != nil {
			image := *m.Parts[i].Image
			image.Data = append([]byte(nil), image.Data...)
			m.Parts[i].Image = &image
		}
	}
	if m.ToolUse != nil {
		call := *m.ToolUse
		call.Input = append([]byte(nil), call.Input...)
		m.ToolUse = &call
	}
	m.ToolUses = append([]message.ToolCall(nil), m.ToolUses...)
	for i := range m.ToolUses {
		m.ToolUses[i].Input = append([]byte(nil), m.ToolUses[i].Input...)
	}
	if m.ToolResult != nil {
		result := *m.ToolResult
		m.ToolResult = &result
	}
	m.ToolResults = append([]message.ToolResult(nil), m.ToolResults...)
	m.ProviderData = append([]byte(nil), m.ProviderData...)
	return m
}

type CheckpointStore interface {
	Save(context.Context, GoalCheckpoint) error
	Load(context.Context, GoalID) (GoalCheckpoint, bool, error)
	Latest(context.Context, GoalID) (GoalCheckpoint, bool, error)
	Delete(context.Context, GoalID) error
}

type MemoryCheckpointStore struct {
	mu    sync.RWMutex
	items map[GoalID][]GoalCheckpoint
}

func NewMemoryCheckpointStore() *MemoryCheckpointStore {
	return &MemoryCheckpointStore{items: make(map[GoalID][]GoalCheckpoint)}
}
func (s *MemoryCheckpointStore) Save(ctx context.Context, c GoalCheckpoint) error {
	if err := contextErr(ctx); err != nil {
		return err
	}
	if c.GoalID == "" {
		return errors.New("checkpoint goal id is empty")
	}
	if c.CreatedAt.IsZero() {
		c.CreatedAt = time.Now()
	}
	if c.ID == "" {
		c.ID = string(c.GoalID) + "-" + c.CreatedAt.UTC().Format("20060102150405.000000000")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.items[c.GoalID] = append(s.items[c.GoalID], c.Clone())
	return nil
}
func (s *MemoryCheckpointStore) Load(ctx context.Context, id GoalID) (GoalCheckpoint, bool, error) {
	return s.Latest(ctx, id)
}
func (s *MemoryCheckpointStore) Latest(ctx context.Context, id GoalID) (GoalCheckpoint, bool, error) {
	if err := contextErr(ctx); err != nil {
		return GoalCheckpoint{}, false, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	a := s.items[id]
	if len(a) == 0 {
		return GoalCheckpoint{}, false, nil
	}
	return a[len(a)-1].Clone(), true, nil
}
func (s *MemoryCheckpointStore) Delete(ctx context.Context, id GoalID) error {
	if err := contextErr(ctx); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.items, id)
	return nil
}

// GoalInput is a durable external control signal for a goal.
type GoalInputKind string

const (
	GoalInputSteer   GoalInputKind = "steer"
	GoalInputApprove GoalInputKind = "approve"
	GoalInputReject  GoalInputKind = "reject"
	GoalInputClarify GoalInputKind = "clarify"
	GoalInputResume  GoalInputKind = "resume"
	GoalInputPause   GoalInputKind = "pause"
	GoalInputCancel  GoalInputKind = "cancel"
)

type GoalInput struct {
	ID          string
	GoalID      GoalID
	Kind        GoalInputKind
	Content     string
	PlanVersion int
	CreatedAt   time.Time
	ConsumedAt  *time.Time
}

func (i GoalInput) Clone() GoalInput {
	if i.ConsumedAt != nil {
		t := *i.ConsumedAt
		i.ConsumedAt = &t
	}
	return i
}

type GoalInputQueue interface {
	Enqueue(context.Context, GoalInput) error
	Dequeue(context.Context, GoalID) (GoalInput, bool, error)
	ListPending(context.Context, GoalID) ([]GoalInput, error)
}
type MemoryGoalInputQueue struct {
	mu    sync.Mutex
	items map[GoalID][]GoalInput
}

func NewMemoryGoalInputQueue() *MemoryGoalInputQueue {
	return &MemoryGoalInputQueue{items: make(map[GoalID][]GoalInput)}
}
func (q *MemoryGoalInputQueue) Enqueue(ctx context.Context, i GoalInput) error {
	if err := contextErr(ctx); err != nil {
		return err
	}
	if i.GoalID == "" {
		return errors.New("input goal id is empty")
	}
	if i.ID == "" {
		return errors.New("input id is empty")
	}
	if i.CreatedAt.IsZero() {
		i.CreatedAt = time.Now()
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	q.items[i.GoalID] = append(q.items[i.GoalID], i.Clone())
	return nil
}
func (q *MemoryGoalInputQueue) Dequeue(ctx context.Context, id GoalID) (GoalInput, bool, error) {
	if err := contextErr(ctx); err != nil {
		return GoalInput{}, false, err
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	a := q.items[id]
	for n := range a {
		if a[n].ConsumedAt == nil {
			t := time.Now()
			a[n].ConsumedAt = &t
			q.items[id] = a
			return a[n].Clone(), true, nil
		}
	}
	return GoalInput{}, false, nil
}
func (q *MemoryGoalInputQueue) ListPending(ctx context.Context, id GoalID) ([]GoalInput, error) {
	if err := contextErr(ctx); err != nil {
		return nil, err
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	out := []GoalInput{}
	for _, i := range q.items[id] {
		if i.ConsumedAt == nil {
			out = append(out, i.Clone())
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

var _ CheckpointStore = (*MemoryCheckpointStore)(nil)
var _ GoalInputQueue = (*MemoryGoalInputQueue)(nil)
