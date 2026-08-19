package task

import "sync"

type taskUpdateBroker struct {
	mu     sync.Mutex
	subs   map[chan struct{}]struct{}
	closed bool
}

func newTaskUpdateBroker() *taskUpdateBroker {
	return &taskUpdateBroker{subs: make(map[chan struct{}]struct{})}
}

func (b *taskUpdateBroker) subscribe() (<-chan struct{}, func()) {
	if b == nil {
		closed := make(chan struct{})
		close(closed)
		return closed, func() {}
	}
	ch := make(chan struct{}, 1)
	b.mu.Lock()
	if b.closed {
		close(ch)
		b.mu.Unlock()
		return ch, func() {}
	}
	b.subs[ch] = struct{}{}
	b.mu.Unlock()

	var once sync.Once
	return ch, func() {
		once.Do(func() {
			b.mu.Lock()
			if _, ok := b.subs[ch]; ok {
				delete(b.subs, ch)
				close(ch)
			}
			b.mu.Unlock()
		})
	}
}

func (b *taskUpdateBroker) publish() {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	for ch := range b.subs {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

func (b *taskUpdateBroker) close() {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	b.closed = true
	for ch := range b.subs {
		delete(b.subs, ch)
		close(ch)
	}
}
