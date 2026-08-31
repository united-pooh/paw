package app

import (
	"context"
	"fmt"
	"strings"
)

type SessionSnapshot struct {
	SessionProjection
	StreamID      string          `json:"stream_id"`
	EventSequence uint64          `json:"event_sequence"`
	Parts         []StreamingPart `json:"parts,omitempty"`
}

func (s *SessionService) ConsistentSnapshot(ctx context.Context, sessionID string, request SnapshotRequest, hub *EventHub) (SessionSnapshot, error) {
	if s == nil || s.store == nil {
		return SessionSnapshot{}, fmt.Errorf("session service is unavailable")
	}
	sessionID = strings.TrimSpace(sessionID)
	projection, err := projectSession(ctx, s.store, nil, sessionID, request)
	if err != nil {
		return SessionSnapshot{}, err
	}
	var consistent CoordinatorSnapshot
	if s.coordinator != nil {
		consistent = s.coordinator.ConsistentSnapshot(hub)
	} else if hub != nil {
		consistent.Cursor = hub.CurrentCursor()
	}
	projection.SessionVersion = consistent.State.SessionVersion[sessionID]
	projection.Queue = append([]InputDraft(nil), consistent.State.Queue[sessionID]...)
	for _, interaction := range consistent.State.Pending {
		if interaction.SessionID == sessionID {
			projection.Pending = append(projection.Pending, interaction)
		}
	}
	if consistent.State.ActiveSessionID == sessionID {
		projection.ActiveTurnID = consistent.State.ActiveTurnID
	}
	parts := make([]StreamingPart, 0)
	for _, part := range consistent.State.Parts {
		if part.SessionID == sessionID {
			parts = append(parts, part)
		}
	}
	return SessionSnapshot{
		SessionProjection: projection,
		StreamID:          consistent.Cursor.StreamID, EventSequence: consistent.Cursor.Sequence,
		Parts: parts,
	}, nil
}
