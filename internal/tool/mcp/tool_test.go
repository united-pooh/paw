package mcp

import (
	"context"
	"encoding/json"
	coremcp "paw/internal/mcp"
	"testing"
)

type fakeBroker struct {
	snapshot coremcp.Snapshot
	name     string
	input    json.RawMessage
}

func (b *fakeBroker) Snapshot() coremcp.Snapshot { return b.snapshot.Clone() }

func (b *fakeBroker) Call(_ context.Context, name string, input json.RawMessage) (string, error) {
	b.name = name
	b.input = append(json.RawMessage(nil), input...)
	return "ok", nil
}

func TestToolRoutesThroughBrokerAndPreservesSchema(t *testing.T) {
	broker := &fakeBroker{snapshot: coremcp.Snapshot{Tools: []coremcp.ToolSpec{{
		Name:        "codegraph__explore",
		Description: "Explore the graph",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"}}}`),
		Server:      "codegraph",
		MCPName:     "explore",
		Kind:        coremcp.KindTool,
	}}}}
	tools := NewTools(broker)
	if len(tools) != 1 {
		t.Fatalf("NewTools()=%d, want 1", len(tools))
	}
	input := json.RawMessage(`{"query":"main"}`)
	result, err := tools[0].Run(context.Background(), input)
	if err != nil || result != "ok" {
		t.Fatalf("Run()=(%q,%v)", result, err)
	}
	if broker.name != "codegraph__explore" || string(broker.input) != string(input) {
		t.Fatalf("broker call=(%q,%s)", broker.name, broker.input)
	}
	if got := string(tools[0].InputSchema()); got != string(broker.snapshot.Tools[0].InputSchema) {
		t.Fatalf("schema=%s", got)
	}
}
