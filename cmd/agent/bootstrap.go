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
	"paw/internal/task"
	"paw/internal/tool"
	toolexec "paw/internal/tool/exec"
	toolfile "paw/internal/tool/file"
	toolmcp "paw/internal/tool/mcp"
	uiiface "paw/internal/ui"
	"time"
)

type taskRuntimeContext struct {
	workerMode      bool
	depth           int
	maxDepth        int
	parentTaskID    string
	mcpBroker       coremcp.Broker
	disableMainTodo bool
}

func configOpenOptions(paths configv2.Paths, subCtx taskRuntimeContext) configv2.Options {
	return configv2.Options{
		Paths:                 paths,
		DisableModelDiscovery: subCtx.workerMode,
	}
}

func effectiveYoloMode(commandLineEnabled bool, document configv2.Document) bool {
	return commandLineEnabled || document.Yolo
}

type runnerToolConfigurator func(*tool.Registry) error

func buildRunner(ctx context.Context, sessionIDFlag string, output uiiface.UI, allowOutsideRead bool, allowIncompleteConfig bool, configurators ...runnerToolConfigurator) (*loop.Runner, string, *model.Client, *configv2.Controller, *settings.Controller, *task.Manager, *session.JSONLStore, *coremcp.Manager, error) {
	return buildRunnerWithTaskContext(ctx, sessionIDFlag, output, allowOutsideRead, allowIncompleteConfig, taskRuntimeContext{}, configurators...)
}

func buildRunnerWithTaskContext(ctx context.Context, sessionIDFlag string, output uiiface.UI, allowOutsideRead bool, allowIncompleteConfig bool, subCtx taskRuntimeContext, configurators ...runnerToolConfigurator) (*loop.Runner, string, *model.Client, *configv2.Controller, *settings.Controller, *task.Manager, *session.JSONLStore, *coremcp.Manager, error) {
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
	configSnapshot := configManager.Snapshot()
	cfg := configSnapshot.Active
	yoloMode := effectiveYoloMode(allowOutsideRead, configSnapshot.Document)

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
	var notifier task.Notifier
	if n, ok := output.(task.Notifier); ok {
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
	launcher := task.NewProcessPoolLauncher(executable, root)
	launcher.SetDangerousMode(yoloMode)
	launcher.SetMCPBroker(broker)
	// 沙箱生效值接线：全局安全基线（硬 cap）→ 池容量/墙钟；worker 进程经
	// --sandbox-limits 接收资源限制。工作区只能在池容量上覆盖且受全局 cap 约束。
	sandbox := configManager.Snapshot().Sandbox()
	launcher.MaxWorkers = sandbox.MaxWorkers
	launcher.QueueCapacity = sandbox.QueueCapacity
	launcher.JobWallTime = time.Duration(sandbox.JobWallSeconds) * time.Second
	launcher.Args = append(launcher.Args, "--sandbox-limits="+sandboxLimitsFlag(sandbox))
	registry := tool.NewRegistry()
	runner := loop.NewRunnerWithInstructionRoot(client, output, registry, store, sessionID, root)
	runner.SetSkillRegistry(skill.NewRegistry([]string{paths.Skills}))
	if err := runner.SetContextMaintenanceConfig(settingsController.CurrentSettings().ContextMaintenance); err != nil {
		if mcpManager != nil {
			_ = mcpManager.Close(context.Background())
		}
		return nil, "", nil, nil, nil, nil, nil, nil, fmt.Errorf("configure context maintenance: %w", err)
	}
	taskManager := task.NewManager(task.Config{
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
	runner.SetStreamMATaskRunner(streamMATaskAdapter{
		manager:         taskManager,
		parentSessionID: sessionID,
	})
	runner.SetTaskTokensProvider(taskManager)
	runner.SetTurnOwnedTaskCleaner(taskManager)
	if err := registerTools(registry, root, runner.SkillRoots(), taskManager, sessionID, broker, yoloMode); err != nil {
		if mcpManager != nil {
			_ = mcpManager.Close(context.Background())
		}
		return nil, "", nil, nil, nil, nil, nil, nil, err
	}
	// worker 进程以沙箱模式覆盖文件/Bash 工具（主 agent 非 workerMode 不变）：
	//  - Write/Edit：拒绝写入 root/.paw（仅内部会话存储写入），沿用共享 read-state；
	//  - Bash：追加拦截提权/权限/块设备写等不该由 task worker 执行的高危命令。
	if subCtx.workerMode {
		readState := toolfile.NewReadStateStore()
		registry.Register(&toolfile.ReadTool{Root: root, ReadRoots: runner.SkillRoots(), ReadState: readState, AllowOutsideRoot: yoloMode})
		registry.Register(&toolfile.WriteTool{Root: root, ReadState: readState, ForbidDotPaw: true})
		registry.Register(&toolfile.EditTool{Root: root, ReadState: readState, ForbidDotPaw: true})
		registry.Register(&toolexec.BashTool{Root: root, Sandboxed: true})
	}
	runner.SetYoloModeHandler(launcher.SetDangerousMode)
	lastConfiguredYolo := yoloMode
	configController.SetSnapshotHandler(func(snapshot configv2.Snapshot) {
		runner.SetContextLimitTokens(model.EffectiveContextLimitTokens(snapshot.Active))
		desiredYolo := effectiveYoloMode(allowOutsideRead, snapshot.Document)
		if desiredYolo != lastConfiguredYolo {
			_, _ = runner.SetYoloMode(desiredYolo)
			lastConfiguredYolo = desiredYolo
		}
	})
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
	return runner, sessionID, client, configController, settingsController, taskManager, store, mcpManager, nil
}

// sandboxLimitsFlag 把生效沙箱资源限制编码为 --sandbox-limits CSV，由 worker
// 进程在入口处解析并应用 rlimit（缺失字段回落默认值）。
func sandboxLimitsFlag(effective configv2.EffectiveSandbox) string {
	return fmt.Sprintf("cpu=%d,file_mb=%d,proc=%d,nofile=%d,wall=%d",
		effective.CPUSeconds, effective.FileSizeMiB, effective.MaxProcesses,
		effective.OpenFiles, effective.JobWallSeconds)
}

type streamMATaskAdapter struct {
	manager         *task.Manager
	parentSessionID string
}

func (a streamMATaskAdapter) StreamTask(ctx context.Context, req loop.StreamMATaskRequest) (loop.StreamMATaskStream, error) {
	if a.manager == nil {
		return loop.StreamMATaskStream{}, fmt.Errorf("streamma task manager is nil")
	}
	stream, err := a.manager.Stream(ctx, task.Request{
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
		return loop.StreamMATaskStream{}, err
	}
	return loop.StreamMATaskStream{
		Events:         stream.Events,
		AgentName:      stream.AgentName,
		AgentColor:     stream.AgentColor,
		SessionID:      stream.SessionID,
		TranscriptPath: stream.TranscriptPath,
		OutputPath:     stream.OutputPath,
	}, nil
}
