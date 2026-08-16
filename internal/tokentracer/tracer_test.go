package tokentracer

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"paw/internal/model"
)

func TestUsageFromModelUsageNormalizesCachedPromptTokens(t *testing.T) {
	usage := UsageFromModelUsage(model.Usage{
		PromptTokens:     100,
		CompletionTokens: 8,
		PromptTokensDetails: model.TokenDetails{
			CachedTokens: 40,
		},
	})

	if usage.Input != 60 || usage.CacheRead != 40 || usage.Output != 8 || usage.TotalContext != 100 {
		t.Fatalf("UsageFromModelUsage() = %#v, want input=60 cache=40 output=8 total_context=100", usage)
	}
}

func TestTracerAggregatesAPICallsAndPublishesEvents(t *testing.T) {
	tracer := New("unit")
	events, unsubscribe := tracer.Subscribe(true)
	defer unsubscribe()

	tracer.RecordAPICall("stage-1", "Planning", "agent-1", "planner", "openai", "gpt-test", Usage{Input: 10, CacheRead: 5, Output: 3}, nil)

	snapshot := tracer.Snapshot()
	if snapshot.Pipeline.Calls != 1 {
		t.Fatalf("calls = %d, want 1", snapshot.Pipeline.Calls)
	}
	if got := snapshot.Pipeline.Total.TotalContext; got != 15 {
		t.Fatalf("total_context = %d, want 15", got)
	}
	if len(snapshot.Pipeline.Stages) != 1 || len(snapshot.Pipeline.Stages[0].Agents) != 1 {
		t.Fatalf("snapshot stages = %#v, want one stage and one agent", snapshot.Pipeline.Stages)
	}

	deadline := time.After(time.Second)
	seen := false
	for !seen {
		select {
		case event := <-events:
			seen = event.Type == "api_call"
		case <-deadline:
			t.Fatal("timed out waiting for api_call event")
		}
	}
}

func TestServerExposesState(t *testing.T) {
	tracer := New("server-test")
	stageID, _ := tracer.StartTurn("hello", "conversation")
	tracer.RecordAPICall(stageID, "Turn 1", "assistant", "assistant", "test", "model", Usage{Input: 12, CacheRead: 8, Output: 4}, nil)
	server := NewServer(tracer, ServerConfig{Host: "127.0.0.1"})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := server.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), time.Second)
		defer shutdownCancel()
		_ = server.Shutdown(shutdownCtx)
	}()

	resp, err := http.Get(server.URL() + "/api/state")
	if err != nil {
		t.Fatalf("GET /api/state error = %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var snapshot Snapshot
	if err := json.NewDecoder(resp.Body).Decode(&snapshot); err != nil {
		t.Fatalf("decode state: %v", err)
	}
	if snapshot.Pipeline.Name != "server-test" || snapshot.ServerURL == "" {
		t.Fatalf("snapshot = %#v, want pipeline name and server url", snapshot)
	}
	if len(snapshot.Timeline.Rows) == 0 {
		t.Fatalf("timeline rows = 0, want projected rows")
	}
	if snapshot.Pipeline.Name == "" || snapshot.Events == nil {
		t.Fatalf("snapshot lost compatibility fields: %#v", snapshot)
	}
}

func TestTimelineProjectionShowsParallelAgentsAndFailure(t *testing.T) {
	base := time.Date(2026, 6, 26, 14, 37, 9, 0, time.UTC)
	ts := func(offset time.Duration) string { return base.Add(offset).Format(time.RFC3339Nano) }
	pipeline := Pipeline{
		ID:        "run-test",
		Name:      "Paw",
		StartTime: ts(0),
		Status:    "live",
		Stages: []*Stage{{
			ID:        "turn-1",
			Name:      "StreamMA 1",
			StartTime: ts(0),
			EndTime:   ts(3 * time.Minute),
			Status:    "failed",
			Subtotal:  Usage{Input: 30, CacheRead: 20, Output: 10}.Normalized(),
			Calls:     3,
		}},
		Total: Usage{Input: 30, CacheRead: 20, Output: 10}.Normalized(),
		Calls: 3,
	}
	events := []Event{
		{Seq: 1, Type: "stage_start", Timestamp: ts(0), Data: map[string]any{"stage_id": "turn-1", "name": "StreamMA 1"}},
		{Seq: 2, Type: "streamma.task_start", Timestamp: ts(0), Data: map[string]any{"stage_id": "turn-1", "agent_id": "scout", "role": "workspace_scout", "session_id": "scout-session", "invocation_index": 1}},
		{Seq: 3, Type: "streamma.task_start", Timestamp: ts(10 * time.Millisecond), Data: map[string]any{"stage_id": "turn-1", "agent_id": "planner", "role": "source_planner", "session_id": "planner-session", "invocation_index": 1}},
		{Seq: 4, Type: "api_call", Timestamp: ts(10 * time.Second), Data: map[string]any{"stage_id": "turn-1", "agent_id": "scout", "session_id": "scout-session", "invocation_index": 1, "usage": Usage{Input: 10, CacheRead: 5, Output: 2}}},
		{Seq: 5, Type: "api_call", Timestamp: ts(20 * time.Second), Data: map[string]any{"stage_id": "turn-1", "agent_id": "planner", "session_id": "planner-session", "invocation_index": 1, "usage": Usage{Input: 20, CacheRead: 15, Output: 3}}},
		{Seq: 6, Type: "streamma.task_end", Timestamp: ts(40 * time.Second), Data: map[string]any{"stage_id": "turn-1", "agent_id": "scout", "role": "workspace_scout", "session_id": "scout-session", "invocation_index": 1}},
		{Seq: 7, Type: "streamma.agent.step.committed", Timestamp: ts(50 * time.Second), Data: map[string]any{"stage_id": "turn-1", "agent_id": "planner", "step_id": "planner:1"}},
		{Seq: 8, Type: "streamma.task_start", Timestamp: ts(55 * time.Second), Data: map[string]any{"stage_id": "turn-1", "agent_id": "builder", "role": "implementation_builder", "session_id": "builder-session", "invocation_index": 1}},
		{Seq: 9, Type: "api_call", Timestamp: ts(2 * time.Minute), Data: map[string]any{"stage_id": "turn-1", "agent_id": "builder", "session_id": "builder-session", "invocation_index": 1, "usage": Usage{CacheRead: 10, Output: 5}}},
		{Seq: 10, Type: "streamma.run.failed", Timestamp: ts(3 * time.Minute), Data: map[string]any{"stage_id": "turn-1", "error": "parser builder: missing END_STEP"}},
		{Seq: 11, Type: "task_end", Timestamp: ts(3 * time.Minute), Data: map[string]any{"session_id": "builder-session", "status": "failed", "error": "missing END_STEP"}},
		{Seq: 12, Type: "stage_end", Timestamp: ts(3*time.Minute + time.Second), Data: map[string]any{"stage_id": "turn-1", "status": "failed", "error": "context canceled"}},
	}

	timeline := buildTimeline(pipeline, events, base.Add(4*time.Minute))
	scout := findTimelineRow(t, timeline, "scout")
	planner := findTimelineRow(t, timeline, "planner")
	builder := findTimelineRow(t, timeline, "builder")
	if !(mustParseTime(t, scout.StartTime).Before(mustParseTime(t, planner.EndTime)) &&
		mustParseTime(t, planner.StartTime).Before(mustParseTime(t, scout.EndTime))) {
		t.Fatalf("scout/planner rows do not overlap: scout=%#v planner=%#v", scout, planner)
	}
	if builder.Status != "failed" || builder.Error == "" || !timelineHasMarker(builder, "failure") {
		t.Fatalf("builder row = %#v, want failed with marker", builder)
	}
	if planner.Calls != 1 || planner.Usage.Input != 20 || planner.Usage.CacheRead != 15 {
		t.Fatalf("planner usage = %#v calls=%d, want api usage aggregated", planner.Usage, planner.Calls)
	}
	if planner.TokenShare == 0 || timeline.TokenGrandTotal == 0 {
		t.Fatalf("timeline token share = %#v total=%d, want non-zero", planner.TokenShare, timeline.TokenGrandTotal)
	}
	if timeline.Error != "parser builder: missing END_STEP" {
		t.Fatalf("timeline error = %q, want parser root cause", timeline.Error)
	}
	if timeline.MaxConcurrency != 2 || timeline.OverlapMS <= 0 {
		t.Fatalf("timeline concurrency = %dx overlap=%dms, want overlap between scout/planner", timeline.MaxConcurrency, timeline.OverlapMS)
	}
}

func TestTimelineProjectionUsesNowForLiveRows(t *testing.T) {
	base := time.Date(2026, 6, 26, 14, 37, 9, 0, time.UTC)
	now := base.Add(30 * time.Second)
	timeline := buildTimeline(Pipeline{ID: "run-live", Name: "Paw", Status: "live"}, []Event{
		{Seq: 1, Type: "streamma.task_start", Timestamp: base.Format(time.RFC3339Nano), Data: map[string]any{"stage_id": "turn-1", "agent_id": "planner", "session_id": "planner-session", "invocation_index": 1}},
	}, now)
	planner := findTimelineRow(t, timeline, "planner")
	if planner.Status != "live" {
		t.Fatalf("planner status = %q, want live", planner.Status)
	}
	if got := mustParseTime(t, planner.EndTime); !got.Equal(now) {
		t.Fatalf("planner end = %s, want now %s", got, now)
	}
	if planner.DurationMS <= 0 {
		t.Fatalf("duration = %d, want positive", planner.DurationMS)
	}
}

func findTimelineRow(t *testing.T, timeline Timeline, agentID string) TimelineRow {
	t.Helper()
	for _, row := range timeline.Rows {
		if row.AgentID == agentID {
			return row
		}
	}
	t.Fatalf("agent %q not found in rows %#v", agentID, timeline.Rows)
	return TimelineRow{}
}

func timelineHasMarker(row TimelineRow, markerType string) bool {
	for _, marker := range row.Markers {
		if marker.Type == markerType {
			return true
		}
	}
	return false
}

func mustParseTime(t *testing.T, value string) time.Time {
	t.Helper()
	ts, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		t.Fatalf("parse time %q: %v", value, err)
	}
	return ts
}
