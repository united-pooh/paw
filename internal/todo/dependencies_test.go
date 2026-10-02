package todo

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestDependenciesDecodeAndNormalize(t *testing.T) {
	input, err := DecodeUpdateInput(json.RawMessage(`{"items":[
		{"id":"ship","content":"Ship","status":"pending","blocked_by":[" test ","build"]},
		{"id":"test","content":"Test","status":"pending","blocked_by":["build"]},
		{"id":"build","content":"Build","status":"in_progress"}
	]}`))
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"blocked_by":["build","test"]`) {
		t.Fatalf("dependencies not normalized and retained: %s", data)
	}
}

func TestDependenciesRejectInvalidGraphWithoutPublishing(t *testing.T) {
	for _, tt := range []struct{ name, items, want string }{
		{"empty", `{"id":"a","content":"A","status":"pending","blocked_by":[" "]}`, "must not be empty"},
		{"missing", `{"id":"a","content":"A","status":"pending","blocked_by":["missing"]}`, "unknown"},
		{"self", `{"id":"a","content":"A","status":"pending","blocked_by":["a"]}`, "itself"},
		{"duplicate", `{"id":"a","content":"A","status":"pending","blocked_by":["b"," b "]},{"id":"b","content":"B","status":"pending"}`, "duplicate"},
		{"cycle", `{"id":"a","content":"A","status":"pending","blocked_by":["b"]},{"id":"b","content":"B","status":"pending","blocked_by":["a"]}`, "cycle"},
		{"active blocked", `{"id":"a","content":"A","status":"in_progress","blocked_by":["b"]},{"id":"b","content":"B","status":"pending"}`, "unfinished"},
		{"completed blocked", `{"id":"a","content":"A","status":"completed","blocked_by":["b"]},{"id":"b","content":"B","status":"pending"}`, "unfinished"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			broker := NewBroker()
			tool := NewTool(broker)
			if _, err := tool.Run(context.Background(), json.RawMessage(`{"items":[{"id":"old","content":"Keep","status":"pending"}]}`)); err != nil {
				t.Fatal(err)
			}
			before, _ := broker.Latest()
			persisted := false
			tool.OnUpsert = func(context.Context, Snapshot) error { persisted = true; return nil }
			_, err := tool.Run(context.Background(), json.RawMessage(`{"items":[`+tt.items+`]}`))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want %q", err, tt.want)
			}
			if strings.Contains(err.Error(), "unknown field") {
				t.Fatalf("must validate dependency graph, got %v", err)
			}
			after, _ := broker.Latest()
			if persisted || !reflect.DeepEqual(before, after) {
				t.Fatal("invalid graph changed persisted or published state")
			}
		})
	}
}

func TestDependenciesToolSchema(t *testing.T) {
	tool := NewTool(nil)
	var schema map[string]any
	if err := json.Unmarshal(tool.InputSchema(), &schema); err != nil {
		t.Fatal(err)
	}
	item := schema["properties"].(map[string]any)["items"].(map[string]any)["items"].(map[string]any)
	dependency, ok := item["properties"].(map[string]any)["blocked_by"].(map[string]any)
	if !ok || dependency["type"] != "array" {
		t.Fatalf("missing blocked_by array schema: %v", item)
	}
	if !strings.Contains(tool.Description(), "blocked_by") {
		t.Fatal("tool instructions omit dependency usage")
	}
}

func TestDependenciesUnblockOnlyAfterEveryPrerequisiteCompletes(t *testing.T) {
	broker := NewBroker()
	tool := NewTool(broker)
	items := []Item{
		{ID: "ship", Content: "Ship", Status: StatusPending, BlockedBy: []string{"build", "test"}},
		{ID: "test", Content: "Test", Status: StatusPending, BlockedBy: []string{"build"}},
		{ID: "build", Content: "Build", Status: StatusInProgress},
	}
	update := func() Snapshot {
		t.Helper()
		raw, err := json.Marshal(UpdateInput{Items: items})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tool.Run(context.Background(), raw); err != nil {
			t.Fatal(err)
		}
		snapshot, _ := broker.Latest()
		return snapshot
	}
	first := update()
	if got := first.UnresolvedBlockers("ship"); !reflect.DeepEqual(got, []string{"build", "test"}) {
		t.Fatalf("initial blockers: %v", got)
	}
	items[2].Status = StatusCompleted
	items[1].Status = StatusInProgress
	second := update()
	if len(second.UnresolvedBlockers("test")) != 0 || !reflect.DeepEqual(second.UnresolvedBlockers("ship"), []string{"test"}) {
		t.Fatalf("wrong partial unblock: %#v", second)
	}
	if second.AllCompleted() {
		t.Fatal("blocked task counted as complete")
	}
	items[1].Status = StatusCompleted
	items[0].Status = StatusInProgress
	third := update()
	if len(third.UnresolvedBlockers("ship")) != 0 || len(third.Items[0].BlockedBy) != 2 {
		t.Fatal("must unblock without deleting declared dependencies")
	}
	items[0].Status = StatusCompleted
	if !update().AllCompleted() {
		t.Fatal("all completed graph remains unfinished")
	}
}

func TestDependenciesSnapshotsOwnNestedSlices(t *testing.T) {
	items := []Item{
		{ID: "a", Content: "A", Status: StatusPending, BlockedBy: []string{" b "}},
		{ID: "b", Content: "B", Status: StatusPending},
	}
	input, err := NormalizeAndValidate(UpdateInput{Items: items})
	if err != nil {
		t.Fatal(err)
	}
	if items[0].BlockedBy[0] != " b " {
		t.Fatal("normalization mutated caller dependencies")
	}
	original := Snapshot{Items: input.Items}
	cloned := original.Clone()
	cloned.Items[0].BlockedBy[0] = "mutated"
	if original.Items[0].BlockedBy[0] != "b" {
		t.Fatal("Clone shared dependency slice")
	}
	broker := NewBroker()
	broker.Publish(original)
	original.Items[0].BlockedBy[0] = "mutated"
	latest, _ := broker.Latest()
	if latest.Items[0].BlockedBy[0] != "b" {
		t.Fatal("Publish shared dependency slice")
	}
	latest.Items[0].BlockedBy[0] = "mutated"
	next, err := broker.Next(context.Background())
	if err != nil || next.Items[0].BlockedBy[0] != "b" {
		t.Fatalf("Latest shared queued state: %v %v", next, err)
	}
	next.Items[0].BlockedBy[0] = "mutated"
	latest, _ = broker.Latest()
	if latest.Items[0].BlockedBy[0] != "b" {
		t.Fatal("Next shared latest state")
	}
	blockers := latest.UnresolvedBlockers("a")
	blockers[0] = "mutated"
	if latest.Items[0].BlockedBy[0] != "b" {
		t.Fatal("UnresolvedBlockers shared dependencies")
	}
}

func TestDependenciesInvalidReplacementPreservesInput(t *testing.T) {
	items := []Item{{ID: "a", Content: "A", Status: StatusPending, BlockedBy: []string{" z ", "a"}}}
	if _, err := NormalizeAndValidate(UpdateInput{Items: items}); err == nil {
		t.Fatal("accepted unknown dependency")
	}
	if items[0].BlockedBy[0] != " z " {
		t.Fatal("failed normalization mutated input")
	}
}
