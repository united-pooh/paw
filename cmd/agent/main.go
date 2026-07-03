package main

import (
	"codex-agent-go/internal/loop"
	"codex-agent-go/internal/model"
	"codex-agent-go/internal/session"
	"codex-agent-go/internal/settings"
	"codex-agent-go/internal/subagent"
	"codex-agent-go/internal/tokentracer"
	"codex-agent-go/internal/tool"
	toolexec "codex-agent-go/internal/tool/exec"
	toolfile "codex-agent-go/internal/tool/file"
	toolwebfetch "codex-agent-go/internal/tool/webfetch"
	uiiface "codex-agent-go/internal/ui"
	"codex-agent-go/internal/ui/headless"
	"codex-agent-go/internal/wsserver"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"strconv"
	"strings"
	"time"
	"sync"
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
	value := strings.ToLower(strings.TrimSpace(os.Getenv("GOCODE_STREAMMA")))
	return value != "0" && value != "false" && value != "off" && value != "no"
}

func defaultTokenTracerEnabled() bool {
	value := strings.ToLower(strings.TrimSpace(os.Getenv("GOCODE_TOKEN_TRACER")))
	return value != "0" && value != "false" && value != "off" && value != "no"
}

func defaultTokenTracerOpen() bool {
	value := strings.ToLower(strings.TrimSpace(os.Getenv("GOCODE_TOKEN_TRACER_OPEN")))
	return value == "1" || value == "true" || value == "on" || value == "yes"
}

func defaultTokenTracerPort() int {
	value := strings.TrimSpace(os.Getenv("GOCODE_TOKEN_TRACER_PORT"))
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

	if err := runWSMode(ctx, opts); err != nil {
		log.Fatal(err)
	}
}

func runSingleTurnMode(ctx context.Context, opts options) error {
	output := headless.New(os.Stdout)
	runner, sessionID, _, _, _, _, err := buildRunner(ctx, opts.sessionID, output)
	if err != nil {
		return err
	}
	runner.SetStreamMAEnabled(opts.streamMA)

	fmt.Fprintf(os.Stderr, "session: %s\n", sessionID)
	_, err = runner.RunTurn(ctx, opts.prompt)
	return err
}

func runWSMode(ctx context.Context, opts options) error {
	server := wsserver.NewServer()

	// Create WSUI with empty sessionID initially; we update it after buildRunner
	// resolves the final session ID.
	wsui := wsserver.NewWSUI(server, "")

	runner, sessionID, _, _, _, _, err := buildRunner(ctx, opts.sessionID, wsui)
	if err != nil {
		return err
	}
	wsui.SetSessionID(sessionID)
	runner.SetStreamMAEnabled(opts.streamMA)

	handler := wsserver.NewHandler(runner)

	serverErr := make(chan error, 1)
	go func() { serverErr <- server.ListenAndServe(ctx, handler) }()

	// Give the server 300ms to bind; a bind error surfaces immediately.
	select {
	case err := <-serverErr:
		return fmt.Errorf("WS server failed to start (port conflict?): %w", err)
	case <-time.After(300 * time.Millisecond):
	}

	log.Printf("Agent ready. WS server started. session=%s", sessionID)
	<-ctx.Done()
	return nil
}

func startTokenTracer(ctx context.Context, sessionID string, opts options) (*tokentracer.Tracer, *tokentracer.Server, error) {
	tracer := tokentracer.New("GoCode")
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
	var req subagent.WorkerRequest
	if err := json.NewDecoder(input).Decode(&req); err != nil {
		return fmt.Errorf("decode subagent worker request: %w", err)
	}
	if strings.TrimSpace(req.SessionID) == "" {
		return fmt.Errorf("subagent worker session_id is required")
	}

	workerUI := &workerUsageUI{UI: headless.New(io.Discard)}
	runner, sessionID, _, _, _, _, err := buildRunnerWithSubagentContext(ctx, req.SessionID, workerUI, subagentRuntimeContext{
		depth:        req.Depth,
		maxDepth:     req.MaxDepth,
		parentTaskID: req.TaskID,
	})
	result := subagent.WorkerResult{
		TaskID:    req.TaskID,
		SessionID: sessionID,
		ExitCode:  0,
	}
	if err != nil {
		result.Error = err.Error()
		result.ExitCode = 1
		return json.NewEncoder(output).Encode(result)
	}

	msg, err := runner.RunTurn(ctx, req.Prompt)
	if err != nil {
		result.Error = err.Error()
		result.ExitCode = 1
		result.UsedTokens = runner.ContextStats(1<<30, "").UsedTokens
		result.Usage = workerUI.Usage()
		return json.NewEncoder(output).Encode(result)
	}
	result.Content = strings.TrimSpace(msg.Content)
	result.UsedTokens = runner.ContextStats(1<<30, "").UsedTokens
	result.Usage = workerUI.Usage()
	return json.NewEncoder(output).Encode(result)
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
}

func buildRunner(ctx context.Context, sessionIDFlag string, output uiiface.UI) (*loop.Runner, string, *model.Client, *settings.Controller, *subagent.Manager, *session.JSONLStore, error) {
	return buildRunnerWithSubagentContext(ctx, sessionIDFlag, output, subagentRuntimeContext{})
}

func buildRunnerWithSubagentContext(ctx context.Context, sessionIDFlag string, output uiiface.UI, subCtx subagentRuntimeContext) (*loop.Runner, string, *model.Client, *settings.Controller, *subagent.Manager, *session.JSONLStore, error) {
	cfg, err := model.LoadConfigFromEnv()
	if err != nil {
		return nil, "", nil, nil, nil, nil, err
	}

	root, err := os.Getwd()
	if err != nil {
		return nil, "", nil, nil, nil, nil, err
	}

	store, err := session.NewJSONLStoreInCwd()
	if err != nil {
		return nil, "", nil, nil, nil, nil, fmt.Errorf("初始化 session store 失败: %w", err)
	}

	sessionID, err := resolveSessionID(ctx, store, sessionIDFlag, root)
	if err != nil {
		return nil, "", nil, nil, nil, nil, err
	}

	client := model.NewClient(cfg)
	settingsController, err := settings.NewControllerInCwd()
	if err != nil {
		return nil, "", nil, nil, nil, nil, err
	}
	var notifier subagent.Notifier
	if n, ok := output.(subagent.Notifier); ok {
		notifier = n
	}
	executable, err := os.Executable()
	if err != nil {
		return nil, "", nil, nil, nil, nil, fmt.Errorf("resolve executable: %w", err)
	}
	registry := tool.NewRegistry()
	runner := loop.NewRunnerWithInstructionRoot(client, output, registry, store, sessionID, root)
	runner.SetEventStore(loop.NewInMemorySessionEventStore())
	runner.SetSnapshotStore(loop.NewInMemorySnapshotStore())
	subagentManager := subagent.NewManager(subagent.Config{
		Model:        client,
		Store:        store,
		Root:         root,
		Settings:     settingsController,
		Notifier:     notifier,
		Context:      runner,
		Launcher:     subagent.NewProcessLauncher(executable, root),
		Depth:        subCtx.depth,
		MaxDepth:     subCtx.maxDepth,
		ParentTaskID: subCtx.parentTaskID,
	})
	runner.SetStreamMASubagentRunner(streamMASubagentAdapter{
		manager:         subagentManager,
		parentSessionID: sessionID,
	})
	runner.SetSubagentTokensProvider(subagentManager)
	registerTools(registry, root, runner.SkillRoots(), subagentManager, sessionID)

	return runner, sessionID, client, settingsController, subagentManager, store, nil
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

func registerTools(registry *tool.Registry, root string, readRoots []string, subagentManager *subagent.Manager, sessionID string) {
	if registry == nil {
		return
	}
	registry.Register(&toolfile.LSTool{Root: root, ReadRoots: readRoots})
	registry.Register(&toolfile.ReadTool{Root: root, ReadRoots: readRoots})
	registry.Register(&toolfile.WriteTool{Root: root})
	registry.Register(&toolfile.GrepTool{Root: root, ReadRoots: readRoots})
	registry.Register(&toolfile.GlobTool{Root: root, ReadRoots: readRoots})
	registry.Register(&toolexec.BashTool{Root: root})
	registry.Register(&toolwebfetch.Tool{})
	registry.Register(subagent.NewTool(subagentManager, sessionID))
	registry.Register(subagent.NewStatusTool(subagentManager))
	registry.Register(subagent.NewStopTool(subagentManager))
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
