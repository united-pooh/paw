package selecttool

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func validSingleInput() json.RawMessage {
	return json.RawMessage(`{"prompt":"Pick","mode":"single","options":[{"id":"a","label":"A"}]}`)
}
func TestToolMetadata(t *testing.T) {
	x := New(NewBroker())
	if x.Name() != "Select" || !strings.Contains(x.Description(), "wait") || !strings.Contains(x.Description(), "TUI") {
		t.Fatal("metadata")
	}
	var schema map[string]any
	if e := json.Unmarshal(x.InputSchema(), &schema); e != nil {
		t.Fatal(e)
	}
}
func TestToolRunReturnsStableSubmittedJSON(t *testing.T) {
	b := NewBroker()
	x := New(b)
	go func() {
		e, _ := b.NextEvent(context.Background())
		b.Complete(e.Request.ID, Result{SelectedIDs: []string{"metrics", "logs"}})
	}()
	got, e := x.Run(context.Background(), json.RawMessage(`{"prompt":"Choose","mode":"multiple","options":[{"id":"logs","label":"Logs"},{"id":"metrics","label":"Metrics"}]}`))
	if e != nil || got != `{"cancelled":false,"selected_ids":["metrics","logs"]}` {
		t.Fatalf("got=%s err=%v", got, e)
	}
}
func TestToolRunReturnsCancellationJSON(t *testing.T) {
	b := NewBroker()
	x := New(b)
	go func() {
		e, _ := b.NextEvent(context.Background())
		b.Complete(e.Request.ID, Result{Cancelled: true, SelectedIDs: []string{}})
	}()
	got, e := x.Run(context.Background(), validSingleInput())
	if e != nil || got != `{"cancelled":true,"selected_ids":[]}` {
		t.Fatalf("got=%s err=%v", got, e)
	}
}
func TestToolRunDoesNotPublishInvalidInput(t *testing.T) {
	b := NewBroker()
	_, e := New(b).Run(context.Background(), json.RawMessage(`{"prompt":" ","mode":"single","options":[{"id":"a","label":"A"}]}`))
	if e == nil || e.Error() != "prompt is required" {
		t.Fatal(e)
	}
	ctx, c := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer c()
	if _, e = b.NextEvent(ctx); !errors.Is(e, context.DeadlineExceeded) {
		t.Fatal(e)
	}
}
func TestToolRunReturnsContextCancellation(t *testing.T) {
	b := NewBroker()
	ctx, c := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, e := New(b).Run(ctx, validSingleInput()); done <- e }()
	_, _ = b.NextEvent(context.Background())
	c()
	if e := <-done; !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
}
