package selecttool

import (
	"context"
	"errors"
	"sync"
)

var ErrBrokerClosed = errors.New("selection broker is closed")

type completion struct {
	result Result
	err    error
}

type pendingRequest struct {
	request Request
	done    chan completion
}

type Broker struct {
	mu         sync.Mutex
	nextID     uint64
	queue      []*pendingRequest
	active     *pendingRequest
	eventQueue []Event
	eventHead  int
	wake       chan struct{}
	closed     bool
}

func NewBroker() *Broker { return &Broker{wake: make(chan struct{}, 1)} }

func (b *Broker) Ask(ctx context.Context, request Request) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	pending := &pendingRequest{request: request.Clone(), done: make(chan completion, 1)}
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return Result{}, ErrBrokerClosed
	}
	b.nextID++
	pending.request.ID = "select-" + formatUint(b.nextID)
	b.queue = append(b.queue, pending)
	b.promoteLocked()
	b.mu.Unlock()

	select {
	case completed := <-pending.done:
		return completed.result.Clone(), completed.err
	case <-ctx.Done():
		if b.cancelPending(pending, ctx.Err()) {
			return Result{}, ctx.Err()
		}
		completed := <-pending.done
		return completed.result.Clone(), completed.err
	}
}

func formatUint(n uint64) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

func (b *Broker) NextEvent(ctx context.Context) (Event, error) {
	for {
		b.mu.Lock()
		if b.eventHead < len(b.eventQueue) {
			event := b.eventQueue[b.eventHead]
			b.eventHead++
			if b.eventHead == len(b.eventQueue) {
				// Release references and reset the queue once fully consumed.
				b.eventQueue = nil
				b.eventHead = 0
			} else {
				b.signalLocked()
			}
			b.mu.Unlock()
			event.Request = event.Request.Clone()
			return event, nil
		}
		if b.closed {
			b.mu.Unlock()
			return Event{}, ErrBrokerClosed
		}
		b.mu.Unlock()
		select {
		case <-ctx.Done():
			return Event{}, ctx.Err()
		case <-b.wake:
		}
	}
}

func (b *Broker) Complete(id string, result Result) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed || b.active == nil || b.active.request.ID != id {
		return false
	}
	pending := b.active
	b.active = nil
	pending.done <- completion{result: result.Clone()}
	b.promoteLocked()
	return true
}

func (b *Broker) cancelPending(pending *pendingRequest, err error) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.active == pending {
		b.active = nil
		b.emitLocked(Event{Kind: EventInvalidated, RequestID: pending.request.ID})
		pending.done <- completion{err: err}
		b.promoteLocked()
		return true
	}
	for i, queued := range b.queue {
		if queued == pending {
			b.queue = append(b.queue[:i], b.queue[i+1:]...)
			pending.done <- completion{err: err}
			return true
		}
	}
	return false
}

func (b *Broker) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	b.closed = true
	if b.active != nil {
		b.emitLocked(Event{Kind: EventInvalidated, RequestID: b.active.request.ID})
		b.active.done <- completion{err: ErrBrokerClosed}
		b.active = nil
	}
	for _, pending := range b.queue {
		pending.done <- completion{err: ErrBrokerClosed}
	}
	b.queue = nil
	b.emitLocked(Event{Kind: EventClosed})
	b.signalLocked()
}

func (b *Broker) promoteLocked() {
	if b.closed || b.active != nil || len(b.queue) == 0 {
		return
	}
	b.active = b.queue[0]
	b.queue = b.queue[1:]
	b.emitLocked(Event{Kind: EventRequest, Request: b.active.request.Clone(), RequestID: b.active.request.ID})
}

func (b *Broker) emitLocked(event Event) {
	b.eventQueue = append(b.eventQueue, event)
	b.signalLocked()
}

func (b *Broker) signalLocked() {
	select {
	case b.wake <- struct{}{}:
	default:
	}
}
