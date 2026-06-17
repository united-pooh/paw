package session

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gocode/internal/message"
)

// newTestStore 在临时目录中创建 JSONLStore，baseDir 就是 tempDir 本身。
func newTestStore(t *testing.T) *JSONLStore {
	t.Helper()
	dir := t.TempDir()
	store, err := NewJSONLStore(dir)
	if err != nil {
		t.Fatalf("NewJSONLStore: %v", err)
	}
	return store
}

// createTestSession 创建一个会话并可选地追加消息。
func createTestSession(t *testing.T, store *JSONLStore, sessionID string, msgs []message.Message) {
	t.Helper()
	ctx := context.Background()
	if _, err := store.CreateRoot(ctx, CreateRootRequest{SessionID: sessionID}); err != nil {
		t.Fatalf("CreateRoot(%q): %v", sessionID, err)
	}
	if len(msgs) > 0 {
		if err := store.Append(ctx, sessionID, msgs...); err != nil {
			t.Fatalf("Append(%q): %v", sessionID, err)
		}
	}
}

func TestAppendCreatesRootSessionOnFirstWrite(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	sessionID := "lazy-session"

	exists, err := store.Exists(ctx, sessionID)
	if err != nil {
		t.Fatalf("Exists(%q): %v", sessionID, err)
	}
	if exists {
		t.Fatalf("session %q exists before append", sessionID)
	}

	if err := store.Append(ctx, sessionID, message.Message{Role: message.RoleUser, Content: "hello"}); err != nil {
		t.Fatalf("Append(%q) error = %v", sessionID, err)
	}
	exists, err = store.Exists(ctx, sessionID)
	if err != nil {
		t.Fatalf("Exists(%q) after append: %v", sessionID, err)
	}
	if !exists {
		t.Fatalf("session %q should exist after append", sessionID)
	}
	history, err := store.LoadResolvedHistory(ctx, sessionID)
	if err != nil {
		t.Fatalf("LoadResolvedHistory(%q) error = %v", sessionID, err)
	}
	if len(history) != 1 || history[0].Content != "hello" {
		t.Fatalf("history = %#v, want first appended message", history)
	}
}

// TestListSessions_EmptyDir 验证会话目录不存在时返回空切片而不报错。
func TestListSessions_EmptyDir(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	summaries, err := store.ListSessions(ctx)
	if err != nil {
		t.Fatalf("ListSessions() error = %v", err)
	}
	if len(summaries) != 0 {
		t.Fatalf("ListSessions() = %v, want empty", summaries)
	}
}

// TestListSessions_MultipleSessionsDescOrder 验证多个会话按创建时间倒序返回。
func TestListSessions_MultipleSessionsDescOrder(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	// 创建两个会话，手动设置不同的创建时间
	older := time.Now().Add(-2 * time.Hour).UTC()
	newer := time.Now().Add(-1 * time.Hour).UTC()

	// 先创建 older 会话
	store.nowFn = func() time.Time { return older }
	createTestSession(t, store, "session-old", nil)

	// 再创建 newer 会话
	store.nowFn = func() time.Time { return newer }
	createTestSession(t, store, "session-new", nil)

	summaries, err := store.ListSessions(ctx)
	if err != nil {
		t.Fatalf("ListSessions() error = %v", err)
	}
	if len(summaries) != 2 {
		t.Fatalf("ListSessions() = %d items, want 2", len(summaries))
	}
	// 倒序：newer 在前
	if summaries[0].SessionID != "session-new" {
		t.Fatalf("summaries[0].SessionID = %q, want session-new", summaries[0].SessionID)
	}
	if summaries[1].SessionID != "session-old" {
		t.Fatalf("summaries[1].SessionID = %q, want session-old", summaries[1].SessionID)
	}
}

// TestListSessions_SkipsCorruptSession 验证 meta.json 损坏的会话被跳过，其他会话仍返回。
func TestListSessions_SkipsCorruptSession(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	createTestSession(t, store, "session-good", nil)

	// 手动创建一个 meta.json 内容损坏的会话目录
	corruptDir := filepath.Join(store.baseDir, defaultSessionsDir, "session-corrupt")
	if err := os.MkdirAll(corruptDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(corruptDir, "meta.json"), []byte("not-valid-json"), 0o644); err != nil {
		t.Fatalf("WriteFile corrupt meta: %v", err)
	}

	summaries, err := store.ListSessions(ctx)
	if err != nil {
		t.Fatalf("ListSessions() error = %v", err)
	}
	if len(summaries) != 1 {
		t.Fatalf("ListSessions() = %d items, want 1 (corrupt session skipped)", len(summaries))
	}
	if summaries[0].SessionID != "session-good" {
		t.Fatalf("summaries[0].SessionID = %q, want session-good", summaries[0].SessionID)
	}
}

// TestListSessions_FirstUserMessageTruncated 验证第一条用户消息超过 80 字符时被截断。
func TestListSessions_FirstUserMessageTruncated(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	longMsg := strings.Repeat("a", 100)
	createTestSession(t, store, "session-long", []message.Message{
		{Role: message.RoleUser, Content: longMsg},
	})

	summaries, err := store.ListSessions(ctx)
	if err != nil {
		t.Fatalf("ListSessions() error = %v", err)
	}
	if len(summaries) != 1 {
		t.Fatalf("ListSessions() = %d items, want 1", len(summaries))
	}
	if got := summaries[0].FirstMessage; len(got) > 80 {
		t.Fatalf("FirstMessage length = %d, want <= 80", len(got))
	}
	if got := summaries[0].FirstMessage; got != longMsg[:80] {
		t.Fatalf("FirstMessage = %q, want first 80 chars", got)
	}
}

// TestListSessions_TranscriptSize 验证 TranscriptSize 返回实际文件字节数（大于 0）。
func TestListSessions_TranscriptSize(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	msgs := []message.Message{
		{Role: message.RoleUser, Content: "hello"},
		{Role: message.RoleAssistant, Content: "world"},
		{Role: message.RoleUser, Content: "again"},
	}
	createTestSession(t, store, "session-sized", msgs)

	summaries, err := store.ListSessions(ctx)
	if err != nil {
		t.Fatalf("ListSessions() error = %v", err)
	}
	if len(summaries) != 1 {
		t.Fatalf("ListSessions() = %d items, want 1", len(summaries))
	}
	if got := summaries[0].TranscriptSize; got <= 0 {
		t.Fatalf("TranscriptSize = %d, want > 0 (actual bytes)", got)
	}
}

// TestListSessions_EmptyTranscriptHasZeroSize 验证没有消息的会话 TranscriptSize 为 0。
func TestListSessions_EmptyTranscriptHasZeroSize(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	createTestSession(t, store, "session-empty-transcript", nil)

	summaries, err := store.ListSessions(ctx)
	if err != nil {
		t.Fatalf("ListSessions() error = %v", err)
	}
	if len(summaries) != 1 {
		t.Fatalf("ListSessions() = %d items, want 1", len(summaries))
	}
	if got := summaries[0].TranscriptSize; got != 0 {
		t.Fatalf("TranscriptSize = %d, want 0", got)
	}
}

// TestListSessions_SkipsNonDirectoryEntries 验证 sessions 目录下的普通文件被跳过。
func TestListSessions_SkipsNonDirectoryEntries(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	createTestSession(t, store, "session-real", nil)

	// 在 sessions 目录下放一个普通文件
	sessionsDir := filepath.Join(store.baseDir, defaultSessionsDir)
	if err := os.WriteFile(filepath.Join(sessionsDir, "stray-file.txt"), []byte("ignored"), 0o644); err != nil {
		t.Fatalf("WriteFile stray: %v", err)
	}

	summaries, err := store.ListSessions(ctx)
	if err != nil {
		t.Fatalf("ListSessions() error = %v", err)
	}
	if len(summaries) != 1 {
		t.Fatalf("ListSessions() = %d items, want 1 (stray file skipped)", len(summaries))
	}
}

// TestListSessions_AssistantMessageNotFirstMessage 验证非 user 角色的消息不作为 FirstMessage。
func TestListSessions_AssistantMessageNotFirstMessage(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	msgs := []message.Message{
		{Role: message.RoleAssistant, Content: "assistant first"},
		{Role: message.RoleUser, Content: "user second"},
	}
	createTestSession(t, store, "session-assistant-first", msgs)

	summaries, err := store.ListSessions(ctx)
	if err != nil {
		t.Fatalf("ListSessions() error = %v", err)
	}
	if len(summaries) != 1 {
		t.Fatalf("ListSessions() = %d items, want 1", len(summaries))
	}
	if got := summaries[0].FirstMessage; got != "user second" {
		t.Fatalf("FirstMessage = %q, want 'user second'", got)
	}
}
