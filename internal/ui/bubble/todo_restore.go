package bubble

import (
	"encoding/json"
	"strings"
	"time"

	"paw/internal/message"
	"paw/internal/session"
	"paw/internal/todo"
)

type todoRestoreProjection struct {
	Entries     []transcriptEntry
	Current     todo.Snapshot
	HasCurrent  bool
	WasCleared  bool
	LatestIndex int
}

type todoRestoreCall struct {
	name   string
	turnID string
}

type todoRestoreTracker struct {
	calls        map[string]todoRestoreCall
	latestEntry  int
	latestTurnID string
	current      todo.Snapshot
	hasCurrent   bool
	wasCleared   bool
}

func newTodoRestoreTracker() *todoRestoreTracker {
	return &todoRestoreTracker{calls: make(map[string]todoRestoreCall), latestEntry: -1}
}

// restoreTodoProjection reconstructs Todo-only transcript entries and the
// latest Todo state from resolved session records.
func restoreTodoProjection(records []session.Record) todoRestoreProjection {
	tracker := newTodoRestoreTracker()
	entries := make([]transcriptEntry, 0)
	for _, record := range records {
		tracker.observeCalls(record)
		tracker.foldForAssistant(record, entries)
		entries = tracker.entriesForResultsAt(record, entries)
	}
	return tracker.projection(entries)
}

func (t *todoRestoreTracker) observeCalls(record session.Record) {
	if t == nil {
		return
	}
	for _, call := range appendToolCalls(record.Message) {
		id := strings.TrimSpace(call.ID)
		if id == "" {
			continue
		}
		t.calls[id] = todoRestoreCall{
			name:   strings.TrimSpace(call.Name),
			turnID: strings.TrimSpace(record.TurnID),
		}
	}
}

// entriesForResultsAt appends restored Todo entries to a shared transcript and
// keeps an absolute latest-entry index for subsequent folding.
func (t *todoRestoreTracker) entriesForResultsAt(record session.Record, entries []transcriptEntry) []transcriptEntry {
	if t == nil {
		return entries
	}
	for _, result := range appendToolResults(record.Message) {
		call, ok := t.calls[strings.TrimSpace(result.ToolUseID)]
		if !ok || call.name != "update_todo" || result.IsError {
			continue
		}
		snapshot, ok := decodeTodoResult(result.Content)
		if !ok {
			continue
		}

		if t.latestEntry >= 0 && t.latestEntry < len(entries) {
			previous := &entries[t.latestEntry]
			previous.todoLatest = false
			previous.todoExpanded = false
			touchTranscriptEntry(previous)
		}

		copy := snapshot.Clone()
		entry := transcriptEntry{
			kind:         entryTodo,
			title:        "Todo",
			todoSnapshot: &copy,
			todoExpanded: !copy.Cleared(),
			todoLatest:   true,
			todoCleared:  copy.Cleared(),
			createdAt:    restoredTodoCreatedAt(copy, record.CreatedAt),
		}
		touchTranscriptEntry(&entry)
		entries = append(entries, entry)
		t.latestEntry = len(entries) - 1
		t.latestTurnID = firstNonEmptyString(record.TurnID, call.turnID)

		if copy.Cleared() {
			t.current = todo.Snapshot{}
			t.hasCurrent = false
			t.wasCleared = true
		} else {
			t.current = copy.Clone()
			t.hasCurrent = true
			t.wasCleared = false
		}
	}
	return entries
}

func (t *todoRestoreTracker) foldForAssistant(record session.Record, entries []transcriptEntry) {
	if t == nil || t.latestEntry < 0 || t.latestEntry >= len(entries) {
		return
	}
	if strings.TrimSpace(record.TurnID) != t.latestTurnID || !restoredAssistantIsVisible(record.Message) {
		return
	}
	entry := &entries[t.latestEntry]
	if entry.todoSnapshot == nil || !entry.todoSnapshot.AllCompleted() {
		return
	}
	entry.todoExpanded = false
	entry.todoCompletedFold = true
	touchTranscriptEntry(entry)
}

func (t *todoRestoreTracker) projection(entries []transcriptEntry) todoRestoreProjection {
	projection := todoRestoreProjection{LatestIndex: -1}
	if t == nil {
		return projection
	}
	projection.Entries = copyTranscriptEntries(entries)
	projection.Current = t.current.Clone()
	projection.HasCurrent = t.hasCurrent
	projection.WasCleared = t.wasCleared
	projection.LatestIndex = t.latestEntry
	return projection
}

func decodeTodoResult(content string) (todo.Snapshot, bool) {
	var result todo.UpdateResult
	if err := json.Unmarshal([]byte(content), &result); err != nil || !result.Accepted {
		return todo.Snapshot{}, false
	}
	snapshot, err := todo.ValidateSnapshot(result.Snapshot)
	if err != nil {
		return todo.Snapshot{}, false
	}
	return snapshot, true
}

func restoredAssistantIsVisible(msg message.Message) bool {
	return msg.Role == message.RoleAssistant && strings.TrimSpace(sanitizeAssistantVisibleBody(msg.Content)) != ""
}

func todoProjectionFromEntries(entries []transcriptEntry) todoRestoreProjection {
	projection := todoRestoreProjection{LatestIndex: -1}
	for index := range entries {
		entry := &entries[index]
		if entry.kind != entryTodo || entry.todoSnapshot == nil {
			continue
		}
		projection.LatestIndex = index
		projection.WasCleared = entry.todoCleared || entry.todoSnapshot.Cleared()
		if projection.WasCleared {
			projection.Current = todo.Snapshot{}
			projection.HasCurrent = false
		} else {
			projection.Current = entry.todoSnapshot.Clone()
			projection.HasCurrent = true
		}
	}
	return projection
}

func restoredTodoCreatedAt(snapshot todo.Snapshot, fallback time.Time) time.Time {
	if !snapshot.UpdatedAt.IsZero() {
		return snapshot.UpdatedAt
	}
	return fallback
}
