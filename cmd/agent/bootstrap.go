package main

import (
	"context"
	"fmt"
	"os"
	configv2 "paw/internal/config"
	"paw/internal/loop"
	coremcp "paw/internal/mcp"
	"paw/internal/model"
	"paw/internal/session"
	"paw/internal/settings"
	"paw/internal/skill"
	"paw/internal/subagent"
	"paw/internal/tool"
	toolmcp "paw/internal/tool/mcp"
	uiiface "paw/internal/ui"
)

type subagentRuntimeContext struct {
	depth           int
	maxDepth        int
	parentTaskID    string
	mcpBroker       coremcp.Broker
	disableMainTodo bool
}

func configOpenOptions(paths configv2.Paths, subCtx subagentRuntimeContext) configv2.Options {
	return configv2.Options{
		Paths:                 paths,
		DisableModelDiscovery: subCtx.depth > 0,
	}
}

type runnerToolConfigurator func(*tool.Registry) error

func buildRunner(ctx context.Context, sessionIDFlag string, output uiiface.UI, allowOutsideRead bool, allowIncompleteConfig bool, configurators ...runnerToolConfigurator) (*loop.Runner, string, *model.Client, *configv2.Controller, *settings.Controller, *subagent.Manager, *session.JSONLStore, *coremcp.Manager, error) {
	return buildRunnerWithSubagentContext(ctx, sessionIDFlag, output, allowOutsideRead, allowIncompleteConfig, subagentRuntimeContext{}, configurators...)
}

func buildRunnerWithSubagentContext(ctx context.Context, sessionIDFlag string, output uiiface.UI, allowOutsideRead bool, allowIncompleteConfig bool, subCtx subagentRuntimeContext, configurators ...runnerToolConfigurator) (*loop.Runner, string, *model.Client, *configv2.Controller, *settings.Controller, *subagent.Manager, *session.JSONLStore, *coremcp.Manager, error) {
	root, err := os.Getwd()
	if err != nil {
		return nil, "", nil, nil, nil, nil, nil, nil, err
	}
	paths, err := configv2.ResolvePaths(configv2.PathOptions{WorkspaceRoot: root})
	if err != nil {
		return nil, "", nil, nil, nil, nil, nil, nil, err
	}
	configManager, err := configv2.Open(ctx, configOpenOptions(paths, subCtx))
	if err != nil {
		return nil, "", nil, nil, nil, nil, nil, nil, err
	}
	var configController *configv2.Controller
	success := false
	defer func() {
		if !success {
			if configController != nil {
				_ = configController.Close()
			} else {
				_ = configManager.Close()
			}
		}
	}()
	if !allowIncompleteConfig {
		if err := configManager.RequireReady(); err != nil {
			return nil, "", nil, nil, nil, nil, nil, nil, err
		}
	}
	cfg := configManager.Snapshot().Active

	store, err := session.NewJSONLStoreInCwd()
	if err != nil {
		return nil, "", nil, nil, nil, nil, nil, nil, fmt.Errorf("初始化 session store 失败: %w", err)
	}

	sessionID, err := resolveSessionID(ctx, store, sessionIDFlag, root)
	if err != nil {
		return nil, "", nil, nil, nil, nil, nil, nil, err
	}

	client := model.NewClient(cfg)
	configController = configv2.NewController(configManager, client)
	settingsController, err := settings.NewController(paths.Settings)
	if err != nil {
		return nil, "", nil, nil, nil, nil, nil, nil, err
	}
	var notifier subagent.Notifier
	if n, ok := output.(subagent.Notifier); ok {
		notifier = n
	}
	executable, err := os.Executable()
	if err != nil {
		return nil, "", nil, nil, nil, nil, nil, nil, fmt.Errorf("resolve executable: %w", err)
	}
	broker := subCtx.mcpBroker
	var mcpManager *coremcp.Manager
	if broker == nil {
		mcpConfig, err := coremcp.LoadConfigFile(paths.MCP, root)
		if err != nil {
			return nil, "", nil, nil, nil, nil, nil, nil, err
		}
		mcpManager, err = coremcp.Start(ctx, mcpConfig)
		if err != nil {
			return nil, "", nil, nil, nil, nil, nil, nil, err
		}
		broker = mcpManager
	}
	launcher := subagent.NewProcessLauncher(executable, root)
	launcher.SetDangerousMode(allowOutsideRead)
	launcher.SetMCPBroker(broker)
	registry := tool.NewRegistry()
	runner := loop.NewRunnerWithInstructionRoot(client, output, registry, store, sessionID, root)
	runner.SetSkillRegistry(skill.NewRegistry([]string{paths.Skills}))
	configController.SetSnapshotHandler(func(snapshot configv2.Snapshot) {
		runner.SetContextLimitTokens(model.EffectiveContextLimitTokens(snapshot.Active))
	})
	if err := runner.SetContextMaintenanceConfig(settingsController.CurrentSettings().ContextMaintenance); err != nil {
		if mcpManager != nil {
			_ = mcpManager.Close(context.Background())
		}
		return nil, "", nil, nil, nil, nil, nil, nil, fmt.Errorf("configure context maintenance: %w", err)
	}
	subagentManager := subagent.NewManager(subagent.Config{
		Model:        client,
		Store:        store,
		Root:         root,
		Settings:     settingsController,
		Notifier:     notifier,
		Context:      runner,
		Launcher:     launcher,
		Depth:        subCtx.depth,
		MaxDepth:     subCtx.maxDepth,
		ParentTaskID: subCtx.parentTaskID,
		MCPBroker:    broker,
	})
	runner.SetStreamMASubagentRunner(streamMASubagentAdapter{
		manager:         subagentManager,
		parentSessionID: sessionID,
	})
	runner.SetSubagentTokensProvider(subagentManager)
	runner.SetTurnOwnedTaskCleaner(subagentManager)
	if err := registerTools(registry, root, runner.SkillRoots(), subagentManager, sessionID, broker, allowOutsideRead); err != nil {
		if mcpManager != nil {
			_ = mcpManager.Close(context.Background())
		}
		return nil, "", nil, nil, nil, nil, nil, nil, err
	}
	runner.SetYoloModeHandler(launcher.SetDangerousMode)
	if !subCtx.disableMainTodo {
		if err := registerMainAgentTools(registry, nil); err != nil {
			if mcpManager != nil {
				_ = mcpManager.Close(context.Background())
			}
			return nil, "", nil, nil, nil, nil, nil, nil, err
		}
	}
	for _, configure := range configurators {
		if configure == nil {
			continue
		}
		if err := configure(registry); err != nil {
			if mcpManager != nil {
				_ = mcpManager.Close(context.Background())
			}
			return nil, "", nil, nil, nil, nil, nil, nil, err
		}
	}
	if mcpManager != nil {
		updates, stopUpdates := mcpManager.Subscribe()
		go func() {
			defer stopUpdates()
			for snapshot := range updates {
				adapted := toolmcp.NewSnapshotTools(snapshot, mcpManager)
				tools := make([]tool.Tool, 0, len(adapted))
				for _, item := range adapted {
					tools = append(tools, item)
				}
				_ = registry.ReplaceNamespace("mcp", tools)
			}
		}()
	}

	success = true
	return runner, sessionID, client, configController, settingsController, subagentManager, store, mcpManager, nil
}

type streamMASubagentAdapter struct {
	manager         *subagent.Manager
	parentSessionID string
}

func (a streamMASubagentAdapter) StreamSubagent(ctx context.Context, req loop.StreamMASubagentRequest) (loop.StreamMASubagentStream, error) {
	if a.manager == nil {
		return loop.StreamMASubagentStream{}, fmt.Errorf("streamma subagent manager is nil")
	}
	stream, err := a.manager.Stream(ctx, subagent.Request{
		SessionID:       req.SessionID,
		ParentSessionID: a.parentSessionID,
		Prompt:          req.Prompt,
		SystemPrompt:    req.SystemPrompt,
		Description:     req.Description,
		ContextMode:     settings.ContextMode(req.ContextMode),
		RunMode:         settings.RunModeSync,
		DisableTools:    req.DisableTools,
	})
	if err != nil {
		return loop.StreamMASubagentStream{}, err
	}
	return loop.StreamMASubagentStream{
		Events:         stream.Events,
		AgentName:      stream.AgentName,
		AgentColor:     stream.AgentColor,
		SessionID:      stream.SessionID,
		TranscriptPath: stream.TranscriptPath,
		OutputPath:     stream.OutputPath,
	}, nil
}
