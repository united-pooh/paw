package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"paw/internal/app"
)

const defaultSSEHeartbeat = 15 * time.Second

type heartbeatTicker interface {
	C() <-chan time.Time
	Stop()
}

type realHeartbeatTicker struct{ ticker *time.Ticker }

func (t realHeartbeatTicker) C() <-chan time.Time { return t.ticker.C }
func (t realHeartbeatTicker) Stop()               { t.ticker.Stop() }

type SSEHandler struct {
	Supervisor *app.Supervisor
	NewTicker  func(time.Duration) heartbeatTicker
	Heartbeat  time.Duration
}

func (h SSEHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if h.Supervisor == nil {
		writeJSONError(writer, http.StatusServiceUnavailable, "events_unavailable", "event service unavailable", RequestID(request.Context()))
		return
	}
	runtime, ok := h.Supervisor.Runtime(app.WorkspaceID(request.PathValue("workspace_id")))
	if !ok || runtime.EventHub == nil {
		writeJSONError(writer, http.StatusNotFound, "workspace_not_loaded", "workspace is not loaded", RequestID(request.Context()))
		return
	}
	flusher, ok := writer.(http.Flusher)
	if !ok {
		writeJSONError(writer, http.StatusInternalServerError, "stream_unsupported", "streaming is unsupported", RequestID(request.Context()))
		return
	}
	cursor, err := parseEventCursor(request)
	if err != nil {
		writeJSONError(writer, http.StatusBadRequest, "invalid_cursor", err.Error(), RequestID(request.Context()))
		return
	}
	subscription, err := runtime.EventHub.Subscribe(cursor)
	if err != nil {
		writeJSONError(writer, http.StatusInternalServerError, "event_subscribe_failed", err.Error(), RequestID(request.Context()))
		return
	}
	defer subscription.Close()
	writer.Header().Set("Content-Type", "text/event-stream")
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("Connection", "keep-alive")

	writeEvent := func(event app.AppEvent) bool {
		data, err := json.Marshal(event)
		if err != nil {
			return false
		}
		_, _ = fmt.Fprintf(writer, "id: %s:%d\n", event.StreamID, event.Sequence)
		_, _ = fmt.Fprintf(writer, "event: %s\n", event.Type)
		_, _ = fmt.Fprintf(writer, "data: %s\n\n", data)
		flusher.Flush()
		return true
	}
	writeReset := func(reason app.ResetReason) {
		cursor := runtime.EventHub.CurrentCursor()
		reset, resetErr := app.NewAppEvent(app.WorkspaceID(request.PathValue("workspace_id")), "", "", app.EventResetRequired, time.Now(), 0, app.ResetRequiredPayload{
			Reason: string(reason), CurrentStreamID: cursor.StreamID, LatestSequence: cursor.Sequence,
		})
		if resetErr == nil {
			reset.StreamID = cursor.StreamID
			reset.Sequence = cursor.Sequence
			_ = writeEvent(reset)
		}
	}
	select {
	case reason, ok := <-subscription.Reset:
		if ok {
			writeReset(reason)
		}
		return
	default:
	}
	for _, event := range subscription.Replay {
		if !writeEvent(event) {
			return
		}
	}

	heartbeat := h.Heartbeat
	if heartbeat <= 0 {
		heartbeat = defaultSSEHeartbeat
	}
	newTicker := h.NewTicker
	if newTicker == nil {
		newTicker = func(interval time.Duration) heartbeatTicker {
			return realHeartbeatTicker{ticker: time.NewTicker(interval)}
		}
	}
	ticker := newTicker(heartbeat)
	defer ticker.Stop()
	for {
		select {
		case event, ok := <-subscription.Events:
			if !ok {
				return
			}
			if !writeEvent(event) {
				return
			}
		case reason, ok := <-subscription.Reset:
			if ok {
				writeReset(reason)
			}
			return
		case <-ticker.C():
			_, _ = writer.Write([]byte(": heartbeat\n\n"))
			flusher.Flush()
		case <-request.Context().Done():
			return
		}
	}
}

func parseEventCursor(request *http.Request) (app.EventCursor, error) {
	value := strings.TrimSpace(request.URL.Query().Get("after"))
	if value == "" {
		value = strings.TrimSpace(request.Header.Get("Last-Event-ID"))
	}
	if value == "" {
		return app.EventCursor{}, nil
	}
	streamID, sequenceText, ok := strings.Cut(value, ":")
	if !ok || streamID == "" {
		return app.EventCursor{}, fmt.Errorf("cursor must be stream_id:sequence")
	}
	sequence, err := strconv.ParseUint(sequenceText, 10, 64)
	if err != nil {
		return app.EventCursor{}, fmt.Errorf("invalid cursor sequence: %w", err)
	}
	return app.EventCursor{StreamID: streamID, Sequence: sequence}, nil
}
