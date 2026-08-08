// Package plan implements a standalone plan-authoring runtime. Plans are
// spec/scope documents that exist independently of Goals: plan mode clarifies
// a requirement with the user, writes a markdown plan file under
// docs/superpowers/plans/, and finalizes it after user approval. Goals do not
// reference plans; the agent explores freely inside a Goal.
package plan

import (
	"fmt"
	"time"
)

type PlanID string

type PlanStatus string

const (
	PlanDraft    PlanStatus = "draft"
	PlanApproved PlanStatus = "approved"
)

// PlanDoc is the persisted plan document. The file at Path is the source of
// truth; Status is derived from the session lifecycle.
type PlanDoc struct {
	ID        PlanID
	Title     string
	Path      string
	Content   string
	Status    PlanStatus
	CreatedAt time.Time
	UpdatedAt time.Time
}

func (d PlanDoc) Clone() PlanDoc {
	return d
}

// SessionStatus is the lifecycle of one plan-authoring session.
type SessionStatus string

const (
	SessionClarifying     SessionStatus = "clarifying"
	SessionDrafting       SessionStatus = "drafting"
	SessionAwaitingApprov SessionStatus = "awaiting_approval"
	SessionApproved       SessionStatus = "approved"
	SessionPaused         SessionStatus = "paused"
	SessionFailed         SessionStatus = "failed"
	SessionCancelled      SessionStatus = "cancelled"
)

func (s SessionStatus) Terminal() bool {
	return s == SessionApproved || s == SessionFailed || s == SessionCancelled
}

// PauseReason describes why a plan session paused.
type PauseReason string

const (
	PauseNoProgress        PauseReason = "no_progress"
	PauseBudgetExhausted   PauseReason = "budget_exhausted"
	PauseBlocked           PauseReason = "blocked"
	PauseUserInputRequired PauseReason = "user_input_required"
)

// Session is the in-memory lifecycle state of a plan session.
type Session struct {
	ID               PlanID
	Status           SessionStatus
	Requirement      string
	Continuations    int
	NoProgress       int
	PauseReason      PauseReason
	LastDecision     string
	CurrentTaskID    string
	TurnsUsed        int
	ToolCallsUsed    int
	MaxTurns         int
	MaxContinuations int
	MaxNoProgress    int
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

func (s Session) Snapshot() Session {
	return s
}

// EventType identifies plan runtime lifecycle events.
type EventType string

const (
	EventStarted   EventType = "plan.started"
	EventTurnDone  EventType = "plan.turn_completed"
	EventFinalized EventType = "plan.finalized"
	EventPaused    EventType = "plan.paused"
	EventFailed    EventType = "plan.failed"
	EventCancelled EventType = "plan.cancelled"
	EventResumed   EventType = "plan.resumed"
)

type Event struct {
	Type     EventType
	PlanID   PlanID
	Status   SessionStatus
	Doc      *PlanDoc
	Decision string
	Err      error
	At       time.Time
}

type EventSink func(Event)

func (e Event) String() string {
	return fmt.Sprintf("%s plan=%s status=%s", e.Type, e.PlanID, e.Status)
}
