package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"gocode/internal/session"
)

func TestClearTerminalWindowWritesClearAndScrollbackSequence(t *testing.T) {
	var output bytes.Buffer

	clearTerminalWindow(&output)

	if got, want := output.String(), "\x1b[H\x1b[2J\x1b[3J"; got != want {
		t.Fatalf("clearTerminalWindow() wrote %q, want %q", got, want)
	}
}

// TestResolveSessionIDWithFlag 验证 -s flag 存在时复用已有 session。
func TestResolveSessionIDWithFlag(t *testing.T) {
	dir := t.TempDir()
	store, err := session.NewJSONLStore(dir)
	if err != nil {
		t.Fatalf("NewJSONLStore: %v", err)
	}
	ctx := context.Background()

	// 预先创建一个 session
	id, err := session.GenerateSessionID()
	if err != nil {
		t.Fatalf("GenerateSessionID: %v", err)
	}
	if _, err := store.CreateRoot(ctx, session.CreateRootRequest{SessionID: id}); err != nil {
		t.Fatalf("CreateRoot: %v", err)
	}

	got, err := resolveSessionID(ctx, store, id, dir)
	if err != nil {
		t.Fatalf("resolveSessionID with flag: %v", err)
	}
	if got != id {
		t.Fatalf("resolveSessionID = %q, want %q", got, id)
	}
}

// TestResolveSessionIDWithFlagNotFound 验证 -s flag 指向不存在的 session 时返回错误。
func TestResolveSessionIDWithFlagNotFound(t *testing.T) {
	dir := t.TempDir()
	store, err := session.NewJSONLStore(dir)
	if err != nil {
		t.Fatalf("NewJSONLStore: %v", err)
	}
	ctx := context.Background()

	_, err = resolveSessionID(ctx, store, "nonexistent-session-id", dir)
	if err == nil {
		t.Fatalf("expected error for nonexistent session, got nil")
	}
	if !strings.Contains(err.Error(), "session 不存在") {
		t.Fatalf("error = %q, want 'session 不存在'", err.Error())
	}
}

// TestResolveSessionIDWithoutFlag 验证无 -s flag 时每次生成新 session。
func TestResolveSessionIDWithoutFlag(t *testing.T) {
	dir := t.TempDir()
	store, err := session.NewJSONLStore(dir)
	if err != nil {
		t.Fatalf("NewJSONLStore: %v", err)
	}
	ctx := context.Background()

	id1, err := resolveSessionID(ctx, store, "", dir)
	if err != nil {
		t.Fatalf("resolveSessionID without flag: %v", err)
	}
	if id1 == "" {
		t.Fatalf("resolveSessionID returned empty session ID")
	}

	// 第二次调用应生成不同的 session ID（每次新建）
	id2, err := resolveSessionID(ctx, store, "", dir)
	if err != nil {
		t.Fatalf("second resolveSessionID without flag: %v", err)
	}
	if id1 == id2 {
		t.Fatalf("expected different session IDs, both = %q", id1)
	}

	// 两个 session 都应该存在
	for _, id := range []string{id1, id2} {
		exists, err := store.Exists(ctx, id)
		if err != nil {
			t.Fatalf("Exists(%q): %v", id, err)
		}
		if !exists {
			t.Fatalf("session %q should exist", id)
		}
	}
}
