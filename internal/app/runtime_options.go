package app

import (
	"paw/internal/mcp"
	"paw/internal/todo"
	"paw/internal/tool"
	selecttool "paw/internal/tool/select"
	"paw/internal/ui"
)

type WorkerContext struct {
	WorkerMode      bool
	Depth           int
	MaxDepth        int
	ParentTaskID    string
	MCPBroker       mcp.Broker
	DisableMainTodo bool
	InstanceID      string
}

type ToolConfigurator func(*tool.Registry) error

type WorkspaceRuntimeOptions struct {
	Root             string
	SessionID        string
	Output           ui.UI
	AllowOutsideRead bool
	AllowIncomplete  bool
	WorkerContext    WorkerContext
	SelectionBroker  *selecttool.Broker
	TodoBroker       *todo.Broker
	ControllerLease  *ControllerLease
	ControllerMode   ControllerMode
	ResourceGovernor *ResourceGovernor
}
