package app

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

var (
	ErrRuntimeCapacity = errors.New("runtime_capacity")
	ErrWorkspaceBusy   = errors.New("workspace_busy")
	ErrRuntimeNotFound = errors.New("runtime_not_found")
)

type RuntimeFactory func(context.Context, WorkspaceRuntimeOptions) (*WorkspaceRuntime, error)

type RuntimeActivity struct {
	ActiveTurn          int `json:"active_turn"`
	ActiveTasks         int `json:"active_tasks"`
	PendingInteractions int `json:"pending_interactions"`
	QueuedInputs        int `json:"queued_inputs"`
	ActiveWrites        int `json:"active_writes"`
}

func (a RuntimeActivity) Busy() bool {
	return a.ActiveTurn > 0 || a.ActiveTasks > 0 || a.PendingInteractions > 0 || a.QueuedInputs > 0 || a.ActiveWrites > 0
}

type SupervisorConfig struct {
	Capacity int
	Factory  RuntimeFactory
	Recent   *RecentWorkspaceStore
}

type runtimeEntryState uint8

const (
	runtimeOpening runtimeEntryState = iota + 1
	runtimeLoaded
	runtimeClosing
)

type runtimeEntry struct {
	workspace WorkspacePath
	runtime   *WorkspaceRuntime
	activity  RuntimeActivity
	state     runtimeEntryState
	access    uint64
	done      chan struct{}
}

type Supervisor struct {
	mu       sync.Mutex
	capacity int
	factory  RuntimeFactory
	recent   *RecentWorkspaceStore
	entries  map[WorkspaceID]*runtimeEntry
	access   uint64
}

func NewSupervisor(cfg SupervisorConfig) *Supervisor {
	capacity := cfg.Capacity
	if capacity <= 0 {
		capacity = 2
	}
	factory := cfg.Factory
	if factory == nil {
		factory = func(ctx context.Context, opts WorkspaceRuntimeOptions) (*WorkspaceRuntime, error) {
			return BuildWorkspaceRuntime(ctx, opts)
		}
	}
	return &Supervisor{
		capacity: capacity,
		factory:  factory,
		recent:   cfg.Recent,
		entries:  make(map[WorkspaceID]*runtimeEntry),
	}
}

func (s *Supervisor) Open(ctx context.Context, opts WorkspaceRuntimeOptions) (*WorkspaceRuntime, error) {
	if s == nil {
		return nil, errors.New("supervisor is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	workspace, err := CanonicalWorkspace(opts.Root)
	if err != nil {
		return nil, err
	}
	opts.Root = workspace.Path

	for {
		s.mu.Lock()
		if entry := s.entries[workspace.ID]; entry != nil {
			s.touchLocked(entry)
			if entry.state == runtimeLoaded {
				runtime := entry.runtime
				s.mu.Unlock()
				return runtime, nil
			}
			done := entry.done
			s.mu.Unlock()
			select {
			case <-done:
				continue
			case <-ctx.Done():
				return nil, context.Cause(ctx)
			}
		}

		if len(s.entries) >= s.capacity {
			s.refreshActivitiesLocked()
			victim := s.oldestIdleLocked()
			if victim == nil {
				s.mu.Unlock()
				return nil, ErrRuntimeCapacity
			}
			victim.state = runtimeClosing
			victim.done = make(chan struct{})
			s.mu.Unlock()
			if err := s.finishClose(victim); err != nil {
				return nil, fmt.Errorf("evict workspace %s: %w", victim.workspace.ID, err)
			}
			continue
		}

		entry := &runtimeEntry{workspace: workspace, state: runtimeOpening, done: make(chan struct{})}
		s.touchLocked(entry)
		s.entries[workspace.ID] = entry
		s.mu.Unlock()

		runtime, buildErr := s.factory(ctx, opts)
		s.mu.Lock()
		current := s.entries[workspace.ID]
		if current == entry {
			if buildErr != nil {
				delete(s.entries, workspace.ID)
			} else {
				entry.runtime = runtime
				entry.state = runtimeLoaded
				s.touchLocked(entry)
			}
			close(entry.done)
		}
		s.mu.Unlock()
		if buildErr != nil {
			return nil, buildErr
		}
		if s.recent != nil {
			if err := s.recent.Remember(ctx, workspace); err != nil {
				_ = s.Close(context.Background(), workspace.ID)
				return nil, err
			}
		}
		return runtime, nil
	}
}

func (s *Supervisor) SetActivity(id WorkspaceID, activity RuntimeActivity) error {
	if s == nil {
		return errors.New("supervisor is nil")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entry := s.entries[id]
	if entry == nil || entry.state != runtimeLoaded {
		return ErrRuntimeNotFound
	}
	entry.activity = activity
	s.touchLocked(entry)
	return nil
}

func (s *Supervisor) Activity(id WorkspaceID) (RuntimeActivity, bool) {
	if s == nil {
		return RuntimeActivity{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entry := s.entries[id]
	if entry == nil || entry.state != runtimeLoaded {
		return RuntimeActivity{}, false
	}
	s.refreshEntryActivityLocked(entry)
	return entry.activity, true
}

func (s *Supervisor) LoadedWorkspaces() []RecentWorkspace {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	items := make([]RecentWorkspace, 0, len(s.entries))
	for _, entry := range s.entries {
		if entry.state != runtimeLoaded {
			continue
		}
		items = append(items, RecentWorkspace{ID: entry.workspace.ID, Path: entry.workspace.Path, Name: entry.workspace.Name})
	}
	return items
}

func (s *Supervisor) Runtime(id WorkspaceID) (*WorkspaceRuntime, bool) {
	if s == nil {
		return nil, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entry := s.entries[id]
	if entry == nil || entry.state != runtimeLoaded {
		return nil, false
	}
	s.touchLocked(entry)
	return entry.runtime, true
}

func (s *Supervisor) Close(ctx context.Context, id WorkspaceID) error {
	if s == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	for {
		s.mu.Lock()
		entry := s.entries[id]
		if entry == nil {
			s.mu.Unlock()
			return ErrRuntimeNotFound
		}
		if entry.state != runtimeLoaded {
			done := entry.done
			s.mu.Unlock()
			select {
			case <-done:
				continue
			case <-ctx.Done():
				return context.Cause(ctx)
			}
		}
		s.refreshEntryActivityLocked(entry)
		if entry.activity.Busy() {
			s.mu.Unlock()
			return ErrWorkspaceBusy
		}
		entry.state = runtimeClosing
		entry.done = make(chan struct{})
		s.mu.Unlock()
		return s.finishClose(entry)
	}
}

func (s *Supervisor) Shutdown(ctx context.Context) error {
	if s == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	var entries []*runtimeEntry
	s.mu.Lock()
	for _, entry := range s.entries {
		if entry.state == runtimeLoaded {
			entry.state = runtimeClosing
			entry.done = make(chan struct{})
			entries = append(entries, entry)
		}
	}
	s.mu.Unlock()
	var errs []error
	for _, entry := range entries {
		if err := contextError(ctx); err != nil {
			errs = append(errs, err)
			break
		}
		if err := s.finishClose(entry); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (s *Supervisor) CloseAll(ctx context.Context) error {
	if s == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	var errs []error
	for {
		s.mu.Lock()
		var id WorkspaceID
		for candidate, entry := range s.entries {
			if entry.state == runtimeLoaded && !entry.activity.Busy() {
				id = candidate
				break
			}
		}
		s.mu.Unlock()
		if id == "" {
			break
		}
		if err := s.Close(ctx, id); err != nil {
			errs = append(errs, err)
			break
		}
	}
	return errors.Join(errs...)
}

func (s *Supervisor) ListRecent(ctx context.Context) ([]RecentWorkspace, error) {
	if s == nil || s.recent == nil {
		return nil, nil
	}
	return s.recent.List(ctx)
}

func (s *Supervisor) ForgetRecent(ctx context.Context, id WorkspaceID) error {
	if s == nil || s.recent == nil {
		return nil
	}
	return s.recent.Forget(ctx, id)
}

func (s *Supervisor) LoadedCount() int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	count := 0
	for _, entry := range s.entries {
		if entry.state == runtimeLoaded {
			count++
		}
	}
	return count
}

func (s *Supervisor) touchLocked(entry *runtimeEntry) {
	s.access++
	entry.access = s.access
}

func (s *Supervisor) refreshActivitiesLocked() {
	for _, entry := range s.entries {
		s.refreshEntryActivityLocked(entry)
	}
}

func (s *Supervisor) refreshEntryActivityLocked(entry *runtimeEntry) {
	if entry == nil || entry.runtime == nil || entry.runtime.Coordinator == nil {
		return
	}
	entry.activity = entry.runtime.Coordinator.Activity()
}

func (s *Supervisor) oldestIdleLocked() *runtimeEntry {
	var oldest *runtimeEntry
	for _, entry := range s.entries {
		if entry.state != runtimeLoaded || entry.activity.Busy() {
			continue
		}
		if oldest == nil || entry.access < oldest.access {
			oldest = entry
		}
	}
	return oldest
}

func (s *Supervisor) finishClose(entry *runtimeEntry) error {
	var closeErr error
	if entry != nil && entry.runtime != nil {
		closeErr = entry.runtime.Close()
	}
	s.mu.Lock()
	if current := s.entries[entry.workspace.ID]; current == entry {
		delete(s.entries, entry.workspace.ID)
		close(entry.done)
	}
	s.mu.Unlock()
	return closeErr
}
