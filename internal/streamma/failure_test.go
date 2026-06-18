package streamma

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestRuntimeFailFastOnModelError(t *testing.T) {
	spec := GraphSpec{
		RunID:  "run-model-fail",
		Agents: []AgentSpec{{ID: "a"}, {ID: "b"}},
		Edges:  []EdgeSpec{{From: "a", To: "b"}},
	}
	model := newFakeModel(fakeResponse{err: errors.New("boom")})

	result, err := RunGraph(context.Background(), spec, model, "problem")
	if err == nil {
		t.Fatal("expected error")
	}
	if result.Status != RunFailed {
		t.Fatalf("status = %s, want failed", result.Status)
	}
	if len(model.Calls()) != 1 {
		t.Fatalf("calls = %d, want fail-fast single call", len(model.Calls()))
	}
	if !strings.Contains(result.Error, "model a") || !strings.Contains(result.Error, "boom") {
		t.Fatalf("unexpected failure summary: %q", result.Error)
	}
}

func TestRuntimeFailFastOnParserFatal(t *testing.T) {
	spec := GraphSpec{
		RunID:      "run-parser-fail",
		StepPolicy: StepPolicy{MaxStepBytes: 3},
		Agents:     []AgentSpec{{ID: "a"}, {ID: "b"}},
		Edges:      []EdgeSpec{{From: "a", To: "b"}},
	}
	model := newFakeModel(fakeTextResponse("too long"))

	result, err := RunGraph(context.Background(), spec, model, "problem")
	if err == nil {
		t.Fatal("expected error")
	}
	if result.Status != RunFailed {
		t.Fatalf("status = %s, want failed", result.Status)
	}
	if len(model.Calls()) != 1 {
		t.Fatalf("calls = %d, want fail-fast single call", len(model.Calls()))
	}
	if !strings.Contains(result.Error, "parser a") || !strings.Contains(result.Error, "max bytes") {
		t.Fatalf("unexpected parser failure summary: %q", result.Error)
	}
	foundFailure := false
	for _, event := range result.Events {
		if event.Type == EventRunFailed {
			foundFailure = true
		}
	}
	if !foundFailure {
		t.Fatal("event log missing run.failed")
	}
}
