package loop_test

import (
	"encoding/json"
	"testing"
	"time"

	"codex-agent-go/internal/loop"
)

func TestSessionUserInputPayload_TargetAgentID_roundtrip(t *testing.T) {
	p := loop.SessionUserInputPayload{Text: "hello", TargetAgentID: "abc-uuid"}
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var got loop.SessionUserInputPayload
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got.TargetAgentID != "abc-uuid" {
		t.Errorf("TargetAgentID: got %q want %q", got.TargetAgentID, "abc-uuid")
	}
}

func TestSessionUserInputPayload_TargetAgentID_omitempty(t *testing.T) {
	p := loop.SessionUserInputPayload{Text: "hello"}
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	if _, ok := m["target_agent_id"]; ok {
		t.Error("target_agent_id must be omitted when empty")
	}
}

func TestEventKindSubagentsSnapshot_roundtrip(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	ev := loop.SessionEvent{
		Kind: loop.EventKindSubagentsSnapshot,
		SubagentsSnapshot: &loop.SessionSubagentsSnapshotPayload{
			Agents: []loop.AgentInfo{
				{ID: "id1", Name: "Alice", Color: "#FF0000", Status: "idle"},
				{ID: "id2", Name: "Bob", Color: "#00FF00", Status: "running", StartedAt: &now},
			},
		},
	}
	data, err := json.Marshal(ev)
	if err != nil {
		t.Fatal(err)
	}
	var got loop.SessionEvent
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got.Kind != loop.EventKindSubagentsSnapshot {
		t.Errorf("kind: got %q want %q", got.Kind, loop.EventKindSubagentsSnapshot)
	}
	if got.SubagentsSnapshot == nil {
		t.Fatal("SubagentsSnapshot is nil")
	}
	if len(got.SubagentsSnapshot.Agents) != 2 {
		t.Fatalf("agents len: got %d want 2", len(got.SubagentsSnapshot.Agents))
	}
	if got.SubagentsSnapshot.Agents[1].StartedAt == nil {
		t.Error("StartedAt should survive round-trip")
	}
}

func TestEventKindSystemMessage_roundtrip(t *testing.T) {
	ev := loop.SessionEvent{
		Kind: loop.EventKindSystemMessage,
		SystemMessage: &loop.SessionSystemMessagePayload{
			Title:     "Agent",
			Body:      "final answer",
			Color:     "#FFAA00",
			TaskID:    "task-1",
			AgentID:   "agent-session",
			AgentName: "Agent",
			Status:    "completed",
		},
	}
	data, err := json.Marshal(ev)
	if err != nil {
		t.Fatal(err)
	}
	var got loop.SessionEvent
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got.Kind != loop.EventKindSystemMessage {
		t.Errorf("kind: got %q want %q", got.Kind, loop.EventKindSystemMessage)
	}
	if got.SystemMessage == nil {
		t.Fatal("SystemMessage is nil")
	}
	if got.SystemMessage.Body != "final answer" || got.SystemMessage.TaskID != "task-1" || got.SystemMessage.Status != "completed" {
		t.Fatalf("SystemMessage = %#v", got.SystemMessage)
	}
}

func TestAgentInfo_StartedAt_omitempty(t *testing.T) {
	ai := loop.AgentInfo{ID: "x", Name: "X", Color: "#000", Status: "idle"}
	data, _ := json.Marshal(ai)
	var m map[string]interface{}
	_ = json.Unmarshal(data, &m)
	if _, ok := m["started_at"]; ok {
		t.Error("started_at must be omitted when nil")
	}
}
