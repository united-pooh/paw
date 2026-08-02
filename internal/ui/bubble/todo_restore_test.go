package bubble

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"paw/internal/message"
	"paw/internal/session"
	"paw/internal/todo"
)

func todoCallRecord(seq int64, turnID, callID string) session.Record {
	return session.Record{
		Seq:       seq,
		TurnID:    turnID,
		CreatedAt: time.Date(2026, 8, 2, 10, 0, int(seq), 0, time.UTC),
		Message: message.Message{Role: message.RoleAssistant, ToolUse: &message.ToolCall{
			ID: callID, Name: "update_todo", Input: json.RawMessage(`{"items":[]}`),
		}},
	}
}

func todoResultRecord(seq int64, turnID, callID, content string, isError bool) session.Record {
	return session.Record{
		Seq:       seq,
		TurnID:    turnID,
		CreatedAt: time.Date(2026, 8, 2, 10, 0, int(seq), 0, time.UTC),
		Message: message.Message{Role: message.RoleUser, ToolResult: &message.ToolResult{
			ToolUseID: callID, Content: content, IsError: isError,
		}},
	}
}

func acceptedTodoResult(items string, updatedAt string) string {
	return `{"accepted":true,"snapshot":{"items":` + items + `,"updated_at":"` + updatedAt + `"}}`
}

func TestRestoreTodoEntriesFromSuccessfulToolResults(t *testing.T) {
	records := []session.Record{
		todoCallRecord(1, "turn-1", "call-1"),
		todoResultRecord(2, "turn-1", "call-1", acceptedTodoResult(
			`[{"id":"a","content":"A","status":"in_progress"}]`,
			"2026-08-02T10:00:00Z",
		), false),
	}

	restored := restoreTodoProjection(records)
	if len(restored.Entries) != 1 || !restored.HasCurrent || len(restored.Current.Items) != 1 || restored.Current.Items[0].ID != "a" {
		t.Fatalf("projection = %#v", restored)
	}
	entry := restored.Entries[0]
	if !entry.todoLatest || !entry.todoExpanded || entry.createdAt != time.Date(2026, 8, 2, 10, 0, 0, 0, time.UTC) {
		t.Fatalf("entry = %#v", entry)
	}
}

func TestRestoreTodoKeepsOnlyLatestExpanded(t *testing.T) {
	records := []session.Record{
		todoCallRecord(1, "turn-1", "call-1"),
		todoResultRecord(2, "turn-1", "call-1", acceptedTodoResult(
			`[{"id":"a","content":"A","status":"in_progress"}]`, "2026-08-02T10:00:00Z"), false),
		todoCallRecord(3, "turn-1", "call-2"),
		todoResultRecord(4, "turn-1", "call-2", acceptedTodoResult(
			`[{"id":"a","content":"A","status":"completed"},{"id":"b","content":"B","status":"in_progress"}]`, "2026-08-02T10:01:00Z"), false),
	}

	restored := restoreTodoProjection(records)
	if len(restored.Entries) != 2 || restored.LatestIndex != 1 {
		t.Fatalf("projection = %#v", restored)
	}
	if restored.Entries[0].todoLatest || restored.Entries[0].todoExpanded {
		t.Fatalf("old entry not folded: %#v", restored.Entries[0])
	}
	if !restored.Entries[1].todoLatest || !restored.Entries[1].todoExpanded {
		t.Fatalf("latest entry not expanded: %#v", restored.Entries[1])
	}
	if restored.Current.CompletedCount() != 1 || restored.Current.TotalCount() != 2 {
		t.Fatalf("current = %#v", restored.Current)
	}
}

func TestRestoreTodoClearSnapshotClearsCurrent(t *testing.T) {
	records := []session.Record{
		todoCallRecord(1, "turn-1", "call-1"),
		todoResultRecord(2, "turn-1", "call-1", acceptedTodoResult(
			`[{"id":"a","content":"A","status":"in_progress"}]`, "2026-08-02T10:00:00Z"), false),
		todoCallRecord(3, "turn-1", "call-2"),
		todoResultRecord(4, "turn-1", "call-2", acceptedTodoResult(`[]`, "2026-08-02T10:01:00Z"), false),
	}

	restored := restoreTodoProjection(records)
	if restored.HasCurrent || !restored.WasCleared || !restored.Current.Cleared() {
		t.Fatalf("projection = %#v", restored)
	}
	if len(restored.Entries) != 2 || !restored.Entries[1].todoCleared || restored.Entries[1].todoExpanded {
		t.Fatalf("entries = %#v", restored.Entries)
	}
}

func TestRestoreTodoIgnoresInvalidResultsWithoutOverwritingCurrent(t *testing.T) {
	valid := acceptedTodoResult(`[{"id":"a","content":"A","status":"in_progress"}]`, "2026-08-02T10:00:00Z")
	cases := []struct {
		name    string
		callID  string
		content string
		isError bool
		addCall bool
	}{
		{name: "tool error", callID: "call-2", content: acceptedTodoResult(`[]`, "2026-08-02T10:01:00Z"), isError: true, addCall: true},
		{name: "malformed", callID: "call-2", content: `{`, addCall: true},
		{name: "rejected", callID: "call-2", content: `{"accepted":false,"snapshot":{"items":[]}}`, addCall: true},
		{name: "unknown call", callID: "missing", content: acceptedTodoResult(`[]`, "2026-08-02T10:01:00Z")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			records := []session.Record{
				todoCallRecord(1, "turn-1", "call-1"),
				todoResultRecord(2, "turn-1", "call-1", valid, false),
			}
			if tc.addCall {
				records = append(records, todoCallRecord(3, "turn-1", tc.callID))
			}
			records = append(records, todoResultRecord(4, "turn-1", tc.callID, tc.content, tc.isError))

			restored := restoreTodoProjection(records)
			if len(restored.Entries) != 1 || !restored.HasCurrent || restored.Current.Items[0].ID != "a" {
				t.Fatalf("projection = %#v", restored)
			}
		})
	}
}

func TestRestoreTodoIgnoresUnfinishedToolUse(t *testing.T) {
	restored := restoreTodoProjection([]session.Record{todoCallRecord(1, "turn-1", "call-1")})
	if len(restored.Entries) != 0 || restored.HasCurrent || restored.WasCleared || restored.LatestIndex != -1 {
		t.Fatalf("projection = %#v", restored)
	}
}

func TestRestoreTodoCompletedSnapshotFoldsAfterSameTurnFinalAssistant(t *testing.T) {
	records := []session.Record{
		todoCallRecord(1, "turn-1", "call-1"),
		todoResultRecord(2, "turn-1", "call-1", acceptedTodoResult(
			`[{"id":"a","content":"A","status":"completed"}]`, "2026-08-02T10:00:00Z"), false),
		{Seq: 3, TurnID: "turn-1", Message: message.Message{Role: message.RoleAssistant, Content: "All done."}},
	}

	restored := restoreTodoProjection(records)
	entry := restored.Entries[0]
	if entry.todoExpanded || !entry.todoCompletedFold {
		t.Fatalf("entry = %#v", entry)
	}
}

func TestRestoreTodoCompletedSnapshotStaysExpandedWithoutFinalAssistant(t *testing.T) {
	records := []session.Record{
		todoCallRecord(1, "turn-1", "call-1"),
		todoResultRecord(2, "turn-1", "call-1", acceptedTodoResult(
			`[{"id":"a","content":"A","status":"completed"}]`, "2026-08-02T10:00:00Z"), false),
		{Seq: 3, TurnID: "turn-2", Message: message.Message{Role: message.RoleAssistant, Content: "Different turn."}},
	}

	restored := restoreTodoProjection(records)
	entry := restored.Entries[0]
	if !entry.todoExpanded || entry.todoCompletedFold {
		t.Fatalf("entry = %#v", entry)
	}
}

func TestRestoreTodoCardsAppearImmediatelyAfterToolResults(t *testing.T) {
	records := []session.Record{
		todoCallRecord(1, "turn-1", "call-1"),
		todoResultRecord(2, "turn-1", "call-1", acceptedTodoResult(
			`[{"id":"a","content":"A","status":"in_progress"}]`, "2026-08-02T10:00:00Z"), false),
		{Seq: 3, TurnID: "turn-1", Message: message.Message{Role: message.RoleAssistant, Content: "continuing"}},
	}

	entries := transcriptEntriesFromRecords(records, nil, "")
	if len(entries) != 4 {
		t.Fatalf("entries = %#v", entries)
	}
	if entries[0].kind != entryTool || entries[1].kind != entryTool || entries[2].kind != entryTodo || entries[3].kind != entryAssistant {
		t.Fatalf("entry order = %#v", []entryKind{entries[0].kind, entries[1].kind, entries[2].kind, entries[3].kind})
	}
}

func TestSessionRestoreAppliesAndClearsTodoState(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.currentTodo = todo.Snapshot{Items: []todo.Item{{ID: "old", Content: "Old", Status: todo.StatusInProgress}}}
	model.hasCurrentTodo = true
	model.todoWasCleared = false
	model.latestTodoIndex = 7
	model.todoPage = newTodoPage()

	snapshot := todo.Snapshot{Items: []todo.Item{{ID: "new", Content: "New", Status: todo.StatusInProgress}}}
	entrySnapshot := snapshot.Clone()
	model.applySessionPickerRestore(sessionRestoredMsg{
		sessionID:       "session-a",
		entries:         []transcriptEntry{{kind: entryTodo, todoSnapshot: &entrySnapshot, todoLatest: true, todoExpanded: true}},
		currentTodo:     snapshot,
		hasCurrentTodo:  true,
		latestTodoIndex: 0,
	})
	if !model.hasCurrentTodo || model.currentTodo.Items[0].ID != "new" || model.latestTodoIndex != 0 || model.todoPage != nil {
		t.Fatalf("restored model = %#v", model)
	}

	model.applySessionPickerRestore(sessionRestoredMsg{sessionID: "session-b", latestTodoIndex: -1})
	if model.hasCurrentTodo || model.todoWasCleared || !model.currentTodo.Cleared() || model.latestTodoIndex != -1 {
		t.Fatalf("cleared model current=%#v has=%v wasCleared=%v latest=%d", model.currentTodo, model.hasCurrentTodo, model.todoWasCleared, model.latestTodoIndex)
	}
}

func TestSubagentRestoreDoesNotModifyMainTodo(t *testing.T) {
	model := newTestModel(&fakeRunner{})
	model.currentTodo = todo.Snapshot{Items: []todo.Item{{ID: "main", Content: "Main", Status: todo.StatusInProgress}}}
	model.hasCurrentTodo = true
	model.latestTodoIndex = 0
	model.applySubagentPreviewRestore(sessionRestoredMsg{
		source: sessionRestoreSubagentEnter,
		subagentPreview: &subagentTranscriptPreview{
			sessionID: "subagent-session",
		},
	})
	if !model.hasCurrentTodo || model.currentTodo.Items[0].ID != "main" || model.latestTodoIndex != 0 {
		t.Fatalf("main Todo changed: current=%#v has=%v latest=%d", model.currentTodo, model.hasCurrentTodo, model.latestTodoIndex)
	}
}

type todoRestoreStore struct {
	records []session.Record
}

func (s *todoRestoreStore) ListSessions(context.Context) ([]session.SessionSummary, error) {
	return nil, nil
}
func (s *todoRestoreStore) LoadResolvedRecords(context.Context, string) ([]session.Record, error) {
	return append([]session.Record(nil), s.records...), nil
}

func TestLoadSessionHistoryRestoresTodoProjection(t *testing.T) {
	records := []session.Record{
		todoCallRecord(1, "turn-1", "call-1"),
		todoResultRecord(2, "turn-1", "call-1", acceptedTodoResult(
			`[{"id":"a","content":"A","status":"in_progress"}]`, "2026-08-02T10:00:00Z"), false),
	}
	runner := &fakeRunner{loadHistoryMsgs: []message.Message{records[0].Message, records[1].Message}}
	cmd := loadSessionHistoryCmd(context.Background(), runner, &todoRestoreStore{records: records}, "session-a")
	msg, ok := cmd().(sessionRestoredMsg)
	if !ok {
		t.Fatalf("cmd result type = %T", cmd())
	}
	if msg.err != nil || !msg.hasCurrentTodo || len(msg.currentTodo.Items) != 1 || msg.currentTodo.Items[0].ID != "a" {
		t.Fatalf("message = %#v", msg)
	}
	if msg.latestTodoIndex < 0 || msg.latestTodoIndex >= len(msg.entries) || msg.entries[msg.latestTodoIndex].kind != entryTodo {
		t.Fatalf("latest index %d for entries %#v", msg.latestTodoIndex, msg.entries)
	}
}
