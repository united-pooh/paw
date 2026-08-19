package sessionactor

import (
	"encoding/json"
	"time"

	"paw/internal/loop"
	"paw/internal/message"
)

const actorType = "session"

const (
	msgRunTurn            = "session.run_turn"
	msgGetState           = "session.get_state"
	msgGoalBind           = "session.goal_bind"
	msgGoalClear          = "session.goal_clear"
	msgPlanSave           = "session.plan_save"
	msgPlanClear          = "session.plan_clear"
	msgPermissionDecision = "session.permission_decision"

	EventTurnInterrupted     = "session.turn_interrupted"
	EventToolStarted         = "session.tool_started"
	EventPermissionRequested = "session.permission_requested"
	EventPermissionDecided   = "session.permission_decided"
	EventGoalActivated       = "session.goal_activated"
	EventGoalCleared         = "session.goal_cleared"
	EventPlanSnapshot        = "session.plan_snapshot"
	EventPlanCleared         = "session.plan_cleared"
)

type turnRequest struct {
	Input  message.Message `json:"input"`
	Timing loop.TurnTiming `json:"timing"`
	Single bool            `json:"single,omitempty"`
}

type turnReply struct {
	Execution loop.TurnExecution
	Error     string
}

type goalBinding struct {
	GoalID string `json:"goal_id"`
	Status string `json:"status,omitempty"`
}

type planBinding struct {
	PlanID   string          `json:"plan_id"`
	Status   string          `json:"status,omitempty"`
	Snapshot json.RawMessage `json:"snapshot"`
}

type interruptedTurn struct {
	TurnID string    `json:"turn_id"`
	Error  string    `json:"error"`
	At     time.Time `json:"at"`
}

type permissionRecord struct {
	ID       string                  `json:"id"`
	Request  loop.PermissionRequest  `json:"request"`
	Decision loop.PermissionDecision `json:"decision,omitempty"`
	At       time.Time               `json:"at"`
}

type PermissionState struct {
	ID       string                  `json:"id"`
	Request  loop.PermissionRequest  `json:"request"`
	Decision loop.PermissionDecision `json:"decision,omitempty"`
}

type ToolStartState struct {
	loop.ToolStart
}

type State struct {
	ActiveGoalID string            `json:"active_goal_id,omitempty"`
	GoalStatus   string            `json:"goal_status,omitempty"`
	ActivePlanID string            `json:"active_plan_id,omitempty"`
	PlanStatus   string            `json:"plan_status,omitempty"`
	PlanSnapshot json.RawMessage   `json:"plan_snapshot,omitempty"`
	Permissions  []PermissionState `json:"permissions,omitempty"`
	StartedTools []ToolStartState  `json:"started_tools,omitempty"`
}

func (s State) clone() State {
	s.PlanSnapshot = append(json.RawMessage(nil), s.PlanSnapshot...)
	s.Permissions = append([]PermissionState(nil), s.Permissions...)
	s.StartedTools = append([]ToolStartState(nil), s.StartedTools...)
	return s
}
