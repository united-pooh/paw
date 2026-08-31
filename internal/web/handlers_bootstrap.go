package web

import (
	"encoding/json"
	"net/http"

	"paw/internal/app"
)

type bootstrapResponse struct {
	SchemaVersion    uint16                `json:"schema_version"`
	RecentWorkspaces []app.RecentWorkspace `json:"recent_workspaces"`
	LoadedRuntimes   int                   `json:"loaded_runtimes"`
}

func (s *Server) handleBootstrap(writer http.ResponseWriter, request *http.Request) {
	recent, err := s.supervisor.ListRecent(request.Context())
	if err != nil {
		writeJSONError(writer, http.StatusInternalServerError, "bootstrap_failed", err.Error(), RequestID(request.Context()))
		return
	}
	writeJSON(writer, http.StatusOK, bootstrapResponse{
		SchemaVersion: app.AppEventSchemaVersion, RecentWorkspaces: recent, LoadedRuntimes: s.supervisor.LoadedCount(),
	})
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}
