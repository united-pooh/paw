package goal

import "time"

type GoalID string

type GoalStatus string

const (
	GoalDraft      GoalStatus = "draft"
	GoalPlanning   GoalStatus = "planning"
	GoalRunning    GoalStatus = "running"
	GoalReplanning GoalStatus = "replanning"
	GoalCompleted  GoalStatus = "completed"
	GoalPaused     GoalStatus = "paused"
	GoalBlocked    GoalStatus = "blocked"
	GoalFailed     GoalStatus = "failed"
	GoalCancelled  GoalStatus = "cancelled"
)

func (s GoalStatus) Terminal() bool {
	return s == GoalCompleted || s == GoalFailed || s == GoalCancelled
}

type PauseReason string

const (
	PausePermissionRequired PauseReason = "permission_required"
	PauseDangerousCommand   PauseReason = "dangerous_command"
	PauseNoProgress         PauseReason = "no_progress"
	PauseBudgetExhausted    PauseReason = "budget_exhausted"
	PauseBlocked            PauseReason = "blocked"
	PauseVerificationFailed PauseReason = "verification_failed"
	PausePlanStale          PauseReason = "plan_stale"
	PauseUserInputRequired  PauseReason = "user_input_required"
)

type GoalBudget struct {
	MaxTurns         int
	MaxToolCalls     int
	MaxContinuations int
	MaxNoProgress    int
	Deadline         time.Time
}

func (b GoalBudget) Normalize() GoalBudget {
	if b.MaxTurns < 0 {
		b.MaxTurns = 0
	}
	if b.MaxToolCalls < 0 {
		b.MaxToolCalls = 0
	}
	if b.MaxContinuations < 0 {
		b.MaxContinuations = 0
	}
	if b.MaxNoProgress < 0 {
		b.MaxNoProgress = 0
	}
	return b
}

type Goal struct {
	ID                 GoalID
	SessionID          string
	Objective          string
	AcceptanceCriteria []string
	Verification       []VerificationSpec
	Status             GoalStatus
	CurrentTaskID      string
	ContinuationUsed   int
	NoProgressCount    int
	TurnsUsed          int
	ToolCallsUsed      int
	Budget             GoalBudget
	PauseReason        PauseReason
	LastDecision       string
	CreatedAt          time.Time
	UpdatedAt          time.Time
	Revision           uint64
}

type GoalSnapshot struct {
	ID                 GoalID
	SessionID          string
	Objective          string
	AcceptanceCriteria []string
	Verification       []VerificationSpec
	Status             GoalStatus
	CurrentTaskID      string
	ContinuationUsed   int
	NoProgressCount    int
	TurnsUsed          int
	ToolCallsUsed      int
	Budget             GoalBudget
	PauseReason        PauseReason
	LastDecision       string
	CreatedAt          time.Time
	UpdatedAt          time.Time
	Revision           uint64
}

func (g Goal) Snapshot() GoalSnapshot {
	criteria := append([]string(nil), g.AcceptanceCriteria...)
	verification := append([]VerificationSpec(nil), g.Verification...)
	for i := range verification {
		verification[i].Scope = append([]string(nil), verification[i].Scope...)
	}
	return GoalSnapshot{ID: g.ID, SessionID: g.SessionID, Objective: g.Objective, AcceptanceCriteria: criteria, Verification: verification, Status: g.Status, CurrentTaskID: g.CurrentTaskID, ContinuationUsed: g.ContinuationUsed, NoProgressCount: g.NoProgressCount, TurnsUsed: g.TurnsUsed, ToolCallsUsed: g.ToolCallsUsed, Budget: g.Budget, PauseReason: g.PauseReason, LastDecision: g.LastDecision, CreatedAt: g.CreatedAt, UpdatedAt: g.UpdatedAt, Revision: g.Revision}
}

func cloneGoal(g Goal) Goal {
	g.AcceptanceCriteria = append([]string(nil), g.AcceptanceCriteria...)
	g.Verification = append([]VerificationSpec(nil), g.Verification...)
	for i := range g.Verification {
		g.Verification[i].Scope = append([]string(nil), g.Verification[i].Scope...)
	}
	return g
}
