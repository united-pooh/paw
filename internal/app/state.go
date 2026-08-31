package app

import "time"

type InputDraft struct {
	CommandID string    `json:"command_id,omitempty"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at,omitempty"`
}

type InteractionState struct {
	RequestID string `json:"request_id"`
	SessionID string `json:"session_id"`
	TurnID    string `json:"turn_id,omitempty"`
	Kind      string `json:"kind"`
}

type WorkspaceState struct {
	ActiveSessionID string                      `json:"active_session_id,omitempty"`
	ActiveTurnID    string                      `json:"active_turn_id,omitempty"`
	SessionVersion  map[string]uint64           `json:"session_version"`
	Queue           map[string][]InputDraft     `json:"queue"`
	Pending         map[string]InteractionState `json:"pending"`
	ActiveTasks     int                         `json:"active_tasks"`
	ActiveWrites    int                         `json:"active_writes"`
}

type SessionState struct {
	SessionID      string             `json:"session_id"`
	SessionVersion uint64             `json:"session_version"`
	Queue          []InputDraft       `json:"queue"`
	Pending        []InteractionState `json:"pending"`
}

func cloneWorkspaceState(state WorkspaceState) WorkspaceState {
	cloned := WorkspaceState{
		ActiveSessionID: state.ActiveSessionID,
		ActiveTurnID:    state.ActiveTurnID,
		SessionVersion:  make(map[string]uint64, len(state.SessionVersion)),
		Queue:           make(map[string][]InputDraft, len(state.Queue)),
		Pending:         make(map[string]InteractionState, len(state.Pending)),
		ActiveTasks:     state.ActiveTasks,
		ActiveWrites:    state.ActiveWrites,
	}
	for sessionID, version := range state.SessionVersion {
		cloned.SessionVersion[sessionID] = version
	}
	for sessionID, queue := range state.Queue {
		cloned.Queue[sessionID] = append([]InputDraft(nil), queue...)
	}
	for requestID, pending := range state.Pending {
		cloned.Pending[requestID] = pending
	}
	return cloned
}
