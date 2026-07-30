package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"paw/internal/loop"
	coremcp "paw/internal/mcp"
	"paw/internal/model"
	"paw/internal/session"
	"paw/internal/settings"
	"paw/internal/subagent"
	"paw/internal/tokentracer"
	"paw/internal/tool"
	toolexec "paw/internal/tool/exec"
	toolfile "paw/internal/tool/file"
	toolmcp "paw/internal/tool/mcp"
	toolwebfetch "paw/internal/tool/webfetch"
	uiiface "paw/internal/ui"
	bubbleui "paw/internal/ui/bubble"
	"paw/internal/ui/headless"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type options struct {
	prompt          string
	sessionID       string
	subagentWorker  bool
	streamMA        bool
	tokenTracer     bool
	tokenTracerOpen bool
	tokenTracerPort int
}

func parseOptions() options {
	prompt := flag.String("p", "", "single-turn prompt; omit to start Bubble Tea UI")
	sessionID := flag.String("s", "", "session ID; omit to resume/create by cwd")
	subagentWorker := flag.Bool("subagent-worker", false, "run hidden subagent worker")
	streamMA := flag.Bool("streamma", defaultStreamMAEnabled(), "enable /streamma and /streamma-trace commands")
	tokenTracer := flag.Bool("token-tracer", defaultTokenTracerEnabled(), "start local Token Tracer dashboard in interactive mode")
	tokenTracerOpen := flag.Bool("token-tracer-open", defaultTokenTracerOpen(), "open Token Tracer dashboard in the default browser")
	tokenTracerPort := flag.Int("token-tracer-port", defaultTokenTracerPort(), "Token Tracer dashboard port; 0 selects a free port")
	flag.Parse()

	return options{
		prompt:          *prompt,
		sessionID:       *sessionID,
		subagentWorker:  *subagentWorker,
		streamMA:        *streamMA,
		tokenTracer:     *tokenTracer,
		tokenTracerOpen: *tokenTracerOpen,
		tokenTracerPort: *tokenTracerPort,
	}
}

func defaultStreamMAEnabled() bool {
	value := strings.ToLower(strings.TrimSpace(os.Getenv("PAW_STREAMMA")))
	return value != "0" && value != "false" && value != "off" && value != "no"
}

func defaultTokenTracerEnabled() bool {
	value := strings.ToLower(strings.TrimSpace(os.Getenv("PAW_TOKEN_TRACER")))
	return value != "0" && value != "false" && value != "off" && value != "no"
}

func defaultTokenTracerOpen() bool {
	value := strings.ToLower(strings.TrimSpace(os.Getenv("PAW_TOKEN_TRACER_OPEN")))
	return value == "1" || value == "true" || value == "on" || value == "yes"
}

func defaultTokenTracerPort() int {
	value := strings.TrimSpace(os.Getenv("PAW_TOKEN_TRACER_PORT"))
	if value == "" {
		return 8999
	}
	port, err := strconv.Atoi(value)
	if err != nil || port < 0 {
		return 8999
	}
	return port
}

func main() {
	opts := parseOptions()
	ctx := context.Background()

	if opts.subagentWorker {
		if err := runSubagentWorkerMode(ctx, os.Stdin, os.Stdout); err != nil {
			log.Fatal(err)
		}
		return
	}

	if opts.prompt != "" {
		if err := runSingleTurnMode(ctx, opts); err != nil {
			log.Fatal(err)
		}
		return
	}

	if err := runInteractiveMode(ctx, opts); err != nil {
		log.Fatal(err)
	}
}

func runSingleTurnMode(ctx context.Context, opts options) error {
	output := headless.New(os.Stdout)
	runner, sessionID, _, _, _, _, mcpManager, err := buildRunner(ctx, opts.sessionID, output)
	if err != nil {
		return err
	}
	if mcpManager != nil {
		defer func() { _ = mcpManager.Close(context.Background()) }()
	}
	runner.SetStreamMAEnabled(opts.streamMA)

	fmt.Fprintf(os.Stderr, "session: %s\n", sessionID)
	_, err = runner.RunTurn(ctx, opts.prompt)
	return err
}

func runInteractiveMode(ctx context.Context, opts options) error {
	clearTerminalWindow(os.Stdout)

	output := bubbleui.New()
	runner, sessionID, client, settingsController, subagentManager, store, mcpManager, err := buildRunner(ctx, opts.sessionID, output)
	if err != nil {
		return err
	}
	if mcpManager != nil {
		defer func() { _ = mcpManager.Close(context.Background()) }()
	}
	runner.SetStreamMAEnabled(opts.streamMA)
	if opts.tokenTracer {
		tracer, server, err := startTokenTracer(ctx, sessionID, opts)
		if err != nil {
			return err
		}
		defer func() {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_ = server.Shutdown(shutdownCtx)
		}()
		runner.SetTokenTracer(tracer)
		subagentManager.SetTokenTracer(tracer)
	}

	output.SetModelConfigController(client)
	output.SetSettingsController(settingsController)
	output.SetSubagentController(subagentManager)
	output.SetSessionStore(store)
	output.SetMCPStatusController(mcpManager)
	return output.Run(ctx, runner, sessionID)
}

func startTokenTracer(ctx context.Context, sessionID string, opts options) (*tokentracer.Tracer, *tokentracer.Server, error) {
	tracer := tokentracer.New("Paw")
	tracer.SetSessionID(sessionID)
	if cwd, err := os.Getwd(); err == nil {
		tracer.SetWorkspace(cwd)
	}
	server := tokentracer.NewServer(tracer, tokentracer.ServerConfig{
		Host:        "127.0.0.1",
		Port:        opts.tokenTracerPort,
		OpenBrowser: opts.tokenTracerOpen,
	})
	if err := server.Start(ctx); err != nil {
		return nil, nil, err
	}
	return tracer, server, nil
}

func runSubagentWorkerMode(ctx context.Context, input io.Reader, output io.Writer) error {
	decoder := json.NewDecoder(input)
	var start subagent.WorkerMessage
	if err := decoder.Decode(&start); err != nil {
		return fmt.Errorf("decode subagent worker.start: %w", err)
	}
	if start.Type != subagent.WorkerMessageStart {
		return fmt.Errorf("subagent worker.start is required")
	}
	req := start.Request()
	if strings.TrimSpace(req.SessionID) == "" {
		return fmt.Errorf("subagent worker session_id is required")
	}

	workerCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	broker := newWorkerMCPBroker(workerCtx, start.Snapshot, output)
	workerDone := make(chan subagent.WorkerResult, 1)
	go func() {
		workerUI := &workerUsageUI{UI: headless.New(io.Discard)}
		runner, sessionID, _, _, _, _, _, err := buildRunnerWithSubagentContext(workerCtx, req.SessionID, workerUI, subagentRuntimeContext{
			depth:        req.Depth,
			maxDepth:     req.MaxDepth,
			parentTaskID: req.TaskID,
			mcpBroker:    broker,
		})
		result := subagent.WorkerResult{TaskID: req.TaskID, SessionID: sessionID, ExitCode: 0}
		if err != nil {
			result.Error = err.Error()
			result.ExitCode = 1
			workerDone <- result
			return
		}
		broker.SetUpdateHandler(func(snapshot coremcp.Snapshot) {
			adapted := toolmcp.NewSnapshotTools(snapshot, broker)
			tools := make([]tool.Tool, 0, len(adapted))
			for _, item := range adapted {
				tools = append(tools, item)
			}
			_ = runner.ReplaceToolNamespace("mcp", tools)
		})
		broker.Update(broker.Snapshot())
		msg, err := runner.RunTurn(workerCtx, req.Prompt)
		result.UsedTokens = runner.ContextStats(1<<30, "").UsedTokens
		result.Usage = workerUI.Usage()
		if err != nil {
			result.Error = err.Error()
			result.ExitCode = 1
		} else {
			result.Content = strings.TrimSpace(msg.Content)
		}
		workerDone <- result
	}()

	messages := make(chan subagent.WorkerMessage)
	decodeErr := make(chan error, 1)
	go func() {
		for {
			var message subagent.WorkerMessage
			if err := decoder.Decode(&message); err != nil {
				decodeErr <- err
				close(messages)
				return
			}
			messages <- message
		}
	}()
	for {
		select {
		case result := <-workerDone:
			return broker.send(subagent.NewWorkerResultMessage(result))
		case message, ok := <-messages:
			if !ok {
				cancel()
				return <-decodeErr
			}
			switch message.Type {
			case subagent.WorkerMessageMCPResult:
				broker.resolve(message.RequestID, message.Content, message.Error)
			case subagent.WorkerMessageSnapshot:
				broker.Update(message.Snapshot)
			case subagent.WorkerMessageCancel:
				cancel()
			}
		case err := <-decodeErr:
			cancel()
			return fmt.Errorf("read subagent worker input: %w", err)
		}
	}
}

type workerMCPBroker struct {
	ctx      context.Context
	mu       sync.RWMutex
	snapshot coremcp.Snapshot
	output   io.Writer
	writeMu  sync.Mutex
	pending  map[string]chan workerMCPResult
	sequence uint64
	onUpdate func(coremcp.Snapshot)
}

type workerMCPResult struct {
	content string
	err     string
}

func newWorkerMCPBroker(ctx context.Context, snapshot coremcp.Snapshot, output io.Writer) *workerMCPBroker {
	return &workerMCPBroker{
		ctx:      ctx,
		snapshot: snapshot.Clone(),
		output:   output,
		pending:  make(map[string]chan workerMCPResult),
	}
}

func (b *workerMCPBroker) Snapshot() coremcp.Snapshot {
	if b == nil {
		return coremcp.Snapshot{}
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.snapshot.Clone()
}

func (b *workerMCPBroker) Update(snapshot coremcp.Snapshot) {
	if b == nil {
		return
	}
	b.mu.Lock()
	b.snapshot = snapshot.Clone()
	onUpdate := b.onUpdate
	b.mu.Unlock()
	if onUpdate != nil {
		onUpdate(snapshot)
	}
}

func (b *workerMCPBroker) SetUpdateHandler(handler func(coremcp.Snapshot)) {
	if b == nil {
		return
	}
	b.mu.Lock()
	b.onUpdate = handler
	b.mu.Unlock()
}

func (b *workerMCPBroker) Call(ctx context.Context, name string, input json.RawMessage) (string, error) {
	if b == nil {
		return "", fmt.Errorf("MCP worker broker is nil")
	}
	if ctx == nil {
		ctx = b.ctx
	}
	b.mu.RLock()
	found := false
	for _, item := range b.snapshot.Tools {
		if item.Name == name {
			found = true
			break
		}
	}
	b.mu.RUnlock()
	if !found {
		return "", fmt.Errorf("MCP tool %q is not in the parent snapshot", name)
	}
	sequence := atomic.AddUint64(&b.sequence, 1)
	requestID := fmt.Sprintf("mcp-%d", sequence)
	result := make(chan workerMCPResult, 1)
	b.mu.Lock()
	b.pending[requestID] = result
	b.mu.Unlock()
	if err := b.send(subagent.WorkerMessage{
		Type:      subagent.WorkerMessageMCPCall,
		RequestID: requestID,
		Tool:      name,
		Input:     append(json.RawMessage(nil), input...),
	}); err != nil {
		b.mu.Lock()
		delete(b.pending, requestID)
		b.mu.Unlock()
		return "", err
	}
	select {
	case response := <-result:
		if response.err != "" {
			return response.content, fmt.Errorf("MCP parent call failed: %s", response.err)
		}
		return response.content, nil
	case <-ctx.Done():
		b.mu.Lock()
		delete(b.pending, requestID)
		b.mu.Unlock()
		return "", ctx.Err()
	case <-b.ctx.Done():
		return "", b.ctx.Err()
	}
}

func (b *workerMCPBroker) resolve(requestID, content, errText string) {
	b.mu.Lock()
	response, ok := b.pending[requestID]
	if ok {
		delete(b.pending, requestID)
	}
	b.mu.Unlock()
	if ok {
		response <- workerMCPResult{content: content, err: errText}
	}
}

func (b *workerMCPBroker) send(message subagent.WorkerMessage) error {
	b.writeMu.Lock()
	defer b.writeMu.Unlock()
	if b.output == nil {
		return fmt.Errorf("subagent worker output is nil")
	}
	if err := json.NewEncoder(b.output).Encode(message); err != nil {
		return fmt.Errorf("write subagent worker message: %w", err)
	}
	return nil
}

type workerUsageUI struct {
	uiiface.UI

	mu    sync.RWMutex
	usage *tokentracer.Usage
}

func (u *workerUsageUI) OnModelUsage(usage model.Usage) {
	structured := tokentracer.UsageFromModelUsage(usage)
	if structured.Empty() {
		return
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	u.usage = &structured
}

func (u *workerUsageUI) Usage() *tokentracer.Usage {
	u.mu.RLock()
	defer u.mu.RUnlock()
	if u.usage == nil {
		return nil
	}
	copied := *u.usage
	return &copied
}

func clearTerminalWindow(w io.Writer) {
	if w == nil {
		return
	}
	_, _ = io.WriteString(w, "\x1b[H\x1b[2J\x1b[3J")
}

type subagentRuntimeContext struct {
	depth        int
	maxDepth     int
	parentTaskID string
	mcpBroker    coremcp.Broker
}

func buildRunner(ctx context.Context, sessionIDFlag string, output uiiface.UI) (*loop.Runner, string, *model.Client, *settings.Controller, *subagent.Manager, *session.JSONLStore, *coremcp.Manager, error) {
	return buildRunnerWithSubagentContext(ctx, sessionIDFlag, output, subagentRuntimeContext{})
}

func buildRunnerWithSubagentContext(ctx context.Context, sessionIDFlag string, output uiiface.UI, subCtx subagentRuntimeContext) (*loop.Runner, string, *model.Client, *settings.Controller, *subagent.Manager, *session.JSONLStore, *coremcp.Manager, error) {
	cfg, err := model.LoadConfigFromEnv()
	if err != nil {
		return nil, "", nil, nil, nil, nil, nil, err
	}

	root, err := os.Getwd()
	if err != nil {
		return nil, "", nil, nil, nil, nil, nil, err
	}

	store, err := session.NewJSONLStoreInCwd()
	if err != nil {
		return nil, "", nil, nil, nil, nil, nil, fmt.Errorf("初始化 session store 失败: %w", err)
	}

	sessionID, err := resolveSessionID(ctx, store, sessionIDFlag, root)
	if err != nil {
		return nil, "", nil, nil, nil, nil, nil, err
	}

	client := model.NewClient(cfg)
	settingsController, err := settings.NewDefaultController(nil)
	if err != nil {
		return nil, "", nil, nil, nil, nil, nil, err
	}
	var notifier subagent.Notifier
	if n, ok := output.(subagent.Notifier); ok {
		notifier = n
	}
	executable, err := os.Executable()
	if err != nil {
		return nil, "", nil, nil, nil, nil, nil, fmt.Errorf("resolve executable: %w", err)
	}
	broker := subCtx.mcpBroker
	var mcpManager *coremcp.Manager
	if broker == nil {
		mcpConfig, err := coremcp.LoadConfig("", root)
		if err != nil {
			return nil, "", nil, nil, nil, nil, nil, err
		}
		mcpManager, err = coremcp.Start(ctx, mcpConfig)
		if err != nil {
			return nil, "", nil, nil, nil, nil, nil, err
		}
		broker = mcpManager
	}
	launcher := subagent.NewProcessLauncher(executable, root)
	launcher.SetMCPBroker(broker)
	registry := tool.NewRegistry()
	runner := loop.NewRunnerWithInstructionRoot(client, output, registry, store, sessionID, root)
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
	if err := registerTools(registry, root, runner.SkillRoots(), subagentManager, sessionID, broker); err != nil {
		if mcpManager != nil {
			_ = mcpManager.Close(context.Background())
		}
		return nil, "", nil, nil, nil, nil, nil, err
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

	return runner, sessionID, client, settingsController, subagentManager, store, mcpManager, nil
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

func registerTools(registry *tool.Registry, root string, readRoots []string, subagentManager *subagent.Manager, sessionID string, broker coremcp.Broker) error {
	if registry == nil {
		return fmt.Errorf("tool registry is nil")
	}
	readState := toolfile.NewReadStateStore()
	registry.Register(&toolfile.LSTool{Root: root, ReadRoots: readRoots})
	registry.Register(&toolfile.ReadTool{Root: root, ReadRoots: readRoots, ReadState: readState})
	registry.Register(&toolfile.WriteTool{Root: root, ReadState: readState})
	registry.Register(&toolfile.EditTool{Root: root, ReadState: readState})
	registry.Register(&toolfile.GrepTool{Root: root, ReadRoots: readRoots})
	registry.Register(&toolfile.GlobTool{Root: root, ReadRoots: readRoots})
	registry.Register(&toolexec.BashTool{Root: root})
	registry.Register(&toolwebfetch.Tool{})
	registry.Register(subagent.NewTool(subagentManager, sessionID))
	registry.Register(subagent.NewStatusTool(subagentManager))
	registry.Register(subagent.NewStopTool(subagentManager))
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
