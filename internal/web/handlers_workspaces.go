package web

import (
	"errors"
	"net/http"
	"strings"

	"paw/internal/app"
)

type openWorkspaceRequest struct {
	Path string `json:"path"`
}

type workspaceResponse struct {
	ID     app.WorkspaceID `json:"id"`
	Path   string          `json:"path"`
	Name   string          `json:"name"`
	Loaded bool            `json:"loaded"`
}

func (s *Server) handleRecentWorkspaces(writer http.ResponseWriter, request *http.Request) {
	items, err := s.supervisor.ListRecent(request.Context())
	if err != nil {
		writeJSONError(writer, http.StatusInternalServerError, "recent_workspaces_failed", err.Error(), RequestID(request.Context()))
		return
	}
	writeJSON(writer, http.StatusOK, struct {
		Items []app.RecentWorkspace `json:"items"`
	}{Items: items})
}

func (s *Server) handleOpenWorkspace(writer http.ResponseWriter, request *http.Request) {
	var input openWorkspaceRequest
	if err := DecodeJSON(writer, request, &input); err != nil {
		writeDecodeError(writer, request, err)
		return
	}
	workspace, err := app.CanonicalWorkspace(strings.TrimSpace(input.Path))
	if err != nil {
		writeJSONError(writer, http.StatusBadRequest, "invalid_workspace", err.Error(), RequestID(request.Context()))
		return
	}
	runtime, err := s.openRuntime(request.Context(), app.WorkspaceRuntimeOptions{
		Root: workspace.Path, AllowIncomplete: true, ControllerMode: app.ControllerModeWeb,
	})
	if err != nil {
		writeAppError(writer, request, err)
		return
	}
	writeJSON(writer, http.StatusOK, workspaceResponse{ID: workspace.ID, Path: runtime.Root, Name: workspace.Name, Loaded: true})
}

func (s *Server) handleCloseWorkspace(writer http.ResponseWriter, request *http.Request) {
	id := app.WorkspaceID(request.PathValue("workspace_id"))
	if err := s.supervisor.Close(request.Context(), id); err != nil {
		writeAppError(writer, request, err)
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleForgetRecent(writer http.ResponseWriter, request *http.Request) {
	id := app.WorkspaceID(request.PathValue("workspace_id"))
	if err := s.supervisor.ForgetRecent(request.Context(), id); err != nil {
		writeJSONError(writer, http.StatusInternalServerError, "forget_recent_failed", err.Error(), RequestID(request.Context()))
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

func (s *Server) runtime(request *http.Request) (*app.WorkspaceRuntime, bool) {
	return s.supervisor.Runtime(app.WorkspaceID(request.PathValue("workspace_id")))
}

func writeAppError(writer http.ResponseWriter, request *http.Request, err error) {
	requestID := RequestID(request.Context())
	switch {
	case errors.Is(err, app.ErrRuntimeCapacity):
		writeJSONError(writer, http.StatusConflict, "resource_capacity", err.Error(), requestID)
	case errors.Is(err, app.ErrWorkspaceBusy):
		writeJSONError(writer, http.StatusConflict, "workspace_busy", err.Error(), requestID)
	case errors.Is(err, app.ErrRuntimeNotFound):
		writeJSONError(writer, http.StatusNotFound, "workspace_not_loaded", err.Error(), requestID)
	case errors.Is(err, app.ErrWorkspaceLocked):
		writeJSONError(writer, http.StatusConflict, "workspace_locked", err.Error(), requestID)
	default:
		writeJSONError(writer, http.StatusInternalServerError, "internal_error", err.Error(), requestID)
	}
}

func writeDecodeError(writer http.ResponseWriter, request *http.Request, err error) {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) || strings.Contains(err.Error(), "request body too large") {
		writeJSONError(writer, http.StatusRequestEntityTooLarge, "body_too_large", err.Error(), RequestID(request.Context()))
		return
	}
	writeJSONError(writer, http.StatusBadRequest, "invalid_json", err.Error(), RequestID(request.Context()))
}
