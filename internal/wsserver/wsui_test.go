package wsserver_test

import (
	"context"
	"net/http/httptest"
	"testing"

	"codex-agent-go/internal/loop"
	"codex-agent-go/internal/model"
	"codex-agent-go/internal/subagent"
	"codex-agent-go/internal/ui"
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

func TestWSUI_OnSystemMessage_broadcasts(t *testing.T) {
	registry := wsserver.NewAgentRegistry(func(_ context.Context, _ string) (*loop.Runner, error) {
		return nil, nil
	})
	server := wsserver.NewServer()
	handler := wsserver.NewHandler(nil, registry)
	srv := httptest.NewServer(server.BuildMux(wsserver.ServerDeps{
		Handler:   handler,
		Registry:  registry,
		SessionID: "sess",
	}))
	t.Cleanup(srv.Close)

	conn := wsDialTest(t, srv)
	readEvent(t, conn) // initial subagents_snapshot

	wsui := wsserver.NewWSUI(server, "sess")
	err := wsui.OnSystemMessage(ui.SystemEvent{
		Title:     "Agent",
		Body:      "background answer",
		Color:     "#FFAA00",
		TaskID:    "task-1",
		AgentID:   "agent-session",
		AgentName: "Agent",
		Status:    "completed",
	})
	if err != nil {
		t.Fatalf("OnSystemMessage() error = %v", err)
	}

	ev := readEvent(t, conn)
	if ev.Kind != loop.EventKindSystemMessage {
		t.Fatalf("kind: got %q want %q", ev.Kind, loop.EventKindSystemMessage)
	}
	if ev.SystemMessage == nil {
		t.Fatal("SystemMessage payload is nil")
	}
	if ev.SystemMessage.Body != "background answer" || ev.SystemMessage.TaskID != "task-1" || ev.SystemMessage.AgentName != "Agent" {
		t.Fatalf("SystemMessage = %#v", ev.SystemMessage)
	}
}

func TestWSUI_OnTaskStarted_nil_registry_does_not_panic(t *testing.T) {
	server := wsserver.NewServer()
	wsui := wsserver.NewWSUI(server, "sess")
	// SetRegistry not called
	wsui.OnTaskStarted(subagent.TaskSnapshot{Name: "nobody"})
}
