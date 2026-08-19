package main

import (
	"context"
	"testing"
	"time"

	"paw/internal/loop"
	selecttool "paw/internal/tool/select"
)

func TestSelectionPermissionCancellationDeniesRead(t *testing.T) {
	broker := selecttool.NewBroker()
	defer broker.Close()
	prompter := selectionPermissionPrompter{broker: broker}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	type response struct {
		decision loop.PermissionDecision
		err      error
	}
	done := make(chan response, 1)
	go func() {
		decision, err := prompter.PromptPermission(ctx, loop.PermissionRequest{CanonicalPath: "/tmp/outside.txt"})
		done <- response{decision: decision, err: err}
	}()
	event, err := broker.NextEvent(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(event.Request.Options) != 2 || event.Request.Options[0].Label != "Allow once" || event.Request.Options[1].Label != "Deny" {
		t.Fatalf("permission options = %#v", event.Request.Options)
	}
	if !event.Request.OptionsOnly {
		t.Fatal("permission request must hide custom and chat actions")
	}
	if !broker.Complete(event.Request.ID, selecttool.Result{Cancelled: true}) {
		t.Fatal("cancel permission request")
	}
	result := <-done
	if result.err != nil || result.decision != loop.PermissionDeny {
		t.Fatalf("decision=%q err=%v", result.decision, result.err)
	}
}
