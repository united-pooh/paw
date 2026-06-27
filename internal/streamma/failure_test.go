package streamma

import (
	"codex-agent-go/internal/model"
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

func TestRuntimeFailsAfterRetryLimit(t *testing.T) {
	spec := GraphSpec{
		RunID:      "run-retry-limit",
		StepPolicy: StepPolicy{RequireBoundary: true, MaxAttempts: 2},
		Agents:     []AgentSpec{{ID: "a"}},
	}
	agent := &scriptedAgentStreamer{
		responses: []scriptedAgentResponse{
			{events: []model.StreamEvent{{Delta: "still missing boundary"}, {Done: true}}},
			{events: []model.StreamEvent{{Delta: "still missing boundary"}, {Done: true}}},
		},
	}

	result, err := RunGraphWithAgent(context.Background(), spec, agent, "problem")
	if err == nil {
		t.Fatal("expected error")
	}
	if result.Status != RunFailed {
		t.Fatalf("status = %s, want failed", result.Status)
	}
	calls := agent.Calls()
	if len(calls) != 2 {
		t.Fatalf("calls = %d, want 2", len(calls))
	}
	if calls[0].Attempt != 1 || calls[1].Attempt != 2 {
		t.Fatalf("attempts = %#v, want [1 2]", []int{calls[0].Attempt, calls[1].Attempt})
	}
	retryEvents := 0
	failureAgent := ""
	for _, event := range result.Events {
		if event.Type == EventAgentRetry {
			retryEvents++
		}
		if event.Type == EventRunFailed {
			failureAgent = event.ProducerAgentID
		}
	}
	if retryEvents != 1 {
		t.Fatalf("retry events = %d, want 1", retryEvents)
	}
	if failureAgent != "a" {
		t.Fatalf("failure agent = %q, want a", failureAgent)
	}
	if !strings.Contains(result.Error, "parser a") {
		t.Fatalf("unexpected failure summary: %q", result.Error)
	}
}

func TestRuntimeDoesNotRetryAfterPartialCommittedStep(t *testing.T) {
	spec := GraphSpec{
		RunID:      "run-no-retry-after-step",
		StepPolicy: StepPolicy{RequireBoundary: true, MaxAttempts: 2},
		Agents:     []AgentSpec{{ID: "a"}},
	}
	agent := &scriptedAgentStreamer{
		responses: []scriptedAgentResponse{
			{events: []model.StreamEvent{
				{Delta: "partial success\nEND_STEP\n"},
				{Err: errors.New("boom after step")},
			}},
		},
	}

	result, err := RunGraphWithAgent(context.Background(), spec, agent, "problem")
	if err == nil {
		t.Fatal("expected error")
	}
	if result.Status != RunFailed {
		t.Fatalf("status = %s, want failed", result.Status)
	}
	if len(agent.Calls()) != 1 {
		t.Fatalf("calls = %d, want 1", len(agent.Calls()))
	}
	for _, event := range result.Events {
		if event.Type == EventAgentRetry {
			t.Fatalf("unexpected retry event: %#v", event)
		}
	}
}
