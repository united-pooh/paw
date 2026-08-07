package goal

import (
	"context"
	"fmt"
	"sort"
	"sync"
)

type GoalStore interface {
	Create(context.Context, Goal) error
	Get(context.Context, GoalID) (Goal, bool, error)
	Update(context.Context, Goal) error
	List(context.Context, string) ([]GoalSnapshot, error)
	Delete(context.Context, GoalID) error
}

type MemoryStore struct {
	mu    sync.RWMutex
	goals map[GoalID]Goal
}

func NewMemoryStore() *MemoryStore { return &MemoryStore{goals: make(map[GoalID]Goal)} }

func contextErr(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}

func (s *MemoryStore) Create(ctx context.Context, g Goal) error {
	if err := contextErr(ctx); err != nil {
		return err
	}
	if s == nil {
		return fmt.Errorf("goal store is nil")
	}
	if g.ID == "" {
		return fmt.Errorf("goal id is empty")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.goals[g.ID]; ok {
		return fmt.Errorf("goal %q already exists", g.ID)
	}
	g.Budget = g.Budget.Normalize()
	s.goals[g.ID] = g
	return nil
}

func (s *MemoryStore) Get(ctx context.Context, id GoalID) (Goal, bool, error) {
	if err := contextErr(ctx); err != nil {
		return Goal{}, false, err
	}
	if s == nil {
		return Goal{}, false, fmt.Errorf("goal store is nil")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	g, ok := s.goals[id]
	return g, ok, nil
}

func (s *MemoryStore) Update(ctx context.Context, g Goal) error {
	if err := contextErr(ctx); err != nil {
		return err
	}
	if s == nil {
		return fmt.Errorf("goal store is nil")
	}
	if g.ID == "" {
		return fmt.Errorf("goal id is empty")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.goals[g.ID]; !ok {
		return fmt.Errorf("goal %q not found", g.ID)
	}
	g.Budget = g.Budget.Normalize()
	s.goals[g.ID] = g
	return nil
}

func (s *MemoryStore) List(ctx context.Context, sessionID string) ([]GoalSnapshot, error) {
	if err := contextErr(ctx); err != nil {
		return nil, err
	}
	if s == nil {
		return nil, fmt.Errorf("goal store is nil")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]GoalSnapshot, 0)
	for _, g := range s.goals {
		if sessionID == "" || g.SessionID == sessionID {
			out = append(out, g.Snapshot())
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt.Before(out[j].UpdatedAt) })
	return out, nil
}

func (s *MemoryStore) Delete(ctx context.Context, id GoalID) error {
	if err := contextErr(ctx); err != nil {
		return err
	}
	if s == nil {
		return fmt.Errorf("goal store is nil")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.goals[id]; !ok {
		return fmt.Errorf("goal %q not found", id)
	}
	delete(s.goals, id)
	return nil
}
