package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"paw/internal/loop"
	selecttool "paw/internal/tool/select"
)

func TestInteractionQuestionAnswerFlow(t *testing.T) {
	coordinator := NewWorkspaceCoordinator()
	if _, err := coordinator.BeginTurn("s1", "turn-1"); err != nil {
		t.Fatal(err)
	}
	hub := newTestEventHub(t, EventHubConfig{WorkspaceID: "workspace", StreamID: "stream"})
	interactions := NewInteractionHub("workspace", coordinator, hub)
	t.Cleanup(func() { _ = interactions.Close() })

	request := selecttool.Request{Prompt: "Pick one", Mode: selecttool.ModeSingle, Options: []selecttool.Option{{ID: "a", Label: "A"}, {ID: "b", Label: "B"}}}
	resultChannel := make(chan selecttool.BatchResult, 1)
	errorChannel := make(chan error, 1)
	go func() {
		result, err := interactions.RequestQuestion(context.Background(), "s1", "turn-1", request)
		resultChannel <- result
		errorChannel <- err
	}()

	deadline := time.Now().Add(time.Second)
	var pending InteractionState
	for {
		sessionState := coordinator.SessionSnapshot("s1")
		if len(sessionState.Pending) == 1 {
			pending = sessionState.Pending[0]
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("question was not registered")
		}
		time.Sleep(time.Millisecond)
	}
	if pending.Kind != InteractionKindQuestion || pending.TurnID != "turn-1" {
		t.Fatalf("pending = %#v", pending)
	}
	if err := interactions.AnswerQuestion(pending.RequestID, InteractionAnswer{SelectedOptions: []selecttool.SelectedOption{{ID: "b", Label: "B"}}}); err != nil {
		t.Fatal(err)
	}
	result := <-resultChannel
	if err := <-errorChannel; err != nil {
		t.Fatal(err)
	}
	if len(result.Results) != 1 || len(result.Results[0].SelectedOptions) != 1 || result.Results[0].SelectedOptions[0].ID != "b" {
		t.Fatalf("result = %#v", result)
	}
	if state := coordinator.SessionSnapshot("s1"); len(state.Pending) != 0 {
		t.Fatalf("pending after answer = %#v", state.Pending)
	}
	if err := interactions.AnswerQuestion(pending.RequestID, InteractionAnswer{SelectedOptions: []selecttool.SelectedOption{{ID: "a", Label: "A"}}}); err != nil {
		t.Fatalf("repeated answer must stay idempotent: %v", err)
	}
}

func TestInteractionPermissionDecisionFlow(t *testing.T) {
	coordinator := NewWorkspaceCoordinator()
	if _, err := coordinator.BeginTurn("s1", "turn-1"); err != nil {
		t.Fatal(err)
	}
	hub := newTestEventHub(t, EventHubConfig{WorkspaceID: "workspace", StreamID: "stream"})
	interactions := NewInteractionHub("workspace", coordinator, hub)
	t.Cleanup(func() { _ = interactions.Close() })
	request := loop.PermissionRequest{SessionID: "s1", TurnID: "turn-1", ToolCallID: "call-1", CanonicalPath: "/tmp/project/file.go"}

	decisionChannel := make(chan loop.PermissionDecision, 1)
	errorChannel := make(chan error, 1)
	go func() {
		decision, err := interactions.RequestPermission(context.Background(), "s1", "turn-1", request)
		decisionChannel <- decision
		errorChannel <- err
	}()
	deadline := time.Now().Add(time.Second)
	var pending InteractionState
	for {
		sessionState := coordinator.SessionSnapshot("s1")
		if len(sessionState.Pending) == 1 {
			pending = sessionState.Pending[0]
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("permission was not registered")
		}
		time.Sleep(time.Millisecond)
	}
	if pending.Kind != InteractionKindPermission {
		t.Fatalf("pending = %#v", pending)
	}
	if err := interactions.DecidePermission(pending.RequestID, DecisionAllowOnce); err != nil {
		t.Fatal(err)
	}
	if decision := <-decisionChannel; decision != loop.PermissionAllowOnce {
		t.Fatalf("decision = %q", decision)
	}
	if err := <-errorChannel; err != nil {
		t.Fatal(err)
	}
	if err := interactions.DecidePermission(pending.RequestID, DecisionDeny); err != nil {
		t.Fatalf("repeated decision must stay idempotent: %v", err)
	}
	if err := interactions.DecidePermission("missing", DecisionDeny); !errors.Is(err, ErrInteractionNotFound) {
		t.Fatalf("missing request error = %v", err)
	}
}

func TestInteractionScopeValidationAndClose(t *testing.T) {
	coordinator := NewWorkspaceCoordinator()
	if _, err := coordinator.BeginTurn("s1", "turn-1"); err != nil {
		t.Fatal(err)
	}
	hub := newTestEventHub(t, EventHubConfig{WorkspaceID: "workspace", StreamID: "stream"})
	interactions := NewInteractionHub("workspace", coordinator, hub)

	resultChannel := make(chan selecttool.BatchResult, 1)
	errorChannel := make(chan error, 1)
	go func() {
		result, err := interactions.RequestQuestion(context.Background(), "s1", "turn-1", selecttool.Request{Prompt: "q", Mode: selecttool.ModeSingle, Options: []selecttool.Option{{ID: "a", Label: "A"}}})
		resultChannel <- result
		errorChannel <- err
	}()
	deadline := time.Now().Add(time.Second)
	var pending InteractionState
	for {
		sessionState := coordinator.SessionSnapshot("s1")
		if len(sessionState.Pending) == 1 {
			pending = sessionState.Pending[0]
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("question was not registered")
		}
		time.Sleep(time.Millisecond)
	}
	if _, err := coordinator.FailTurn("s1", "turn-1"); err != nil {
		t.Fatal(err)
	}
	interactions.CloseInteraction(pending.RequestID, "s1", "turn-1", InteractionKindQuestion, "turn terminal")
	if err := <-errorChannel; !errors.Is(err, ErrInteractionClosed) {
		t.Fatalf("closed error = %v", err)
	}
	select {
	case <-resultChannel:
	default:
	}
	if state := coordinator.SessionSnapshot("s1"); len(state.Pending) != 0 {
		t.Fatalf("pending after close = %#v", state.Pending)
	}
}
