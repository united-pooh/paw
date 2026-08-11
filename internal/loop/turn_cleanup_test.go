package loop

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"paw/internal/message"
	"paw/internal/model"
	"paw/internal/tool"
)

type recordingTurnCleaner struct {
	mu        sync.Mutex
	sessionID string
	turnID    string
	reason    string
	calls     int
}

func (c *recordingTurnCleaner) StopOwnedTasks(_ context.Context, sessionID, turnID, reason string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sessionID, c.turnID, c.reason = sessionID, turnID, reason
	c.calls++
}

type turnOwnerProbeTool struct {
	owner TurnOwner
	ok    bool
}

func (*turnOwnerProbeTool) Name() string                 { return "OwnerProbe" }
func (*turnOwnerProbeTool) Description() string          { return "record turn owner" }
func (*turnOwnerProbeTool) InputSchema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (t *turnOwnerProbeTool) Run(ctx context.Context, _ json.RawMessage) (string, error) {
	t.owner, t.ok = TurnOwnerFromContext(ctx)
	return "started background work", nil
}

func TestFailedTurnCleansOwnedTasksAndPropagatesTurnOwner(t *testing.T) {
	streamer := &fakeModel{rounds: []fakeRound{
		{events: []model.StreamEvent{
			{ToolCalls: []message.ToolCall{{ID: "probe-1", Name: "OwnerProbe", Input: json.RawMessage(`{}`)}}, Done: true},
		}},
		{err: errors.New("responses stream failed")},
	}}
	probe := &turnOwnerProbeTool{}
	registry := tool.NewRegistry()
	registry.Register(probe)
	cleaner := &recordingTurnCleaner{}
	runner := NewRunner(streamer, &fakeUI{}, registry, nil, "parent-session")
	runner.SetTurnOwnedTaskCleaner(cleaner)

	if _, err := runner.RunTurn(context.Background(), "start work"); err == nil || !strings.Contains(err.Error(), "responses stream failed") {
		t.Fatalf("RunTurn() error = %v", err)
	}
	if !probe.ok || probe.owner.SessionID != "parent-session" || probe.owner.TurnID == "" {
		t.Fatalf("tool owner = %#v ok=%v", probe.owner, probe.ok)
	}
	cleaner.mu.Lock()
	defer cleaner.mu.Unlock()
	if cleaner.calls != 1 || cleaner.sessionID != probe.owner.SessionID || cleaner.turnID != probe.owner.TurnID {
		t.Fatalf("cleanup = calls=%d owner=%q/%q, tool owner=%#v", cleaner.calls, cleaner.sessionID, cleaner.turnID, probe.owner)
	}
	if !strings.Contains(cleaner.reason, "responses stream failed") {
		t.Fatalf("cleanup reason = %q", cleaner.reason)
	}
}

func TestCompletedTurnDoesNotCleanOwnedTasks(t *testing.T) {
	cleaner := &recordingTurnCleaner{}
	runner := NewRunner(&fakeModel{rounds: []fakeRound{{events: []model.StreamEvent{{Delta: "done"}, {Done: true}}}}}, &fakeUI{}, tool.NewRegistry(), nil, "parent-session")
	runner.SetTurnOwnedTaskCleaner(cleaner)
	if _, err := runner.RunTurn(context.Background(), "finish"); err != nil {
		t.Fatal(err)
	}
	cleaner.mu.Lock()
	defer cleaner.mu.Unlock()
	if cleaner.calls != 0 {
		t.Fatalf("cleanup calls = %d, want 0", cleaner.calls)
	}
}
