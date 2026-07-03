package wsserver

import (
	"context"
	"encoding/json"
	"log"

	"codex-agent-go/internal/loop"
	"github.com/gorilla/websocket"
)

// PreHook runs before processing a client message.
// Return a non-nil error to abort processing for that message.
type PreHook func(ctx context.Context, event loop.SessionEvent) error

// AfterHook runs after processing completes (success or failure).
// It receives the original event and any processing error.
type AfterHook func(ctx context.Context, event loop.SessionEvent, err error)

// Handler dispatches inbound WebSocket messages to the loop.Runner.
type Handler struct {
	runner     *loop.Runner
	preHooks   []PreHook
	afterHooks []AfterHook
}

// NewHandler creates a Handler backed by runner.
func NewHandler(runner *loop.Runner) *Handler {
	return &Handler{runner: runner}
}

// UsePre registers a pre-processing hook. Returns self for chaining.
func (h *Handler) UsePre(hook PreHook) *Handler {
	h.preHooks = append(h.preHooks, hook)
	return h
}

// UseAfter registers a post-processing hook. Returns self for chaining.
func (h *Handler) UseAfter(hook AfterHook) *Handler {
	h.afterHooks = append(h.afterHooks, hook)
	return h
}

// runPreHooks executes all pre hooks in registration order, returning the
// first error encountered.
func (h *Handler) runPreHooks(ctx context.Context, event loop.SessionEvent) error {
	for _, hook := range h.preHooks {
		if err := hook(ctx, event); err != nil {
			return err
		}
	}
	return nil
}

// runAfterHooks executes all after hooks regardless of processing error.
func (h *Handler) runAfterHooks(ctx context.Context, event loop.SessionEvent, err error) {
	for _, hook := range h.afterHooks {
		hook(ctx, event, err)
	}
}

// HandleConn is the per-connection read loop. It reads JSON SessionEvent messages
// from conn and dispatches them to the runner until the connection closes or ctx
// is cancelled.
func (h *Handler) HandleConn(ctx context.Context, conn *websocket.Conn) {
	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			return // connection closed or context cancelled
		}

		var event loop.SessionEvent
		if err := json.Unmarshal(msg, &event); err != nil {
			log.Printf("ws handler: json parse error: %v", err)
			continue
		}

		// Run pre hooks; abort on error.
		if err := h.runPreHooks(ctx, event); err != nil {
			log.Printf("ws handler: pre hook rejected event %s: %v", event.Kind, err)
			h.runAfterHooks(ctx, event, err)
			continue
		}

		// Dispatch to runner based on event kind.
		var procErr error
		switch event.Kind {
		case loop.EventKindUserInput:
			if event.UserInput != nil {
				_, procErr = h.runner.RunTurn(ctx, event.UserInput.Text)
			}
		case loop.EventKindHistoryReset:
			h.runner.ResetHistory()
		default:
			log.Printf("ws handler: unhandled event kind %s", event.Kind)
		}

		h.runAfterHooks(ctx, event, procErr)
	}
}
