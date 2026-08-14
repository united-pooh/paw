package eventing

import (
	"context"
	"errors"
	"sync"
)

var ErrRuntimeBusClosed = errors.New("runtime bus is closed")

// RuntimeBus is an in-process notification bus for already committed and
// projected batches. It is deliberately not a durable queue: consumers rebuild
// missed state from the event streams.
type RuntimeBus struct {
	mu          sync.RWMutex
	nextID      uint64
	subscribers map[uint64]chan Commit
	closed      bool
}

func NewRuntimeBus() *RuntimeBus {
	return &RuntimeBus{subscribers: make(map[uint64]chan Commit)}
}

// Subscribe returns a commit channel and an idempotent cancellation function.
// The optional buffer defaults to 16.
func (b *RuntimeBus) Subscribe(buffer ...int) (<-chan Commit, func()) {
	size := 16
	if len(buffer) > 0 && buffer[0] >= 0 {
		size = buffer[0]
	}
	ch := make(chan Commit, size)
	if b == nil {
		close(ch)
		return ch, func() {}
	}

	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		close(ch)
		return ch, func() {}
	}
	id := b.nextID
	b.nextID++
	b.subscribers[id] = ch
	b.mu.Unlock()

	var once sync.Once
	cancel := func() {
		once.Do(func() {
			b.mu.Lock()
			if current, ok := b.subscribers[id]; ok {
				delete(b.subscribers, id)
				close(current)
			}
			b.mu.Unlock()
		})
	}
	return ch, cancel
}

// Publish synchronously fans out a commit. Holding the read lock through sends
// makes cancellation/close safe without allowing sends on a closed channel.
func (b *RuntimeBus) Publish(ctx context.Context, commit Commit) error {
	if b == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	if b.closed {
		return ErrRuntimeBusClosed
	}
	for _, ch := range b.subscribers {
		select {
		case ch <- cloneCommit(commit):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

func (b *RuntimeBus) Close() {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	b.closed = true
	for id, ch := range b.subscribers {
		delete(b.subscribers, id)
		close(ch)
	}
}
