package wsserver

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"sync"
	"sync/atomic"

	"codex-agent-go/internal/loop"
	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true }, // allow all origins for local dev
}

// ServerDeps bundles all dependencies the WebSocket server needs per-session.
type ServerDeps struct {
	Handler   *Handler
	Registry  *AgentRegistry
	Store     loop.SessionEventStore
	SessionID string
}

// Server manages WebSocket connections and broadcasts SessionEvents to all clients.
type Server struct {
	addr    string
	clients sync.Map // key: uint64 connID, value: *websocket.Conn
	nextID  atomic.Uint64
}

// NewServer creates a Server. The listen address is taken from the AGENT_WS_PORT
// environment variable (default: :8765).
func NewServer() *Server {
	addr := os.Getenv("AGENT_WS_PORT")
	if addr == "" {
		addr = ":8765"
	} else {
		addr = ":" + addr
	}
	return &Server{addr: addr}
}

// Broadcast marshals event as JSON and sends it to all connected clients.
// Connections that fail to receive are removed.
func (s *Server) Broadcast(event loop.SessionEvent) {
	log.Printf("broadcast: kind=%s clients=%d", event.Kind, s.clientCount())
	data, err := json.Marshal(event)
	if err != nil {
		return
	}
	s.clients.Range(func(key, value any) bool {
		conn := value.(*websocket.Conn)
		if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
			s.clients.Delete(key)
			conn.Close()
		}
		return true
	})
}

// BuildMux returns an http.ServeMux with /ws registered. Used by ListenAndServe
// and directly by tests via httptest.NewServer.
func (s *Server) BuildMux(deps ServerDeps) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", s.handleWSWithDeps(deps))
	return mux
}

// ListenAndServe starts the WebSocket server and blocks until ctx is cancelled.
func (s *Server) ListenAndServe(ctx context.Context, deps ServerDeps) error {
	mux := s.BuildMux(deps)
	srv := &http.Server{Addr: s.addr, Handler: mux}
	go func() {
		<-ctx.Done()
		srv.Close()
	}()
	log.Printf("WS server listening on %s", s.addr)
	err := srv.ListenAndServe()
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}

// handleWSWithDeps returns an http.HandlerFunc that upgrades to WebSocket,
// sends the initial snapshot + history, then delegates to handler.HandleConn.
func (s *Server) handleWSWithDeps(deps ServerDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			log.Printf("ws upgrade error: %v", err)
			return
		}

		// Send initial state BEFORE registering in clients map.
		// This prevents concurrent Broadcast() calls from racing with setup writes.
		s.pushSnapshot(conn, deps.Registry, deps.SessionID)
		s.pushHistory(r.Context(), conn, deps.Store, deps.SessionID)

		id := s.nextID.Add(1)
		s.clients.Store(id, conn)
		defer func() {
			s.clients.Delete(id)
			conn.Close()
		}()

		if deps.Handler != nil {
			deps.Handler.HandleConn(r.Context(), conn)
		}
	}
}

// pushSnapshot sends a subagents_snapshot event to one connection.
func (s *Server) pushSnapshot(conn *websocket.Conn, registry *AgentRegistry, sessionID string) {
	if registry == nil {
		return
	}
	ev := loop.SessionEvent{
		SessionID: sessionID,
		Kind:      loop.EventKindSubagentsSnapshot,
		SubagentsSnapshot: &loop.SessionSubagentsSnapshotPayload{
			Agents: registry.Snapshot(),
		},
	}
	data, err := json.Marshal(ev)
	if err != nil {
		log.Printf("ws pushSnapshot marshal: %v", err)
		return
	}
	if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
		log.Printf("ws pushSnapshot write: %v", err)
	}
}

// pushHistory replays history_message events from the store to one connection.
// Other event kinds (delta_chunk, tool_call_fired, etc.) are skipped.
func (s *Server) pushHistory(ctx context.Context, conn *websocket.Conn, store loop.SessionEventStore, sessionID string) {
	if store == nil || sessionID == "" {
		return
	}
	events, err := store.Load(ctx, sessionID)
	if err != nil {
		log.Printf("ws pushHistory load: %v", err)
		return
	}
	for _, ev := range events {
		if ev.Kind != loop.EventKindHistoryMessage {
			continue
		}
		data, err := json.Marshal(ev)
		if err != nil {
			continue
		}
		if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
			log.Printf("ws pushHistory write: %v", err)
			return
		}
	}
}

func (s *Server) clientCount() int {
    n := 0
    s.clients.Range(func(_, _ any) bool { n++; return true })
    return n
}
