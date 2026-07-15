package wsserver

import (
	"context"
	"encoding/json"

	"codex-agent-go/internal/loop"
	"codex-agent-go/internal/model"
	"codex-agent-go/internal/subagent"
	"codex-agent-go/internal/ui"
)

// WSUI implements ui.UI (and optional extensions) by broadcasting SessionEvents
// over WebSocket to all connected clients.
type WSUI struct {
	server    *Server
	sessionID string
	registry  *AgentRegistry // routes lifecycle callbacks to persona slots
}

// Ensure WSUI satisfies ui.UI and all optional extensions at compile time.
var _ ui.UI = (*WSUI)(nil)
var _ ui.ThinkingDeltaReceiver = (*WSUI)(nil)
var _ ui.SystemNotifier = (*WSUI)(nil)
var _ ui.OldContentConsumer = (*WSUI)(nil)

// Ensure WSUI satisfies taskLifecycleNotifier at compile time.
// taskLifecycleNotifier is defined (unexported) in internal/subagent/manager.go.
var _ interface {
	OnTaskStarted(task subagent.TaskSnapshot)
	OnTaskFinished(task subagent.TaskSnapshot)
} = (*WSUI)(nil)

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

// SetRegistry wires the AgentRegistry so task lifecycle callbacks update persona state.
func (w *WSUI) SetRegistry(r *AgentRegistry) {
	w.registry = r
}

// OnTaskStarted activates the named persona and broadcasts an updated snapshot.
// Called by subagent.Manager when a task starts (via taskLifecycleNotifier duck-type).
func (w *WSUI) OnTaskStarted(task subagent.TaskSnapshot) {
	if w.registry == nil {
		return
	}
	if _, accepted := w.registry.ActivateTask(context.Background(), task.Name, task.ID); !accepted {
		return
	}
	w.broadcastSnapshot()
}

// OnTaskFinished records the exact terminal task state and broadcasts a snapshot.
func (w *WSUI) OnTaskFinished(task subagent.TaskSnapshot) {
	if w.registry == nil {
		return
	}
	status, valid := agentStatusForTask(task.Status)
	if !valid || !w.registry.FinishTask(task.Name, task.ID, status) {
		return
	}
	w.broadcastSnapshot()
}

func agentStatusForTask(status subagent.TaskStatus) (AgentStatus, bool) {
	switch status {
	case subagent.TaskCompleted:
		return AgentStatusDone, true
	case subagent.TaskFailed:
		return AgentStatusFailed, true
	case subagent.TaskStopped:
		return AgentStatusStopped, true
	default:
		return "", false
	}
}

// OnModelUsage broadcasts a usage_update event after each model response.
func (w *WSUI) OnModelUsage(usage model.Usage) {
	ev := w.newEvent(loop.EventKindUsageUpdate)
	ev.Usage = &loop.SessionUsagePayload{Usage: usage, IsSession: false}
	w.server.Broadcast(ev)
}

// broadcastSnapshot calls registry.Snapshot() and broadcasts a subagents_snapshot event.
func (w *WSUI) broadcastSnapshot() {
	if w.registry == nil {
		return
	}
	ev := w.newEvent(loop.EventKindSubagentsSnapshot)
	ev.SubagentsSnapshot = &loop.SessionSubagentsSnapshotPayload{
		Agents: w.registry.Snapshot(),
	}
	w.server.Broadcast(ev)
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

// OnSystemMessage broadcasts a system_message event.
func (w *WSUI) OnSystemMessage(event ui.SystemEvent) error {
	ev := w.newEvent(loop.EventKindSystemMessage)
	ev.SystemMessage = &loop.SessionSystemMessagePayload{
		Title:     event.Title,
		Body:      event.Body,
		Color:     event.Color,
		TaskID:    event.TaskID,
		AgentID:   event.AgentID,
		AgentName: event.AgentName,
		Status:    event.Status,
		IsError:   event.IsError,
	}
	w.server.Broadcast(ev)
	return nil
}

// ConsumesOldContent satisfies ui.OldContentConsumer. WSUI does not use OldContent,
// so returns false to avoid unnecessary disk reads.
func (w *WSUI) ConsumesOldContent() bool {
	return false
}
