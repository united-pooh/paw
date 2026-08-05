package session

import (
	"context"
	"errors"
	"os"
	"reflect"
	"testing"
	"time"

	"paw/internal/message"
)

func TestJSONLStoreTurnMetadataRoundTrip(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	if _, err := store.CreateRoot(ctx, CreateRootRequest{SessionID: "s1"}); err != nil {
		t.Fatal(err)
	}
	if err := store.Append(ctx, "s1",
		message.Message{Role: message.RoleUser, Content: "hi"},
		message.Message{Role: message.RoleAssistant, Content: "hello"},
	); err != nil {
		t.Fatal(err)
	}
	seq := int64(1)
	started := time.Date(2026, 7, 30, 7, 45, 0, 0, time.UTC)
	response := started.Add(95 * time.Second)
	want := TurnMetadata{
		TurnID:       "turn-1",
		AssistantSeq: &seq,
		StartedAt:    started,
		ResponseAt:   &response,
		DurationMS:   95000,
		Status:       TurnStatusCompleted,
	}
	if err := store.AppendTurnMetadata(ctx, "s1", want); err != nil {
		t.Fatal(err)
	}
	got, err := store.LoadTurnMetadata(ctx, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !reflect.DeepEqual(got[0], want) {
		t.Fatalf("metadata = %#v, want %#v", got, want)
	}
	raw, err := os.ReadFile(store.TurnMetadataPath("s1"))
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{`"turn_id"`, `"assistant_seq"`, `"started_at"`, `"response_at"`, `"duration_ms"`, `"status"`} {
		if !containsString(string(raw), field) {
			t.Fatalf("sidecar %q does not contain %s", raw, field)
		}
	}
}

func TestJSONLStoreTurnMetadataSkipsCorruptLines(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	createTestSession(t, store, "s1", nil)
	first := TurnMetadata{TurnID: "first", Status: TurnStatusCompleted}
	second := TurnMetadata{TurnID: "second", Status: TurnStatusCompleted}
	if err := store.AppendTurnMetadata(ctx, "s1", first); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(store.TurnMetadataPath("s1"), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("not-json\n"); err != nil {
		_ = f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendTurnMetadata(ctx, "s1", second); err != nil {
		t.Fatal(err)
	}
	got, err := store.LoadTurnMetadata(ctx, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].TurnID != first.TurnID || got[1].TurnID != second.TurnID {
		t.Fatalf("metadata after corrupt line = %#v", got)
	}
}

func TestAppendWithSequences(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	first, last, err := store.AppendWithSequences(ctx, "s1",
		message.Message{Role: message.RoleUser, Content: "one"},
		message.Message{Role: message.RoleAssistant, Content: "two"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if first != 0 || last != 1 {
		t.Fatalf("sequence range = %d..%d, want 0..1", first, last)
	}
	first, last, err = store.AppendWithSequences(ctx, "s1", message.Message{Role: message.RoleUser, Content: "three"})
	if err != nil {
		t.Fatal(err)
	}
	if first != 2 || last != 2 {
		t.Fatalf("second sequence range = %d..%d, want 2..2", first, last)
	}
}

func TestLoadResolvedRecordsProjectsHistoryWithoutSidecar(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	createTestSession(t, store, "s1", []message.Message{
		{Role: message.RoleUser, Content: "hello"},
		{Role: message.RoleAssistant, Content: "world"},
	})
	records, err := store.LoadResolvedRecords(ctx, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 || records[1].Seq != 1 || records[1].Message.Content != "world" {
		t.Fatalf("resolved records = %#v", records)
	}
	history, err := store.LoadResolvedHistory(ctx, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(history, []message.Message{records[0].Message, records[1].Message}) {
		t.Fatalf("history = %#v, records = %#v", history, records)
	}
}

func TestPartialAssistantJournalVisibleButExcludedFromModelHistory(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	turnID := "turn-partial-text"
	if err := store.BeginTurn(ctx, "s1", turnID, message.Message{Role: message.RoleUser, Content: "hello"}); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendPartialAssistant(ctx, "s1", turnID, message.Message{Role: message.RoleAssistant, Content: "partial answer"}); err != nil {
		t.Fatal(err)
	}
	if err := store.FailTurn(ctx, "s1", turnID, errors.New("stream truncated")); err != nil {
		t.Fatal(err)
	}

	records, err := store.LoadResolvedRecords(ctx, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 || records[1].Kind != JournalAssistantPartial || records[1].Message.Content != "partial answer" {
		t.Fatalf("records=%#v, want visible partial assistant record", records)
	}
	history, err := store.LoadResolvedHistory(ctx, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 1 || history[0].Content != "hello" {
		t.Fatalf("history=%#v, want partial excluded", history)
	}
	snapshot, err := store.LoadSnapshot(ctx, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Messages) != 2 || snapshot.Messages[1].Content != "partial answer" {
		t.Fatalf("snapshot messages=%#v, want visible partial", snapshot.Messages)
	}
	if len(snapshot.ActiveHistory) != 1 || snapshot.ActiveHistory[0].Content != "hello" {
		t.Fatalf("active history=%#v, want partial excluded", snapshot.ActiveHistory)
	}
	if snapshot.Recovery == nil || snapshot.Recovery.Error != "stream truncated" {
		t.Fatalf("recovery=%#v, want failed turn recovery", snapshot.Recovery)
	}
}

func TestForkDefaultRetainsPartialDisplayRecordsButExcludesActiveHistory(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	turnID := "turn-parent"
	if err := store.BeginTurn(ctx, "parent", turnID, message.Message{Role: message.RoleUser, Content: "hello"}); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendPartialAssistant(ctx, "parent", turnID, message.Message{Role: message.RoleAssistant, Content: "partial answer"}); err != nil {
		t.Fatal(err)
	}
	if err := store.FailTurn(ctx, "parent", turnID, errors.New("stream truncated")); err != nil {
		t.Fatal(err)
	}

	meta, err := store.Fork(ctx, ForkRequest{SessionID: "child", ParentSessionID: "parent", ForkFromSeq: -1})
	if err != nil {
		t.Fatal(err)
	}
	if meta.ForkFromSeq != 2 {
		t.Fatalf("ForkFromSeq=%d, want full display record count 2", meta.ForkFromSeq)
	}
	records, err := store.LoadResolvedRecords(ctx, "child")
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 || records[1].Kind != JournalAssistantPartial || records[1].Message.Content != "partial answer" {
		t.Fatalf("child records=%#v, want retained display partial", records)
	}
	history, err := store.LoadResolvedHistory(ctx, "child")
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 1 || history[0].Content != "hello" {
		t.Fatalf("child history=%#v, want partial excluded", history)
	}
	snapshot, err := store.LoadSnapshot(ctx, "child")
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Messages) != 2 || len(snapshot.ActiveHistory) != 1 || snapshot.ActiveHistory[0].Content != "hello" {
		t.Fatalf("child snapshot=%#v, want display partial but active history without it", snapshot)
	}
}

func TestTurnJournalPersistsFailedTurnAndBuildsSnapshot(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	turnID := "turn-failed"
	call := message.ToolCall{ID: "call-1", Name: "Read", Input: []byte(`{"file_path":"go.mod"}`)}
	result := message.ToolResult{ToolUseID: "call-1", Content: "module paw", IsError: false}
	if err := store.BeginTurn(ctx, "s1", turnID, message.Message{Role: message.RoleUser, Content: "inspect"}); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendAssistant(ctx, "s1", turnID, message.Message{Role: message.RoleAssistant, ToolUse: &call}); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendToolResult(ctx, "s1", turnID, 0, result); err != nil {
		t.Fatal(err)
	}
	if err := store.FailTurn(ctx, "s1", turnID, errors.New("模型接口返回异常状态 502")); err != nil {
		t.Fatal(err)
	}

	snapshot, err := store.LoadSnapshot(ctx, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Messages) != 3 || len(snapshot.ActiveHistory) != 3 {
		t.Fatalf("snapshot messages=%#v active=%#v", snapshot.Messages, snapshot.ActiveHistory)
	}
	if snapshot.Recovery == nil || snapshot.Recovery.Error != "模型接口返回异常状态 502" {
		t.Fatalf("recovery=%#v", snapshot.Recovery)
	}
	history, err := store.LoadResolvedHistory(ctx, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 3 || history[2].ToolResult == nil || history[2].ToolResult.Content != "module paw" {
		t.Fatalf("history=%#v", history)
	}
}

func TestTurnJournalDropsIncompleteMultiToolGroupFromActiveHistory(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	turnID := "turn-partial"
	calls := []message.ToolCall{
		{ID: "call-a", Name: "Read"},
		{ID: "call-b", Name: "Grep"},
	}
	if err := store.BeginTurn(ctx, "s1", turnID, message.Message{Role: message.RoleUser, Content: "inspect"}); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendAssistant(ctx, "s1", turnID, message.Message{Role: message.RoleAssistant, ToolUses: calls}); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendToolResult(ctx, "s1", turnID, 0, message.ToolResult{ToolUseID: "call-a", Content: "done"}); err != nil {
		t.Fatal(err)
	}
	if err := store.FailTurn(ctx, "s1", turnID, errors.New("502")); err != nil {
		t.Fatal(err)
	}

	snapshot, err := store.LoadSnapshot(ctx, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.ActiveHistory) != 1 || snapshot.ActiveHistory[0].Content != "inspect" {
		t.Fatalf("active history=%#v, want only user input", snapshot.ActiveHistory)
	}
	if snapshot.Recovery == nil || len(snapshot.Recovery.DroppedToolCalls) != 1 || len(snapshot.Recovery.CompletedToolResults) != 1 {
		t.Fatalf("recovery=%#v", snapshot.Recovery)
	}
}

func TestTurnJournalCompletedTurnHasNoRecovery(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	if err := store.BeginTurn(ctx, "s1", "turn-ok", message.Message{Role: message.RoleUser, Content: "hello"}); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendAssistant(ctx, "s1", "turn-ok", message.Message{Role: message.RoleAssistant, Content: "world"}); err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteTurn(ctx, "s1", "turn-ok"); err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.LoadSnapshot(ctx, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Recovery != nil || len(snapshot.ActiveHistory) != 2 {
		t.Fatalf("snapshot=%#v", snapshot)
	}
}

func TestJSONLStoreIgnoresTornFinalJournalLine(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	createTestSession(t, store, "s1", []message.Message{{Role: message.RoleUser, Content: "hello"}})
	f, err := os.OpenFile(store.TranscriptPath("s1"), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"seq":99,"message":`); err != nil {
		_ = f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	history, err := store.LoadResolvedHistory(ctx, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 1 || history[0].Content != "hello" {
		t.Fatalf("history=%#v", history)
	}
}

func containsString(value, needle string) bool {
	for i := 0; i+len(needle) <= len(value); i++ {
		if value[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
