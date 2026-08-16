package task

import (
	"context"
	"strings"
	"testing"
)

func TestInProcessLauncherConvertsPanicToWorkerFailure(t *testing.T) {
	launcher := newInProcessLauncher(func(context.Context, WorkerRequest) (WorkerResult, error) {
		panic("worker panic")
	})

	process, err := launcher.Start(context.Background(), WorkerRequest{TaskID: "panic-task", SessionID: "panic-session"})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	result, waitErr := process.Wait()
	if waitErr == nil {
		t.Fatal("Wait() error = nil, want panic converted to error")
	}
	if result.ExitCode != 1 {
		t.Fatalf("result.ExitCode = %d, want 1", result.ExitCode)
	}
	if !strings.Contains(result.Error, "worker panic") || !strings.Contains(result.Error, "goroutine") {
		t.Fatalf("result.Error = %q, want panic value and stack trace", result.Error)
	}
}
