package app

import (
	"context"
	"errors"
	"sync"
	"testing"
)

type supervisorRuntimeFactory struct {
	mu       sync.Mutex
	built    []string
	closed   []string
	runtimes map[string]*WorkspaceRuntime
}

func (f *supervisorRuntimeFactory) build(_ context.Context, opts WorkspaceRuntimeOptions) (*WorkspaceRuntime, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.built = append(f.built, opts.Root)
	runtime := &WorkspaceRuntime{Root: opts.Root}
	runtime.closeStages = []runtimeCloseStage{{name: "test", close: func(context.Context) error {
		f.mu.Lock()
		f.closed = append(f.closed, opts.Root)
		f.mu.Unlock()
		return nil
	}}}
	if f.runtimes == nil {
		f.runtimes = make(map[string]*WorkspaceRuntime)
	}
	f.runtimes[opts.Root] = runtime
	return runtime, nil
}

func (f *supervisorRuntimeFactory) counts() (built, closed int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.built), len(f.closed)
}

func TestSupervisorEvictsOldestIdleRuntime(t *testing.T) {
	factory := &supervisorRuntimeFactory{}
	supervisor := NewSupervisor(SupervisorConfig{Capacity: 2, Factory: factory.build})
	workspaceA := mustCanonicalWorkspace(t, t.TempDir())
	workspaceB := mustCanonicalWorkspace(t, t.TempDir())
	workspaceC := mustCanonicalWorkspace(t, t.TempDir())

	runtimeA, err := supervisor.Open(context.Background(), WorkspaceRuntimeOptions{Root: workspaceA.Path})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := supervisor.Open(context.Background(), WorkspaceRuntimeOptions{Root: workspaceB.Path}); err != nil {
		t.Fatal(err)
	}
	if _, err := supervisor.Open(context.Background(), WorkspaceRuntimeOptions{Root: workspaceC.Path}); err != nil {
		t.Fatal(err)
	}
	if supervisor.LoadedCount() != 2 {
		t.Fatalf("LoadedCount() = %d, want 2", supervisor.LoadedCount())
	}
	if _, ok := supervisor.Runtime(workspaceA.ID); ok {
		t.Fatal("oldest idle runtime A was not evicted")
	}
	if runtimeA.Close() != nil {
		t.Fatal("evicted runtime Close() should remain idempotent")
	}
	built, closed := factory.counts()
	if built != 3 || closed != 1 {
		t.Fatalf("factory counts = built %d closed %d, want 3/1", built, closed)
	}
}

func TestSupervisorRejectsThirdRuntimeWhenBothLoadedAreBusy(t *testing.T) {
	factory := &supervisorRuntimeFactory{}
	supervisor := NewSupervisor(SupervisorConfig{Capacity: 2, Factory: factory.build})
	workspaceA := mustCanonicalWorkspace(t, t.TempDir())
	workspaceB := mustCanonicalWorkspace(t, t.TempDir())
	workspaceC := mustCanonicalWorkspace(t, t.TempDir())
	if _, err := supervisor.Open(context.Background(), WorkspaceRuntimeOptions{Root: workspaceA.Path}); err != nil {
		t.Fatal(err)
	}
	if _, err := supervisor.Open(context.Background(), WorkspaceRuntimeOptions{Root: workspaceB.Path}); err != nil {
		t.Fatal(err)
	}
	runtimeA, ok := supervisor.Runtime(workspaceA.ID)
	if !ok {
		t.Fatal("runtime A is not loaded")
	}
	runtimeA.Coordinator = NewWorkspaceCoordinator()
	if _, err := runtimeA.Coordinator.BeginTurn("session-a", "turn-a"); err != nil {
		t.Fatal(err)
	}
	runtimeB, ok := supervisor.Runtime(workspaceB.ID)
	if !ok {
		t.Fatal("runtime B is not loaded")
	}
	runtimeB.Coordinator = NewWorkspaceCoordinator()
	runtimeB.Coordinator.SetActiveTasks(1)
	if _, err := supervisor.Open(context.Background(), WorkspaceRuntimeOptions{Root: workspaceC.Path}); !errors.Is(err, ErrRuntimeCapacity) {
		t.Fatalf("Open(C) error = %v, want runtime capacity", err)
	}
	if err := supervisor.Close(context.Background(), workspaceA.ID); !errors.Is(err, ErrWorkspaceBusy) {
		t.Fatalf("Close(A) error = %v, want workspace busy", err)
	}
	built, closed := factory.counts()
	if built != 2 || closed != 0 {
		t.Fatalf("factory counts = built %d closed %d, want 2/0", built, closed)
	}
}

func TestSupervisorOpenSameWorkspaceBuildsOnce(t *testing.T) {
	factory := &supervisorRuntimeFactory{}
	supervisor := NewSupervisor(SupervisorConfig{Capacity: 2, Factory: factory.build})
	workspace := mustCanonicalWorkspace(t, t.TempDir())

	const callers = 16
	var wait sync.WaitGroup
	runtimes := make(chan *WorkspaceRuntime, callers)
	errs := make(chan error, callers)
	for range callers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			runtime, err := supervisor.Open(context.Background(), WorkspaceRuntimeOptions{Root: workspace.Path})
			runtimes <- runtime
			errs <- err
		}()
	}
	wait.Wait()
	close(runtimes)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	var first *WorkspaceRuntime
	for runtime := range runtimes {
		if first == nil {
			first = runtime
		} else if runtime != first {
			t.Fatal("concurrent Open returned different runtimes")
		}
	}
	built, _ := factory.counts()
	if built != 1 {
		t.Fatalf("factory builds = %d, want 1", built)
	}
}

func mustCanonicalWorkspace(t *testing.T, path string) WorkspacePath {
	t.Helper()
	workspace, err := CanonicalWorkspace(path)
	if err != nil {
		t.Fatal(err)
	}
	return workspace
}
