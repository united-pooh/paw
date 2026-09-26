package app

import (
	"context"
	"encoding/json"
	"paw/internal/storage/session"
	"paw/internal/todo"
	"strings"
	"testing"
)

func TestStateBlockPreservesTodoDependenciesAfterReload(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store, err := session.NewJSONLStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	var snapshot todo.Snapshot
	if err := json.Unmarshal([]byte(`{
		"items": [
			{"id":"design","content":"设计已确认","status":"completed"},
			{"id":"repair","content":"修复前置问题","status":"in_progress"},
			{"id":"release","content":"继续原任务","status":"pending","blocked_by":["design","repair"]}
		],
		"updated_at":"2026-10-09T01:00:00Z"
	}`), &snapshot); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendTodoSnapshot(ctx, "dependencies", snapshot); err != nil {
		t.Fatal(err)
	}
	reopened, err := session.NewJSONLStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{"store", "restored broker"} {
		t.Run(source, func(t *testing.T) {
			broker := todo.NewBroker()
			defer broker.Close()
			provider := newStateBlockProvider(nil, broker, "dependencies", "", "")
			provider.todoStore = reopened
			if source == "restored broker" {
				restoreTodoBroker(reopened, broker, "dependencies")
			}
			out, err := provider.BuildStateContext(ctx)
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{
				"[pending] 继续原任务 (id: release)",
				`blocked_by: ["design" "repair"]`,
				`unresolved_blockers: ["repair"]`,
			} {
				if !strings.Contains(out, want) {
					t.Fatalf("state recovery lost %q:\n%s", want, out)
				}
			}
		})
	}

	snapshot.Items[1].Status = todo.StatusCompleted
	if _, err := reopened.AppendTodoSnapshot(ctx, "dependencies", snapshot); err != nil {
		t.Fatal(err)
	}
	provider := newStateBlockProvider(nil, nil, "dependencies", "", "")
	provider.todoStore = reopened
	out, err := provider.BuildStateContext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`blocked_by: ["design" "repair"]`, "unresolved_blockers: []"} {
		if !strings.Contains(out, want) {
			t.Fatalf("resolved dependencies missing %q:\n%s", want, out)
		}
	}
}
