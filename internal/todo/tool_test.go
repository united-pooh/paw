package todo

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestToolMetadata(t *testing.T) {
	tool := NewTool(nil)
	if tool.Name() != "update_todo" {
		t.Fatalf("Name() = %q", tool.Name())
	}
	description := tool.Description()
	for _, phrase := range []string{"complex", "full", "in_progress", "simple"} {
		if !strings.Contains(description, phrase) {
			t.Fatalf("Description() missing %q: %q", phrase, description)
		}
	}
	var schema map[string]any
	if err := json.Unmarshal(tool.InputSchema(), &schema); err != nil {
		t.Fatalf("schema error: %v", err)
	}
	if schema["additionalProperties"] != false {
		t.Fatalf("root additionalProperties = %#v", schema["additionalProperties"])
	}
	properties := schema["properties"].(map[string]any)
	items := properties["items"].(map[string]any)
	itemSchema := items["items"].(map[string]any)
	if itemSchema["additionalProperties"] != false {
		t.Fatalf("item additionalProperties = %#v", itemSchema["additionalProperties"])
	}
	if !tool.IsConcurrencySafe(nil) {
		t.Fatal("IsConcurrencySafe() = false")
	}
}

func TestToolRunReturnsAuthoritativeSnapshot(t *testing.T) {
	broker := NewBroker()
	fixed := time.Date(2026, 8, 2, 12, 34, 56, 0, time.UTC)
	tool := NewTool(broker)
	tool.nowFn = func() time.Time { return fixed }

	got, err := tool.Run(context.Background(), json.RawMessage(`{
        "explanation":" start ",
        "items":[{"id":"build","content":" Build page ","status":"in_progress"}]
    }`))
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	want := `{"accepted":true,"snapshot":{"explanation":"start","items":[{"id":"build","content":"Build page","status":"in_progress"}],"updated_at":"2026-08-02T12:34:56Z"}}`
	if got != want {
		t.Fatalf("Run() = %s, want %s", got, want)
	}
	published, err := broker.Next(context.Background())
	if err != nil || published.UpdatedAt != fixed {
		t.Fatalf("published = %#v, error = %v", published, err)
	}
}

func TestToolRunRejectsInvalidInputWithoutPublishing(t *testing.T) {
	broker := NewBroker()
	tool := NewTool(broker)
	if _, err := tool.Run(context.Background(), json.RawMessage(`{"items":[{"id":"a","content":"A","status":"blocked"}]}`)); err == nil {
		t.Fatal("Run() accepted invalid input")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := broker.Next(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("broker.Next() error = %v", err)
	}
}

func TestToolRunClearPublishesNonNilEmptyItems(t *testing.T) {
	broker := NewBroker()
	got, err := NewTool(broker).Run(context.Background(), json.RawMessage(`{"items":[]}`))
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	var result UpdateResult
	if err := json.Unmarshal([]byte(got), &result); err != nil {
		t.Fatal(err)
	}
	if result.Snapshot.Items == nil {
		t.Fatal("result items is nil")
	}
	published, err := broker.Next(context.Background())
	if err != nil || published.Items == nil {
		t.Fatalf("published = %#v, error = %v", published, err)
	}
}

func TestToolRunHonorsCancelledContext(t *testing.T) {
	broker := NewBroker()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := NewTool(broker).Run(ctx, json.RawMessage(`{"items":[]}`))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() error = %v", err)
	}
	if _, ok := broker.Latest(); ok {
		t.Fatal("cancelled Run() published a snapshot")
	}
}

func TestToolRunSucceedsWithClosedOrNilBroker(t *testing.T) {
	broker := NewBroker()
	broker.Close()
	for _, tool := range []*Tool{NewTool(broker), NewTool(nil), nil} {
		got, err := tool.Run(context.Background(), json.RawMessage(`{"items":[]}`))
		if err != nil || !strings.Contains(got, `"accepted":true`) {
			t.Fatalf("Run() = %q, %v", got, err)
		}
	}
}

func TestToolRejectsModelSuppliedTimestamp(t *testing.T) {
	_, err := NewTool(nil).Run(context.Background(), json.RawMessage(`{"items":[],"updated_at":"2026-08-02T00:00:00Z"}`))
	if err == nil || !strings.Contains(err.Error(), `unknown field "updated_at"`) {
		t.Fatalf("Run() error = %v", err)
	}
}

func TestToolRunInvokesOnUpsert(t *testing.T) {
	var got *Snapshot
	tool := NewTool(nil)
	tool.OnUpsert = func(ctx context.Context, snapshot Snapshot) error {
		copy := snapshot.Clone()
		got = &copy
		return nil
	}
	out, err := tool.Run(context.Background(), json.RawMessage(`{"explanation":"e","items":[{"id":"a","content":"c","status":"in_progress"}]}`))
	if err != nil || !strings.Contains(out, `"accepted":true`) {
		t.Fatalf("Run() = %q, %v", out, err)
	}
	if got == nil || got.Explanation != "e" || len(got.Items) != 1 || got.Items[0].Status != StatusInProgress {
		t.Fatalf("OnUpsert snapshot = %+v", got)
	}
}

func TestToolRunOnUpsertErrorDoesNotFailTool(t *testing.T) {
	tool := NewTool(nil)
	tool.OnUpsert = func(ctx context.Context, snapshot Snapshot) error {
		return errors.New("event store down")
	}
	if _, err := tool.Run(context.Background(), json.RawMessage(`{"items":[]}`)); err != nil {
		t.Fatalf("event failure must not fail the tool: %v", err)
	}
}
