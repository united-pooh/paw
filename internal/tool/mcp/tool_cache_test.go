package mcp

import (
	"encoding/json"
	"testing"

	coremcp "paw/internal/mcp"
)

func TestToolSchemaIsStableAndReturnedAsCopy(t *testing.T) {
	spec := coremcp.ToolSpec{Name: "read", InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}}}`)}
	tool := NewTool(spec, nil)
	first := tool.InputSchema()
	if string(first) != string(spec.InputSchema) {
		t.Fatalf("schema = %s, want %s", first, spec.InputSchema)
	}
	first[0] = 'x'
	second := tool.InputSchema()
	if string(second) != string(spec.InputSchema) {
		t.Fatalf("schema was externally mutated: %s", second)
	}
}
