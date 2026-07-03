package loop

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// SessionEventStore persists and retrieves SessionEvents for a session.
type SessionEventStore interface {
	Append(ctx context.Context, sessionID string, events ...SessionEvent) error
	Load(ctx context.Context, sessionID string) ([]SessionEvent, error)
}

// InMemorySessionEventStore is a thread-safe in-memory implementation
// suitable for tests.
type InMemorySessionEventStore struct {
	mu     sync.RWMutex
	events map[string][]SessionEvent
}

// NewInMemorySessionEventStore creates an empty InMemorySessionEventStore.
func NewInMemorySessionEventStore() *InMemorySessionEventStore {
	return &InMemorySessionEventStore{
		events: make(map[string][]SessionEvent),
	}
}

// Append adds events for the given session.
func (s *InMemorySessionEventStore) Append(_ context.Context, sessionID string, events ...SessionEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events[sessionID] = append(s.events[sessionID], events...)
	return nil
}

// Load returns all events for the given session in append order.
func (s *InMemorySessionEventStore) Load(_ context.Context, sessionID string) ([]SessionEvent, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	evs := s.events[sessionID]
	if len(evs) == 0 {
		return nil, nil
	}
	out := make([]SessionEvent, len(evs))
	copy(out, evs)
	return out, nil
}

// JSONLSessionEventStore persists each event as a JSON line to a file named
// <sessionID>-events.jsonl inside Dir.
type JSONLSessionEventStore struct {
	Dir string
}

// NewJSONLSessionEventStore creates a store that writes to dir.
func NewJSONLSessionEventStore(dir string) *JSONLSessionEventStore {
	return &JSONLSessionEventStore{Dir: dir}
}

func (s *JSONLSessionEventStore) filePath(sessionID string) string {
	return filepath.Join(s.Dir, sessionID+"-events.jsonl")
}

// Append marshals each event as a JSON line and appends to the session file.
func (s *JSONLSessionEventStore) Append(_ context.Context, sessionID string, events ...SessionEvent) error {
	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
		return fmt.Errorf("session_event_store: mkdir %s: %w", s.Dir, err)
	}
	f, err := os.OpenFile(s.filePath(sessionID), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("session_event_store: open: %w", err)
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for _, ev := range events {
		if err := enc.Encode(ev); err != nil {
			return fmt.Errorf("session_event_store: encode event %s: %w", ev.ID, err)
		}
	}
	return nil
}

// Load reads all events from the session JSONL file.
// Returns nil, nil when the file does not exist.
func (s *JSONLSessionEventStore) Load(_ context.Context, sessionID string) ([]SessionEvent, error) {
	f, err := os.Open(s.filePath(sessionID))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("session_event_store: open: %w", err)
	}
	defer f.Close()
	var events []SessionEvent
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var ev SessionEvent
		if err := json.Unmarshal(line, &ev); err != nil {
			return nil, fmt.Errorf("session_event_store: unmarshal: %w", err)
		}
		events = append(events, ev)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("session_event_store: scan: %w", err)
	}
	return events, nil
}
