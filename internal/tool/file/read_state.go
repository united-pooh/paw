package file

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"
)

// ReadStateStore records a per-path content hash when files are Read, so that
// Edit/Write can detect external modification between the last Read and the
// write (lost-update / stale-write protection). If no prior Read was recorded
// for a path, Verify is lenient and returns nil.
type ReadStateStore struct {
	mu     sync.Mutex
	states map[string]string
}

func NewReadStateStore() *ReadStateStore {
	return &ReadStateStore{states: make(map[string]string)}
}

// Record stores the content hash for path as the last-seen-on-Read baseline.
func (s *ReadStateStore) Record(path string, content []byte) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.states == nil {
		s.states = make(map[string]string)
	}
	s.states[path] = contentHash(content)
}

// Verify returns an error if a prior Read recorded a hash for path and
// current no longer matches it. Returns nil when no prior Read was recorded.
func (s *ReadStateStore) Verify(path string, current []byte) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	recorded, ok := s.states[path]
	s.mu.Unlock()
	if !ok {
		return nil
	}
	if got := contentHash(current); got != recorded {
		return fmt.Errorf("file has been modified since last read: %s; read it again before editing", path)
	}
	return nil
}

// RecordAfterWrite updates the baseline to the freshly written content so a
// subsequent Edit on the same file does not falsely report stale.
func (s *ReadStateStore) RecordAfterWrite(path string, content []byte) {
	s.Record(path, content)
}

func contentHash(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}
