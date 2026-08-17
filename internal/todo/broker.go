package todo

import (
	"context"
	"errors"
	"sync"
)

var ErrBrokerClosed = errors.New("todo broker is closed")

type Broker struct {
	mu        sync.Mutex
	events    []Snapshot
	latest    Snapshot
	hasLatest bool
	wake      chan struct{}
	closed    bool
}

func NewBroker() *Broker {
	return &Broker{wake: make(chan struct{}, 1)}
}

func (b *Broker) Publish(snapshot Snapshot) bool {
	if b == nil {
		return false
	}
	copy := snapshot.Clone()
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return false
	}
	b.events = append(b.events, copy)
	b.latest = copy.Clone()
	b.hasLatest = true
	b.mu.Unlock()
	b.signal()
	return true
}

func (b *Broker) Next(ctx context.Context) (Snapshot, error) {
	if b == nil {
		return Snapshot{}, ErrBrokerClosed
	}
	if ctx == nil {
		ctx = context.Background()
	}
	for {
		b.mu.Lock()
		if len(b.events) > 0 {
			snapshot := b.events[0].Clone()
			b.events[0] = Snapshot{}
			b.events = b.events[1:]
			b.mu.Unlock()
			return snapshot, nil
		}
		if b.closed {
			b.mu.Unlock()
			return Snapshot{}, ErrBrokerClosed
		}
		wake := b.wake
		b.mu.Unlock()

		select {
		case <-ctx.Done():
			return Snapshot{}, ctx.Err()
		case <-wake:
		}
	}
}

func (b *Broker) Latest() (Snapshot, bool) {
	if b == nil {
		return Snapshot{}, false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.hasLatest {
		return Snapshot{}, false
	}
	return b.latest.Clone(), true
}

// Restore replaces the in-memory session state without emitting a live tool
// event. Session switching uses this to discard the previous session's queued
// updates before seeding the broker from its durable snapshot.
func (b *Broker) Restore(snapshot Snapshot, ok bool) {
	if b == nil {
		return
	}
	copy := snapshot.Clone()
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	b.events = nil
	b.latest = Snapshot{}
	b.hasLatest = false
	if ok {
		b.latest = copy
		b.hasLatest = true
	}
}

func (b *Broker) Close() {
	if b == nil {
		return
	}
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return
	}
	b.closed = true
	b.mu.Unlock()
	b.signal()
}

func (b *Broker) signal() {
	select {
	case b.wake <- struct{}{}:
	default:
	}
}
