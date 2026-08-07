package goal

import (
	"fmt"
	"time"
)

func CanTransition(from, to GoalStatus) bool {
	if from == to {
		return true
	}
	switch from {
	case GoalDraft:
		return to == GoalRunning || to == GoalCancelled
	case GoalRunning:
		return to == GoalCompleted || to == GoalPaused || to == GoalBlocked || to == GoalFailed || to == GoalCancelled
	case GoalPaused, GoalBlocked:
		return to == GoalRunning || to == GoalCancelled
	default:
		return false
	}
}

func (g *Goal) Transition(to GoalStatus, reason PauseReason) error {
	if g == nil {
		return fmt.Errorf("goal is nil")
	}
	if !CanTransition(g.Status, to) {
		return fmt.Errorf("invalid goal transition: %s -> %s", g.Status, to)
	}
	g.Status = to
	if to == GoalPaused || to == GoalBlocked {
		g.PauseReason = reason
	} else {
		g.PauseReason = ""
	}
	if g.CreatedAt.IsZero() {
		g.CreatedAt = time.Now()
	}
	g.UpdatedAt = time.Now()
	g.Revision++
	return nil
}
