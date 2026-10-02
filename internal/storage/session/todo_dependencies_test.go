package session

import (
	"context"
	"encoding/json"
	"paw/internal/message"
	"paw/internal/todo"
	"reflect"
	"testing"
)

func TestTodoDependenciesSurviveStoreReopen(t *testing.T) {
	dir := t.TempDir()
	store, err := NewJSONLStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	var snapshot todo.Snapshot
	if err := json.Unmarshal([]byte(`{
		"items":[
			{"id":"fix","content":"Fix prerequisite","status":"in_progress"},
			{"id":"continue","content":"Continue original work","status":"pending","blocked_by":["fix"]}
		],
		"updated_at":"2026-10-09T01:00:00Z"
	}`), &snapshot); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendTodoSnapshot(ctx, "dependencies", snapshot); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewJSONLStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	restored, ok, err := reopened.LoadLatestTodoSnapshot(ctx, "dependencies")
	if err != nil || !ok {
		t.Fatalf("restore snapshot: present=%v err=%v", ok, err)
	}
	data, err := json.Marshal(restored)
	if err != nil {
		t.Fatal(err)
	}
	var persisted struct {
		Items []struct {
			ID        string   `json:"id"`
			BlockedBy []string `json:"blocked_by"`
		} `json:"items"`
	}
	if err := json.Unmarshal(data, &persisted); err != nil {
		t.Fatal(err)
	}
	if len(persisted.Items) != 2 || !reflect.DeepEqual(persisted.Items[1].BlockedBy, []string{"fix"}) {
		t.Fatalf("dependency was lost after reopen: %s", data)
	}
}

func TestTodoWithoutDependenciesRestoresLegacyJSONAfterReopen(t *testing.T) {
	dir := t.TempDir()
	store, err := NewJSONLStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := store.Append(ctx, "legacy",
		message.Message{Role: message.RoleAssistant, ToolUse: &message.ToolCall{
			ID: "legacy-todo", Name: "update_todo", Input: json.RawMessage(`{"items":[]}`),
		}},
		message.Message{Role: message.RoleUser, ToolResult: &message.ToolResult{
			ToolUseID: "legacy-todo",
			Content:   `{"accepted":true,"snapshot":{"items":[{"id":"legacy","content":"Continue old work","status":"pending"}],"updated_at":"2026-08-01T00:00:00Z"}}`,
		}},
	); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewJSONLStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, ok, err := reopened.LoadLatestTodoSnapshot(ctx, "legacy")
	if err != nil || !ok {
		t.Fatalf("restore legacy snapshot: present=%v err=%v", ok, err)
	}
	if len(snapshot.Items) != 1 || snapshot.Items[0].ID != "legacy" || snapshot.Items[0].Status != todo.StatusPending {
		t.Fatalf("legacy snapshot changed: %#v", snapshot)
	}
}
