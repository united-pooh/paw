package session

import (
	"encoding/json"
	"fmt"

	"paw/internal/es"
	"paw/internal/message"
	"paw/internal/todo"
)

// Session 域事件类型。与 JournalKind 一一对应（tool_call 独立事件未引入，
// 保留在 assistant_message payload 内，见设计文档 4.3.3 注）。
const (
	EventUserMessage      = "session.user_message"
	EventTurnStarted      = "session.turn_started"
	EventAssistant        = "session.assistant_message"
	EventAssistantPartial = "session.assistant_partial"
	EventToolResult       = "session.tool_result"
	EventTurnCompleted    = "session.turn_completed"
	EventTurnFailed       = "session.turn_failed"
	EventTurnStopped      = "session.turn_stopped"
	EventTodoUpserted     = "session.todo_upserted"
	EventMemoryUpdated    = "session.memory_updated"
	EventAriadneUpdated   = "session.ariadne_updated"
	EventStateCompacted   = "session.state_compacted"
	EventCommandReceipt   = "session.command_receipt"
)

// sessionEventPayload 是 session 域事件的统一 payload。字段与 Record 一一
// 对应，保证 Record ↔ Envelope 转换无损失。
type sessionEventPayload struct {
	TurnID         string              `json:"turn_id,omitempty"`
	CallIndex      *int                `json:"call_index,omitempty"`
	Message        *message.Message    `json:"message,omitempty"`
	ToolResult     *message.ToolResult `json:"tool_result,omitempty"`
	Error          string              `json:"error,omitempty"`
	Snapshot       *todo.Snapshot      `json:"snapshot,omitempty"`
	StateEvent     *StateEventRecord   `json:"state_event,omitempty"`
	CommandReceipt *CommandReceipt     `json:"command_receipt,omitempty"`
}

var kindToEvent = map[JournalKind]string{
	JournalMessage:          EventUserMessage,
	JournalTurnStarted:      EventTurnStarted,
	JournalAssistant:        EventAssistant,
	JournalAssistantPartial: EventAssistantPartial,
	JournalToolResult:       EventToolResult,
	JournalTurnCompleted:    EventTurnCompleted,
	JournalTurnFailed:       EventTurnFailed,
	JournalTurnStopped:      EventTurnStopped,
	JournalTodoSnapshot:     EventTodoUpserted,
	JournalMemoryUpdated:    EventMemoryUpdated,
	JournalAriadneUpdated:   EventAriadneUpdated,
	JournalStateCompacted:   EventStateCompacted,
	JournalCommandReceipt:   EventCommandReceipt,
}

var eventToKind = map[string]JournalKind{
	EventUserMessage:      JournalMessage,
	EventTurnStarted:      JournalTurnStarted,
	EventAssistant:        JournalAssistant,
	EventAssistantPartial: JournalAssistantPartial,
	EventToolResult:       JournalToolResult,
	EventTurnCompleted:    JournalTurnCompleted,
	EventTurnFailed:       JournalTurnFailed,
	EventTurnStopped:      JournalTurnStopped,
	EventTodoUpserted:     JournalTodoSnapshot,
	EventMemoryUpdated:    JournalMemoryUpdated,
	EventAriadneUpdated:   JournalAriadneUpdated,
	EventStateCompacted:   JournalStateCompacted,
	EventCommandReceipt:   JournalCommandReceipt,
}

// recordToEnvelope 将一条 Record 映射为统一信封。seq 与 created_at 由调用方
// （appendRecords）在映射前填充。
func recordToEnvelope(rec Record) (es.Envelope, error) {
	typ, ok := kindToEvent[rec.Kind]
	if !ok {
		return es.Envelope{}, fmt.Errorf("session: record kind %q has no event type", rec.Kind)
	}
	payload := sessionEventPayload{
		TurnID:         rec.TurnID,
		CallIndex:      rec.CallIndex,
		Message:        nil,
		ToolResult:     rec.ToolResult,
		Error:          rec.Error,
		Snapshot:       rec.TodoSnapshot,
		StateEvent:     rec.StateEvent,
		CommandReceipt: rec.CommandReceipt,
	}
	if rec.Kind == JournalMessage || rec.Kind == JournalAssistant || rec.Kind == JournalAssistantPartial || rec.Kind == JournalToolResult {
		msg := rec.Message
		payload.Message = &msg
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return es.Envelope{}, fmt.Errorf("session: encode payload: %w", err)
	}
	return es.Envelope{
		Seq:           rec.Seq,
		Kind:          es.KindDomain,
		Type:          typ,
		OccurredAt:    rec.CreatedAt,
		SchemaVersion: 1,
		Payload:       raw,
	}, nil
}

// envelopeToRecord 将统一信封映射回 Record。todo 快照事件映射为带
// TodoSnapshot 的 JournalTodoSnapshot 记录；其余字段无损失往返。
func envelopeToRecord(env es.Envelope) (Record, error) {
	kind, ok := eventToKind[env.Type]
	if !ok {
		return Record{}, fmt.Errorf("session: event type %q has no record kind", env.Type)
	}
	var payload sessionEventPayload
	if err := json.Unmarshal(env.Payload, &payload); err != nil {
		return Record{}, fmt.Errorf("session: decode payload of %q: %w", env.Type, err)
	}
	rec := Record{
		Seq:            env.Seq,
		Kind:           kind,
		TurnID:         payload.TurnID,
		CallIndex:      payload.CallIndex,
		ToolResult:     payload.ToolResult,
		Error:          payload.Error,
		TodoSnapshot:   payload.Snapshot,
		StateEvent:     payload.StateEvent,
		CommandReceipt: payload.CommandReceipt,
		CreatedAt:      env.OccurredAt,
	}
	if payload.Message != nil {
		rec.Message = *payload.Message
	}
	return rec, nil
}

// isEnvelopeLine 检测一行 transcript 是统一信封（新格式）还是 legacy Record。
func isEnvelopeLine(line []byte) bool {
	var probe struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(line, &probe); err != nil {
		return false
	}
	return probe.Type != ""
}

// ParseTranscriptLine 解析一行 transcript，统一信封（新格式）与 legacy
// Record（旧格式）都还原为 Record。语义与 store 内部读取（readOwnRecords）
// 完全一致，供 UI 等外部读取方复用。
func ParseTranscriptLine(line []byte) (Record, error) {
	if isEnvelopeLine(line) {
		var env es.Envelope
		if err := json.Unmarshal(line, &env); err != nil {
			return Record{}, err
		}
		if err := env.Validate(); err != nil {
			return Record{}, err
		}
		if env.Kind == es.KindRuntime {
			return Record{}, nil
		}
		if _, ok := eventToKind[env.Type]; !ok {
			return Record{}, nil
		}
		return envelopeToRecord(env)
	}
	var rec Record
	if err := json.Unmarshal(line, &rec); err != nil {
		return Record{}, err
	}
	if rec.Kind == "" {
		rec.Kind = JournalMessage
	}
	return rec, nil
}
