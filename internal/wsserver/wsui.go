package wsserver

import (
	"encoding/json"

	"codex-agent-go/internal/loop"
	"codex-agent-go/internal/ui"
)

// WSUI implements ui.UI (and optional extensions) by broadcasting SessionEvents
// over WebSocket to all connected clients.
type WSUI struct {
	server    *Server
	sessionID string
}

// Ensure WSUI satisfies ui.UI and all optional extensions at compile time.
var _ ui.UI = (*WSUI)(nil)
var _ ui.ThinkingDeltaReceiver = (*WSUI)(nil)
var _ ui.SystemNotifier = (*WSUI)(nil)
var _ ui.OldContentConsumer = (*WSUI)(nil)

// NewWSUI creates a WSUI that broadcasts events via server, tagging each event
// with the given sessionID.
func NewWSUI(server *Server, sessionID string) *WSUI {
	return &WSUI{server: server, sessionID: sessionID}
}

// SetSessionID updates the session ID used to tag broadcast events.
// Call this after the runner has resolved the final session ID.
func (w *WSUI) SetSessionID(sessionID string) {
	w.sessionID = sessionID
}

func (w *WSUI) newEvent(kind loop.SessionEventKind) loop.SessionEvent {
	return loop.SessionEvent{
		SessionID: w.sessionID,
		Kind:      kind,
	}
}

// OnAssistantDelta broadcasts a delta_chunk event for each non-empty streaming chunk.
func (w *WSUI) OnAssistantDelta(text string) error {
	if text == "" {
		return nil
	}
	ev := w.newEvent(loop.EventKindDeltaChunk)
	ev.DeltaChunk = &loop.SessionDeltaChunkPayload{Text: text}
	w.server.Broadcast(ev)
	return nil
}

// OnThinkingDelta satisfies ui.ThinkingDeltaReceiver; thinking deltas are not broadcast.
func (w *WSUI) OnThinkingDelta(text string) error {
	return nil
}

// OnToolCall broadcasts a tool_call_fired event.
func (w *WSUI) OnToolCall(event ui.ToolCallEvent) error {
	input, _ := json.Marshal(event.Input)
	ev := w.newEvent(loop.EventKindToolCallFired)
	ev.ToolCall = &loop.SessionToolCallPayload{
		ID:    event.ID,
		Name:  event.Name,
		Input: input,
	}
	w.server.Broadcast(ev)
	return nil
}

// OnToolResult broadcasts a tool_result event.
func (w *WSUI) OnToolResult(event ui.ToolResultEvent) error {
	ev := w.newEvent(loop.EventKindToolResult)
	ev.ToolResult = &loop.SessionToolResultPayload{
		ToolUseID: event.ToolUseID,
		Name:      event.Name,
		Content:   event.Content,
		IsError:   event.IsError,
	}
	w.server.Broadcast(ev)
	return nil
}

// OnDone broadcasts a turn_committed event signalling the end of a turn.
func (w *WSUI) OnDone() error {
	ev := w.newEvent(loop.EventKindTurnCommitted)
	ev.TurnCommit = &loop.SessionTurnCommitPayload{MessageCount: 0}
	w.server.Broadcast(ev)
	return nil
}

// OnSystemMessage satisfies ui.SystemNotifier; system messages are not broadcast.
func (w *WSUI) OnSystemMessage(event ui.SystemEvent) error {
	return nil
}

// ConsumesOldContent satisfies ui.OldContentConsumer. WSUI does not use OldContent,
// so returns false to avoid unnecessary disk reads.
func (w *WSUI) ConsumesOldContent() bool {
	return false
}
