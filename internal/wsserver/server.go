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

// HandleWS returns an http.HandlerFunc that upgrades HTTP connections to WebSocket
// and delegates each connection to handler.HandleConn.
func (s *Server) HandleWS(handler *Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			log.Printf("ws upgrade error: %v", err)
			return
		}
		id := s.nextID.Add(1)
		s.clients.Store(id, conn)
		defer func() {
			s.clients.Delete(id)
			conn.Close()
		}()
		handler.HandleConn(r.Context(), conn)
	}
}

// ListenAndServe starts the HTTP/WebSocket server on s.addr and blocks until ctx
// is cancelled or a fatal error occurs.
func (s *Server) ListenAndServe(ctx context.Context, handler *Handler) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", s.HandleWS(handler))
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
