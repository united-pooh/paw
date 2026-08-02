package todo

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestDecodeUpdateInputNormalizesWhitespace(t *testing.T) {
	got, err := DecodeUpdateInput(json.RawMessage(`{
        "explanation":"  start implementation  ",
        "items":[
            {"id":" inspect ","content":" Inspect existing code ","status":"completed"},
            {"id":"build","content":" Build Todo page ","status":"in_progress"}
        ]
    }`))
	if err != nil {
		t.Fatalf("DecodeUpdateInput() error = %v", err)
	}
	want := UpdateInput{
		Explanation: "start implementation",
		Items: []Item{
			{ID: "inspect", Content: "Inspect existing code", Status: StatusCompleted},
			{ID: "build", Content: "Build Todo page", Status: StatusInProgress},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("DecodeUpdateInput() = %#v, want %#v", got, want)
	}
}

func TestDecodeUpdateInputAcceptsClear(t *testing.T) {
	got, err := DecodeUpdateInput(json.RawMessage(`{"items":[]}`))
	if err != nil {
		t.Fatalf("DecodeUpdateInput() error = %v", err)
	}
	if got.Items == nil || len(got.Items) != 0 {
		t.Fatalf("Items = %#v, want non-nil empty slice", got.Items)
	}
}

func TestDecodeUpdateInputRejectsInvalidSnapshots(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{"missing items", `{}`, "items is required"},
		{"empty id", `{"items":[{"id":" ","content":"A","status":"pending"}]}`, "items[0].id must not be empty"},
		{"empty content", `{"items":[{"id":"a","content":" ","status":"pending"}]}`, "items[0].content must not be empty"},
		{"duplicate id", `{"items":[{"id":"a","content":"A","status":"pending"},{"id":"a","content":"Again","status":"completed"}]}`, `items contains duplicate id "a"`},
		{"invalid status", `{"items":[{"id":"a","content":"A","status":"blocked"}]}`, "items[0].status must be pending, in_progress, or completed"},
		{"multiple active", `{"items":[{"id":"a","content":"A","status":"in_progress"},{"id":"b","content":"B","status":"in_progress"}]}`, "items may contain at most one in_progress item"},
		{"unknown top field", `{"items":[],"phase":"build"}`, `decode update_todo input: json: unknown field "phase"`},
		{"unknown item field", `{"items":[{"id":"a","content":"A","status":"pending","depends_on":[]}]}`, `decode update_todo input: json: unknown field "depends_on"`},
		{"multiple values", `{"items":[]} {"items":[]}`, "decode update_todo input: multiple JSON values"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := DecodeUpdateInput(json.RawMessage(tt.raw))
			if err == nil || err.Error() != tt.want {
				t.Fatalf("error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestNormalizeAndValidateAllowsSnapshotReplacement(t *testing.T) {
	items := make([]Item, 1001)
	for i := range items {
		items[i] = Item{ID: string(rune(0x1000 + i)), Content: "item", Status: StatusPending}
	}
	items[500].Status = StatusInProgress
	got, err := NormalizeAndValidate(UpdateInput{Items: items})
	if err != nil {
		t.Fatalf("NormalizeAndValidate() error = %v", err)
	}
	if len(got.Items) != 1001 {
		t.Fatalf("len(Items) = %d", len(got.Items))
	}
	got.Items[0].Content = "changed"
	if items[0].Content != "item" {
		t.Fatal("NormalizeAndValidate() aliases input Items")
	}
}

func TestNormalizeAndValidatePreservesIDSemantics(t *testing.T) {
	got, err := NormalizeAndValidate(UpdateInput{Items: []Item{
		{ID: "Task A", Content: "one", Status: StatusPending},
		{ID: "task a", Content: "two", Status: StatusCompleted},
		{ID: "task  a", Content: "three", Status: StatusPending},
	}})
	if err != nil {
		t.Fatalf("NormalizeAndValidate() error = %v", err)
	}
	ids := []string{got.Items[0].ID, got.Items[1].ID, got.Items[2].ID}
	if strings.Join(ids, "|") != "Task A|task a|task  a" {
		t.Fatalf("IDs changed: %#v", ids)
	}
}

func TestValidateSnapshotPreservesUpdatedAt(t *testing.T) {
	updatedAt := time.Date(2026, 8, 2, 10, 0, 0, 0, time.UTC)
	got, err := ValidateSnapshot(Snapshot{
		Explanation: " done ",
		Items:       []Item{{ID: " a ", Content: " A ", Status: StatusCompleted}},
		UpdatedAt:   updatedAt,
	})
	if err != nil {
		t.Fatalf("ValidateSnapshot() error = %v", err)
	}
	if got.UpdatedAt != updatedAt || got.Explanation != "done" || got.Items[0].ID != "a" {
		t.Fatalf("ValidateSnapshot() = %#v", got)
	}
}
