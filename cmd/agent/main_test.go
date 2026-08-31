package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"paw/internal/model"
	"paw/internal/session"
	"paw/internal/task"
	"paw/internal/tokentracer"
	"paw/internal/ui/headless"
)

func TestClearTerminalWindowWritesClearAndScrollbackSequence(t *testing.T) {
	var output bytes.Buffer

	clearTerminalWindow(&output)

	if got, want := output.String(), "\x1b[H\x1b[2J\x1b[3J"; got != want {
		t.Fatalf("clearTerminalWindow() wrote %q, want %q", got, want)
	}
}

func TestTokenTracerDefaultsFromEnv(t *testing.T) {
	t.Setenv("PAW_TOKEN_TRACER", "0")
	if defaultTokenTracerEnabled() {
		t.Fatalf("defaultTokenTracerEnabled() = true, want false when disabled by env")
	}
	t.Setenv("PAW_TOKEN_TRACER", "")
	if !defaultTokenTracerEnabled() {
		t.Fatalf("defaultTokenTracerEnabled() = false, want true by default")
	}
	t.Setenv("PAW_TOKEN_TRACER_OPEN", "true")
	if !defaultTokenTracerOpen() {
		t.Fatalf("defaultTokenTracerOpen() = false, want true")
	}
	t.Setenv("PAW_TOKEN_TRACER_PORT", "")
	if got := defaultTokenTracerPort(); got != 8999 {
		t.Fatalf("defaultTokenTracerPort() = %d, want 8999 by default", got)
	}
	t.Setenv("PAW_TOKEN_TRACER_PORT", "bad")
	if got := defaultTokenTracerPort(); got != 8999 {
		t.Fatalf("defaultTokenTracerPort() = %d, want 8999 for invalid env", got)
	}
	t.Setenv("PAW_TOKEN_TRACER_PORT", "43210")
	if got := defaultTokenTracerPort(); got != 43210 {
		t.Fatalf("defaultTokenTracerPort() = %d, want 43210", got)
	}
}

func TestStreamMADefaultFromEnv(t *testing.T) {
	t.Setenv("PAW_STREAMMA", "0")
	if defaultStreamMAEnabled() {
		t.Fatalf("defaultStreamMAEnabled() = true, want false when disabled by env")
	}
	t.Setenv("PAW_STREAMMA", "off")
	if defaultStreamMAEnabled() {
		t.Fatalf("defaultStreamMAEnabled() = true, want false for off")
	}
	t.Setenv("PAW_STREAMMA", "")
	if !defaultStreamMAEnabled() {
		t.Fatalf("defaultStreamMAEnabled() = false, want true by default")
	}
}

func TestStartTokenTracerStartsDashboardService(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, server, err := startTokenTracer(ctx, "session-test", options{tokenTracerPort: 0})
	if err != nil {
		t.Fatalf("startTokenTracer() error = %v", err)
	}
	defer func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), time.Second)
		defer shutdownCancel()
		_ = server.Shutdown(shutdownCtx)
	}()

	resp, err := http.Get(server.URL() + "/api/state")
	if err != nil {
		t.Fatalf("GET /api/state error = %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var snapshot tokentracer.Snapshot
	if err := json.NewDecoder(resp.Body).Decode(&snapshot); err != nil {
		t.Fatalf("decode state: %v", err)
	}
	if snapshot.SessionID != "session-test" || snapshot.ServerURL == "" {
		t.Fatalf("snapshot = %#v, want session and server url", snapshot)
	}
}

// 端口被占时回退到随机空闲端口继续，而不是让会话启动失败。
func TestStartTokenTracerFallsBackWhenPortBusy(t *testing.T) {
	blocker, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Close()
	busyPort := blocker.Addr().(*net.TCPAddr).Port

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, server, err := startTokenTracer(ctx, "session-busy-port", options{tokenTracerPort: busyPort})
	if err != nil {
		t.Fatalf("startTokenTracer() error = %v, want fallback to free port", err)
	}
	defer func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), time.Second)
		defer shutdownCancel()
		_ = server.Shutdown(shutdownCtx)
	}()

	resp, err := http.Get(server.URL() + "/api/state")
	if err != nil {
		t.Fatalf("GET /api/state on fallback server error = %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
}

func TestWorkerUsageUICapturesStructuredUsage(t *testing.T) {
	var events []task.WorkerStreamEvent
	workerUI := &workerUsageUI{UI: headless.New(io.Discard), emit: func(event task.WorkerStreamEvent) {
		events = append(events, event)
	}}

	if err := workerUI.OnAssistantDelta("partial answer"); err != nil {
		t.Fatal(err)
	}
	workerUI.OnModelUsage(model.Usage{
		PromptTokens:         120,
		CompletionTokens:     9,
		PromptCacheHitTokens: 50,
	})

	usage := workerUI.Usage()
	if usage == nil {
		t.Fatal("Usage() = nil, want structured usage")
	}
	if usage.Input != 70 || usage.CacheRead != 50 || usage.Output != 9 {
		t.Fatalf("Usage() = %#v, want input/cache/output split", usage)
	}
	if len(events) != 2 || events[0].Delta != "partial answer" || events[1].Usage == nil {
		t.Fatalf("worker events = %#v", events)
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

// TestResolveSessionIDWithoutFlag 验证无 -s flag 时每次生成新 session ID，但不立即落盘。
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

	for _, id := range []string{id1, id2} {
		exists, err := store.Exists(ctx, id)
		if err != nil {
			t.Fatalf("Exists(%q): %v", id, err)
		}
		if exists {
			t.Fatalf("session %q should not exist before first append", id)
		}
	}
}

// TestResolveSessionID_NoFlag_CreatesNewSessions 验证无 flag 时每次调用生成不同的新会话 ID。
func TestResolveSessionID_NoFlag_CreatesNewSessions(t *testing.T) {
	dir := t.TempDir()
	store, err := session.NewJSONLStore(dir)
	if err != nil {
		t.Fatalf("NewJSONLStore: %v", err)
	}
	ctx := context.Background()

	id1, err := resolveSessionID(ctx, store, "", dir)
	if err != nil {
		t.Fatalf("first resolveSessionID: %v", err)
	}
	if id1 == "" {
		t.Fatalf("first resolveSessionID returned empty ID")
	}

	id2, err := resolveSessionID(ctx, store, "", dir)
	if err != nil {
		t.Fatalf("second resolveSessionID: %v", err)
	}
	if id2 == "" {
		t.Fatalf("second resolveSessionID returned empty ID")
	}

	if id1 == id2 {
		t.Fatalf("expected different IDs for two calls without flag, both = %q", id1)
	}
}

// TestResolveSessionID_WithExistingFlag 验证传入已存在的 session ID 时原样返回。
func TestResolveSessionID_WithExistingFlag(t *testing.T) {
	dir := t.TempDir()
	store, err := session.NewJSONLStore(dir)
	if err != nil {
		t.Fatalf("NewJSONLStore: %v", err)
	}
	ctx := context.Background()

	id, err := session.GenerateSessionID()
	if err != nil {
		t.Fatalf("GenerateSessionID: %v", err)
	}
	if _, err := store.CreateRoot(ctx, session.CreateRootRequest{SessionID: id}); err != nil {
		t.Fatalf("CreateRoot: %v", err)
	}

	got, err := resolveSessionID(ctx, store, id, dir)
	if err != nil {
		t.Fatalf("resolveSessionID with existing flag: %v", err)
	}
	if got != id {
		t.Fatalf("resolveSessionID = %q, want %q", got, id)
	}
}

// TestResolveSessionID_WithMissingFlag 验证传入不存在的 session ID 时返回错误。
func TestResolveSessionID_WithMissingFlag(t *testing.T) {
	dir := t.TempDir()
	store, err := session.NewJSONLStore(dir)
	if err != nil {
		t.Fatalf("NewJSONLStore: %v", err)
	}
	ctx := context.Background()

	_, err = resolveSessionID(ctx, store, "nonexistent-random-session-id", dir)
	if err == nil {
		t.Fatalf("expected error for missing session, got nil")
	}
}
