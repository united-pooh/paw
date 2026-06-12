package bubble

import "testing"

func TestQueryGuardModelLifecycle(t *testing.T) {
	var guard QueryGuard
	if !guard.StartModel() {
		t.Fatalf("StartModel() = false, want true")
	}
	if !guard.IsModelRunning() || guard.CanStartQueued() {
		t.Fatalf("model running/can start queued = %v/%v", guard.IsModelRunning(), guard.CanStartQueued())
	}
	if guard.StartModel() || guard.StartTerminal() {
		t.Fatalf("guard allowed reentrant start")
	}
	guard.FinishModel()
	if !guard.CanStartQueued() || guard.IsRunning() {
		t.Fatalf("after FinishModel canStart/running = %v/%v", guard.CanStartQueued(), guard.IsRunning())
	}
}

func TestQueryGuardTerminalAndCancelLifecycle(t *testing.T) {
	var guard QueryGuard
	if !guard.StartTerminal() {
		t.Fatalf("StartTerminal() = false, want true")
	}
	if !guard.IsTerminalRunning() {
		t.Fatalf("IsTerminalRunning() = false")
	}
	guard.FinishTerminal()
	if !guard.CanStartQueued() {
		t.Fatalf("CanStartQueued() = false after terminal finish")
	}
	guard.Cancel()
	if guard.CanStartQueued() || guard.StartModel() {
		t.Fatalf("canceled guard allowed queued/model start")
	}
	guard.FinishModel()
	guard.FinishTerminal()
	if !guard.IsCanceled() {
		t.Fatalf("IsCanceled() = false")
	}
}
