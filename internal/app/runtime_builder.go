package app

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	coremcp "paw/internal/capability/mcp"
	"paw/internal/capability/model"
	"paw/internal/capability/skill"
	"paw/internal/capability/tool"
	toolexec "paw/internal/capability/tool/exec"
	toolfile "paw/internal/capability/tool/file"
	toolmcp "paw/internal/capability/tool/mcp"
	toolwebfetch "paw/internal/capability/tool/webfetch"
	configv2 "paw/internal/platform/config"
	"paw/internal/platform/settings"
	"paw/internal/runtime/loop"
	"paw/internal/runtime/sessionactor"
	"paw/internal/runtime/task"
	"paw/internal/storage/session"
	"paw/internal/tokentracer"
)

func BuildWorkspaceRuntime(ctx context.Context, opts WorkspaceRuntimeOptions, configurators ...ToolConfigurator) (_ *WorkspaceRuntime, err error) {
	if ctx == nil {
		ctx = context.Background()
	}
	root, err := explicitWorkspaceRoot(opts.Root)
	if err != nil {
		return nil, err
	}
	workspace, err := CanonicalWorkspace(root)
	if err != nil {
		return nil, err
	}
	eventHub, err := NewEventHub(EventHubConfig{WorkspaceID: workspace.ID})
	if err != nil {
		return nil, err
	}
	coordinator := NewWorkspaceCoordinator()
	uiAdapter := NewUIAdapter(workspace.ID, coordinator, eventHub)
	interactions := NewInteractionHub(workspace.ID, coordinator, eventHub)
	traceDetail := NewTraceDetailStore(eventHub)
	output := opts.Output
	if output == nil {
		output = uiAdapter
	}
	runtime := &WorkspaceRuntime{
		Root: root, ControllerLease: opts.ControllerLease,
		Coordinator: coordinator, UIAdapter: uiAdapter, Interactions: interactions, TraceDetail: traceDetail, EventHub: eventHub,
	}
	runtime.initializeCloseStages()
	success := false
	defer func() {
		if !success {
			err = errorsJoin(err, runtime.Close())
		}
	}()

	if runtime.ControllerLease == nil && opts.ControllerMode != "" {
		store, err := session.NewJSONLStoreForWorkspace(root)
		if err != nil {
			return nil, fmt.Errorf("initialize session store for lease: %w", err)
		}
		lease, err := AcquireControllerLease(store.Root(), opts.ControllerMode)
		if err != nil {
			return nil, err
		}
		runtime.ControllerLease = lease
	}

	paths, err := configv2.ResolvePaths(configv2.PathOptions{WorkspaceRoot: root})
	if err != nil {
		return nil, err
	}
	configManager, err := configv2.Open(ctx, configOpenOptions(paths, opts.WorkerContext))
	if err != nil {
		return nil, err
	}
	runtime.configManager = configManager
	if !opts.AllowIncomplete {
		if !configManager.Snapshot().Ready {
			if err := configManager.WaitForModelDiscovery(ctx); err != nil {
				return nil, err
			}
		}
		if err := configManager.RequireReady(); err != nil {
			return nil, err
		}
	}
	configSnapshot := configManager.Snapshot()
	yoloMode := effectiveYoloMode(opts.AllowOutsideRead, configSnapshot.Document)

	store, err := session.NewJSONLStoreForWorkspace(root)
	if err != nil {
		return nil, fmt.Errorf("初始化 session store 失败: %w", err)
	}
	runtime.Store = store
	if err := restoreQueuedInputs(ctx, store, runtime.Coordinator, workspace.ID, eventHub); err != nil {
		return nil, fmt.Errorf("恢复排队输入失败: %w", err)
	}
	runtime.SessionService = NewSessionService(store, runtime.Coordinator)
	sessionID, err := ResolveRuntimeSessionID(ctx, store, opts.SessionID)
	if err != nil {
		return nil, err
	}
	runtime.SessionID = sessionID

	client := model.NewClient(configSnapshot.Active)
	runtime.Model = client
	recorder, telemetryErr := tokentracer.NewRecorder(tokentracer.RecorderConfig{Home: paths.Home, Workspace: root, ParentInstanceID: os.Getenv("PAW_TRACER_PARENT_INSTANCE_ID")})
	if telemetryErr != nil {
		log.Printf("warning: Token Tracer collection unavailable: %v", telemetryErr)
	} else {
		runtime.Telemetry = recorder
		client.SetRequestObserver(func(event model.RequestEvent) { _ = recorder.Record(event) })
	}
	runtime.ConfigController = configv2.NewController(configManager, client)
	settingsController, err := settings.NewController(paths.Settings)
	if err != nil {
		return nil, err
	}
	runtime.SettingsController = settingsController

	var notifier task.Notifier
	if value, ok := output.(task.Notifier); ok {
		notifier = value
	}
	executable, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("resolve executable: %w", err)
	}
	broker := opts.WorkerContext.MCPBroker
	if broker == nil {
		mcpConfig, err := coremcp.LoadConfigFile(paths.MCP, root)
		if err != nil {
			return nil, err
		}
		runtime.MCPManager, err = coremcp.Start(ctx, mcpConfig)
		if err != nil {
			return nil, err
		}
		broker = runtime.MCPManager
	}

	launcher := task.NewProcessPoolLauncher(executable, root)
	launcher.Env = append(launcher.Env, "PAW_CONFIG_HOME="+paths.Home)
	if runtime.Telemetry != nil {
		launcher.Env = append(launcher.Env, "PAW_TRACER_PARENT_INSTANCE_ID="+runtime.Telemetry.InstanceID())
	}
	runtime.taskLauncher = launcher
	launcher.SetDangerousMode(yoloMode)
	launcher.SetMCPBroker(broker)
	launcher.WorkerGovernor = opts.ResourceGovernor
	if instanceID := strings.TrimSpace(opts.WorkerContext.InstanceID); instanceID != "" {
		launcher.Env = append(launcher.Env, "PAW_CONTROLLER_INSTANCE_ID="+instanceID)
	}
	sandbox := configManager.Snapshot().Sandbox()
	launcher.MaxWorkers = sandbox.MaxWorkers
	launcher.QueueCapacity = sandbox.QueueCapacity
	launcher.JobWallTime = time.Duration(sandbox.JobWallSeconds) * time.Second
	launcher.Args = append(launcher.Args, "--sandbox-limits="+SandboxLimitsFlag(sandbox))

	registry := tool.NewRegistry()
	engine := loop.NewEngineWithInstructionRoot(client, output, registry, store, sessionID, root)
	engine.BindContextLimitSource(func() int {
		return model.ResolveContextLimitTokens(client.CurrentModelConfig(), settingsController.CurrentSettings().UI.ContextLimitTokens)
	})
	sessionHost, err := sessionactor.NewHost(engine, store, sessionID)
	if err != nil {
		return nil, err
	}
	runtime.SessionHost = sessionHost
	runtime.TurnService = NewTurnService(sessionHost, store, coordinator, eventHub, uiAdapter)
	sessionHost.SetSkillRegistry(skill.NewRegistry([]string{paths.Skills}))
	if err := sessionHost.SetContextMaintenanceConfig(settingsController.CurrentSettings().ContextMaintenance); err != nil {
		return nil, fmt.Errorf("configure context maintenance: %w", err)
	}
	taskManager := task.NewManager(task.Config{
		Model:                              client,
		Store:                              store,
		Root:                               root,
		Settings:                           settingsController,
		Notifier:                           notifier,
		Context:                            sessionHost,
		Launcher:                           launcher,
		Depth:                              opts.WorkerContext.Depth,
		MaxDepth:                           opts.WorkerContext.MaxDepth,
		ParentTaskID:                       opts.WorkerContext.ParentTaskID,
		MCPBroker:                          broker,
		DisableStartupOrphanReconciliation: opts.WorkerContext.WorkerMode,
	})
	runtime.TaskManager = taskManager
	sessionHost.SetStreamMATaskRunner(streamMATaskAdapter{manager: taskManager, parentSessionID: sessionHost.CurrentSessionID})
	sessionHost.SetTaskTokensProvider(taskManager)
	sessionHost.SetTurnOwnedTaskCleaner(taskManager)
	readRoots := append(sessionHost.SkillRoots(), store.Root())
	if err := RegisterBuiltinTools(registry, root, readRoots, taskManager, sessionID, broker, yoloMode, opts.WorkerContext.WorkerMode); err != nil {
		return nil, err
	}
	if opts.WorkerContext.WorkerMode {
		readState := toolfile.NewReadStateStore()
		registry.Register(&toolfile.ReadTool{Root: root, ReadRoots: readRoots, ReadState: readState, AllowOutsideRoot: yoloMode})
		registry.Register(&toolfile.WriteTool{Root: root, ReadState: readState, ForbidDotPaw: true})
		registry.Register(&toolfile.EditTool{Root: root, ReadState: readState, ForbidDotPaw: true})
		registry.Register(&toolexec.BashTool{Root: root, Sandboxed: true})
	}

	runtime.Toolset = NewToolset(opts.TodoBroker)
	if !opts.WorkerContext.DisableMainTodo {
		if err := runtime.Toolset.RegisterMain(registry); err != nil {
			return nil, err
		}
	}
	if opts.SelectionBroker != nil {
		if err := runtime.Toolset.RegisterInteractive(registry, opts.SelectionBroker); err != nil {
			return nil, err
		}
	}
	for _, configure := range configurators {
		if configure != nil {
			if err := configure(registry); err != nil {
				return nil, err
			}
		}
	}

	sessionHost.SetYoloModeHandler(launcher.SetDangerousMode)
	var configuredYoloMu sync.Mutex
	lastConfiguredYolo := yoloMode
	runtime.ConfigController.SetSnapshotHandler(func(_ configv2.Snapshot) {
		configuredYoloMu.Lock()
		defer configuredYoloMu.Unlock()
		desiredYolo := effectiveYoloMode(opts.AllowOutsideRead, runtime.ConfigController.Snapshot().Document)
		if desiredYolo != lastConfiguredYolo {
			_, _ = sessionHost.SetYoloMode(desiredYolo)
			lastConfiguredYolo = desiredYolo
		}
	})
	if runtime.MCPManager != nil {
		updates, stopUpdates := runtime.MCPManager.Subscribe()
		go func() {
			defer stopUpdates()
			for snapshot := range updates {
				adapted := toolmcp.NewSnapshotTools(snapshot, runtime.MCPManager)
				tools := make([]tool.Tool, 0, len(adapted))
				for _, item := range adapted {
					tools = append(tools, item)
				}
				_ = registry.ReplaceNamespace("mcp", tools)
			}
		}()
	}

	success = true
	return runtime, nil
}

// restoreUnfinishedTurns 把重启前没有终态的 turn 投影为 interrupted：
// 进程重启后无法恢复的流式上下文必须显式标记，而不是伪装成仍在运行。
func restoreUnfinishedTurns(sessionID string, records []session.Record, coordinator *WorkspaceCoordinator, events *EventHub, workspaceID WorkspaceID) {
	state := map[string]bool{}
	order := []string{}
	for _, record := range records {
		switch record.Kind {
		case session.JournalTurnStarted:
			state[record.TurnID] = true
			order = append(order, record.TurnID)
		case session.JournalTurnCompleted, session.JournalTurnFailed, session.JournalTurnStopped:
			state[record.TurnID] = false
		}
	}
	for _, turnID := range order {
		if !state[turnID] {
			continue
		}
		coordinator.RestoreInterruptedTurn(sessionID, turnID)
		event, err := NewAppEvent(workspaceID, sessionID, turnID, EventTurnInterrupted, time.Now(), 0, map[string]any{
			"turn_id": turnID, "detected_at": time.Now().UTC(), "reason": "service restarted before the turn reached a terminal state",
		})
		if err != nil || events == nil {
			continue
		}
		_, _ = events.Publish(event)
	}
}

func restoreQueuedInputs(ctx context.Context, store *session.JSONLStore, coordinator *WorkspaceCoordinator, workspaceID WorkspaceID, eventHub *EventHub) error {
	page, err := store.ListSessionPage(ctx, session.SessionPageRequest{Limit: 100})
	if err != nil {
		return err
	}
	for {
		for _, summary := range page.Items {
			records, err := store.LoadResolvedJournalRecords(ctx, summary.SessionID)
			if err != nil {
				return err
			}
			consumed := make(map[string]bool)
			for _, record := range records {
				receipt := record.CommandReceipt
				if receipt != nil && receipt.Kind == CommandKindSubmitTurn && strings.HasSuffix(receipt.CommandID, ":queued") {
					consumed[strings.TrimSuffix(receipt.CommandID, ":queued")] = true
				}
			}
			var queue []InputDraft
			var version uint64
			for _, record := range records {
				receipt := record.CommandReceipt
				if receipt == nil {
					continue
				}
				if receipt.SessionVersion > version {
					version = receipt.SessionVersion
				}
				if receipt.Kind != CommandKindQueueTurn || receipt.Input == nil {
					continue
				}
				if consumed[receipt.CommandID] {
					continue
				}
				queue = append(queue, InputDraft{CommandID: receipt.CommandID, Content: receipt.Input.Content, CreatedAt: receipt.Input.CreatedAt})
			}
			coordinator.RestoreSessionState(summary.SessionID, version, queue)
			restoreUnfinishedTurns(summary.SessionID, records, coordinator, eventHub, workspaceID)
		}
		if page.NextCursor == "" {
			break
		}
		page, err = store.ListSessionPage(ctx, session.SessionPageRequest{Cursor: page.NextCursor, Limit: 100})
		if err != nil {
			return err
		}
	}
	return nil
}

func explicitWorkspaceRoot(root string) (string, error) {
	if !filepath.IsAbs(root) {
		return "", fmt.Errorf("workspace runtime root must be absolute: %q", root)
	}
	absolute, err := filepath.Abs(filepath.Clean(root))
	if err != nil {
		return "", fmt.Errorf("resolve workspace runtime root: %w", err)
	}
	return absolute, nil
}

func configOpenOptions(paths configv2.Paths, worker WorkerContext) configv2.Options {
	return configv2.Options{
		Paths: paths, DisableWatch: worker.WorkerMode,
		DisableModelDiscovery: worker.WorkerMode, AsyncModelDiscovery: !worker.WorkerMode,
	}
}

func effectiveYoloMode(commandLineEnabled bool, document configv2.Document) bool {
	return commandLineEnabled || document.Yolo
}

func ResolveRuntimeSessionID(ctx context.Context, store *session.JSONLStore, requested string) (string, error) {
	if requested != "" {
		exists, err := store.Exists(ctx, requested)
		if err != nil {
			return "", fmt.Errorf("查询 session 失败: %w", err)
		}
		if !exists {
			return "", fmt.Errorf("session 不存在: %s", requested)
		}
		return requested, nil
	}
	sessionID, err := session.GenerateSessionID()
	if err != nil {
		return "", fmt.Errorf("生成 session ID 失败: %w", err)
	}
	return sessionID, nil
}

func SandboxLimitsFlag(effective configv2.EffectiveSandbox) string {
	return fmt.Sprintf("cpu=%d,file_mb=%d,proc=%d,nofile=%d,wall=%d",
		effective.CPUSeconds, effective.FileSizeMiB, effective.MaxProcesses,
		effective.OpenFiles, effective.JobWallSeconds)
}

type streamMATaskAdapter struct {
	manager         *task.Manager
	parentSessionID func() string
}

func (a streamMATaskAdapter) StreamTask(ctx context.Context, req loop.StreamMATaskRequest) (loop.StreamMATaskStream, error) {
	if a.manager == nil {
		return loop.StreamMATaskStream{}, fmt.Errorf("streamma task manager is nil")
	}
	parentSessionID := ""
	if a.parentSessionID != nil {
		parentSessionID = a.parentSessionID()
	}
	stream, err := a.manager.Stream(ctx, task.Request{
		SessionID: req.SessionID, ParentSessionID: parentSessionID,
		Prompt: req.Prompt, SystemPrompt: req.SystemPrompt, Description: req.Description,
		ContextMode: settings.ContextMode(req.ContextMode), RunMode: settings.RunModeSync,
		DisableTools: req.DisableTools,
	})
	if err != nil {
		return loop.StreamMATaskStream{}, err
	}
	return loop.StreamMATaskStream{
		Events: stream.Events, AgentName: stream.AgentName, AgentColor: stream.AgentColor,
		SessionID: stream.SessionID, TranscriptPath: stream.TranscriptPath, OutputPath: stream.OutputPath,
	}, nil
}

func RegisterBuiltinTools(registry *tool.Registry, root string, readRoots []string, taskManager *task.Manager, sessionID string, broker coremcp.Broker, allowOutsideRead bool, workerMode bool) error {
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
	if !workerMode {
		registry.Register(task.NewTool(taskManager, sessionID))
		registry.Register(task.NewStatusTool(taskManager))
		registry.Register(task.NewStopTool(taskManager))
		registry.Register(task.NewWaitTool(taskManager))
	}
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

func errorsJoin(primary, secondary error) error {
	if primary == nil {
		return secondary
	}
	if secondary == nil {
		return primary
	}
	return fmt.Errorf("%v; cleanup: %w", primary, secondary)
}
