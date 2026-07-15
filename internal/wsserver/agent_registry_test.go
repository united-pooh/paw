package wsserver_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"codex-agent-go/internal/loop"
	"codex-agent-go/internal/message"
	"codex-agent-go/internal/model"
	"codex-agent-go/internal/tool"
	"codex-agent-go/internal/ui"
	"codex-agent-go/internal/wsserver"
)

type registryConversationModel struct{}

func (*registryConversationModel) StreamMessage(context.Context, []message.Message, []model.ToolDefinition) (<-chan model.StreamEvent, error) {
	events := make(chan model.StreamEvent, 2)
	events <- model.StreamEvent{Delta: "conversation reply"}
	events <- model.StreamEvent{Done: true}
	close(events)
	return events, nil
}

type registryConversationUI struct{}

func (*registryConversationUI) OnAssistantDelta(string) error         { return nil }
func (*registryConversationUI) OnToolCall(ui.ToolCallEvent) error     { return nil }
func (*registryConversationUI) OnToolResult(ui.ToolResultEvent) error { return nil }
func (*registryConversationUI) OnDone() error                         { return nil }

func newRegistryConversationRunner(sessionID string) *loop.Runner {
	return loop.NewRunner(
		&registryConversationModel{},
		&registryConversationUI{},
		tool.NewRegistry(),
		nil,
		sessionID,
	)
}

// noopFactory returns a nil Runner with no error (for unit tests that don't need routing).
var noopFactory = wsserver.RunnerFactory(func(_ context.Context, _ string) (*loop.Runner, error) {
	return nil, nil
})

func TestNewAgentRegistry_40_idle_slots(t *testing.T) {
	r := wsserver.NewAgentRegistry(noopFactory)
	snap := r.Snapshot()
	if len(snap) != 40 {
		t.Fatalf("got %d slots, want 40", len(snap))
	}
	for _, a := range snap {
		if a.Status != "idle" {
			t.Errorf("slot %q: want idle, got %s", a.Name, a.Status)
		}
		if a.ID == "" {
			t.Errorf("slot %q: ID is empty", a.Name)
		}
		if a.Color == "" {
			t.Errorf("slot %q: Color is empty", a.Name)
		}
	}
}

func TestAgentRegistry_personaID_stable_across_instances(t *testing.T) {
	r1 := wsserver.NewAgentRegistry(noopFactory)
	r2 := wsserver.NewAgentRegistry(noopFactory)
	s1, s2 := r1.Snapshot(), r2.Snapshot()
	for i := range s1 {
		if s1[i].ID != s2[i].ID {
			t.Errorf("slot %d ID not stable: %q vs %q", i, s1[i].ID, s2[i].ID)
		}
	}
}

func TestAgentRegistry_Activate_sets_running(t *testing.T) {
	r := wsserver.NewAgentRegistry(noopFactory)
	firstName := r.Snapshot()[0].Name

	id, ok := r.Activate(context.Background(), firstName)
	if !ok {
		t.Fatal("Activate returned ok=false")
	}
	if id == "" {
		t.Fatal("Activate returned empty id")
	}

	for _, a := range r.Snapshot() {
		if a.Name == firstName {
			if a.Status != "running" {
				t.Errorf("status: got %q want running", a.Status)
			}
			if a.StartedAt == nil {
				t.Error("StartedAt must be set after Activate")
			}
			if a.FinishedAt != nil {
				t.Error("FinishedAt must stay nil while running")
			}
			return
		}
	}
	t.Errorf("slot %q not found in snapshot", firstName)
}

func TestAgentRegistry_Activate_unknown_name_returns_false(t *testing.T) {
	r := wsserver.NewAgentRegistry(noopFactory)
	_, ok := r.Activate(context.Background(), "nobody")
	if ok {
		t.Error("Activate with unknown name should return ok=false")
	}
}

func TestAgentRegistry_Activate_factory_error_rolls_back(t *testing.T) {
	failFactory := wsserver.RunnerFactory(func(_ context.Context, _ string) (*loop.Runner, error) {
		return nil, errors.New("factory error")
	})
	r := wsserver.NewAgentRegistry(failFactory)
	name := r.Snapshot()[0].Name

	_, ok := r.Activate(context.Background(), name)
	if ok {
		t.Error("Activate should return ok=false when factory fails")
	}
	for _, a := range r.Snapshot() {
		if a.Name == name && a.Status != "idle" {
			t.Errorf("slot %q should be idle after factory failure, got %s", name, a.Status)
		}
	}
}

func TestAgentRegistry_Deactivate_sets_done(t *testing.T) {
	r := wsserver.NewAgentRegistry(noopFactory)
	name := r.Snapshot()[0].Name

	r.Activate(context.Background(), name)
	r.Deactivate(name)

	for _, a := range r.Snapshot() {
		if a.Name == name {
			if a.Status != "done" {
				t.Errorf("status after Deactivate: got %q want done", a.Status)
			}
			return
		}
	}
	t.Error("slot not found after Deactivate")
}

func TestAgentRegistry_Finish_preserves_terminal_conversation_and_duration(t *testing.T) {
	tests := []struct {
		name   string
		status wsserver.AgentStatus
	}{
		{name: "done", status: wsserver.AgentStatusDone},
		{name: "failed", status: wsserver.AgentStatusFailed},
		{name: "stopped", status: wsserver.AgentStatusStopped},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := wsserver.NewAgentRegistry(func(_ context.Context, sessionID string) (*loop.Runner, error) {
				return newRegistryConversationRunner(sessionID), nil
			})
			name := r.Snapshot()[0].Name
			id, ok := r.ActivateTask(context.Background(), name, "task-a")
			if !ok {
				t.Fatal("ActivateTask returned ok=false")
			}
			if !r.FinishTask(name, "task-a", tt.status) {
				t.Fatal("FinishTask returned false")
			}

			for _, agent := range r.Snapshot() {
				if agent.Name != name {
					continue
				}
				if agent.Status != string(tt.status) || agent.TaskID != "task-a" || agent.FinishedAt == nil || !agent.ConversationAvailable {
					t.Fatalf("terminal snapshot = %#v", agent)
				}
			}
			if err := r.RouteInput(context.Background(), id, "continue the conversation"); err != nil {
				t.Fatalf("RouteInput after terminal task returned error: %v", err)
			}
			if r.FinishTask(name, "task-a", wsserver.AgentStatusDone) {
				t.Fatal("duplicate terminal transition was accepted")
			}
		})
	}
}

func TestAgentRegistry_TaskGenerationRejectsStaleCallbacksAndClearsFinishTime(t *testing.T) {
	r := wsserver.NewAgentRegistry(func(_ context.Context, _ string) (*loop.Runner, error) {
		return &loop.Runner{}, nil
	})
	name := r.Snapshot()[0].Name

	if _, ok := r.ActivateTask(context.Background(), name, "task-a"); !ok {
		t.Fatal("ActivateTask(task-a) returned ok=false")
	}
	if !r.FinishTask(name, "task-a", wsserver.AgentStatusDone) {
		t.Fatal("FinishTask(task-a) returned false")
	}
	if _, ok := r.ActivateTask(context.Background(), name, "task-b"); !ok {
		t.Fatal("persona was not reusable by task-b")
	}
	if r.FinishTask(name, "task-a", wsserver.AgentStatusFailed) {
		t.Fatal("stale task-a callback was accepted")
	}
	for _, agent := range r.Snapshot() {
		if agent.Name == name && (agent.Status != string(wsserver.AgentStatusRunning) || agent.TaskID != "task-b" || agent.FinishedAt != nil) {
			t.Fatalf("task-b snapshot = %#v", agent)
		}
	}
}

func TestAgentRegistry_RouteInput_nil_runner_returns_error(t *testing.T) {
	r := wsserver.NewAgentRegistry(noopFactory) // noopFactory gives nil runner
	snap := r.Snapshot()
	name := snap[0].Name
	id, _ := r.Activate(context.Background(), name) // runner will be nil

	err := r.RouteInput(context.Background(), id, "hello")
	if err == nil {
		t.Error("RouteInput with nil runner should return error")
	}
}

func TestAgentRegistry_RouteInput_unknown_id_returns_error(t *testing.T) {
	r := wsserver.NewAgentRegistry(noopFactory)
	err := r.RouteInput(context.Background(), "no-such-id", "hello")
	if err == nil {
		t.Error("RouteInput with unknown id should return error")
	}
}

func TestAgentRegistry_Snapshot_running_before_idle(t *testing.T) {
	r := wsserver.NewAgentRegistry(noopFactory)
	snap := r.Snapshot()
	name := snap[5].Name // pick a non-first slot

	r.Activate(context.Background(), name)
	snap2 := r.Snapshot()

	// The running slot must appear before all idle slots
	runningIdx := -1
	for i, a := range snap2 {
		if a.Name == name {
			runningIdx = i
		}
		if a.Status == "idle" && runningIdx >= 0 && i < runningIdx {
			t.Errorf("idle slot at %d appeared before running slot at %d", i, runningIdx)
		}
	}
	if runningIdx < 0 {
		t.Error("running slot not found in snapshot")
	}
}

func TestAgentRegistry_Snapshot_most_recent_running_first(t *testing.T) {
	r := wsserver.NewAgentRegistry(noopFactory)
	snap := r.Snapshot()
	name1, name2 := snap[0].Name, snap[1].Name

	r.Activate(context.Background(), name1)
	time.Sleep(2 * time.Millisecond)
	r.Activate(context.Background(), name2)

	snap2 := r.Snapshot()
	// name2 started later → shorter runtime → should appear first
	if snap2[0].Name != name2 {
		t.Errorf("most recently activated %q should be first, got %q", name2, snap2[0].Name)
	}
	if snap2[1].Name != name1 {
		t.Errorf("earlier activated %q should be second, got %q", name1, snap2[1].Name)
	}
}
