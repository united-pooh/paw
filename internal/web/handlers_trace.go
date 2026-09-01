package web

import (
	"encoding/json"
	"errors"
	"net/http"

	"paw/internal/app"
)

func (s *Server) handleTraceDetail(writer http.ResponseWriter, request *http.Request) {
	runtime, ok := s.runtime(request)
	if !ok || runtime.TraceDetail == nil {
		writeJSONError(writer, http.StatusNotFound, "workspace_not_loaded", "workspace is not loaded", RequestID(request.Context()))
		return
	}
	detail, err := runtime.TraceDetail.Get(request.Context(), request.PathValue("event_id"))
	if err != nil {
		if errors.Is(err, app.ErrTraceDetailNotFound) {
			writeJSONError(writer, http.StatusNotFound, "trace_detail_not_found", "trace detail is unknown or has been evicted", RequestID(request.Context()))
			return
		}
		writeJSONError(writer, http.StatusInternalServerError, "trace_detail_failed", "trace detail could not be read", RequestID(request.Context()))
		return
	}
	writer.Header().Set("Content-Type", "application/json")
	writer.Header().Set("Cache-Control", "no-store")
	writer.WriteHeader(http.StatusOK)
	_ = writeJSONValue(writer, detail)
}

func writeJSONValue(writer http.ResponseWriter, value any) error {
	return json.NewEncoder(writer).Encode(value)
}
