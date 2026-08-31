package session

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"paw/internal/message"
)

// TestProjectNameFor 覆盖：同名 basename 不同路径生成不同项目名；
// 同一路径生成稳定名称；特殊字符被 slug 化。
func TestProjectNameFor(t *testing.T) {
	a := projectNameFor("/Users/u/python project/go-code")
	b := projectNameFor("/Users/u/other/go-code")
	if a == b {
		t.Fatalf("same basename different path must differ: %q == %q", a, b)
	}
	if projectNameFor("/Users/u/python project/go-code") != a {
		t.Fatal("project name must be stable for same cwd")
	}
	if got := projectNameFor("/tmp/项目 空间"); got != "project-" {
		// 全非 ASCII basename slug 化为空后回退 "project"，仍带哈希。
		if len(got) < len("project-")+8 {
			t.Fatalf("fallback name must carry hash: %q", got)
		}
	}
}

// newGlobalStore 构造全局布局 store（baseDir 全局 + legacyBaseDir 旧工作区）。
func newGlobalStore(t *testing.T) (*JSONLStore, string) {
	t.Helper()
	root := t.TempDir()
	global := filepath.Join(root, "home", ".paw", "projects", "proj-abc123")
	legacy := filepath.Join(root, "cwd", ".paw")
	store, err := NewJSONLStore(global)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	store.legacyBaseDir = legacy
	return store, root
}

// seedLegacySession 在 legacy 目录创建一个会话（模拟旧工作区数据）。
func seedLegacySession(t *testing.T, store *JSONLStore, sessionID string) {
	t.Helper()
	legacyStore, err := NewJSONLStore(store.legacyBaseDir)
	if err != nil {
		t.Fatalf("legacy store: %v", err)
	}
	ctx := context.Background()
	if _, err := legacyStore.CreateRoot(ctx, CreateRootRequest{SessionID: sessionID}); err != nil {
		t.Fatalf("legacy create root: %v", err)
	}
	if err := legacyStore.Append(ctx, sessionID, testMessage("legacy first message")); err != nil {
		t.Fatalf("legacy append: %v", err)
	}
}

func testMessage(content string) message.Message {
	return message.Message{Role: message.RoleUser, Content: content}
}

func TestNewJSONLStoreForWorkspaceMigratesLegacySession(t *testing.T) {
	workspace := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	legacyStore, err := NewJSONLStore(filepath.Join(workspace, ".paw"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := legacyStore.CreateRoot(ctx, CreateRootRequest{SessionID: "workspace-legacy"}); err != nil {
		t.Fatal(err)
	}
	if err := legacyStore.Append(ctx, "workspace-legacy", testMessage("legacy first message")); err != nil {
		t.Fatal(err)
	}

	store, err := NewJSONLStoreForWorkspace(workspace)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Append(ctx, "workspace-legacy", testMessage("continued")); err != nil {
		t.Fatal(err)
	}
	history, err := store.LoadResolvedHistory(ctx, "workspace-legacy")
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 2 || history[0].Content != "legacy first message" || history[1].Content != "continued" {
		t.Fatalf("history = %+v", history)
	}
	if _, err := os.Stat(filepath.Join(store.Root(), "sessions", "workspace-legacy", "meta.json")); err != nil {
		t.Fatalf("migrated meta: %v", err)
	}
}

// TestLegacyReadFallback 覆盖：未迁移的 legacy 会话可被全局 store 读取。
func TestLegacyReadFallback(t *testing.T) {
	store, _ := newGlobalStore(t)
	ctx := context.Background()
	sid := "legacy-read-fallback"
	seedLegacySession(t, store, sid)

	meta, err := store.GetMeta(ctx, sid)
	if err != nil {
		t.Fatalf("get meta via fallback: %v", err)
	}
	if meta.SessionID != sid {
		t.Fatalf("meta session id: %q", meta.SessionID)
	}
	if ok, _ := store.Exists(ctx, sid); !ok {
		t.Fatal("exists must detect legacy session")
	}
	history, err := store.LoadResolvedHistory(ctx, sid)
	if err != nil {
		t.Fatalf("load history: %v", err)
	}
	if len(history) != 1 || history[0].Content != "legacy first message" {
		t.Fatalf("history mismatch: %+v", history)
	}
	// 全局目录不应被创建（只读 fallback）。
	if _, err := os.Stat(filepath.Join(store.baseDir, "sessions", sid)); !os.IsNotExist(err) {
		t.Fatalf("global session dir must not exist for un-migrated session")
	}
}

// TestLazyMigrationOnAppend 覆盖：继续 legacy 会话（append）时自动迁移到
// 全局，旧数据 + 新数据完整，legacy 原样保留。
func TestLazyMigrationOnAppend(t *testing.T) {
	store, root := newGlobalStore(t)
	ctx := context.Background()
	sid := "lazy-migrate"
	seedLegacySession(t, store, sid)

	if err := store.Append(ctx, sid, testMessage("new message after resume")); err != nil {
		t.Fatalf("append triggers migration: %v", err)
	}

	// 全局目录存在且含完整数据。
	globalDir := filepath.Join(store.baseDir, "sessions", sid)
	if _, err := os.Stat(filepath.Join(globalDir, "meta.json")); err != nil {
		t.Fatalf("global meta after migration: %v", err)
	}
	history, err := store.LoadResolvedHistory(ctx, sid)
	if err != nil {
		t.Fatalf("load history: %v", err)
	}
	if len(history) != 2 {
		t.Fatalf("history must contain legacy+new, got %d", len(history))
	}
	if history[0].Content != "legacy first message" || history[1].Content != "new message after resume" {
		t.Fatalf("history order mismatch: %+v", history)
	}

	// legacy 原样保留（只读源）。
	if _, err := os.Stat(filepath.Join(store.legacyBaseDir, "sessions", sid, "transcript.jsonl")); err != nil {
		t.Fatalf("legacy source must remain: %v", err)
	}

	// 二次 append 不再重复迁移（全局已存在）。
	if err := store.Append(ctx, sid, testMessage("third")); err != nil {
		t.Fatalf("second append: %v", err)
	}
	history, _ = store.LoadResolvedHistory(ctx, sid)
	if len(history) != 3 {
		t.Fatalf("history after second append: %d", len(history))
	}
	_ = root
}

// TestListSessionsMergesLegacyAndGlobal 覆盖：ListSessions 同时列出
// legacy 与全局会话（同 ID 不重复）。
func TestListSessionsMergesLegacyAndGlobal(t *testing.T) {
	store, _ := newGlobalStore(t)
	ctx := context.Background()
	seedLegacySession(t, store, "legacy-session-a")
	seedLegacySession(t, store, "legacy-session-b")

	// 全局新建一个会话。
	if _, err := store.CreateRoot(ctx, CreateRootRequest{SessionID: "global-session"}); err != nil {
		t.Fatalf("create root: %v", err)
	}

	sessions, err := store.ListSessions(ctx)
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	ids := map[string]bool{}
	for _, s := range sessions {
		ids[s.SessionID] = true
	}
	for _, want := range []string{"legacy-session-a", "legacy-session-b", "global-session"} {
		if !ids[want] {
			t.Fatalf("session %q missing from list: %v", want, ids)
		}
	}
}

// TestLegacyKeyIndexFallback 覆盖：OpenOrCreate 通过 legacy 索引找到
// 旧会话，继续后懒迁移。
func TestLegacyKeyIndexFallback(t *testing.T) {
	store, _ := newGlobalStore(t)
	ctx := context.Background()
	sid := "legacy-index-session"
	seedLegacySession(t, store, sid)

	// 在 legacy store 写索引（模拟旧 OpenOrCreate 留下的绑定）。
	legacyStore, err := NewJSONLStore(store.legacyBaseDir)
	if err != nil {
		t.Fatalf("legacy store: %v", err)
	}
	key := "/some/work/dir"
	if err := os.MkdirAll(filepath.Dir(legacyStore.keyIndexPath(key)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacyStore.keyIndexPath(key), []byte(sid), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := store.OpenOrCreate(ctx, key)
	if err != nil {
		t.Fatalf("open or create: %v", err)
	}
	if got != sid {
		t.Fatalf("open or create must return legacy session, got %q", got)
	}

	// 继续该会话触发懒迁移。
	if err := store.Append(ctx, got, testMessage("resumed")); err != nil {
		t.Fatalf("append after legacy index: %v", err)
	}
	history, err := store.LoadResolvedHistory(ctx, got)
	if err != nil || len(history) != 2 {
		t.Fatalf("history after resume: n=%d err=%v", len(history), err)
	}
}

// TestLoadRecentTurns 覆盖：只返回最后 n 轮；工具参数被清洗；文本保留。
func TestLoadRecentTurns(t *testing.T) {
	dir := t.TempDir()
	store, err := NewJSONLStore(dir)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	ctx := context.Background()
	sid := "recent-turns"
	if _, err := store.CreateRoot(ctx, CreateRootRequest{SessionID: sid}); err != nil {
		t.Fatal(err)
	}
	// 3 个 turn，每个含 user 文本 + assistant 工具调用 + 工具结果。
	for i := 0; i < 3; i++ {
		turnID := "turn-" + string(rune('a'+i))
		if err := store.BeginTurn(ctx, sid, turnID, testMessage("q"+string(rune('a'+i)))); err != nil {
			t.Fatal(err)
		}
		if _, err := store.AppendAssistantWithSequence(ctx, sid, turnID, message.Message{
			Role:     message.RoleAssistant,
			ToolUses: []message.ToolCall{{ID: "call-1", Name: "Read", Input: jsonRaw(`{"path":"/secret/long"}`)}},
		}); err != nil {
			t.Fatal(err)
		}
		big := strings.Repeat("x", 2000)
		if err := store.AppendToolResult(ctx, sid, turnID, 0, message.ToolResult{ToolUseID: "call-1", Content: big}); err != nil {
			t.Fatal(err)
		}
		if err := store.CompleteTurn(ctx, sid, turnID); err != nil {
			t.Fatal(err)
		}
	}

	msgs, err := store.LoadRecentTurns(ctx, sid, 1)
	if err != nil {
		t.Fatalf("load recent: %v", err)
	}
	// 最后 1 轮 = 第 3 轮：user + assistant(tool_use) + user(tool_result) + completed 不产生消息。
	if len(msgs) != 3 {
		t.Fatalf("want 3 messages for last turn, got %d", len(msgs))
	}
	if msgs[0].Content != "qc" {
		t.Fatalf("first message: %q", msgs[0].Content)
	}
	if len(msgs[1].ToolUses) != 1 || string(msgs[1].ToolUses[0].Input) != "{}" {
		t.Fatalf("tool input must be cleaned: %+v", msgs[1].ToolUses)
	}
	if msgs[1].ToolUses[0].Name != "Read" {
		t.Fatalf("tool name must be kept: %+v", msgs[1].ToolUses[0])
	}
	if len([]rune(msgs[2].ToolResult.Content)) > recoveryResultCap+20 {
		t.Fatalf("tool result must be truncated: %d", len(msgs[2].ToolResult.Content))
	}

	all, err := store.LoadRecentTurns(ctx, sid, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 9 {
		t.Fatalf("all turns want 9 messages, got %d", len(all))
	}
}

func jsonRaw(s string) json.RawMessage { return json.RawMessage(s) }
