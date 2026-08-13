package session

import (
	"context"
	"fmt"
	"paw/internal/message"
	"strings"
	"time"
)

// JournalKind identifies an append-only transcript record. Empty Kind is
// treated as a legacy message record by the JSONL decoder.
type JournalKind string

const (
	JournalMessage          JournalKind = "message"
	JournalTurnStarted      JournalKind = "turn_started"
	JournalAssistant        JournalKind = "assistant_message"
	JournalAssistantPartial JournalKind = "assistant_partial"
	JournalToolResult       JournalKind = "tool_result"
	JournalTurnCompleted    JournalKind = "turn_completed"
	JournalTurnFailed       JournalKind = "turn_failed"
	JournalTodoSnapshot     JournalKind = "todo_snapshot"
	JournalMemoryUpdated    JournalKind = "memory_updated"
	JournalAriadneUpdated   JournalKind = "ariadne_updated"
	JournalStateCompacted   JournalKind = "state_compacted"
)

// StateEventKind 区分状态文件更新事件的种类（memory/ariadne）。
type StateEventKind string

const (
	StateEventMemory    StateEventKind = "memory"
	StateEventAriadne   StateEventKind = "ariadne"
	StateEventCompacted StateEventKind = "compacted"
)

// StateEventRecord 是状态文件更新事件的载荷（内容在文件，事件只留审计
// 摘要，设计文档 §8）。
type StateEventRecord struct {
	Kind      StateEventKind `json:"kind"`
	Summary   string         `json:"summary"`
	UpdatedAt time.Time      `json:"updated_at"`
}

// RecoveryState describes the latest turn that was not completed normally.
// It is local recovery metadata and is never sent to the model directly.
type RecoveryState struct {
	TurnID               string
	Error                string
	CompletedToolResults []message.ToolResult
	DroppedToolCalls     []message.ToolCall
	Interrupted          bool
}

// SessionSnapshot separates the messages shown by the UI from the safe
// history sent to the next model request.
type SessionSnapshot struct {
	Messages      []message.Message
	ActiveHistory []message.Message
	Recovery      *RecoveryState
}

type journalEntry struct {
	message   message.Message
	result    *message.ToolResult
	callIndex int
	isResult  bool
}

// TurnJournal is an optional incremental persistence capability. History-only
// stores remain valid; the production JSONL store implements this interface.
type TurnJournal interface {
	BeginTurn(ctx context.Context, sessionID, turnID string, messages ...message.Message) error
	AppendAssistant(ctx context.Context, sessionID, turnID string, msg message.Message) error
	AppendToolResult(ctx context.Context, sessionID, turnID string, callIndex int, result message.ToolResult) error
	CompleteTurn(ctx context.Context, sessionID, turnID string) error
	FailTurn(ctx context.Context, sessionID, turnID string, err error) error
	LoadSnapshot(ctx context.Context, sessionID string) (SessionSnapshot, error)
}

// PartialAssistantJournal is an optional extension for stores that can retain
// visible assistant text from a failed turn without adding it to model history.
type PartialAssistantJournal interface {
	AppendPartialAssistant(ctx context.Context, sessionID, turnID string, msg message.Message) error
}

func journalTurn(kind JournalKind, turnID string) Record {
	return Record{Kind: kind, TurnID: strings.TrimSpace(turnID)}
}

func journalError(err error) string {
	if err == nil {
		return ""
	}
	const maxErrorBytes = 8192
	text := strings.TrimSpace(err.Error())
	if len(text) > maxErrorBytes {
		text = text[:maxErrorBytes] + "..."
	}
	return text
}

func validateTurnArgs(sessionID, turnID string) error {
	if strings.TrimSpace(sessionID) == "" {
		return fmt.Errorf("sessionID 不能为空")
	}
	if strings.TrimSpace(turnID) == "" {
		return fmt.Errorf("turnID 不能为空")
	}
	return nil
}

func copyToolResults(results []message.ToolResult) []message.ToolResult {
	if len(results) == 0 {
		return nil
	}
	out := make([]message.ToolResult, len(results))
	copy(out, results)
	return out
}

func copyToolCalls(calls []message.ToolCall) []message.ToolCall {
	if len(calls) == 0 {
		return nil
	}
	out := make([]message.ToolCall, len(calls))
	copy(out, calls)
	return out
}

func copyRecovery(in *RecoveryState) *RecoveryState {
	if in == nil {
		return nil
	}
	out := *in
	out.CompletedToolResults = copyToolResults(in.CompletedToolResults)
	out.DroppedToolCalls = copyToolCalls(in.DroppedToolCalls)
	return &out
}

func toolCallsFromMessage(msg message.Message) []message.ToolCall {
	if len(msg.ToolUses) > 0 {
		return append([]message.ToolCall(nil), msg.ToolUses...)
	}
	if msg.ToolUse == nil {
		return nil
	}
	return []message.ToolCall{*msg.ToolUse}
}

func buildToolResultsMessage(results []message.ToolResult) message.Message {
	msg := message.Message{Role: message.RoleUser}
	if len(results) == 1 {
		result := results[0]
		msg.ToolResult = &result
	} else if len(results) > 1 {
		msg.ToolResults = append([]message.ToolResult(nil), results...)
	}
	return msg
}

func entriesForTurn(records []Record, turnID string) []journalEntry {
	entries := make([]journalEntry, 0)
	for _, record := range records {
		if record.TurnID != turnID {
			continue
		}
		switch record.Kind {
		case JournalMessage, JournalAssistant:
			entries = append(entries, journalEntry{message: record.Message})
		case JournalToolResult:
			result := record.ToolResult
			if result == nil && record.Message.ToolResult != nil {
				result = record.Message.ToolResult
			}
			if result == nil {
				continue
			}
			index := -1
			if record.CallIndex != nil {
				index = *record.CallIndex
			}
			resultCopy := *result
			entries = append(entries, journalEntry{result: &resultCopy, callIndex: index, isResult: true})
		}
	}
	return entries
}

func safeTurnHistory(entries []journalEntry, recovery *RecoveryState) []message.Message {
	active := make([]message.Message, 0, len(entries))
	for i := 0; i < len(entries); {
		entry := entries[i]
		calls := toolCallsFromMessage(entry.message)
		if len(calls) == 0 {
			if entry.isResult {
				if entry.result != nil {
					recovery.CompletedToolResults = append(recovery.CompletedToolResults, *entry.result)
				}
				i++
				continue
			}
			active = append(active, entry.message)
			i++
			continue
		}

		results := make(map[string]message.ToolResult, len(calls))
		resultEntries := make([]journalEntry, 0, len(calls))
		j := i + 1
		for j < len(entries) && entries[j].isResult {
			if entries[j].result != nil {
				resultEntries = append(resultEntries, entries[j])
				results[entries[j].result.ToolUseID] = *entries[j].result
			}
			j++
		}

		complete := len(results) == len(calls)
		ordered := make([]message.ToolResult, 0, len(calls))
		for _, call := range calls {
			result, ok := results[call.ID]
			if !ok {
				complete = false
				if recovery != nil {
					recovery.DroppedToolCalls = append(recovery.DroppedToolCalls, call)
				}
				continue
			}
			ordered = append(ordered, result)
		}

		if complete {
			active = append(active, entry.message, buildToolResultsMessage(ordered))
		} else if recovery != nil {
			for _, resultEntry := range resultEntries {
				if resultEntry.result != nil {
					recovery.CompletedToolResults = append(recovery.CompletedToolResults, *resultEntry.result)
				}
			}
		}
		i = j
	}
	return active
}
