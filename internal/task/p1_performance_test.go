package task

import (
	"encoding/json"
	"testing"

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
	root := t.TempDir()
	process := &snapshotProcess{}
	manager := &Manager{actors: newTaskActorHost(root, newTaskRegistry(root))}
	manager.actors.bind("task", process)
	t.Cleanup(manager.actors.close)
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
