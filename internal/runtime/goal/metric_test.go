package goal

import (
	"context"
	"encoding/json"
	"paw/internal/message"
	"testing"
	"time"
)

func testMetric() *MetricSpec {
	return &MetricSpec{Name: "accuracy", Comparison: "gte", Target: 0.9, Unit: "ratio", Measurement: MetricMeasurement{Tool: "Bash", Command: "python3 evaluate.py", JSONPointer: "/accuracy"}}
}

func metricHistory(id, output string) []message.Message {
	return []message.Message{
		{Role: message.RoleAssistant, ToolUse: &message.ToolCall{ID: id, Name: "Bash", Input: json.RawMessage(`{"command":"python3 evaluate.py"}`)}},
		{Role: message.RoleUser, ToolResult: &message.ToolResult{ToolUseID: id, Content: output}},
	}
}

func TestMetricExtractRejectsUntrustedOrMalformedResults(t *testing.T) {
	for _, raw := range []string{`{"accuracy":"0.99"}`, `{"passed":true}`, `{"accuracy":1} [exit status: exit status 1]`, `{"accuracy":1} {"accuracy":2}`, `{"accuracy":1e999}`, `{"accuracy":null}`} {
		if _, _, err := extractMetric(*testMetric(), "measure", metricHistory("measure", raw)); err == nil {
			t.Errorf("accepted invalid output %q", raw)
		}
	}
	history := metricHistory("measure", `{"accuracy":0.92}`)
	value, digest, err := extractMetric(*testMetric(), "measure", history)
	if err != nil || value != 0.92 || digest == "" {
		t.Fatalf("extract = %v %q %v", value, digest, err)
	}
	history[1].ToolResult.IsError = true
	if _, _, err := extractMetric(*testMetric(), "measure", history); err == nil {
		t.Fatal("accepted failed measurement")
	}
}

func TestMetricEvidenceSupportsNativeAssistantParts(t *testing.T) {
	history := metricHistory("native", `{"accuracy":0.93}`)
	call := history[0].ToolUse
	history[0].ToolUse = nil
	history[0].AssistantParts = []message.AssistantPart{{Type: message.AssistantPartToolCall, Status: message.AssistantPartCompleted, ToolCall: call}}
	if _, _, err := extractMetric(*testMetric(), "native", history); err != nil {
		t.Fatal(err)
	}
	if !historyCalls(history)["native"] || experimentProgress(history, nil) == "" || !measurementFresh("native", history) {
		t.Fatal("native evidence missing from goal provenance or progress")
	}
	history[0].ToolUse = call
	if _, _, err := extractMetric(*testMetric(), "native", history); err != nil {
		t.Fatalf("compatibility alias duplicated canonical native call: %v", err)
	}
}

func TestMetricStoreRoundTripAndFreeze(t *testing.T) {
	store, err := NewEventStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := store.Create(ctx, Goal{ID: "g", SessionID: "s", Objective: "reach accuracy", Status: GoalRunning}); err != nil {
		t.Fatal(err)
	}
	g, _, _ := store.Get(ctx, "g")
	g.Metric = testMetric()
	if err := store.Update(ctx, g); err != nil {
		t.Fatal(err)
	}
	g, _, _ = store.Get(ctx, "g")
	if g.Metric == nil {
		t.Fatal("metric definition lost during event replay")
	}
	g.Measurements = append(g.Measurements, MetricResult{ToolCallID: "measure", Value: 0.92, Digest: "verified", RecordedAt: time.Now()})
	if err := store.Update(ctx, g); err != nil {
		t.Fatal(err)
	}
	g, _, _ = store.Get(ctx, "g")
	if len(g.Measurements) != 1 || !g.Metric.Meets(g.Measurements[0].Value) {
		t.Fatalf("measurement not persisted: %+v", g)
	}
	g.Metric.Target = 0.1
	if err := store.Update(ctx, g); err == nil {
		t.Fatal("metric target was silently weakened")
	}
}
