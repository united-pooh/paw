package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"

	"paw/internal/app"
)

const maxExportBytes = 2 * 1024 * 1024

var unsafeFilename = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

func (s *Server) handleExportSession(writer http.ResponseWriter, request *http.Request) {
	runtime, ok := s.runtime(request)
	if !ok || runtime.SessionService == nil {
		writeJSONError(writer, http.StatusNotFound, "workspace_not_loaded", "workspace is not loaded", RequestID(request.Context()))
		return
	}
	snapshot, err := runtime.SessionService.ConsistentSnapshot(request.Context(), request.PathValue("session_id"), app.SnapshotRequest{Limit: 100}, runtime.EventHub)
	if err != nil {
		writeJSONError(writer, http.StatusNotFound, "session_export_failed", err.Error(), RequestID(request.Context()))
		return
	}
	data, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		writeJSONError(writer, http.StatusInternalServerError, "session_export_failed", err.Error(), RequestID(request.Context()))
		return
	}
	truncated := false
	if len(data) > maxExportBytes {
		data = data[:maxExportBytes]
		truncated = true
	}
	filename := unsafeFilename.ReplaceAllString(snapshot.SessionID, "-") + ".json"
	writer.Header().Set("Content-Type", "application/json")
	writer.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename=%q`, filename))
	if truncated {
		writer.Header().Set("X-Paw-Truncated", "true")
	}
	writer.WriteHeader(http.StatusOK)
	_, _ = writer.Write(data)
}
