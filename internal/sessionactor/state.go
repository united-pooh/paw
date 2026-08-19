package sessionactor

import (
	"encoding/json"

	"paw/internal/es"
	"paw/internal/loop"
	"paw/internal/message"
	"paw/internal/session"
)

func (a *SessionActor) Fold(env es.Envelope) error {
	switch env.Type {
	case EventGoalActivated:
		var binding goalBinding
		if err := json.Unmarshal(env.Payload, &binding); err != nil {
			return err
		}
		a.state.ActiveGoalID, a.state.GoalStatus = binding.GoalID, binding.Status
	case EventGoalCleared:
		a.state.ActiveGoalID, a.state.GoalStatus = "", ""
	case EventPlanSnapshot:
		var binding planBinding
		if err := json.Unmarshal(env.Payload, &binding); err != nil {
			return err
		}
		a.state.ActivePlanID, a.state.PlanStatus = binding.PlanID, binding.Status
		a.state.PlanSnapshot = append(json.RawMessage(nil), binding.Snapshot...)
	case EventPlanCleared:
		a.state.ActivePlanID, a.state.PlanStatus, a.state.PlanSnapshot = "", "", nil
	case EventPermissionRequested:
		var record permissionRecord
		if err := json.Unmarshal(env.Payload, &record); err != nil {
			return err
		}
		if a.state.permissionIndex(record.ID) < 0 {
			a.state.Permissions = append(a.state.Permissions, PermissionState{ID: record.ID, Request: record.Request})
		}
	case EventPermissionDecided:
		var record permissionRecord
		if err := json.Unmarshal(env.Payload, &record); err != nil {
			return err
		}
		a.applyPermissionDecision(record)
	case EventToolStarted:
		var started loop.ToolStart
		if err := json.Unmarshal(env.Payload, &started); err != nil {
			return err
		}
		if a.state.startedToolIndex(started.ToolCallID) < 0 {
			a.state.StartedTools = append(a.state.StartedTools, ToolStartState{ToolStart: started})
		}
	case session.EventToolResult:
		var payload struct {
			ToolResult *message.ToolResult `json:"tool_result"`
		}
		if err := json.Unmarshal(env.Payload, &payload); err != nil {
			return err
		}
		if payload.ToolResult != nil {
			a.state.removeStartedTool(payload.ToolResult.ToolUseID)
		}
	case session.EventTurnCompleted, session.EventTurnFailed:
		var payload struct {
			TurnID string `json:"turn_id"`
		}
		if err := json.Unmarshal(env.Payload, &payload); err != nil {
			return err
		}
		a.state.removeTurnTools(payload.TurnID)
	}
	return nil
}

func (a *SessionActor) applyPermissionDecision(record permissionRecord) {
	if index := a.state.permissionIndex(record.ID); index >= 0 {
		a.state.Permissions[index].Decision = record.Decision
		return
	}
	a.state.Permissions = append(a.state.Permissions, PermissionState{ID: record.ID, Request: record.Request, Decision: record.Decision})
}

func (s State) startedToolIndex(callID string) int {
	for i := range s.StartedTools {
		if s.StartedTools[i].ToolCallID == callID {
			return i
		}
	}
	return -1
}

func (s *State) removeStartedTool(callID string) {
	if index := s.startedToolIndex(callID); index >= 0 {
		s.StartedTools = append(s.StartedTools[:index], s.StartedTools[index+1:]...)
	}
}

func (s *State) removeTurnTools(turnID string) {
	kept := s.StartedTools[:0]
	for _, started := range s.StartedTools {
		if started.TurnID != turnID {
			kept = append(kept, started)
		}
	}
	s.StartedTools = kept
}

func (s State) permissionIndex(id string) int {
	for i := range s.Permissions {
		if s.Permissions[i].ID == id {
			return i
		}
	}
	return -1
}
