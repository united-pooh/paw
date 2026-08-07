package goal

import "testing"

func TestGoalStateTransitions(t *testing.T) {
	tests := []struct {
		name string
		from GoalStatus
		to   GoalStatus
		want bool
	}{
		{"draft-running", GoalDraft, GoalRunning, true},
		{"running-completed", GoalRunning, GoalCompleted, true},
		{"running-paused", GoalRunning, GoalPaused, true},
		{"running-blocked", GoalRunning, GoalBlocked, true},
		{"running-failed", GoalRunning, GoalFailed, true},
		{"running-cancelled", GoalRunning, GoalCancelled, true},
		{"paused-running", GoalPaused, GoalRunning, true},
		{"blocked-running", GoalBlocked, GoalRunning, true},
		{"completed-running", GoalCompleted, GoalRunning, false},
		{"cancelled-running", GoalCancelled, GoalRunning, false},
		{"draft-completed", GoalDraft, GoalCompleted, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CanTransition(tt.from, tt.to); got != tt.want {
				t.Fatalf("CanTransition(%q, %q) = %v, want %v", tt.from, tt.to, got, tt.want)
			}
		})
	}
}

func TestGoalTransitionUpdatesRevisionAndPauseReason(t *testing.T) {
	goal := Goal{ID: "g1", Status: GoalRunning, Revision: 4}
	if err := goal.Transition(GoalPaused, PauseNoProgress); err != nil {
		t.Fatal(err)
	}
	if goal.Status != GoalPaused || goal.PauseReason != PauseNoProgress || goal.Revision != 5 {
		t.Fatalf("unexpected goal after pause: %+v", goal)
	}
	if err := goal.Transition(GoalRunning, ""); err != nil {
		t.Fatal(err)
	}
	if goal.PauseReason != "" || goal.Revision != 6 {
		t.Fatalf("unexpected goal after resume: %+v", goal)
	}
}
