package plan

import (
	"fmt"
	"time"
)

// CanTransition reports whether a plan session may move from one status to
// another.
func CanTransition(from, to SessionStatus) bool {
	if from == to {
		return true
	}
	switch from {
	case SessionClarifying:
		return to == SessionDrafting || to == SessionAwaitingApprov || to == SessionApproved || to == SessionPaused || to == SessionFailed || to == SessionCancelled
	case SessionDrafting:
		return to == SessionAwaitingApprov || to == SessionApproved || to == SessionPaused || to == SessionFailed || to == SessionCancelled
	case SessionAwaitingApprov:
		return to == SessionDrafting || to == SessionApproved || to == SessionPaused || to == SessionFailed || to == SessionCancelled
	case SessionPaused:
		return to == SessionClarifying || to == SessionDrafting || to == SessionAwaitingApprov || to == SessionCancelled
	default:
		return false
	}
}

// Transition moves the session to a new status, recording the reason for
// paused states and bumping timestamps.
func (s *Session) Transition(to SessionStatus, reason PauseReason) error {
	if s == nil {
		return fmt.Errorf("plan session is nil")
	}
	if !CanTransition(s.Status, to) {
		return fmt.Errorf("invalid plan session transition: %s -> %s", s.Status, to)
	}
	s.Status = to
	if to == SessionPaused {
		s.PauseReason = reason
	} else {
		s.PauseReason = ""
	}
	if s.CreatedAt.IsZero() {
		s.CreatedAt = time.Now()
	}
	s.UpdatedAt = time.Now()
	return nil
}
