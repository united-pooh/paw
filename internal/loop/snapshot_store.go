package loop

import (
	"context"
	"sync"

	"codex-agent-go/internal/message"
	"codex-agent-go/internal/model"
)

// SessionSnapshot is a point-in-time materialised view of a session's state,
// used to avoid replaying the full event log on every read.
type SessionSnapshot struct {
	SessionID         string            `json:"session_id"`
	LastSeq           int64             `json:"last_seq"`
	History           []message.Message `json:"history"`
	Usage             model.Usage       `json:"usage"`
	UsageKnown        bool              `json:"usage_known"`
	SessionUsage      model.Usage       `json:"session_usage"`
	SessionUsageKnown bool              `json:"session_usage_known"`
	Supplements       []string          `json:"supplements"`
}

// SnapshotStore persists and retrieves SessionSnapshots.
type SnapshotStore interface {
	Save(ctx context.Context, snap SessionSnapshot) error
	Load(ctx context.Context, sessionID string) (SessionSnapshot, bool, error)
}

// InMemorySnapshotStore is a thread-safe in-memory SnapshotStore.
// IntervalEvents controls how frequently snapshots should be taken
// (the caller is responsible for enforcing the interval; this field is
// exposed so callers can read the configured value).
type InMemorySnapshotStore struct {
	mu             sync.RWMutex
	snaps          map[string]SessionSnapshot
	IntervalEvents int // default 50
}

// NewInMemorySnapshotStore creates an InMemorySnapshotStore with the default
// snapshot interval.
func NewInMemorySnapshotStore() *InMemorySnapshotStore {
	return &InMemorySnapshotStore{
		snaps:          make(map[string]SessionSnapshot),
		IntervalEvents: 50,
	}
}

// NewInMemorySnapshotStoreWithInterval creates an InMemorySnapshotStore with a
// custom snapshot interval.
func NewInMemorySnapshotStoreWithInterval(intervalEvents int) *InMemorySnapshotStore {
	return &InMemorySnapshotStore{
		snaps:          make(map[string]SessionSnapshot),
		IntervalEvents: intervalEvents,
	}
}

// Save stores the snapshot, replacing any previous one for the same session.
func (s *InMemorySnapshotStore) Save(_ context.Context, snap SessionSnapshot) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.snaps[snap.SessionID] = snap
	return nil
}

// Load retrieves the snapshot for a session. ok is false when none exists.
func (s *InMemorySnapshotStore) Load(_ context.Context, sessionID string) (SessionSnapshot, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	snap, ok := s.snaps[sessionID]
	return snap, ok, nil
}
