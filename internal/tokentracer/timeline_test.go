package tokentracer

import (
	"testing"
	"time"
)

func TestTimelineSubagentLifecycleUsesTaskMetadataAndAggregateTokens(t *testing.T) {
	base := time.Date(2026, 6, 27, 9, 30, 0, 0, time.UTC)
	ts := func(offset time.Duration) string { return base.Add(offset).Format(time.RFC3339Nano) }
	startedAt := ts(2 * time.Second)
	finishedAt := ts(12 * time.Second)
	events := []Event{
		{Seq: 1, Type: "subagent_task_start", Timestamp: ts(time.Second), Data: map[string]any{"task_id": "task-1", "name": "Research citations", "description": "Collect source links", "session_id": "researcher-session", "started_at": startedAt}},
		{Seq: 2, Type: "subagent_task_end", Timestamp: ts(10 * time.Second), Data: map[string]any{"task_id": "task-1", "name": "Research citations", "description": "Collect source links", "session_id": "researcher-session", "status": "completed", "finished_at": finishedAt, "used_tokens": 4479}},
	}

	timeline := buildTimeline(Pipeline{ID: "run-aggregate", Name: "Paw", StartTime: ts(0), Status: "completed"}, events, base.Add(time.Minute))
	researcher := findTimelineRowBySession(t, timeline, "researcher-session")

	if researcher.AgentID != "subagent" {
		t.Fatalf("agent id = %q, want subagent fallback", researcher.AgentID)
	}
	if researcher.Name != "Research citations" || researcher.DisplayName != "Research citations" {
		t.Fatalf("name/display_name = %q/%q, want task name", researcher.Name, researcher.DisplayName)
	}
	if researcher.StartTime != startedAt {
		t.Fatalf("start_time = %q, want started_at %q", researcher.StartTime, startedAt)
	}
	if researcher.EndTime != finishedAt {
		t.Fatalf("end_time = %q, want finished_at %q", researcher.EndTime, finishedAt)
	}
	if researcher.TokenGrandTotal != 4479 {
		t.Fatalf("token_grand_total = %d, want 4479", researcher.TokenGrandTotal)
	}
	if researcher.Usage.Input != 4479 || researcher.Usage.Output != 0 || researcher.Usage.CacheRead != 0 || researcher.Usage.CacheCreation != 0 {
		t.Fatalf("usage = %#v, want aggregate used_tokens mapped without cache/output fields", researcher.Usage)
	}
	if timeline.TokenGrandTotal != 4479 {
		t.Fatalf("timeline token_grand_total = %d, want 4479", timeline.TokenGrandTotal)
	}
}

func TestTimelineSubagentLifecyclePrefersStructuredUsageOverUsedTokens(t *testing.T) {
	base := time.Date(2026, 6, 27, 9, 45, 0, 0, time.UTC)
	ts := func(offset time.Duration) string { return base.Add(offset).Format(time.RFC3339Nano) }
	events := []Event{
		{Seq: 1, Type: "subagent_task_start", Timestamp: ts(0), Data: map[string]any{"task_id": "task-structured", "name": "Build patch", "session_id": "structured-session", "started_at": ts(0)}},
		{Seq: 2, Type: "subagent_task_end", Timestamp: ts(5 * time.Second), Data: map[string]any{
			"task_id":     "task-structured",
			"name":        "Build patch",
			"session_id":  "structured-session",
			"status":      "completed",
			"finished_at": ts(5 * time.Second),
			"usage":       Usage{Input: 12, CacheRead: 5, CacheCreation: 2, Output: 3},
			"used_tokens": 9999,
		}},
	}

	timeline := buildTimeline(Pipeline{ID: "run-structured", Name: "Paw", StartTime: ts(0), Status: "completed"}, events, base.Add(time.Minute))
	builder := findTimelineRowBySession(t, timeline, "structured-session")

	if builder.Usage.Input != 12 || builder.Usage.CacheRead != 5 || builder.Usage.CacheCreation != 2 || builder.Usage.Output != 3 {
		t.Fatalf("usage = %#v, want structured usage fields", builder.Usage)
	}
	if builder.TokenGrandTotal != 22 {
		t.Fatalf("token_grand_total = %d, want structured total 22 instead of used_tokens", builder.TokenGrandTotal)
	}
	if timeline.TokenGrandTotal != 22 {
		t.Fatalf("timeline token_grand_total = %d, want structured total 22", timeline.TokenGrandTotal)
	}
}

func TestTimelineSubagentLifecycleDoesNotOverrideAPICallUsage(t *testing.T) {
	base := time.Date(2026, 6, 27, 10, 15, 0, 0, time.UTC)
	ts := func(offset time.Duration) string { return base.Add(offset).Format(time.RFC3339Nano) }
	events := []Event{
		{Seq: 1, Type: "subagent_task_start", Timestamp: ts(0), Data: map[string]any{"task_id": "task-2", "description": "Implement the patch", "session_id": "builder-session", "started_at": ts(0)}},
		{Seq: 2, Type: "api_call", Timestamp: ts(5 * time.Second), Data: map[string]any{"session_id": "builder-session", "usage": Usage{Input: 10, CacheRead: 3, Output: 2}}},
		{Seq: 3, Type: "subagent_task_end", Timestamp: ts(10 * time.Second), Data: map[string]any{"task_id": "task-2", "description": "Implement the patch", "session_id": "builder-session", "status": "completed", "finished_at": ts(10 * time.Second), "usage": Usage{Input: 1000, CacheRead: 500, Output: 250}, "used_tokens": 9999}},
	}

	timeline := buildTimeline(Pipeline{ID: "run-api", Name: "Paw", StartTime: ts(0), Status: "completed"}, events, base.Add(time.Minute))
	builder := findTimelineRowBySession(t, timeline, "builder-session")

	if builder.Name != "Implement the patch" || builder.DisplayName != "Implement the patch" {
		t.Fatalf("name/display_name = %q/%q, want description fallback", builder.Name, builder.DisplayName)
	}
	if builder.Calls != 1 {
		t.Fatalf("calls = %d, want 1", builder.Calls)
	}
	if builder.Usage.Input != 10 || builder.Usage.CacheRead != 3 || builder.Usage.Output != 2 {
		t.Fatalf("usage = %#v, want api_call usage unchanged", builder.Usage)
	}
	if builder.TokenGrandTotal != 15 {
		t.Fatalf("token_grand_total = %d, want api_call total 15", builder.TokenGrandTotal)
	}
}

func TestTimelineTotalIncludesLifecycleFallbackUsageWithPipelineTotal(t *testing.T) {
	base := time.Date(2026, 6, 27, 11, 0, 0, 0, time.UTC)
	ts := func(offset time.Duration) string { return base.Add(offset).Format(time.RFC3339Nano) }
	events := []Event{
		{Seq: 1, Type: "subagent_task_start", Timestamp: ts(time.Second), Data: map[string]any{"task_id": "task-3", "name": "Check references", "session_id": "checker-session", "started_at": ts(time.Second)}},
		{Seq: 2, Type: "subagent_task_end", Timestamp: ts(4 * time.Second), Data: map[string]any{"task_id": "task-3", "name": "Check references", "session_id": "checker-session", "status": "completed", "finished_at": ts(4 * time.Second), "used_tokens": 25}},
	}
	pipeline := Pipeline{
		ID:        "run-total",
		Name:      "Paw",
		StartTime: ts(0),
		Status:    "completed",
		Total:     Usage{Input: 100, Output: 10}.Normalized(),
		Calls:     1,
	}

	timeline := buildTimeline(pipeline, events, base.Add(time.Minute))
	checker := findTimelineRowBySession(t, timeline, "checker-session")

	if timeline.TokenGrandTotal != 135 {
		t.Fatalf("timeline token_grand_total = %d, want existing 110 plus fallback 25", timeline.TokenGrandTotal)
	}
	if timeline.TokenTotal.Input != 125 || timeline.TokenTotal.Output != 10 {
		t.Fatalf("timeline token_total = %#v, want fallback usage added to existing total", timeline.TokenTotal)
	}
	if checker.TokenGrandTotal != 25 {
		t.Fatalf("checker token_grand_total = %d, want 25", checker.TokenGrandTotal)
	}
	if checker.TokenShare != 18.5 {
		t.Fatalf("checker token_share = %.1f, want 18.5", checker.TokenShare)
	}
}

func findTimelineRowBySession(t *testing.T, timeline Timeline, sessionID string) TimelineRow {
	t.Helper()
	for _, row := range timeline.Rows {
		if row.SessionID == sessionID {
			return row
		}
	}
	t.Fatalf("session %q not found in rows %#v", sessionID, timeline.Rows)
	return TimelineRow{}
}
