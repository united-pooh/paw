// 覆盖 subagent transcript 预览加载：store 读取路径（统一信封格式）
// 与文件 fallback 路径（envelope + legacy 双格式）。
package bubble

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"paw/internal/message"
	"paw/internal/session"
	"paw/internal/subagent"
)

type fakeResolvedRecordStore struct {
	records []session.Record
	err     error
}

func (f *fakeResolvedRecordStore) ListSessions(context.Context) ([]session.SessionSummary, error) {
	return nil, nil
}

func (f *fakeResolvedRecordStore) LoadResolvedRecords(context.Context, string) ([]session.Record, error) {
	return f.records, f.err
}

func TestLoadSubagentTranscriptEntriesViaStore(t *testing.T) {
	now := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
	store := &fakeResolvedRecordStore{records: []session.Record{
		{Seq: 0, Kind: session.JournalTurnStarted, TurnID: "t1", CreatedAt: now},
		{Seq: 1, Kind: session.JournalMessage, Message: message.Message{Role: message.RoleUser, Content: "subagent prompt"}, CreatedAt: now},
		{Seq: 2, Kind: session.JournalAssistant, TurnID: "t1", Message: message.Message{Role: message.RoleAssistant, Content: "subagent answer"}, CreatedAt: now},
		{Seq: 3, Kind: session.JournalTurnCompleted, TurnID: "t1", CreatedAt: now},
	}}
	task := subagent.TaskSnapshot{ID: "agent-1", SessionID: "agent-1", Status: subagent.TaskCompleted}

	entries, err := loadSubagentTranscriptEntries(context.Background(), store, task, now, "")
	if err != nil {
		t.Fatalf("load via store: %v", err)
	}
	var bodies []string
	for _, entry := range entries {
		bodies = append(bodies, entry.body)
	}
	if len(entries) != 2 || bodies[0] != "subagent prompt" || bodies[1] != "subagent answer" {
		t.Fatalf("entries = %#v, want prompt + answer", entries)
	}
}

func TestLoadSubagentTranscriptEntriesFallsBackToEnvelopeFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "transcript.jsonl")
	content := `{"seq":0,"type":"session.user_message","occurred_at":"2026-08-14T12:00:00Z","schema_version":1,"payload":{"turn_id":"t1","message":{"role":"user","content":"envelope prompt"}}}` + "\n" +
		`{"seq":1,"type":"session.assistant_message","occurred_at":"2026-08-14T12:00:01Z","schema_version":1,"payload":{"turn_id":"t1","message":{"role":"assistant","content":"envelope answer"}}}` + "\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	task := subagent.TaskSnapshot{ID: "agent-1", SessionID: "agent-1", Status: subagent.TaskCompleted, TranscriptPath: path}

	// store 为 nil（或不可用）时走文件 fallback，envelope 行也必须能解析
	entries, err := loadSubagentTranscriptEntries(context.Background(), nil, task, time.Now(), "")
	if err != nil {
		t.Fatalf("load file fallback: %v", err)
	}
	var bodies []string
	for _, entry := range entries {
		bodies = append(bodies, entry.body)
	}
	if len(entries) != 2 || bodies[0] != "envelope prompt" || bodies[1] != "envelope answer" {
		t.Fatalf("entries = %#v, want envelope prompt + answer", entries)
	}
}

func TestLoadSubagentTranscriptEntriesFallbackUsesContentWhenFileMissing(t *testing.T) {
	task := subagent.TaskSnapshot{
		ID:        "agent-1",
		SessionID: "agent-1",
		Status:    subagent.TaskCompleted,
		Content:   "final summary only",
	}
	entries, err := loadSubagentTranscriptEntries(context.Background(), nil, task, time.Now(), "")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(entries) != 1 || entries[0].body != "final summary only" {
		t.Fatalf("entries = %#v, want content fallback", entries)
	}
}
