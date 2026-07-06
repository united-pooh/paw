package wsserver_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"codex-agent-go/internal/loop"
	"codex-agent-go/internal/wsserver"
)

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
