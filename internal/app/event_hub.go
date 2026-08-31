package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"
)

const (
	defaultEventRingMaxEvents      = 10_000
	defaultEventRingMaxBytes       = 16 * 1024 * 1024
	defaultEventRingMaxAge         = 120 * time.Second
	defaultSubscriberMaxEvents     = 1_000
	defaultSubscriberMaxBytes      = 1024 * 1024
	defaultAppEventPayloadMaxBytes = 64 * 1024
)

var (
	ErrEventHubClosed = errors.New("event_hub_closed")
	ErrEventTooLarge  = errors.New("event_too_large")
)

type ResetReason string

const (
	ResetStreamMismatch ResetReason = "stream_mismatch"
	ResetCursorTooOld   ResetReason = "cursor_too_old"
	ResetCursorAhead    ResetReason = "cursor_ahead"
	ResetSlowConsumer   ResetReason = "slow_consumer"
	ResetHubClosed      ResetReason = "hub_closed"
)

type EventHubConfig struct {
	WorkspaceID         WorkspaceID
	StreamID            string
	MaxEvents           int
	MaxBytes            int
	MaxAge              time.Duration
	SubscriberMaxEvents int
	SubscriberMaxBytes  int
	PayloadMaxBytes     int
	Now                 func() time.Time
}

type eventHubEntry struct {
	event AppEvent
	size  int
}

type subscriberEvent struct {
	event AppEvent
	size  int
}

type eventSubscriber struct {
	inbox       chan subscriberEvent
	events      chan AppEvent
	reset       chan ResetReason
	done        chan struct{}
	queuedBytes int
	queuedCount int
	closed      bool
}

type Subscription struct {
	Replay []AppEvent
	Events <-chan AppEvent
	Reset  <-chan ResetReason
	Close  func()
}

type EventHub struct {
	mu sync.Mutex

	workspaceID WorkspaceID
	streamID    string
	sequence    uint64
	ring        []eventHubEntry
	ringBytes   int
	subscribers map[*eventSubscriber]struct{}
	closed      bool

	maxEvents           int
	maxBytes            int
	maxAge              time.Duration
	subscriberMaxEvents int
	subscriberMaxBytes  int
	payloadMaxBytes     int
	now                 func() time.Time

	subscribeHook func()
}

func NewEventHub(cfg EventHubConfig) (*EventHub, error) {
	if cfg.WorkspaceID == "" {
		return nil, fmt.Errorf("workspace ID is required")
	}
	streamID := cfg.StreamID
	if streamID == "" {
		generated, err := newEventStreamID()
		if err != nil {
			return nil, err
		}
		streamID = generated
	}
	maxEvents := cfg.MaxEvents
	if maxEvents <= 0 {
		maxEvents = defaultEventRingMaxEvents
	}
	maxBytes := cfg.MaxBytes
	if maxBytes <= 0 {
		maxBytes = defaultEventRingMaxBytes
	}
	maxAge := cfg.MaxAge
	if maxAge <= 0 {
		maxAge = defaultEventRingMaxAge
	}
	subscriberMaxEvents := cfg.SubscriberMaxEvents
	if subscriberMaxEvents <= 0 {
		subscriberMaxEvents = defaultSubscriberMaxEvents
	}
	subscriberMaxBytes := cfg.SubscriberMaxBytes
	if subscriberMaxBytes <= 0 {
		subscriberMaxBytes = defaultSubscriberMaxBytes
	}
	payloadMaxBytes := cfg.PayloadMaxBytes
	if payloadMaxBytes <= 0 {
		payloadMaxBytes = defaultAppEventPayloadMaxBytes
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &EventHub{
		workspaceID:         cfg.WorkspaceID,
		streamID:            streamID,
		subscribers:         make(map[*eventSubscriber]struct{}),
		maxEvents:           maxEvents,
		maxBytes:            maxBytes,
		maxAge:              maxAge,
		subscriberMaxEvents: subscriberMaxEvents,
		subscriberMaxBytes:  subscriberMaxBytes,
		payloadMaxBytes:     payloadMaxBytes,
		now:                 now,
	}, nil
}

func newEventStreamID() (string, error) {
	buffer := make([]byte, 16)
	if _, err := rand.Read(buffer); err != nil {
		return "", fmt.Errorf("generate event stream ID: %w", err)
	}
	return "stream_" + hex.EncodeToString(buffer), nil
}

func (h *EventHub) Publish(event AppEvent) (AppEvent, error) {
	if h == nil {
		return AppEvent{}, ErrEventHubClosed
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return AppEvent{}, ErrEventHubClosed
	}
	if len(event.Payload) > h.payloadMaxBytes {
		return AppEvent{}, fmt.Errorf("%w: payload is %d bytes, max %d", ErrEventTooLarge, len(event.Payload), h.payloadMaxBytes)
	}
	event.SchemaVersion = AppEventSchemaVersion
	event.StreamID = h.streamID
	h.sequence++
	event.Sequence = h.sequence
	event.WorkspaceID = h.workspaceID
	if event.Time.IsZero() {
		event.Time = h.now().UTC()
	} else {
		event.Time = event.Time.UTC()
	}
	encoded, err := json.Marshal(event)
	if err != nil {
		return AppEvent{}, fmt.Errorf("encode app event: %w", err)
	}
	entry := eventHubEntry{event: cloneAppEvent(event), size: len(encoded)}
	h.ring = append(h.ring, entry)
	h.ringBytes += entry.size
	h.trimLocked(event.Time)
	for subscriber := range h.subscribers {
		h.enqueueLocked(subscriber, entry)
	}
	return cloneAppEvent(event), nil
}

func (h *EventHub) Subscribe(cursor EventCursor) (Subscription, error) {
	if h == nil {
		return Subscription{}, ErrEventHubClosed
	}
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return resetSubscription(ResetHubClosed), nil
	}
	if reason := h.resetReasonLocked(cursor); reason != "" {
		h.mu.Unlock()
		return resetSubscription(reason), nil
	}
	subscriber := &eventSubscriber{
		inbox:  make(chan subscriberEvent, h.subscriberMaxEvents),
		events: make(chan AppEvent),
		reset:  make(chan ResetReason, 1),
		done:   make(chan struct{}),
	}
	h.subscribers[subscriber] = struct{}{}
	replay := make([]AppEvent, 0)
	for _, entry := range h.ring {
		if entry.event.Sequence > cursor.Sequence {
			replay = append(replay, cloneAppEvent(entry.event))
		}
	}
	if h.subscribeHook != nil {
		h.subscribeHook()
	}
	h.mu.Unlock()
	go h.forwardSubscriber(subscriber)
	var closeOnce sync.Once
	return Subscription{
		Replay: replay,
		Events: subscriber.events,
		Reset:  subscriber.reset,
		Close: func() {
			closeOnce.Do(func() { h.closeSubscriber(subscriber, "") })
		},
	}, nil
}

func resetSubscription(reason ResetReason) Subscription {
	events := make(chan AppEvent)
	reset := make(chan ResetReason, 1)
	close(events)
	reset <- reason
	close(reset)
	return Subscription{Events: events, Reset: reset, Close: func() {}}
}

func (h *EventHub) CurrentCursor() EventCursor {
	if h == nil {
		return EventCursor{}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return EventCursor{StreamID: h.streamID, Sequence: h.sequence}
}

func (h *EventHub) Close() error {
	if h == nil {
		return nil
	}
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return nil
	}
	h.closed = true
	subscribers := make([]*eventSubscriber, 0, len(h.subscribers))
	for subscriber := range h.subscribers {
		subscribers = append(subscribers, subscriber)
	}
	for _, subscriber := range subscribers {
		h.closeSubscriberLocked(subscriber, ResetHubClosed)
	}
	h.mu.Unlock()
	return nil
}

func (h *EventHub) resetReasonLocked(cursor EventCursor) ResetReason {
	if cursor.StreamID == "" {
		if cursor.Sequence != 0 {
			return ResetStreamMismatch
		}
	} else if cursor.StreamID != h.streamID {
		return ResetStreamMismatch
	}
	if cursor.Sequence > h.sequence {
		return ResetCursorAhead
	}
	if cursor.Sequence == h.sequence {
		return ""
	}
	if len(h.ring) == 0 {
		if cursor.Sequence < h.sequence {
			return ResetCursorTooOld
		}
		return ""
	}
	first := h.ring[0].event.Sequence
	if cursor.Sequence+1 < first {
		return ResetCursorTooOld
	}
	return ""
}

func (h *EventHub) trimLocked(now time.Time) {
	for len(h.ring) > 0 {
		oldest := h.ring[0]
		overCount := len(h.ring) > h.maxEvents
		overBytes := h.ringBytes > h.maxBytes
		overAge := h.maxAge > 0 && now.Sub(oldest.event.Time) > h.maxAge
		if !overCount && !overBytes && !overAge {
			return
		}
		h.ringBytes -= oldest.size
		h.ring[0] = eventHubEntry{}
		h.ring = h.ring[1:]
	}
}

func (h *EventHub) enqueueLocked(subscriber *eventSubscriber, entry eventHubEntry) {
	if subscriber == nil || subscriber.closed {
		return
	}
	if subscriber.queuedCount+1 > h.subscriberMaxEvents || subscriber.queuedBytes+entry.size > h.subscriberMaxBytes {
		h.closeSubscriberLocked(subscriber, ResetSlowConsumer)
		return
	}
	select {
	case subscriber.inbox <- subscriberEvent{event: cloneAppEvent(entry.event), size: entry.size}:
		subscriber.queuedCount++
		subscriber.queuedBytes += entry.size
	default:
		h.closeSubscriberLocked(subscriber, ResetSlowConsumer)
	}
}

func (h *EventHub) forwardSubscriber(subscriber *eventSubscriber) {
	defer close(subscriber.events)
	defer close(subscriber.reset)
	for {
		select {
		case queued, ok := <-subscriber.inbox:
			if !ok {
				return
			}
			h.mu.Lock()
			if subscriber.queuedCount > 0 {
				subscriber.queuedCount--
			}
			if subscriber.queuedBytes >= queued.size {
				subscriber.queuedBytes -= queued.size
			} else {
				subscriber.queuedBytes = 0
			}
			h.mu.Unlock()
			select {
			case subscriber.events <- queued.event:
			case <-subscriber.done:
				return
			}
		case <-subscriber.done:
			return
		}
	}
}

func (h *EventHub) closeSubscriber(subscriber *eventSubscriber, reason ResetReason) {
	if h == nil || subscriber == nil {
		return
	}
	h.mu.Lock()
	h.closeSubscriberLocked(subscriber, reason)
	h.mu.Unlock()
}

func (h *EventHub) closeSubscriberLocked(subscriber *eventSubscriber, reason ResetReason) {
	if subscriber == nil || subscriber.closed {
		return
	}
	subscriber.closed = true
	delete(h.subscribers, subscriber)
	if reason != "" {
		select {
		case subscriber.reset <- reason:
		default:
		}
	}
	close(subscriber.done)
	close(subscriber.inbox)
}

func cloneAppEvent(event AppEvent) AppEvent {
	event.Payload = append(json.RawMessage(nil), event.Payload...)
	return event
}

func (h *EventHub) WaitForClose(ctx context.Context) error {
	if h == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	for {
		h.mu.Lock()
		closed := h.closed
		h.mu.Unlock()
		if closed {
			return nil
		}
		select {
		case <-ctx.Done():
			return context.Cause(ctx)
		case <-time.After(time.Millisecond):
		}
	}
}
