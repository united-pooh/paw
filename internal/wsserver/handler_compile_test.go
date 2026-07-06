package wsserver_test

import (
	"context"
	"testing"

	"codex-agent-go/internal/loop"
	"codex-agent-go/internal/wsserver"
)

func TestHandler_new_signature_compiles(t *testing.T) {
	registry := wsserver.NewAgentRegistry(func(_ context.Context, _ string) (*loop.Runner, error) {
		return nil, nil
	})
	h := wsserver.NewHandler(nil, registry)
	_ = h
}
