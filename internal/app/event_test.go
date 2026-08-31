package app

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestAppEventJSONGolden(t *testing.T) {
	at := time.Date(2026, 8, 31, 15, 48, 2, 123000000, time.UTC)
	tests := []struct {
		name      string
		eventType EventType
		payload   any
		want      string
	}{
		{
			name: "assistant delta", eventType: EventAssistantDelta,
			payload: AssistantDeltaPayload{PartID: "part-1", Offset: 4, Text: "hello"},
			want:    `{"schema_version":1,"stream_id":"stream-1","sequence":9,"workspace_id":"workspace-1","session_id":"session-1","turn_id":"turn-1","type":"assistant.delta","time":"2026-08-31T15:48:02.123Z","entity_version":4,"payload":{"part_id":"part-1","offset":4,"text":"hello"}}`,
		},
		{
			name: "tool completed", eventType: EventToolCompleted,
			payload: ToolCompletedPayload{ToolUseID: "call-1", Name: "Read", ResultSummary: "42 lines", DetailID: "detail-1", FinishedAt: at, DurationMS: 81},
			want:    `{"schema_version":1,"stream_id":"stream-1","sequence":9,"workspace_id":"workspace-1","session_id":"session-1","turn_id":"turn-1","type":"tool.completed","time":"2026-08-31T15:48:02.123Z","entity_version":4,"payload":{"tool_use_id":"call-1","name":"Read","result_summary":"42 lines","detail_id":"detail-1","finished_at":"2026-08-31T15:48:02.123Z","duration_ms":81}}`,
		},
		{
			name: "question requested", eventType: EventQuestionRequested,
			payload: QuestionRequestedPayload{RequestID: "q-1", Prompt: "Choose", Mode: "single", Options: []QuestionOptionPayload{{ID: "a", Label: "A"}}, CreatedAt: at},
			want:    `{"schema_version":1,"stream_id":"stream-1","sequence":9,"workspace_id":"workspace-1","session_id":"session-1","turn_id":"turn-1","type":"question.requested","time":"2026-08-31T15:48:02.123Z","entity_version":4,"payload":{"request_id":"q-1","prompt":"Choose","mode":"single","options":[{"id":"a","label":"A"}],"created_at":"2026-08-31T15:48:02.123Z"}}`,
		},
		{
			name: "permission requested", eventType: EventPermissionRequested,
			payload: PermissionRequestedPayload{RequestID: "p-1", Operation: "write", CanonicalTarget: "/tmp/project/file.go", CreatedAt: at},
			want:    `{"schema_version":1,"stream_id":"stream-1","sequence":9,"workspace_id":"workspace-1","session_id":"session-1","turn_id":"turn-1","type":"permission.requested","time":"2026-08-31T15:48:02.123Z","entity_version":4,"payload":{"request_id":"p-1","operation":"write","canonical_target":"/tmp/project/file.go","created_at":"2026-08-31T15:48:02.123Z"}}`,
		},
		{
			name: "reset required", eventType: EventResetRequired,
			payload: ResetRequiredPayload{Reason: "cursor_too_old", CurrentStreamID: "stream-1", LatestSequence: 9},
			want:    `{"schema_version":1,"stream_id":"stream-1","sequence":9,"workspace_id":"workspace-1","session_id":"session-1","turn_id":"turn-1","type":"event.reset_required","time":"2026-08-31T15:48:02.123Z","entity_version":4,"payload":{"reason":"cursor_too_old","current_stream_id":"stream-1","latest_sequence":9}}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			event, err := NewAppEvent("workspace-1", "session-1", "turn-1", tt.eventType, at, 4, tt.payload)
			if err != nil {
				t.Fatal(err)
			}
			event.StreamID = "stream-1"
			event.Sequence = 9
			data, err := json.Marshal(event)
			if err != nil {
				t.Fatal(err)
			}
			if string(data) != tt.want {
				t.Fatalf("JSON = %s\nwant = %s", data, tt.want)
			}
			if strings.Contains(string(event.Payload), "full_result") || strings.Contains(string(event.Payload), "raw_error") {
				t.Fatalf("summary event leaked detail: %s", event.Payload)
			}
		})
	}
}

func TestNewAppEventRejectsMissingEnvelopeIdentity(t *testing.T) {
	if _, err := NewAppEvent("", "", "", EventAssistantDelta, time.Time{}, 0, AssistantDeltaPayload{}); err == nil {
		t.Fatal("missing workspace ID accepted")
	}
	if _, err := NewAppEvent("workspace", "", "", "", time.Time{}, 0, struct{}{}); err == nil {
		t.Fatal("missing event type accepted")
	}
}
