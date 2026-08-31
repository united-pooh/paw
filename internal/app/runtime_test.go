package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	configv2 "paw/internal/config"
	uiheadless "paw/internal/ui/headless"
)

func TestWorkspaceRuntimeCloseOrderAndIdempotence(t *testing.T) {
	var mu sync.Mutex
	var order []string
	record := func(name string, err error) runtimeCloseStage {
		return runtimeCloseStage{name: name, close: func(context.Context) error {
			mu.Lock()
			order = append(order, name)
			mu.Unlock()
			return err
		}}
	}
	wantErr := errors.New("task close")
	runtime := &WorkspaceRuntime{closeStages: []runtimeCloseStage{
		record("mcp", nil),
		record("task", wantErr),
		record("runner", nil),
		record("config", nil),
		record("events", nil),
	}}

	const callers = 16
	var wait sync.WaitGroup
	errs := make(chan error, callers)
	for range callers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			errs <- runtime.Close()
		}()
	}
	wait.Wait()
	close(errs)
	for err := range errs {
		if !errors.Is(err, wantErr) {
			t.Fatalf("Close error = %v", err)
		}
	}
	if got, want := order, []string{"mcp", "task", "runner", "config", "events"}; !equalStrings(got, want) {
		t.Fatalf("close order = %v, want %v", got, want)
	}
}

func TestWorkspaceRuntimeCloseTimesOutStageAndContinues(t *testing.T) {
	blocked := make(chan struct{})
	var mu sync.Mutex
	var order []string
	runtime := &WorkspaceRuntime{
		closeTimeout: 20 * time.Millisecond,
		closeStages: []runtimeCloseStage{
			{name: "mcp", close: func(ctx context.Context) error {
				<-blocked
				return nil
			}},
			{name: "task", close: func(context.Context) error {
				mu.Lock()
				order = append(order, "task")
				mu.Unlock()
				return nil
			}},
		},
	}
	err := runtime.Close()
	close(blocked)
	if err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Close error = %v", err)
	}
	if !equalStrings(order, []string{"task"}) {
		t.Fatalf("continued stages = %v", order)
	}
}

func TestWorkerConfigOpenOptionsDisableDiscoveryAndWatchers(t *testing.T) {
	options := configOpenOptions(configv2.Paths{WorkspaceRoot: "/tmp/workspace"}, WorkerContext{WorkerMode: true})
	if !options.DisableModelDiscovery || !options.DisableWatch {
		t.Fatalf("worker config options = %#v", options)
	}
}

func TestBuildWorkspaceRuntimeUsesExplicitRoot(t *testing.T) {
	workspace := t.TempDir()
	other := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("PAW_CONFIG_HOME", filepath.Join(home, "config"))

	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
	if err := os.Chdir(other); err != nil {
		t.Fatal(err)
	}

	runtime, err := BuildWorkspaceRuntime(context.Background(), WorkspaceRuntimeOptions{
		Root:            workspace,
		Output:          uiheadless.New(os.Stdout),
		AllowIncomplete: true,
		WorkerContext: WorkerContext{
			WorkerMode:      true,
			DisableMainTodo: true,
			MCPBroker:       emptyMCPBroker{},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = runtime.Close() }()

	if runtime.Root != workspace || runtime.Runner.WorkspaceRoot() != workspace {
		t.Fatalf("runtime root = %q, runner root = %q", runtime.Root, runtime.Runner.WorkspaceRoot())
	}
	if runtime.ConfigController.Manager().Paths().WorkspaceRoot != workspace {
		t.Fatalf("config workspace root = %q", runtime.ConfigController.Manager().Paths().WorkspaceRoot)
	}
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}
