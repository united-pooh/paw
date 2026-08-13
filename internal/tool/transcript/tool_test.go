package transcript

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"paw/internal/message"
	"paw/internal/session"
)

func TestSearchTranscriptTool(t *testing.T) {
	root := t.TempDir()
	store, err := session.NewJSONLStore(filepath.Join(root, ".paw"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	sid := "search-me"
	if _, err := store.CreateRoot(ctx, session.CreateRootRequest{SessionID: sid}); err != nil {
		t.Fatal(err)
	}
	if err := store.Append(ctx, sid,
		message.Message{Role: message.RoleUser, Content: "帮我看看 internal/loop 的压缩逻辑"},
		message.Message{Role: message.RoleAssistant, Content: "好的，我先检查"},
	); err != nil {
		t.Fatal(err)
	}
	tool := New(store, sid)

	// 命中：压缩。
	out, err := tool.Run(ctx, []byte(`{"query":"压缩"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "matched=1") || !strings.Contains(out, "searched=2") {
		t.Fatalf("hit output mismatch: %s", out)
	}
	if !strings.Contains(out, "压缩逻辑") {
		t.Fatalf("hit content missing: %s", out)
	}

	// 0 命中：显式范围提示（D11）。
	out, err = tool.Run(ctx, []byte(`{"query":"不存在的关键词xyz"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "未找到") || !strings.Contains(out, "查不到 ≠ 不存在") || !strings.Contains(out, "已检索") {
		t.Fatalf("zero-hit output must include range hint: %s", out)
	}

	// 空 query 拒绝。
	if _, err := tool.Run(ctx, []byte(`{"query":""}`)); err == nil {
		t.Fatal("empty query must error")
	}
}
