package session

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"paw/internal/es"
	"paw/internal/message"
	"paw/internal/todo"
)

func msg(role message.Role, content string) message.Message {
	return message.Message{Role: role, Content: content}
}

func TestRecordEnvelopeRoundTrip(t *testing.T) {
	at := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	callIndex := 3
	result := message.ToolResult{ToolUseID: "call_1", Content: "ok"}
	cases := []Record{
		{Seq: 1, Kind: JournalMessage, Message: msg(message.RoleUser, "hello")},
		{Seq: 2, Kind: JournalTurnStarted, TurnID: "t1"},
		{Seq: 3, Kind: JournalAssistant, TurnID: "t1", Message: msg(message.RoleAssistant, "hi")},
		{Seq: 4, Kind: JournalAssistantPartial, TurnID: "t1", Message: msg(message.RoleAssistant, "par")},
		{Seq: 5, Kind: JournalToolResult, TurnID: "t1", CallIndex: &callIndex, Message: msg(message.RoleUser, "res"), ToolResult: &result},
		{Seq: 6, Kind: JournalTurnCompleted, TurnID: "t1"},
		{Seq: 7, Kind: JournalTurnFailed, TurnID: "t1", Error: "boom"},
	}
	snap := todo.Snapshot{Explanation: "x", Items: []todo.Item{{ID: "a", Content: "c", Status: todo.StatusPending}}, UpdatedAt: at}
	cases = append(cases, Record{Seq: 8, Kind: JournalTodoSnapshot, TodoSnapshot: &snap})
	receipt := CommandReceipt{CommandID: "cmd-1", Kind: "session.create", ResourceID: "s1", Status: "accepted", SessionVersion: 2, CreatedAt: at}
	cases = append(cases, Record{Seq: 9, Kind: JournalCommandReceipt, CommandReceipt: &receipt})

	for i, rec := range cases {
		rec.CreatedAt = at
		env, err := recordToEnvelope(rec)
		if err != nil {
			t.Fatalf("case %d: recordToEnvelope: %v", i, err)
		}
		if env.Seq != rec.Seq || env.SchemaVersion != 1 {
			t.Fatalf("case %d: envelope header mismatch: %+v", i, env)
		}
		back, err := envelopeToRecord(env)
		if err != nil {
			t.Fatalf("case %d: envelopeToRecord: %v", i, err)
		}
		back.CreatedAt = at
		if back.Kind != rec.Kind || back.TurnID != rec.TurnID || back.Error != rec.Error {
			t.Fatalf("case %d: round trip mismatch: %+v vs %+v", i, back, rec)
		}
		if rec.Message.Content != "" && back.Message.Content != rec.Message.Content {
			t.Fatalf("case %d: message mismatch: %+v vs %+v", i, back.Message, rec.Message)
		}
		if rec.CallIndex != nil && (back.CallIndex == nil || *back.CallIndex != *rec.CallIndex) {
			t.Fatalf("case %d: call_index mismatch", i)
		}
		if rec.ToolResult != nil && (back.ToolResult == nil || back.ToolResult.ToolUseID != rec.ToolResult.ToolUseID) {
			t.Fatalf("case %d: tool_result mismatch", i)
		}
		if rec.TodoSnapshot != nil && (back.TodoSnapshot == nil || back.TodoSnapshot.Explanation != rec.TodoSnapshot.Explanation) {
			t.Fatalf("case %d: todo snapshot mismatch", i)
		}
		if rec.CommandReceipt != nil && (back.CommandReceipt == nil || back.CommandReceipt.CommandID != rec.CommandReceipt.CommandID || back.CommandReceipt.ResourceID != rec.CommandReceipt.ResourceID) {
			t.Fatalf("case %d: command receipt mismatch", i)
		}
	}
}

func TestIsEnvelopeLine(t *testing.T) {
	env, _ := recordToEnvelope(Record{Seq: 1, Kind: JournalMessage, Message: msg(message.RoleUser, "x"), CreatedAt: time.Now()})
	envJSON, _ := json.Marshal(env)
	if !isEnvelopeLine(envJSON) {
		t.Fatal("envelope line must be detected")
	}
	legacy, _ := json.Marshal(Record{Seq: 1, Kind: JournalMessage, Message: msg(message.RoleUser, "x"), CreatedAt: time.Now()})
	if isEnvelopeLine(legacy) {
		t.Fatal("legacy record line must not be detected as envelope")
	}
	if isEnvelopeLine([]byte("not json")) {
		t.Fatal("garbage must not be detected as envelope")
	}
}

func newTestJSONLStore(t *testing.T) *JSONLStore {
	t.Helper()
	dir := t.TempDir()
	s, err := NewJSONLStore(dir)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	return s
}

func TestAppendWritesEnvelopeFormat(t *testing.T) {
	s := newTestJSONLStore(t)
	ctx := context.Background()
	if _, _, err := s.AppendWithSequences(ctx, "s1", msg(message.RoleUser, "hello")); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := s.BeginTurn(ctx, "s1", "t1", msg(message.RoleUser, "turn start")); err != nil {
		t.Fatalf("begin turn: %v", err)
	}
	data, err := os.ReadFile(s.TranscriptPath("s1"))
	if err != nil {
		t.Fatalf("read transcript: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 3 {
		t.Fatalf("got %d lines, want 3", len(lines))
	}
	for i, line := range lines {
		if !isEnvelopeLine([]byte(line)) {
			t.Fatalf("line %d is not envelope format: %s", i, line)
		}
		var env es.Envelope
		if err := json.Unmarshal([]byte(line), &env); err != nil {
			t.Fatalf("line %d: %v", i, err)
		}
		if env.SchemaVersion != 1 || env.Type == "" {
			t.Fatalf("line %d: bad envelope header: %+v", i, env)
		}
	}
}

func TestReadLegacyRecords(t *testing.T) {
	s := newTestJSONLStore(t)
	ctx := context.Background()
	if _, err := s.CreateRoot(ctx, CreateRootRequest{SessionID: "s1"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	// 手工构造 legacy Record 格式的 transcript
	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	lines := []Record{
		{Seq: 1, Kind: JournalMessage, Message: msg(message.RoleUser, "legacy one"), CreatedAt: now},
		{Seq: 2, Kind: JournalTurnStarted, TurnID: "t1", CreatedAt: now},
		{Seq: 3, Kind: JournalAssistant, TurnID: "t1", Message: msg(message.RoleAssistant, "legacy reply"), CreatedAt: now},
		{Seq: 4, Kind: JournalTurnCompleted, TurnID: "t1", CreatedAt: now},
	}
	var sb strings.Builder
	for _, rec := range lines {
		data, _ := json.Marshal(rec)
		sb.Write(data)
		sb.WriteByte('\n')
	}
	if err := os.WriteFile(s.TranscriptPath("s1"), []byte(sb.String()), 0o644); err != nil {
		t.Fatalf("write legacy fixture: %v", err)
	}

	history, err := s.LoadResolvedHistory(ctx, "s1")
	if err != nil {
		t.Fatalf("load history: %v", err)
	}
	if len(history) != 2 || history[0].Content != "legacy one" || history[1].Content != "legacy reply" {
		t.Fatalf("legacy history mismatch: %+v", history)
	}
	// 追加新格式后 seq 必须从 legacy 尾部继续
	first, last, err := s.AppendWithSequences(ctx, "s1", msg(message.RoleUser, "new format"))
	if err != nil {
		t.Fatalf("append after legacy: %v", err)
	}
	if first != 5 || last != 5 {
		t.Fatalf("seq after legacy = %d..%d, want 5..5", first, last)
	}
}

func TestMixedLegacyAndEnvelopeLines(t *testing.T) {
	s := newTestJSONLStore(t)
	ctx := context.Background()
	if _, err := s.CreateRoot(ctx, CreateRootRequest{SessionID: "s1"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	now := time.Now()
	legacy, _ := json.Marshal(Record{Seq: 1, Kind: JournalMessage, Message: msg(message.RoleUser, "legacy"), CreatedAt: now})
	env, _ := recordToEnvelope(Record{Seq: 2, Kind: JournalMessage, Message: msg(message.RoleUser, "envelope"), CreatedAt: now})
	envJSON, _ := json.Marshal(env)
	content := string(legacy) + "\n" + string(envJSON) + "\n"
	if err := os.WriteFile(s.TranscriptPath("s1"), []byte(content), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	history, err := s.LoadResolvedHistory(ctx, "s1")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(history) != 2 || history[0].Content != "legacy" || history[1].Content != "envelope" {
		t.Fatalf("mixed history mismatch: %+v", history)
	}
}

func TestActorEnvelopesShareTranscriptWithoutEnteringMessageProjection(t *testing.T) {
	s := newTestJSONLStore(t)
	ctx := context.Background()
	if _, err := s.CreateRoot(ctx, CreateRootRequest{SessionID: "s1"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	now := time.Now().UTC()
	legacy, _ := json.Marshal(Record{Seq: 0, Kind: JournalMessage, Message: msg(message.RoleUser, "legacy"), CreatedAt: now})
	if err := os.WriteFile(s.TranscriptPath("s1"), append(legacy, '\n'), 0o644); err != nil {
		t.Fatalf("write legacy: %v", err)
	}

	first, last, err := s.AppendEnvelopes(ctx, "s1", []es.Envelope{
		{Type: "sys.inbox.received", Kind: es.KindRuntime, SchemaVersion: 1, OccurredAt: now, Payload: json.RawMessage(`{"msg_id":"m1"}`)},
		{Type: "session.goal_activated", Kind: es.KindDomain, SchemaVersion: 1, OccurredAt: now, Payload: json.RawMessage(`{"goal_id":"g1"}`)},
	})
	if err != nil {
		t.Fatalf("AppendEnvelopes: %v", err)
	}
	if first != 1 || last != 2 {
		t.Fatalf("actor seq = %d..%d, want 1..2", first, last)
	}
	first, last, err = s.AppendWithSequences(ctx, "s1", msg(message.RoleAssistant, "visible"))
	if err != nil {
		t.Fatalf("AppendWithSequences: %v", err)
	}
	if first != 3 || last != 3 {
		t.Fatalf("message seq = %d..%d, want 3..3", first, last)
	}

	history, err := s.LoadResolvedHistory(ctx, "s1")
	if err != nil {
		t.Fatalf("LoadResolvedHistory: %v", err)
	}
	if len(history) != 2 || history[0].Content != "legacy" || history[1].Content != "visible" {
		t.Fatalf("history includes control events: %+v", history)
	}
	envelopes, truncated, err := s.LoadEnvelopes(ctx, "s1")
	if err != nil || truncated {
		t.Fatalf("LoadEnvelopes = truncated:%t err:%v", truncated, err)
	}
	if len(envelopes) != 4 || envelopes[1].Kind != es.KindRuntime || envelopes[2].Type != "session.goal_activated" {
		t.Fatalf("mixed envelopes = %+v", envelopes)
	}
}

func TestAppendTodoSnapshot(t *testing.T) {
	s := newTestJSONLStore(t)
	ctx := context.Background()
	if _, err := s.AppendTodoSnapshot(ctx, "s1", todo.Snapshot{
		Explanation: "plan",
		Items:       []todo.Item{{ID: "a", Content: "do", Status: todo.StatusInProgress}},
		UpdatedAt:   time.Now().UTC(),
	}); err != nil {
		t.Fatalf("append todo: %v", err)
	}
	// todo 事件在 raw records 中可见（seq 连续）
	raw, err := s.LoadResolvedJournalRecords(ctx, "s1")
	if err != nil {
		t.Fatalf("load raw: %v", err)
	}
	if len(raw) != 1 || raw[0].Kind != JournalTodoSnapshot || raw[0].TodoSnapshot == nil {
		t.Fatalf("raw todo record mismatch: %+v", raw)
	}
	// 投影（消息历史）不含 todo 记录
	resolved, err := s.LoadResolvedRecords(ctx, "s1")
	if err != nil {
		t.Fatalf("load resolved: %v", err)
	}
	if len(resolved) != 0 {
		t.Fatalf("resolved must drop todo records: %+v", resolved)
	}
	// seq 继续（session 流从 0 基线：第一条 seq=0，此处应为 1）
	first, last, err := s.AppendWithSequences(ctx, "s1", msg(message.RoleUser, "after todo"))
	if err != nil {
		t.Fatalf("append after todo: %v", err)
	}
	if first != 1 || last != 1 {
		t.Fatalf("seq after todo = %d..%d, want 1..1", first, last)
	}
}

func TestTodoSnapshotSurvivesReload(t *testing.T) {
	dir := t.TempDir()
	s, err := NewJSONLStore(dir)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	ctx := context.Background()
	snap := todo.Snapshot{Explanation: "e", Items: []todo.Item{{ID: "x", Content: "c", Status: todo.StatusCompleted}}, UpdatedAt: time.Now().UTC()}
	if _, err := s.AppendTodoSnapshot(ctx, "s1", snap); err != nil {
		t.Fatalf("append todo: %v", err)
	}
	// 新 store 实例（模拟重启）从磁盘读取
	s2, err := NewJSONLStore(dir)
	if err != nil {
		t.Fatalf("new store 2: %v", err)
	}
	raw, err := s2.LoadResolvedJournalRecords(ctx, "s1")
	if err != nil {
		t.Fatalf("load raw: %v", err)
	}
	if len(raw) != 1 || raw[0].TodoSnapshot == nil || raw[0].TodoSnapshot.Items[0].ID != "x" {
		t.Fatalf("todo snapshot lost after reload: %+v", raw)
	}
}

func TestLoadLatestTodoSnapshotRestoresNewestPersistedEvent(t *testing.T) {
	dir := t.TempDir()
	store, err := NewJSONLStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := store.AppendTodoSnapshot(ctx, "s1", todo.Snapshot{
		Items:     []todo.Item{{ID: "first", Content: "First", Status: todo.StatusCompleted}},
		UpdatedAt: time.Date(2026, 8, 16, 1, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatal(err)
	}
	want := todo.Snapshot{
		Explanation: "resume this work",
		Items:       []todo.Item{{ID: "current", Content: "Current", Status: todo.StatusInProgress}},
		UpdatedAt:   time.Date(2026, 8, 16, 2, 0, 0, 0, time.UTC),
	}
	if _, err := store.AppendTodoSnapshot(ctx, "s1", want); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewJSONLStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	got, ok, err := reopened.LoadLatestTodoSnapshot(ctx, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if !ok || !reflect.DeepEqual(got, want) {
		t.Fatalf("LoadLatestTodoSnapshot() = (%#v, %v), want (%#v, true)", got, ok, want)
	}
}

func TestLoadLatestTodoSnapshotPreservesExplicitClear(t *testing.T) {
	store := newTestJSONLStore(t)
	ctx := context.Background()
	if _, err := store.AppendTodoSnapshot(ctx, "cleared", todo.Snapshot{
		Items: []todo.Item{{ID: "old", Content: "Old", Status: todo.StatusCompleted}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendTodoSnapshot(ctx, "cleared", todo.Snapshot{}); err != nil {
		t.Fatal(err)
	}
	got, ok, err := store.LoadLatestTodoSnapshot(ctx, "cleared")
	if err != nil {
		t.Fatal(err)
	}
	if !ok || !got.Cleared() {
		t.Fatalf("LoadLatestTodoSnapshot() = (%#v, %v), want explicit clear", got, ok)
	}
}

func TestLoadLatestTodoSnapshotRestoresLegacyToolResult(t *testing.T) {
	store := newTestJSONLStore(t)
	ctx := context.Background()
	if err := store.Append(ctx, "legacy",
		message.Message{Role: message.RoleAssistant, ToolUse: &message.ToolCall{
			ID: "todo-call", Name: "update_todo", Input: json.RawMessage(`{"items":[]}`),
		}},
		message.Message{Role: message.RoleUser, ToolResult: &message.ToolResult{
			ToolUseID: "todo-call",
			Content:   `{"accepted":true,"snapshot":{"explanation":"legacy work","items":[{"id":"legacy","content":"Resume legacy work","status":"pending"}],"updated_at":"2026-08-02T10:00:00Z"}}`,
		}},
	); err != nil {
		t.Fatal(err)
	}

	got, ok, err := store.LoadLatestTodoSnapshot(ctx, "legacy")
	if err != nil {
		t.Fatal(err)
	}
	if !ok || got.Explanation != "legacy work" || len(got.Items) != 1 || got.Items[0].ID != "legacy" || got.Items[0].Status != todo.StatusPending {
		t.Fatalf("LoadLatestTodoSnapshot() = (%#v, %v), want restored legacy snapshot", got, ok)
	}
}

func TestEnvelopeTornTailStillTruncates(t *testing.T) {
	s := newTestJSONLStore(t)
	ctx := context.Background()
	if _, err := s.CreateRoot(ctx, CreateRootRequest{SessionID: "s1"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	now := time.Now()
	env, _ := recordToEnvelope(Record{Seq: 1, Kind: JournalMessage, Message: msg(message.RoleUser, "intact"), CreatedAt: now})
	envJSON, _ := json.Marshal(env)
	content := string(envJSON) + "\n" + `{"seq":2,"type":"session.user_message","paylo` // torn
	if err := os.WriteFile(s.TranscriptPath("s1"), []byte(content), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	history, err := s.LoadResolvedHistory(ctx, "s1")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(history) != 1 || history[0].Content != "intact" {
		t.Fatalf("torn tail must truncate to intact prefix: %+v", history)
	}
	// 追加从完好前缀的 seq 继续
	first, last, err := s.AppendWithSequences(ctx, "s1", msg(message.RoleUser, "after torn"))
	if err != nil {
		t.Fatalf("append after torn: %v", err)
	}
	if first != 2 || last != 2 {
		t.Fatalf("seq after torn = %d..%d, want 2..2", first, last)
	}
}

var _ = filepath.Join

func TestParseTranscriptLine(t *testing.T) {
	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)

	// 统一信封（新格式）
	env, _ := recordToEnvelope(Record{Seq: 7, Kind: JournalMessage, Message: msg(message.RoleUser, "envelope msg"), CreatedAt: now})
	envJSON, _ := json.Marshal(env)
	rec, err := ParseTranscriptLine(envJSON)
	if err != nil {
		t.Fatalf("parse envelope: %v", err)
	}
	if rec.Kind != JournalMessage || rec.Message.Content != "envelope msg" || !rec.CreatedAt.Equal(now) {
		t.Fatalf("envelope parse mismatch: %+v", rec)
	}

	// legacy Record（旧格式）
	legacy, _ := json.Marshal(Record{Seq: 2, Kind: JournalAssistant, TurnID: "t1", Message: msg(message.RoleAssistant, "legacy msg"), CreatedAt: now})
	rec, err = ParseTranscriptLine(legacy)
	if err != nil {
		t.Fatalf("parse legacy: %v", err)
	}
	if rec.Kind != JournalAssistant || rec.Message.Content != "legacy msg" || rec.TurnID != "t1" {
		t.Fatalf("legacy parse mismatch: %+v", rec)
	}

	// 损坏行必须报错
	if _, err := ParseTranscriptLine([]byte("not json")); err == nil {
		t.Fatal("garbage line must error")
	}
}
