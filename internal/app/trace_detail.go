package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

const (
	TraceDetailMaxBytes = 2 * 1024 * 1024
	traceDetailCacheMax = 256
)

var ErrTraceDetailNotFound = errors.New("trace_detail_not_found")

type TraceDetail struct {
	ID        string    `json:"id"`
	Kind      string    `json:"kind"`
	Content   string    `json:"content"`
	Truncated bool      `json:"truncated"`
	CreatedAt time.Time `json:"created_at"`
}

type TraceDetailStore struct {
	now func() time.Time

	mu     sync.RWMutex
	items  map[string]TraceDetail
	events *EventHub
}

func NewTraceDetailStore(events *EventHub) *TraceDetailStore {
	return &TraceDetailStore{now: time.Now, items: make(map[string]TraceDetail), events: events}
}

func (s *TraceDetailStore) Put(kind, content string) (string, bool) {
	if s == nil {
		return "", false
	}
	content = strings.TrimSpace(content)
	if content == "" {
		return "", false
	}
	truncated := false
	if len(content) > TraceDetailMaxBytes {
		content = content[:TraceDetailMaxBytes]
		truncated = true
	}
	sum := sha256.Sum256([]byte(kind + "\x00" + content))
	id := "detail_" + hex.EncodeToString(sum[:12])

	s.mu.Lock()
	s.items[id] = TraceDetail{ID: id, Kind: kind, Content: content, Truncated: truncated, CreatedAt: s.now().UTC()}
	for len(s.items) > traceDetailCacheMax {
		for existing := range s.items {
			delete(s.items, existing)
			break
		}
	}
	s.mu.Unlock()
	return id, true
}

func (s *TraceDetailStore) Get(_ context.Context, id string) (TraceDetail, error) {
	if s == nil {
		return TraceDetail{}, ErrTraceDetailNotFound
	}
	id = strings.TrimSpace(id)
	if !strings.HasPrefix(id, "detail_") {
		return TraceDetail{}, fmt.Errorf("%w: invalid detail ID", ErrTraceDetailNotFound)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	detail, ok := s.items[id]
	if !ok {
		return TraceDetail{}, ErrTraceDetailNotFound
	}
	return detail, nil
}
