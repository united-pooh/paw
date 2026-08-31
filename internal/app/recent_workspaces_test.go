package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRecentWorkspaceStoreRememberSortAndForget(t *testing.T) {
	path := filepath.Join(t.TempDir(), "recent-workspaces.json")
	store, err := NewRecentWorkspaceStore(path)
	if err != nil {
		t.Fatal(err)
	}
	firstTime := time.Date(2026, 8, 31, 1, 0, 0, 0, time.UTC)
	secondTime := firstTime.Add(time.Hour)
	store.now = func() time.Time { return firstTime }
	workspaceA := mustCanonicalWorkspace(t, t.TempDir())
	workspaceB := mustCanonicalWorkspace(t, t.TempDir())
	if err := store.Remember(context.Background(), workspaceA); err != nil {
		t.Fatal(err)
	}
	store.now = func() time.Time { return secondTime }
	if err := store.Remember(context.Background(), workspaceB); err != nil {
		t.Fatal(err)
	}

	items, err := store.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].ID != workspaceB.ID || items[1].ID != workspaceA.ID {
		t.Fatalf("recent workspaces = %#v", items)
	}
	if err := store.Forget(context.Background(), workspaceB.ID); err != nil {
		t.Fatal(err)
	}
	items, err = store.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ID != workspaceA.ID {
		t.Fatalf("recent after forget = %#v", items)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("recent file mode = %o, want 600", info.Mode().Perm())
	}
}

func TestSupervisorForgetRecentDoesNotCloseLoadedRuntime(t *testing.T) {
	path := filepath.Join(t.TempDir(), "recent-workspaces.json")
	recent, err := NewRecentWorkspaceStore(path)
	if err != nil {
		t.Fatal(err)
	}
	factory := &supervisorRuntimeFactory{}
	supervisor := NewSupervisor(SupervisorConfig{Capacity: 2, Factory: factory.build, Recent: recent})
	workspace := mustCanonicalWorkspace(t, t.TempDir())
	if _, err := supervisor.Open(context.Background(), WorkspaceRuntimeOptions{Root: workspace.Path}); err != nil {
		t.Fatal(err)
	}
	if err := supervisor.ForgetRecent(context.Background(), workspace.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := supervisor.Runtime(workspace.ID); !ok {
		t.Fatal("ForgetRecent unexpectedly closed loaded runtime")
	}
	items, err := supervisor.ListRecent(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Fatalf("recent workspaces = %#v, want empty", items)
	}
	_, closed := factory.counts()
	if closed != 0 {
		t.Fatalf("closed runtimes = %d, want 0", closed)
	}
}
