package sessionactor

import (
	"context"
	"encoding/json"

	"paw/internal/es"
	"paw/internal/session"
)

type sessionStream struct {
	store *session.JSONLStore
}

func (s sessionStream) Append(ctx context.Context, id string, events []es.Envelope) (int64, int64, error) {
	return s.store.AppendEnvelopes(ctx, id, events)
}

func (s sessionStream) Load(ctx context.Context, id string) ([]es.Envelope, bool, error) {
	return s.store.LoadEnvelopes(ctx, id)
}

func (s sessionStream) WriteSnapshot(ctx context.Context, id string, seq int64, state json.RawMessage) error {
	return s.store.WriteActorSnapshot(ctx, id, seq, state)
}

func (s sessionStream) ReadSnapshot(ctx context.Context, id string) (es.Snapshot, bool, error) {
	return s.store.ReadActorSnapshot(ctx, id)
}
