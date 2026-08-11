package loop

import (
	"context"
	"strings"
)

// TurnOwner identifies the outer turn that owns background work launched by tools.
type TurnOwner struct {
	SessionID string
	TurnID    string
}

type turnOwnerContextKey struct{}

// WithTurnOwner attaches immutable turn ownership metadata to tool execution.
func WithTurnOwner(ctx context.Context, sessionID, turnID string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	owner := TurnOwner{SessionID: strings.TrimSpace(sessionID), TurnID: strings.TrimSpace(turnID)}
	return context.WithValue(ctx, turnOwnerContextKey{}, owner)
}

// TurnOwnerFromContext returns turn ownership metadata when both IDs are present.
func TurnOwnerFromContext(ctx context.Context) (TurnOwner, bool) {
	if ctx == nil {
		return TurnOwner{}, false
	}
	owner, ok := ctx.Value(turnOwnerContextKey{}).(TurnOwner)
	owner.SessionID = strings.TrimSpace(owner.SessionID)
	owner.TurnID = strings.TrimSpace(owner.TurnID)
	return owner, ok && owner.SessionID != "" && owner.TurnID != ""
}
