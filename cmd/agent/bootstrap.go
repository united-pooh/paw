package main

import (
	"context"
	"fmt"
	"os"

	appcore "paw/internal/app"
	configv2 "paw/internal/config"
	coremcp "paw/internal/mcp"
	"paw/internal/session"
	"paw/internal/task"
	"paw/internal/todo"
	"paw/internal/tool"
	selecttool "paw/internal/tool/select"
	uiiface "paw/internal/ui"
)

type taskRuntimeContext struct {
	workerMode      bool
	depth           int
	maxDepth        int
	parentTaskID    string
	mcpBroker       coremcp.Broker
	disableMainTodo bool
	instanceID      string
}

type appContext = appcore.WorkspaceRuntime

type runnerToolConfigurator = appcore.ToolConfigurator

func (c taskRuntimeContext) appWorkerContext() appcore.WorkerContext {
	return appcore.WorkerContext{
		WorkerMode:      c.workerMode,
		Depth:           c.depth,
		MaxDepth:        c.maxDepth,
		ParentTaskID:    c.parentTaskID,
		MCPBroker:       c.mcpBroker,
		DisableMainTodo: c.disableMainTodo,
		InstanceID:      c.instanceID,
	}
}

func configOpenOptions(paths configv2.Paths, subCtx taskRuntimeContext) configv2.Options {
	return configv2.Options{Paths: paths, DisableWatch: subCtx.workerMode, DisableModelDiscovery: subCtx.workerMode}
}

func effectiveYoloMode(commandLineEnabled bool, document configv2.Document) bool {
	return commandLineEnabled || document.Yolo
}

func buildRunner(ctx context.Context, sessionIDFlag string, output uiiface.UI, allowOutsideRead bool, allowIncompleteConfig bool, configurators ...runnerToolConfigurator) (*appContext, error) {
	root, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	return appcore.BuildWorkspaceRuntime(ctx, appcore.WorkspaceRuntimeOptions{
		Root:             root,
		SessionID:        sessionIDFlag,
		Output:           output,
		AllowOutsideRead: allowOutsideRead,
		AllowIncomplete:  allowIncompleteConfig,
		ControllerMode:   appcore.ControllerModeTUI,
	}, configurators...)
}

func buildRunnerWithBrokers(ctx context.Context, sessionIDFlag string, output uiiface.UI, allowOutsideRead bool, allowIncompleteConfig bool, todoBroker *todo.Broker, selectionBroker *selecttool.Broker, configurators ...runnerToolConfigurator) (*appContext, error) {
	root, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	return appcore.BuildWorkspaceRuntime(ctx, appcore.WorkspaceRuntimeOptions{
		Root:             root,
		SessionID:        sessionIDFlag,
		Output:           output,
		AllowOutsideRead: allowOutsideRead,
		AllowIncomplete:  allowIncompleteConfig,
		TodoBroker:       todoBroker,
		SelectionBroker:  selectionBroker,
		ControllerMode:   appcore.ControllerModeTUI,
	}, configurators...)
}

func buildRunnerWithTaskContext(ctx context.Context, sessionIDFlag string, output uiiface.UI, allowOutsideRead bool, allowIncompleteConfig bool, subCtx taskRuntimeContext, configurators ...runnerToolConfigurator) (*appContext, error) {
	root, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	return appcore.BuildWorkspaceRuntime(ctx, appcore.WorkspaceRuntimeOptions{
		Root:             root,
		SessionID:        sessionIDFlag,
		Output:           output,
		AllowOutsideRead: allowOutsideRead,
		AllowIncomplete:  allowIncompleteConfig,
		WorkerContext:    subCtx.appWorkerContext(),
	}, configurators...)
}

func resolveSessionID(ctx context.Context, store *session.JSONLStore, sessionIDFlag, _ string) (string, error) {
	return appcore.ResolveRuntimeSessionID(ctx, store, sessionIDFlag)
}

func sandboxLimitsFlag(effective configv2.EffectiveSandbox) string {
	return appcore.SandboxLimitsFlag(effective)
}

func registerTools(registry *tool.Registry, root string, readRoots []string, taskManager *task.Manager, sessionID string, broker coremcp.Broker, allowOutsideRead bool, workerMode bool) error {
	if err := appcore.RegisterBuiltinTools(registry, root, readRoots, taskManager, sessionID, broker, allowOutsideRead, workerMode); err != nil {
		return fmt.Errorf("register tools: %w", err)
	}
	return nil
}
