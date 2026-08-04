package subagent

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	coremcp "paw/internal/mcp"
)

type snapshotProcess struct {
	updates []coremcp.Snapshot
}

func (p *snapshotProcess) PID() int                    { return 1 }
func (p *snapshotProcess) Wait() (WorkerResult, error) { return WorkerResult{}, nil }
func (p *snapshotProcess) Stop() error                 { return nil }
func (p *snapshotProcess) UpdateMCPSnapshot(snapshot coremcp.Snapshot) error {
	p.updates = append(p.updates, snapshot)
	return nil
}

func TestForwardMCPSnapshotsSkipsDuplicateContent(t *testing.T) {
	process := &snapshotProcess{}
	manager := &Manager{
		running: map[string]Process{"task": process},
		tasks:   map[string]TaskSnapshot{},
	}
	updates := make(chan coremcp.Snapshot, 3)
	snapshot := coremcp.Snapshot{Version: 1, Tools: []coremcp.ToolSpec{{Name: "mcp__read"}}}
	updates <- snapshot
	updates <- snapshot.Clone()
	updates <- coremcp.Snapshot{Version: 2, Tools: []coremcp.ToolSpec{{Name: "mcp__read"}}}
	close(updates)

	manager.forwardMCPSnapshots(updates, nil)
	if len(process.updates) != 2 {
		t.Fatalf("forwarded updates = %d, want 2", len(process.updates))
	}
	if process.updates[0].Version != 1 || process.updates[1].Version != 2 {
		t.Fatalf("forwarded versions = %#v, want [1 2]", process.updates)
	}
}

func TestListTasksUsesDiskCacheWithinTTL(t *testing.T) {
	root := t.TempDir()
	manager := &Manager{
		root:     root,
		registry: newTaskRegistry(root),
		tasks:    map[string]TaskSnapshot{},
		running:  map[string]Process{},
	}
	first := TaskSnapshot{ID: "first", SessionID: "first", Status: TaskCompleted, StartedAt: time.Now().UTC()}
	if err := manager.registry.saveTask(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	got := manager.ListTasks()
	if len(got) != 1 || got[0].ID != "first" {
		t.Fatalf("first ListTasks = %#v", got)
	}
	second := TaskSnapshot{ID: "second", SessionID: "second", Status: TaskCompleted, StartedAt: first.StartedAt.Add(time.Second)}
	if err := manager.registry.saveTask(context.Background(), second); err != nil {
		t.Fatal(err)
	}
	got = manager.ListTasks()
	if len(got) != 1 || got[0].ID != "first" {
		t.Fatalf("cached ListTasks = %#v, want first task only", got)
	}
	manager.mu.Lock()
	manager.diskTaskCacheAt = time.Now().Add(-taskListCacheTTL)
	manager.mu.Unlock()
	got = manager.ListTasks()
	if len(got) != 2 {
		t.Fatalf("refreshed ListTasks = %#v, want two tasks", got)
	}
}

func BenchmarkListTasksCached(b *testing.B) {
	root := b.TempDir()
	manager := &Manager{
		root:     root,
		registry: newTaskRegistry(root),
		tasks:    map[string]TaskSnapshot{},
		running:  map[string]Process{},
	}
	for i := 0; i < 100; i++ {
		task := TaskSnapshot{ID: fmt.Sprintf("task-%03d", i), SessionID: "session", Status: TaskCompleted, StartedAt: time.Now().UTC()}
		if err := manager.registry.saveTask(context.Background(), task); err != nil {
			b.Fatal(err)
		}
	}
	_ = manager.ListTasks()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = manager.ListTasks()
	}
}

func TestSnapshotFingerprintIsStableForClone(t *testing.T) {
	snapshot := coremcp.Snapshot{Version: 7, Tools: []coremcp.ToolSpec{{Name: "x", InputSchema: json.RawMessage(`{"type":"object"}`)}}}
	manager := &Manager{}
	if manager.snapshotAlreadyForwarded(snapshot) {
		t.Fatal("first snapshot was treated as duplicate")
	}
	if !manager.snapshotAlreadyForwarded(snapshot.Clone()) {
		t.Fatal("cloned snapshot was not treated as duplicate")
	}
}
