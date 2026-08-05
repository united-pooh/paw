package mcp

import (
	"context"
	"encoding/json"
)

type ServerConfig struct {
	Name    string
	Command string
	Args    []string
	WorkDir string
	Enabled bool
	Env     map[string]string
}

type Config struct {
	Path    string
	Servers map[string]ServerConfig
}

type CapabilityKind string

const (
	KindTool          CapabilityKind = "tool"
	KindListResources CapabilityKind = "list_resources"
	KindListTemplates CapabilityKind = "list_resource_templates"
	KindReadResource  CapabilityKind = "read_resource"
	KindListPrompts   CapabilityKind = "list_prompts"
	KindGetPrompt     CapabilityKind = "get_prompt"
)

type ToolSpec struct {
	Name        string
	Description string
	InputSchema json.RawMessage
	Server      string
	MCPName     string
	Kind        CapabilityKind
}

type Snapshot struct {
	Version int64
	Tools   []ToolSpec
}

type ServerStatus struct {
	Name, Command, WorkDir, State, LastError  string
	PID, Tools, Resources, Templates, Prompts int
	BlockedTools                              int
}

type Broker interface {
	Snapshot() Snapshot
	Call(context.Context, string, json.RawMessage) (string, error)
}

func (s ToolSpec) ModelSchema() json.RawMessage {
	if len(s.InputSchema) == 0 {
		return json.RawMessage(`{"type":"object","properties":{},"required":[],"additionalProperties":false}`)
	}
	return append(json.RawMessage(nil), s.InputSchema...)
}

func (s ToolSpec) Clone() ToolSpec {
	s.InputSchema = s.ModelSchema()
	return s
}

func (s Snapshot) Clone() Snapshot {
	cloned := Snapshot{Version: s.Version, Tools: make([]ToolSpec, len(s.Tools))}
	for i, tool := range s.Tools {
		cloned.Tools[i] = tool.Clone()
	}
	return cloned
}
