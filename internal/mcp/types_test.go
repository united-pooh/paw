package mcp

import (
	"encoding/json"
	"testing"
)

func TestToolSpecCloneCopiesSchema(t *testing.T) {
	spec := ToolSpec{
		Name:        "codegraph__explore",
		InputSchema: json.RawMessage(`{"type":"object"}`),
	}
	clone := spec.Clone()
	clone.InputSchema[0] = '{'
	if string(spec.InputSchema) != `{"type":"object"}` {
		t.Fatalf("original schema changed: %s", spec.InputSchema)
	}
}

func TestSnapshotCloneCopiesTools(t *testing.T) {
	snapshot := Snapshot{
		Version: 4,
		Tools:   []ToolSpec{{Name: "codegraph__explore", InputSchema: json.RawMessage(`{"type":"object"}`)}},
	}
	clone := snapshot.Clone()
	clone.Tools[0].Name = "changed"
	clone.Tools[0].InputSchema[0] = '{'
	if snapshot.Version != 4 || snapshot.Tools[0].Name != "codegraph__explore" {
		t.Fatalf("snapshot changed: %#v", snapshot)
	}
}
