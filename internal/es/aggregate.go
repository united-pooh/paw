package es

import (
	"context"
	"encoding/json"
	"fmt"
)

// DefaultSnapshotInterval is how many events an aggregate accumulates before
// a snapshot is written. Snapshots are caches, never the source of truth.
const DefaultSnapshotInterval = 200

// State is the projection of an aggregate: it applies decoded events and can
// snapshot/restore itself. States are derived from the event stream and must
// be fully reconstructible from snapshot + replay.
type State interface {
	Apply(payload Payload, env Envelope) error
	Snapshot() (json.RawMessage, error)
	Restore(data json.RawMessage) error
}

// Command validates against the current state and produces events. Produced
// envelopes carry seq=0 as a placeholder; the store assigns real seqs.
// Returning no events is a valid no-op. Returning an error appends nothing.
type Command interface {
	Execute(state State) ([]Envelope, error)
}

// Loader loads aggregates from a store and commits commands through the
// load → validate → append → apply pipeline.
type Loader struct {
	Store            *JSONLStore
	Registry         *Registry
	SnapshotInterval int
}

func (l *Loader) interval() int {
	if l.SnapshotInterval <= 0 {
		return DefaultSnapshotInterval
	}
	return l.SnapshotInterval
}

func (l *Loader) shouldSnapshot(lastSeq int64) bool {
	n := l.interval()
	return lastSeq > 0 && lastSeq%int64(n) == 0
}

// Load reconstructs state from snapshot (when present) plus the remaining
// event tail. It returns the next seq to assign (last applied seq + 1).
func (l *Loader) Load(ctx context.Context, aggregateID string, state State) (int64, error) {
	next := int64(1)
	snap, ok, err := l.Store.ReadSnapshot(ctx, aggregateID)
	if err != nil {
		return 0, err
	}
	if ok {
		if err := state.Restore(snap.State); err != nil {
			return 0, fmt.Errorf("es: restore snapshot of %q: %w", aggregateID, err)
		}
		next = snap.Seq + 1
	}
	events, _, err := l.Store.Load(ctx, aggregateID)
	if err != nil {
		return 0, err
	}
	for _, env := range events {
		if env.Seq < next {
			continue
		}
		payload, err := l.Registry.Decode(env)
		if err != nil {
			return 0, fmt.Errorf("es: load %q seq %d: %w", aggregateID, env.Seq, err)
		}
		if err := state.Apply(payload, env); err != nil {
			return 0, fmt.Errorf("es: apply %q seq %d: %w", aggregateID, env.Seq, err)
		}
		next = env.Seq + 1
	}
	return next, nil
}

// Commit runs the full pipeline: load current state (snapshot + replay),
// execute the command, append produced events, apply them to state, and
// write a snapshot when the interval is reached. On any failure nothing is
// appended beyond what the store already committed.
func (l *Loader) Commit(ctx context.Context, aggregateID string, state State, cmd Command) ([]Envelope, error) {
	if _, err := l.Load(ctx, aggregateID, state); err != nil {
		return nil, err
	}
	events, err := cmd.Execute(state)
	if err != nil {
		return nil, fmt.Errorf("es: command on %q: %w", aggregateID, err)
	}
	if len(events) == 0 {
		return nil, nil
	}
	first, last, err := l.Store.Append(ctx, aggregateID, events)
	if err != nil {
		return nil, fmt.Errorf("es: append %q: %w", aggregateID, err)
	}
	// Apply the committed tail to the live state with the assigned seqs.
	// Events were envelope-validated during Append; registry decode failures
	// here indicate a state/event contract bug — surface it loudly.
	committed := make([]Envelope, len(events))
	for i, e := range events {
		e.Seq = first + int64(i)
		payload, err := l.Registry.Decode(e)
		if err != nil {
			return nil, fmt.Errorf("es: decode committed %q seq %d: %w", aggregateID, e.Seq, err)
		}
		if err := state.Apply(payload, e); err != nil {
			return nil, fmt.Errorf("es: apply committed %q seq %d: %w", aggregateID, e.Seq, err)
		}
		committed[i] = e
	}
	if l.shouldSnapshot(last) {
		snapState, err := state.Snapshot()
		if err != nil {
			return nil, fmt.Errorf("es: snapshot state of %q: %w", aggregateID, err)
		}
		if err := l.Store.WriteSnapshot(ctx, aggregateID, last, snapState); err != nil {
			// Snapshot is a cache; a failure must not fail the commit itself.
			return committed, nil
		}
	}
	return committed, nil
}
