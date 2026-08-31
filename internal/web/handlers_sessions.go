package web

import (
	"net/http"
	"strconv"

	"paw/internal/app"
)

type createSessionRequest struct {
	CommandID string `json:"command_id"`
	SessionID string `json:"session_id,omitempty"`
}

type forkSessionRequest struct {
	CommandID string `json:"command_id"`
	SessionID string `json:"session_id,omitempty"`
}

func (s *Server) handleSessions(writer http.ResponseWriter, request *http.Request) {
	runtime, ok := s.runtime(request)
	if !ok || runtime.SessionService == nil {
		writeJSONError(writer, http.StatusNotFound, "workspace_not_loaded", "workspace is not loaded", RequestID(request.Context()))
		return
	}
	limit, _ := strconv.Atoi(request.URL.Query().Get("limit"))
	page, err := runtime.SessionService.List(request.Context(), app.SessionPageRequest{
		Cursor: request.URL.Query().Get("cursor"), Limit: limit,
	})
	if err != nil {
		writeJSONError(writer, http.StatusBadRequest, "session_list_failed", err.Error(), RequestID(request.Context()))
		return
	}
	writeJSON(writer, http.StatusOK, page)
}

func (s *Server) handleCreateSession(writer http.ResponseWriter, request *http.Request) {
	runtime, ok := s.runtime(request)
	if !ok || runtime.SessionService == nil {
		writeJSONError(writer, http.StatusNotFound, "workspace_not_loaded", "workspace is not loaded", RequestID(request.Context()))
		return
	}
	var input createSessionRequest
	if err := DecodeJSON(writer, request, &input); err != nil {
		writeDecodeError(writer, request, err)
		return
	}
	result, err := runtime.SessionService.Create(request.Context(), app.CreateSessionCommand{CommandID: input.CommandID, SessionID: input.SessionID})
	if err != nil {
		writeJSONError(writer, http.StatusBadRequest, "session_create_failed", err.Error(), RequestID(request.Context()))
		return
	}
	writeJSON(writer, http.StatusAccepted, result)
}

func (s *Server) handleSessionSnapshot(writer http.ResponseWriter, request *http.Request) {
	runtime, ok := s.runtime(request)
	if !ok || runtime.SessionService == nil {
		writeJSONError(writer, http.StatusNotFound, "workspace_not_loaded", "workspace is not loaded", RequestID(request.Context()))
		return
	}
	limit, _ := strconv.Atoi(request.URL.Query().Get("limit"))
	snapshot, err := runtime.SessionService.ConsistentSnapshot(request.Context(), request.PathValue("session_id"), app.SnapshotRequest{
		Before: request.URL.Query().Get("before"), Limit: limit,
	}, runtime.EventHub)
	if err != nil {
		writeJSONError(writer, http.StatusNotFound, "session_snapshot_failed", err.Error(), RequestID(request.Context()))
		return
	}
	writeJSON(writer, http.StatusOK, snapshot)
}

func (s *Server) handleForkSession(writer http.ResponseWriter, request *http.Request) {
	runtime, ok := s.runtime(request)
	if !ok || runtime.SessionService == nil {
		writeJSONError(writer, http.StatusNotFound, "workspace_not_loaded", "workspace is not loaded", RequestID(request.Context()))
		return
	}
	var input forkSessionRequest
	if err := DecodeJSON(writer, request, &input); err != nil {
		writeDecodeError(writer, request, err)
		return
	}
	result, err := runtime.SessionService.Fork(request.Context(), app.ForkSessionCommand{
		CommandID: input.CommandID, SessionID: input.SessionID, ParentSessionID: request.PathValue("session_id"),
	})
	if err != nil {
		writeJSONError(writer, http.StatusBadRequest, "session_fork_failed", err.Error(), RequestID(request.Context()))
		return
	}
	writeJSON(writer, http.StatusAccepted, result)
}
