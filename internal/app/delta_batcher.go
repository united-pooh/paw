package app

import (
	"sync"
	"time"
)

const (
	defaultDeltaBatchInterval = 25 * time.Millisecond
	defaultDeltaBatchBytes    = 16 * 1024
)

type BatcherClock interface {
	Now() time.Time
	After(time.Duration, func()) func()
}

type realBatcherClock struct{}

func (realBatcherClock) Now() time.Time { return time.Now() }
func (realBatcherClock) After(delay time.Duration, fn func()) func() {
	timer := time.AfterFunc(delay, fn)
	return func() { timer.Stop() }
}

type DeltaBatch struct {
	PartID string `json:"part_id"`
	Offset int    `json:"offset"`
	Text   string `json:"text"`
}

type DeltaBatcher struct {
	mu sync.Mutex

	partID   string
	offset   int
	pending  string
	clock    BatcherClock
	interval time.Duration
	maxBytes int
	emit     func(DeltaBatch)
	cancel   func()
	closed   bool
}

func NewDeltaBatcher(partID string, clock BatcherClock, emit func(DeltaBatch)) *DeltaBatcher {
	if clock == nil {
		clock = realBatcherClock{}
	}
	return &DeltaBatcher{
		partID: partID, clock: clock, emit: emit,
		interval: defaultDeltaBatchInterval, maxBytes: defaultDeltaBatchBytes,
	}
}

func (b *DeltaBatcher) SetLimits(interval time.Duration, maxBytes int) {
	if b == nil {
		return
	}
	b.mu.Lock()
	if interval > 0 {
		b.interval = interval
	}
	if maxBytes > 0 {
		b.maxBytes = maxBytes
	}
	b.mu.Unlock()
}

func (b *DeltaBatcher) Add(text string) {
	if b == nil || text == "" {
		return
	}
	var batch *DeltaBatch
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return
	}
	b.pending += text
	if len(b.pending) >= b.maxBytes {
		flushed := b.takeLocked()
		batch = &flushed
	} else if b.cancel == nil {
		b.cancel = b.clock.After(b.interval, b.flushTimer)
	}
	b.mu.Unlock()
	b.emitBatch(batch)
}

func (b *DeltaBatcher) Flush() {
	if b == nil {
		return
	}
	var batch *DeltaBatch
	b.mu.Lock()
	if b.pending != "" {
		flushed := b.takeLocked()
		batch = &flushed
	} else if b.cancel != nil {
		b.cancel()
		b.cancel = nil
	}
	b.mu.Unlock()
	b.emitBatch(batch)
}

func (b *DeltaBatcher) Close() {
	if b == nil {
		return
	}
	b.Flush()
	b.mu.Lock()
	b.closed = true
	b.mu.Unlock()
}

func (b *DeltaBatcher) Offset() int {
	if b == nil {
		return 0
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.offset + len(b.pending)
}

func (b *DeltaBatcher) flushTimer() {
	if b == nil {
		return
	}
	var batch *DeltaBatch
	b.mu.Lock()
	b.cancel = nil
	if !b.closed && b.pending != "" {
		flushed := b.takeLocked()
		batch = &flushed
	}
	b.mu.Unlock()
	b.emitBatch(batch)
}

func (b *DeltaBatcher) takeLocked() DeltaBatch {
	if b.cancel != nil {
		b.cancel()
		b.cancel = nil
	}
	batch := DeltaBatch{PartID: b.partID, Offset: b.offset, Text: b.pending}
	b.offset += len(b.pending)
	b.pending = ""
	return batch
}

func (b *DeltaBatcher) emitBatch(batch *DeltaBatch) {
	if batch != nil && batch.Text != "" && b.emit != nil {
		b.emit(*batch)
	}
}
