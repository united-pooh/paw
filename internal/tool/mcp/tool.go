package mcp

import (
	coremcp "codex-agent-go/internal/mcp"
	"context"
	"encoding/json"
	"errors"
)

// Tool adapts one model-facing MCP capability to GoCode's normal tool
// interface. The broker may be the main-process Manager or a subagent proxy.
type Tool struct {
	spec   coremcp.ToolSpec
	broker coremcp.Broker
}

func NewTool(spec coremcp.ToolSpec, broker coremcp.Broker) *Tool {
	return &Tool{spec: spec.Clone(), broker: broker}
}

func NewTools(broker coremcp.Broker) []*Tool {
	if broker == nil {
		return nil
	}
	return NewSnapshotTools(broker.Snapshot(), broker)
}

func NewSnapshotTools(snapshot coremcp.Snapshot, broker coremcp.Broker) []*Tool {
	if broker == nil {
		return nil
	}
	result := make([]*Tool, 0, len(snapshot.Tools))
	for _, spec := range snapshot.Tools {
		result = append(result, NewTool(spec, broker))
	}
	return result
}

func (t *Tool) Name() string {
	if t == nil {
		return ""
	}
	return t.spec.Name
}

func (t *Tool) Description() string {
	if t == nil {
		return ""
	}
	return t.spec.Description
}

func (t *Tool) InputSchema() json.RawMessage {
	if t == nil {
		return nil
	}
	return t.spec.ModelSchema()
}

func (t *Tool) Namespace() string {
	return "mcp"
}

func (t *Tool) Run(ctx context.Context, input json.RawMessage) (string, error) {
	if t == nil || t.broker == nil {
		return "", errors.New("MCP tool broker is unavailable")
	}
	return t.broker.Call(ctx, t.spec.Name, input)
}

func (t *Tool) Spec() coremcp.ToolSpec {
	if t == nil {
		return coremcp.ToolSpec{}
	}
	return t.spec.Clone()
}

var _ interface {
	Name() string
	Description() string
	Run(context.Context, json.RawMessage) (string, error)
	InputSchema() json.RawMessage
	Namespace() string
} = (*Tool)(nil)
