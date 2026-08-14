// Package eventing contains the durable event-sourcing primitives shared by
// the runtime bounded contexts.
package eventing

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Event is the wire envelope persisted in a stream.  Payload and Metadata are
// deliberately untyped: aggregates own their event payload schemas while this
// package owns ordering, durability and identity.
type Event struct {
	EventID       string          `json:"event_id"`
	StreamType    string          `json:"stream_type"`
	StreamID      string          `json:"stream_id"`
	StreamVersion int64           `json:"stream_version"`
	Type          string          `json:"type"`
	SchemaVersion int             `json:"schema_version"`
	SessionID     string          `json:"session_id,omitempty"`
	CorrelationID string          `json:"correlation_id,omitempty"`
	CausationID   string          `json:"causation_id,omitempty"`
	Timestamp     time.Time       `json:"timestamp"`
	Payload       json.RawMessage `json:"payload"`
	Metadata      map[string]any  `json:"metadata,omitempty"`
}

// EventEnvelope is kept as an explicit alias for callers that prefer the
// domain language used in the architecture document.
type EventEnvelope = Event

// StreamRef identifies one independently versioned event stream.
type StreamRef struct {
	StreamType string `json:"stream_type"`
	StreamID   string `json:"stream_id"`
}

// AppendRequest describes one atomic command batch. Events may be supplied in
// Events (the canonical field), Batch, or Event for convenience when building
// adapters around a single event.
type AppendRequest struct {
	Stream          StreamRef `json:"stream"`
	ExpectedVersion int64     `json:"expected_version"`
	CommandID       string    `json:"command_id"`
	Events          []Event   `json:"events,omitempty"`
	Batch           []Event   `json:"batch,omitempty"`
	Event           Event     `json:"event,omitempty"`
	// Projection is optional and is applied after the durable append and
	// before RuntimeBus publication. It is intentionally any so both the
	// context-aware and context-free projection forms can be used.
	Projection any `json:"-"`
}

func (r AppendRequest) eventBatch() []Event {
	if len(r.Events) != 0 {
		return append([]Event(nil), r.Events...)
	}
	if len(r.Batch) != 0 {
		return append([]Event(nil), r.Batch...)
	}
	if r.Event.Type != "" || len(r.Event.Payload) != 0 || r.Event.EventID != "" {
		return []Event{r.Event}
	}
	return nil
}

// Commit is the durable result of one command. FirstVersion and LastVersion
// are populated for batch consumers; Version and CommittedVersion are aliases
// for the final stream version and make single-event adapters straightforward.
type Commit struct {
	Stream           StreamRef `json:"stream"`
	CommandID        string    `json:"command_id,omitempty"`
	Events           []Event   `json:"events"`
	FirstVersion     int64     `json:"first_version"`
	LastVersion      int64     `json:"last_version"`
	Version          int64     `json:"version"`
	CommittedVersion int64     `json:"committed_version"`
	Idempotent       bool      `json:"idempotent,omitempty"`
}

// Errors are stable sentinels so callers can use errors.Is while retaining a
// useful, contextual error string.
var (
	ErrVersionConflict     = errors.New("event stream version conflict")
	ErrIdempotencyConflict = errors.New("event command idempotency conflict")
	ErrUnknownSchema       = errors.New("unknown event schema")
	ErrCorruptStream       = errors.New("corrupt event stream")
	ErrEmptyBatch          = errors.New("event batch is empty")
	ErrProjectionLag       = errors.New("event projection is behind committed stream")
)

// CommittedProjectionError reports a projection failure after the event batch
// has been durably committed. The underlying error remains available through
// errors.Is/errors.As and the committed version is never lost.
type CommittedProjectionError struct {
	Stream           StreamRef
	CommittedVersion int64
	Err              error
}

func (e *CommittedProjectionError) Error() string {
	if e == nil {
		return "<nil>"
	}
	return fmt.Sprintf("event stream %s/%s committed at version %d but projection failed: %v", e.Stream.StreamType, e.Stream.StreamID, e.CommittedVersion, e.Err)
}

func (e *CommittedProjectionError) Unwrap() error { return e.Err }

func (e *CommittedProjectionError) Is(target error) bool {
	return target == ErrProjectionLag || errors.Is(e.Err, target)
}

// Version returns the durable version carried by the error.
func (e *CommittedProjectionError) Version() int64 {
	if e == nil {
		return 0
	}
	return e.CommittedVersion
}

func newEventID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate event id: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

func normalizeRef(ref StreamRef) (StreamRef, error) {
	ref.StreamType = strings.TrimSpace(ref.StreamType)
	ref.StreamID = strings.TrimSpace(ref.StreamID)
	if ref.StreamType == "" || ref.StreamID == "" {
		return StreamRef{}, fmt.Errorf("stream type and stream id are required")
	}
	return ref, nil
}

func cloneEvent(in Event) Event {
	out := in
	out.Payload = append(json.RawMessage(nil), in.Payload...)
	if in.Metadata != nil {
		out.Metadata = make(map[string]any, len(in.Metadata))
		for k, v := range in.Metadata {
			out.Metadata[k] = v
		}
	}
	return out
}

func cloneEvents(in []Event) []Event {
	out := make([]Event, len(in))
	for i := range in {
		out[i] = cloneEvent(in[i])
	}
	return out
}

func cloneCommit(in Commit) Commit {
	out := in
	out.Events = cloneEvents(in.Events)
	return out
}
