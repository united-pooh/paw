package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"paw/internal/capability/model"
	"paw/internal/capability/tool"
	"paw/internal/message"
	"paw/internal/runtime/loop"
	"paw/internal/runtime/sessionactor"
	"paw/internal/storage/session"
	"paw/internal/todo"
	"paw/internal/ui/headless"
)

type snapshotUsageModel struct {
	broker *todo.Broker
	calls  int
}

func (m *snapshotUsageModel) StreamMessage(ctx context.Context, _ []message.Message, _ []model.ToolDefinition) (<-chan model.StreamEvent, error) {
	timer := time.NewTimer(10 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-timer.C:
	}
	m.calls++
	status := todo.StatusPending
	if m.calls > 1 {
		status = todo.StatusCompleted
	}
	m.broker.Publish(todo.Snapshot{Items: []todo.Item{{ID: "work", Content: "finish work", Status: status}}})
	events := make(chan model.StreamEvent, 3)
	events <- model.StreamEvent{Usage: &model.Usage{PromptTokens: 1000 * m.calls, CompletionTokens: 100 * m.calls}}
	events <- model.StreamEvent{Delta: fmt.Sprintf("answer %d", m.calls)}
	events <- model.StreamEvent{Done: true}
	close(events)
	return events, nil
}

func TestSessionSnapshotRestoresTimedAutoContinueAfterReopen(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	root := t.TempDir()
	store, err := session.NewJSONLStore(root)
	if err != nil {
		t.Fatal(err)
	}
	const sessionID = "timed-session"
	if err := store.BeginTurn(ctx, sessionID, "legacy-turn", message.Message{Role: message.RoleUser, Content: "old question"}); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendAssistant(ctx, sessionID, "legacy-turn", message.Message{Role: message.RoleAssistant, Content: "old answer"}); err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteTurn(ctx, sessionID, "legacy-turn"); err != nil {
		t.Fatal(err)
	}
	broker := todo.NewBroker()
	defer broker.Close()
	client := &snapshotUsageModel{broker: broker}
	engine := loop.NewEngineWithInstructionRoot(client, headless.New(io.Discard), tool.NewRegistry(), store, sessionID, root)
	engine.SetTodoBroker(broker)
	host, err := sessionactor.NewHost(engine, store, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close()
	result, err := host.RunTurnWithTiming(ctx, "finish work", "first-timed-turn", time.Now())
	if err != nil || result.Message.Content != "answer 2" || result.MetadataPersistErr != nil {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	host.Close()

	before := make(map[string][]byte)
	for _, name := range []string{"transcript.jsonl", "turns.jsonl"} {
		path := filepath.Join(root, "sessions", sessionID, name)
		before[path], err = os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
	}
	reopened, err := session.NewJSONLStore(root)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := NewSessionService(reopened, NewWorkspaceCoordinator()).ConsistentSnapshot(ctx, sessionID, SnapshotRequest{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Turns) != 3 {
		t.Fatalf("turn count=%d, want legacy + two timed rounds", len(snapshot.Turns))
	}
	metadata, err := reopened.LoadTurnMetadata(ctx, sessionID)
	if err != nil || len(metadata) != 2 {
		t.Fatalf("metadata=%+v err=%v", metadata, err)
	}
	records, err := reopened.LoadResolvedJournalRecords(ctx, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range metadata {
		matched := false
		for _, record := range records {
			if item.AssistantSeq != nil && record.Seq == *item.AssistantSeq && record.TurnID == item.TurnID && record.Kind == session.JournalAssistant {
				matched = true
			}
		}
		if !matched {
			t.Errorf("metadata does not match actor journal: %+v", item)
		}
	}
	byID := make(map[string]TurnProjection)
	for _, turn := range snapshot.Turns {
		byID[turn.TurnID] = turn
	}
	if result.Metadata.TurnID == "first-timed-turn" || result.Metadata.TurnID == "" {
		t.Fatalf("continuation ID=%q", result.Metadata.TurnID)
	}
	for i, id := range []string{"first-timed-turn", result.Metadata.TurnID} {
		got := byID[id]
		if got.InputTokens != 1000*(i+1) || got.OutputTokens != 100*(i+1) || got.DurationMS <= 0 || got.StartedAt.IsZero() || got.ResponseAt == nil || got.Status != "completed" {
			t.Fatalf("restored round %d=%+v", i, got)
		}
		if got.DurationMS != got.ResponseAt.Sub(got.StartedAt).Milliseconds() || len(got.Messages) != 2 || got.Messages[1].Content != fmt.Sprintf("answer %d", i+1) {
			t.Fatalf("restored round data=%+v", got)
		}
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Turns []map[string]json.RawMessage `json:"turns"`
	}
	if err := json.Unmarshal(encoded, &wire); err != nil {
		t.Fatal(err)
	}
	for _, turn := range wire.Turns {
		var id string
		if err := json.Unmarshal(turn["turn_id"], &id); err != nil {
			t.Fatal(err)
		}
		for _, field := range []string{"input_tokens", "output_tokens", "duration_ms"} {
			_, exists := turn[field]
			if exists != (id != "legacy-turn") {
				t.Errorf("turn=%s field=%s exists=%v", id, field, exists)
			}
		}
	}
	for path, original := range before {
		after, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(after, original) {
			t.Errorf("snapshot changed %s: err=%v", path, err)
		}
	}
}
