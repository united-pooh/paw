package main

import (
	"context"
	"fmt"
	coremcp "paw/internal/mcp"
	"paw/internal/session"
	"paw/internal/subagent"
	"paw/internal/todo"
	"paw/internal/tool"
	toolexec "paw/internal/tool/exec"
	toolfile "paw/internal/tool/file"
	toolmcp "paw/internal/tool/mcp"
	selecttool "paw/internal/tool/select"
	toolwebfetch "paw/internal/tool/webfetch"
)

func registerMainAgentTools(registry *tool.Registry, broker *todo.Broker) error {
	if registry == nil {
		return fmt.Errorf("tool registry is nil")
	}
	registry.Register(todo.NewTool(broker))
	return nil
}

func registerInteractiveTools(registry *tool.Registry, broker *selecttool.Broker) error {
	if registry == nil {
		return fmt.Errorf("tool registry is nil")
	}
	if broker == nil {
		return fmt.Errorf("selection broker is nil")
	}
	registry.Register(selecttool.New(broker))
	return nil
}

func registerTools(registry *tool.Registry, root string, readRoots []string, subagentManager *subagent.Manager, sessionID string, broker coremcp.Broker, allowOutsideRead bool) error {
	if registry == nil {
		return fmt.Errorf("tool registry is nil")
	}
	readState := toolfile.NewReadStateStore()
	registry.Register(&toolfile.LSTool{Root: root, ReadRoots: readRoots})
	registry.Register(&toolfile.ReadTool{Root: root, ReadRoots: readRoots, ReadState: readState, AllowOutsideRoot: allowOutsideRead})
	registry.Register(&toolfile.WriteTool{Root: root, ReadState: readState})
	registry.Register(&toolfile.EditTool{Root: root, ReadState: readState})
	registry.Register(&toolfile.GrepTool{Root: root, ReadRoots: readRoots})
	registry.Register(&toolfile.GlobTool{Root: root, ReadRoots: readRoots})
	registry.Register(&toolexec.BashTool{Root: root})
	registry.Register(&toolwebfetch.Tool{})
	registry.Register(subagent.NewTool(subagentManager, sessionID))
	registry.Register(subagent.NewStatusTool(subagentManager))
	registry.Register(subagent.NewStopTool(subagentManager))
	registry.Register(subagent.NewWaitTool(subagentManager))
	if broker != nil {
		adapted := toolmcp.NewTools(broker)
		tools := make([]tool.Tool, 0, len(adapted))
		for _, item := range adapted {
			tools = append(tools, item)
		}
		if err := registry.ReplaceNamespace("mcp", tools); err != nil {
			return fmt.Errorf("register MCP tools: %w", err)
		}
	}
	return nil
}

func resolveSessionID(ctx context.Context, store *session.JSONLStore, sessionIDFlag, cwd string) (string, error) {
	if sessionIDFlag != "" {
		exists, err := store.Exists(ctx, sessionIDFlag)
		if err != nil {
			return "", fmt.Errorf("查询 session 失败: %w", err)
		}
		if !exists {
			return "", fmt.Errorf("session 不存在: %s", sessionIDFlag)
		}
		return sessionIDFlag, nil
	}

	sessionID, err := session.GenerateSessionID()
	if err != nil {
		return "", fmt.Errorf("生成 session ID 失败: %w", err)
	}
	return sessionID, nil
}
