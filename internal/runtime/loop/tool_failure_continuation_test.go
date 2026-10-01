package loop

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"paw/internal/capability/model"
	"paw/internal/capability/tool"
	"paw/internal/message"
	"paw/internal/storage/session"
)

type parameterRepairTool struct{ inputs []string }

func (*parameterRepairTool) Name() string        { return "Lookup" }
func (*parameterRepairTool) Description() string { return "lookup one fixture" }
func (*parameterRepairTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"key":{"type":"string"}},"required":["key"]}`)
}
func (t *parameterRepairTool) Run(_ context.Context, input json.RawMessage) (string, error) {
	var args struct {
		Key string `json:"key"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", err
	}
	t.inputs = append(t.inputs, args.Key)
	if args.Key != "existing" {
		return "", errors.New("unknown key; available key: existing")
	}
	return "fixture content", nil
}

func TestRunTurnFailedToolCanBeCorrectedWithoutInterruptingWork(t *testing.T) {
	ctx := context.Background()
	store, err := session.NewJSONLStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	client := &fakeModel{rounds: []fakeRound{
		{events: []model.StreamEvent{{Done: true, ToolCalls: []message.ToolCall{{
			ID: "lookup-failed", Name: "Lookup", Input: json.RawMessage(`{"key":"missing"}`),
		}}}}},
		{events: []model.StreamEvent{{Done: true, ToolCalls: []message.ToolCall{{
			ID: "lookup-corrected", Name: "Lookup", Input: json.RawMessage(`{"key":"existing"}`),
		}}}}},
		{events: []model.StreamEvent{{Delta: "completed using fixture content", Done: true}}},
	}}
	lookup := &parameterRepairTool{}
	registry := tool.NewRegistry()
	registry.Register(lookup)
	output := &fakeUI{}
	runner := NewEngineWithInstructionRoot(client, output, registry, store, "tool-repair", t.TempDir())
	final, err := runner.RunTurn(ctx, "lookup the fixture and complete the task")
	if err != nil {
		t.Fatalf("ordinary tool failure interrupted work: %v", err)
	}
	if final.Content != "completed using fixture content" || len(client.calls) != 3 {
		t.Fatalf("final=%q model calls=%d, want corrected tool and final answer", final.Content, len(client.calls))
	}
	if !reflect.DeepEqual(lookup.inputs, []string{"missing", "existing"}) {
		t.Fatalf("executed inputs=%v, want model-corrected arguments without automatic replay", lookup.inputs)
	}
	want := []message.ToolResult{
		{ToolUseID: "lookup-failed", Content: "unknown key; available key: existing", IsError: true},
		{ToolUseID: "lookup-corrected", Content: "fixture content"},
	}
	collect := func(messages []message.Message) []message.ToolResult {
		var results []message.ToolResult
		for _, msg := range messages {
			results = append(results, toolResultsFromMessage(msg)...)
		}
		return results
	}
	if got := collect(client.calls[1]); !reflect.DeepEqual(got, want[:1]) {
		t.Fatalf("repair request results=%#v, want structured failure", got)
	}
	if got := collect(client.calls[2]); !reflect.DeepEqual(got, want) {
		t.Fatalf("final request results=%#v, want failed and corrected attempts", got)
	}
	snapshot, err := store.LoadSnapshot(ctx, "tool-repair")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Recovery != nil {
		t.Fatalf("success left interrupted recovery state: %#v", snapshot.Recovery)
	}
	if got := collect(snapshot.ActiveHistory); !reflect.DeepEqual(got, want) {
		t.Fatalf("persisted results=%#v, want original failure retained", got)
	}
	if len(output.toolResults) != 2 || !output.toolResults[0].IsError || output.toolResults[1].IsError {
		t.Fatalf("displayed tool results=%#v, want failure still available for inspection", output.toolResults)
	}
}
