package wsserver_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"codex-agent-go/internal/loop"
	"codex-agent-go/internal/wsserver"
	"github.com/gorilla/websocket"
)

func wsDialTest(t *testing.T, srv *httptest.Server) *websocket.Conn {
	t.Helper()
	url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("dial %s: %v", url, err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

func readEvent(t *testing.T, conn *websocket.Conn) loop.SessionEvent {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, msg, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	var ev loop.SessionEvent
	if err := json.Unmarshal(msg, &ev); err != nil {
		t.Fatalf("unmarshal: %v\nraw: %s", err, msg)
	}
	return ev
}

func TestServer_pushes_subagents_snapshot_on_connect(t *testing.T) {
	registry := wsserver.NewAgentRegistry(func(_ context.Context, _ string) (*loop.Runner, error) {
		return nil, nil
	})
	store := loop.NewInMemorySessionEventStore()
	server := wsserver.NewServer()
	handler := wsserver.NewHandler(nil, registry)
	deps := wsserver.ServerDeps{
		Handler:   handler,
		Registry:  registry,
		Store:     store,
		SessionID: "test-sess",
	}
	srv := httptest.NewServer(server.BuildMux(deps))
	t.Cleanup(srv.Close)

	conn := wsDialTest(t, srv)
	ev := readEvent(t, conn)

	if ev.Kind != loop.EventKindSubagentsSnapshot {
		t.Errorf("first message kind: got %q want %q", ev.Kind, loop.EventKindSubagentsSnapshot)
	}
	if ev.SubagentsSnapshot == nil {
		t.Fatal("SubagentsSnapshot payload is nil")
	}
	if len(ev.SubagentsSnapshot.Agents) != 40 {
		t.Errorf("agents count: got %d want 40", len(ev.SubagentsSnapshot.Agents))
	}
}

func TestServer_pushes_history_message_on_connect(t *testing.T) {
	registry := wsserver.NewAgentRegistry(func(_ context.Context, _ string) (*loop.Runner, error) {
		return nil, nil
	})
	store := loop.NewInMemorySessionEventStore()

	// Pre-populate store with one history event
	histEv := loop.SessionEvent{
		ID:        "ev-1",
		SessionID: "test-sess",
		Kind:      loop.EventKindHistoryMessage,
	}
	_ = store.Append(context.Background(), "test-sess", histEv)

	server := wsserver.NewServer()
	handler := wsserver.NewHandler(nil, registry)
	deps := wsserver.ServerDeps{
		Handler:   handler,
		Registry:  registry,
		Store:     store,
		SessionID: "test-sess",
	}
	srv := httptest.NewServer(server.BuildMux(deps))
	t.Cleanup(srv.Close)

	conn := wsDialTest(t, srv)

	// Message 1: subagents_snapshot
	ev1 := readEvent(t, conn)
	if ev1.Kind != loop.EventKindSubagentsSnapshot {
		t.Fatalf("first message: got %q want subagents_snapshot", ev1.Kind)
	}

	// Message 2: history_message
	ev2 := readEvent(t, conn)
	if ev2.Kind != loop.EventKindHistoryMessage {
		t.Errorf("second message kind: got %q want history_message", ev2.Kind)
	}
	if ev2.ID != "ev-1" {
		t.Errorf("history event ID: got %q want ev-1", ev2.ID)
	}
}

func TestServer_skips_non_history_events_in_replay(t *testing.T) {
	registry := wsserver.NewAgentRegistry(func(_ context.Context, _ string) (*loop.Runner, error) {
		return nil, nil
	})
	store := loop.NewInMemorySessionEventStore()

	// Add a delta_chunk event (should NOT be replayed) and a history_message
	_ = store.Append(context.Background(), "sess", loop.SessionEvent{
		ID:   "chunk-1",
		Kind: loop.EventKindDeltaChunk,
	})
	_ = store.Append(context.Background(), "sess", loop.SessionEvent{
		ID:   "hist-1",
		Kind: loop.EventKindHistoryMessage,
	})

	server := wsserver.NewServer()
	deps := wsserver.ServerDeps{
		Handler:   wsserver.NewHandler(nil, registry),
		Registry:  registry,
		Store:     store,
		SessionID: "sess",
	}
	srv := httptest.NewServer(server.BuildMux(deps))
	t.Cleanup(srv.Close)

	conn := wsDialTest(t, srv)

	// Skip snapshot
	readEvent(t, conn)

	// Should receive only hist-1, not chunk-1
	ev := readEvent(t, conn)
	if ev.Kind != loop.EventKindHistoryMessage {
		t.Errorf("expected history_message, got %q", ev.Kind)
	}
	if ev.ID != "hist-1" {
		t.Errorf("expected hist-1, got %q", ev.ID)
	}
}
