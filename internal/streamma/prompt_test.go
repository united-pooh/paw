package streamma

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestPromptExcludesDynamicEventLogFields(t *testing.T) {
	event := Event{
		EventID:   "event-volatile-123",
		Seq:       42,
		Timestamp: time.Date(2026, 6, 18, 1, 2, 3, 0, time.UTC),
		Trace:     map[string]string{"trace_id": "trace-volatile-456"},
		Step:      &StepPacket{Content: StepContent{Text: "stable payload"}},
	}
	transcript := NewTranscript(AgentSpec{ID: "b", SystemPrompt: "system"}, "problem")
	transcript.AppendInbound("a", *event.Step)

	text := promptText(BuildPrompt(transcript))
	if !strings.Contains(text, "stable payload") {
		t.Fatalf("prompt missing stable step payload: %q", text)
	}
	for _, forbidden := range []string{"event-volatile-123", "trace-volatile-456", "event_id", "timestamp", "trace", "Seq", "seq"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("prompt contains dynamic field %q: %q", forbidden, text)
		}
	}
}

func TestPromptSegmentsAreCacheStableAcrossEquivalentTranscripts(t *testing.T) {
	build := func() []PromptSegment {
		transcript := NewTranscript(AgentSpec{ID: "b", SystemPrompt: "system"}, "problem")
		transcript.AppendInbound("a", StepPacket{Content: StepContent{Text: "upstream\n"}})
		transcript.AppendOwn(StepPacket{Content: StepContent{Text: "own\n"}})
		return BuildPromptSegments(transcript)
	}

	first := build()
	second := build()
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("segments are not stable:\n%#v\n%#v", first, second)
	}
	if len(first) != 4 {
		t.Fatalf("segments = %d, want 4: %#v", len(first), first)
	}
	for _, segment := range first {
		if !segment.CacheStable {
			t.Fatalf("segment is not cache stable: %#v", segment)
		}
		for _, forbidden := range []string{"event_id", "timestamp", "trace", "seq"} {
			if strings.Contains(segment.Key, forbidden) || strings.Contains(segment.Content, forbidden) {
				t.Fatalf("segment contains volatile metadata %q: %#v", forbidden, segment)
			}
		}
	}
}
