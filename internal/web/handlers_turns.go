package web

import (
	"errors"
	"net/http"

	"paw/internal/app"
	"paw/internal/session"
)

type activeTurnRequest struct {
	CommandID    string `json:"command_id"`
	ActiveTurnID string `json:"active_turn_id"`
	Text         string `json:"text,omitempty"`
}

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

func (s *Server) handleSteer(writer http.ResponseWriter, request *http.Request) {
	s.handleActiveTurnCommand(writer, request, func(service *app.TurnService, command app.ActiveTurnCommand) (app.CommandReceiptResult, error) {
		return service.Steer(request.Context(), command)
	})
}

func (s *Server) handleQueue(writer http.ResponseWriter, request *http.Request) {
	s.handleActiveTurnCommand(writer, request, func(service *app.TurnService, command app.ActiveTurnCommand) (app.CommandReceiptResult, error) {
		return service.Queue(request.Context(), command)
	})
}

func (s *Server) handleCancel(writer http.ResponseWriter, request *http.Request) {
	s.handleActiveTurnCommand(writer, request, func(service *app.TurnService, command app.ActiveTurnCommand) (app.CommandReceiptResult, error) {
		return service.Cancel(request.Context(), command)
	})
}

func (s *Server) handleActiveTurnCommand(writer http.ResponseWriter, request *http.Request, run func(*app.TurnService, app.ActiveTurnCommand) (app.CommandReceiptResult, error)) {
	runtime, ok := s.runtime(request)
	if !ok || runtime.TurnService == nil {
		writeJSONError(writer, http.StatusNotFound, "workspace_not_loaded", "workspace is not loaded", RequestID(request.Context()))
		return
	}
	var input activeTurnRequest
	if err := DecodeJSON(writer, request, &input); err != nil {
		writeDecodeError(writer, request, err)
		return
	}
	result, err := run(runtime.TurnService, app.ActiveTurnCommand{CommandID: input.CommandID, SessionID: request.PathValue("session_id"), ActiveTurnID: input.ActiveTurnID, Text: input.Text})
	if err != nil {
		writeTurnError(writer, request, runtime.TurnService, err)
		return
	}
	writeJSON(writer, http.StatusAccepted, result)
}

func writeTurnError(writer http.ResponseWriter, request *http.Request, service *app.TurnService, err error) {
	requestID := RequestID(request.Context())
	switch {
	case errors.Is(err, app.ErrInvalidTurnCommand):
		writeJSONError(writer, http.StatusBadRequest, "invalid_turn_command", "turn command is invalid", requestID)
	case errors.Is(err, session.ErrSessionNotFound):
		writeJSONError(writer, http.StatusNotFound, "session_not_found", "session was not found", requestID)
	case errors.Is(err, app.ErrSessionVersionChanged):
		writeJSONError(writer, http.StatusConflict, "session_version_changed", "session changed; reload before retrying", requestID)
	case errors.Is(err, app.ErrActiveTurnChanged), errors.Is(err, app.ErrNoActiveTurn):
		writeJSONError(writer, http.StatusConflict, "active_turn_changed", "active turn changed; reload before retrying", requestID)
	case errors.Is(err, app.ErrSteerNotAccepted):
		writeJSONError(writer, http.StatusConflict, "steer_not_accepted", "the active turn no longer accepts steering", requestID)
	case errors.Is(err, app.ErrWorkspaceBusy):
		sessionID, turnID := service.ActiveTurn()
		writeJSON(writer, http.StatusConflict, struct {
			Error           ErrorBody `json:"error"`
			ActiveSessionID string    `json:"active_session_id"`
			ActiveTurnID    string    `json:"active_turn_id"`
		}{Error: ErrorBody{Code: "workspace_busy", Message: "another session is active in this workspace", RequestID: requestID}, ActiveSessionID: sessionID, ActiveTurnID: turnID})
	default:
		writeJSONError(writer, http.StatusInternalServerError, "turn_command_failed", "turn command failed", requestID)
	}
}
