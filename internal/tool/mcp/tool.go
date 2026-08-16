package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	coremcp "paw/internal/mcp"
)

// Tool adapts one model-facing MCP capability to Paw's normal tool
// interface. The broker may be the main-process Manager or a task proxy.
type Tool struct {
	spec        coremcp.ToolSpec
	modelSchema json.RawMessage
	broker      coremcp.Broker
}

func NewTool(spec coremcp.ToolSpec, broker coremcp.Broker) *Tool {
	cloned := spec.Clone()
	return &Tool{
		spec:        cloned,
		modelSchema: append(json.RawMessage(nil), cloned.ModelSchema()...),
		broker:      broker,
	}
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
	if desc := strings.TrimSpace(t.spec.Description); desc != "" {
		return desc
	}
	server := strings.TrimSpace(t.spec.Server)
	if server == "" {
		server = t.spec.Name
	}
	return "领域工具，来自 MCP server " + server + "；需要时使用它而不是用脚本自行实现"
}

func (t *Tool) InputSchema() json.RawMessage {
	if t == nil {
		return nil
	}
	return append(json.RawMessage(nil), t.modelSchema...)
}

func (t *Tool) Namespace() string {
	return "mcp"
}

// ReadOnly is deliberately false: MCP discovery currently has no reliable
// side-effect metadata, so context maintenance uses the balanced fallback.
func (*Tool) ReadOnly() bool { return false }

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
