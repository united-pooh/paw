package wsserver_test

import (
	"context"
	"testing"

	"codex-agent-go/internal/loop"
	"codex-agent-go/internal/model"
	"codex-agent-go/internal/subagent"
	"codex-agent-go/internal/wsserver"
)

func TestWSUI_SetRegistry_and_OnTaskStarted_activates_slot(t *testing.T) {
	registry := wsserver.NewAgentRegistry(func(_ context.Context, _ string) (*loop.Runner, error) {
		return nil, nil
	})
	server := wsserver.NewServer()
	wsui := wsserver.NewWSUI(server, "sess")
	wsui.SetRegistry(registry)

	name := registry.Snapshot()[0].Name
	wsui.OnTaskStarted(subagent.TaskSnapshot{Name: name})

	for _, a := range registry.Snapshot() {
		if a.Name == name {
			if a.Status != "running" {
				t.Errorf("slot %q: got %q want running", name, a.Status)
			}
			return
		}
	}
	t.Errorf("slot %q not found", name)
}

func TestWSUI_OnTaskFinished_deactivates_slot(t *testing.T) {
	registry := wsserver.NewAgentRegistry(func(_ context.Context, _ string) (*loop.Runner, error) {
		return nil, nil
	})
	server := wsserver.NewServer()
	wsui := wsserver.NewWSUI(server, "sess")
	wsui.SetRegistry(registry)

	name := registry.Snapshot()[0].Name
	wsui.OnTaskStarted(subagent.TaskSnapshot{Name: name})
	wsui.OnTaskFinished(subagent.TaskSnapshot{Name: name})

	for _, a := range registry.Snapshot() {
		if a.Name == name {
			if a.Status != "done" {
				t.Errorf("slot %q: got %q want done", name, a.Status)
			}
			return
		}
	}
	t.Errorf("slot %q not found", name)
}

func TestWSUI_OnModelUsage_does_not_panic_with_no_clients(t *testing.T) {
	server := wsserver.NewServer()
	wsui := wsserver.NewWSUI(server, "sess")
	// No connected clients; must not panic
	wsui.OnModelUsage(model.Usage{InputTokens: 100, OutputTokens: 50})
}

func TestWSUI_OnTaskStarted_nil_registry_does_not_panic(t *testing.T) {
	server := wsserver.NewServer()
	wsui := wsserver.NewWSUI(server, "sess")
	// SetRegistry not called
	wsui.OnTaskStarted(subagent.TaskSnapshot{Name: "nobody"})
}
