package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	coremcp "paw/internal/mcp"
	"paw/internal/session"
	"paw/internal/task"
	"paw/internal/todo"
	"paw/internal/tool"
	toolexec "paw/internal/tool/exec"
	toolfile "paw/internal/tool/file"
	toolmcp "paw/internal/tool/mcp"
	"paw/internal/tool/memory"
	selecttool "paw/internal/tool/select"
	transcripttool "paw/internal/tool/transcript"
	toolwebfetch "paw/internal/tool/webfetch"
	"strings"
)

// mainTodoTool 是主 agent 注册的 update_todo 工具实例。store 在 buildRunner
// 返回后才可用，故事件接线延迟到 wireTodoEvents。
var mainTodoTool *todo.Tool

// mainSearchTranscriptTool 是主 agent 注册的 search_transcript 工具实例。
// store/sessionID 在 buildRunner 返回后才可用，延迟到 wireSearchTranscript。
var mainSearchTranscriptTool *transcripttool.Tool

// mainMemoryTool / mainAriadneTool 是状态文件工具。store/sessionID 在
// buildRunner 返回后才可用，延迟到 wireStateTools 绑定路径与事件。
var mainMemoryTool *memory.UpdateMemoryTool
var mainAriadneTool *memory.UpdateAriadneTool

func registerMainAgentTools(registry *tool.Registry, broker *todo.Broker) error {
	if registry == nil {
		return fmt.Errorf("tool registry is nil")
	}
	mainTodoTool = todo.NewTool(broker)
	registry.Register(mainTodoTool)
	mainSearchTranscriptTool = transcripttool.New(nil, "")
	registry.Register(mainSearchTranscriptTool)
	mainMemoryTool = memory.NewUpdateMemory("", nil)
	registry.Register(mainMemoryTool)
	mainAriadneTool = memory.NewUpdateAriadne("", nil)
	registry.Register(mainAriadneTool)
	return nil
}

// wireTodoEvents 在 session store 与 sessionID 就绪后，把 todo 快照更新
// 接线为 session.todo_upserted 事件（best-effort：事件失败不影响工具结果），
// 并把已完成条目沉淀到 memory/progress.md（跨会话档案，幂等去重）。
func wireTodoEvents(store *session.JSONLStore, sessionID, progressPath string) {
	if mainTodoTool == nil || store == nil {
		return
	}
	var archive *todo.ArchiveWriter
	if strings.TrimSpace(progressPath) != "" {
		archive, _ = todo.NewArchiveWriter(progressPath)
	}
	mainTodoTool.OnUpsert = func(ctx context.Context, snapshot todo.Snapshot) error {
		if archive != nil {
			_, _ = archive.ArchiveCompleted(ctx, snapshot)
		}
		_, err := store.AppendTodoSnapshot(ctx, sessionID, snapshot)
		return err
	}
}

// wireSearchTranscript 在 session store 与 sessionID 就绪后，把检索工具
// 绑定到当前会话（best-effort）。
func wireSearchTranscript(store *session.JSONLStore, sessionID string) {
	if mainSearchTranscriptTool == nil || store == nil {
		return
	}
	mainSearchTranscriptTool.Bind(store, sessionID)
}

// wireStateTools 绑定状态文件工具：memory.md 全局路径、ariadne.md 会话
// 路径、审计事件（session.memory_updated / ariadne_updated）。
func wireStateTools(store *session.JSONLStore, sessionID string) {
	if store == nil {
		return
	}
	home, _ := os.UserHomeDir()
	memoryPath := filepath.Join(home, ".paw", "memory.md")
	ariadnePath := filepath.Join(store.Dir(), "sessions", sessionID, "ariadne.md")
	record := func(ctx context.Context, kind session.StateEventKind, summary string) error {
		_, err := store.AppendStateEvent(ctx, sessionID, kind, summary)
		return err
	}
	if mainMemoryTool != nil {
		mainMemoryTool.Bind(memoryPath, record)
	}
	if mainAriadneTool != nil {
		mainAriadneTool.Bind(ariadnePath, record)
	}
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

func registerTools(registry *tool.Registry, root string, readRoots []string, taskManager *task.Manager, sessionID string, broker coremcp.Broker, allowOutsideRead bool) error {
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
	registry.Register(task.NewTool(taskManager, sessionID))
	registry.Register(task.NewStatusTool(taskManager))
	registry.Register(task.NewStopTool(taskManager))
	registry.Register(task.NewWaitTool(taskManager))
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
