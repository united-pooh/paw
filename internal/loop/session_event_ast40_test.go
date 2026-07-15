package loop

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"
)

func TestAgentInfoAST40OptionalFieldsRoundTrip(t *testing.T) {
	legacy, err := json.Marshal(AgentInfo{ID: "persona-1", Name: "Agent", Color: "#fff", Status: "idle"})
	if err != nil {
		t.Fatalf("Marshal legacy AgentInfo: %v", err)
	}
	for _, field := range [][]byte{[]byte(`"task_id"`), []byte(`"finished_at"`), []byte(`"conversation_available"`)} {
		if bytes.Contains(legacy, field) {
			t.Fatalf("legacy AgentInfo unexpectedly contains %s: %s", field, legacy)
		}
	}

	started := time.Date(2026, 7, 15, 9, 59, 24, 0, time.UTC)
	finished := started.Add(2*time.Minute + 15*time.Second)
	want := AgentInfo{
		ID:                    "persona-1",
		Name:                  "Agent",
		Color:                 "#fff",
		Status:                "done",
		TaskID:                "task-123",
		StartedAt:             &started,
		FinishedAt:            &finished,
		ConversationAvailable: true,
	}
	raw, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("Marshal AgentInfo: %v", err)
	}
	var got AgentInfo
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("Unmarshal AgentInfo: %v", err)
	}
	if got.TaskID != want.TaskID || got.StartedAt == nil || got.FinishedAt == nil || !got.ConversationAvailable {
		t.Fatalf("round-trip AgentInfo = %#v", got)
	}
	if got.FinishedAt.Sub(*got.StartedAt) != 135*time.Second {
		t.Fatalf("duration = %s, want 2m15s", got.FinishedAt.Sub(*got.StartedAt))
	}
}
