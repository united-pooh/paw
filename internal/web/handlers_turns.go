package web

import (
	"errors"
	"net/http"

	"paw/internal/app"
)

type submitMessageRequest struct {
	CommandID      string `json:"command_id"`
	SessionVersion uint64 `json:"session_version"`
	Text           string `json:"text"`
}

func (s *Server) handleSubmitMessage(writer http.ResponseWriter, request *http.Request) {
	runtime, ok := s.runtime(request)
	if !ok || runtime.TurnService == nil {
		writeJSONError(writer, http.StatusNotFound, "workspace_not_loaded", "workspace is not loaded", RequestID(request.Context()))
		return
	}
	var input submitMessageRequest
	if err := DecodeJSON(writer, request, &input); err != nil {
		writeDecodeError(writer, request, err)
		return
	}
	result, err := runtime.TurnService.Submit(request.Context(), app.SubmitCommand{
		CommandID: input.CommandID, SessionID: request.PathValue("session_id"), SessionVersion: input.SessionVersion, Text: input.Text,
	})
	if err != nil {
		writeTurnError(writer, request, runtime.TurnService, err)
		return
	}
	writeJSON(writer, http.StatusAccepted, result)
}

func writeTurnError(writer http.ResponseWriter, request *http.Request, service *app.TurnService, err error) {
	requestID := RequestID(request.Context())
	switch {
	case errors.Is(err, app.ErrSessionVersionChanged):
		writeJSONError(writer, http.StatusConflict, "session_version_changed", err.Error(), requestID)
	case errors.Is(err, app.ErrWorkspaceBusy):
		sessionID, turnID := service.ActiveTurn()
		writeJSON(writer, http.StatusConflict, struct {
			Error           ErrorBody `json:"error"`
			ActiveSessionID string    `json:"active_session_id"`
			ActiveTurnID    string    `json:"active_turn_id"`
		}{Error: ErrorBody{Code: "workspace_busy", Message: err.Error(), RequestID: requestID}, ActiveSessionID: sessionID, ActiveTurnID: turnID})
	default:
		writeJSONError(writer, http.StatusBadRequest, "turn_submit_failed", err.Error(), requestID)
	}
}
