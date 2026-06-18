package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"gocode/internal/loop"
	"gocode/internal/model"
	"gocode/internal/session"
	"gocode/internal/settings"
	"gocode/internal/subagent"
	"gocode/internal/tool"
	toolexec "gocode/internal/tool/exec"
	toolfile "gocode/internal/tool/file"
	toolwebfetch "gocode/internal/tool/webfetch"
	uiiface "gocode/internal/ui"
	bubbleui "gocode/internal/ui/bubble"
	"gocode/internal/ui/headless"
	"io"
	"log"
	"os"
	"strings"
)

type options struct {
	prompt         string
	sessionID      string
	subagentWorker bool
}

func parseOptions() options {
	prompt := flag.String("p", "", "single-turn prompt; omit to start Bubble Tea UI")
	sessionID := flag.String("s", "", "session ID; omit to resume/create by cwd")
	subagentWorker := flag.Bool("subagent-worker", false, "run hidden subagent worker")
	flag.Parse()

	return options{
		prompt:         *prompt,
		sessionID:      *sessionID,
		subagentWorker: *subagentWorker,
	}
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
	runner, sessionID, _, _, _, _, err := buildRunner(ctx, opts.sessionID, output)
	if err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "session: %s\n", sessionID)
	_, err = runner.RunTurn(ctx, opts.prompt)
	return err
}

func runInteractiveMode(ctx context.Context, opts options) error {
	clearTerminalWindow(os.Stdout)

	output := bubbleui.New()
	runner, sessionID, client, settingsController, subagentManager, store, err := buildRunner(ctx, opts.sessionID, output)
	if err != nil {
		return err
	}

	output.SetModelConfigController(client)
	output.SetSettingsController(settingsController)
	output.SetSubagentController(subagentManager)
	output.SetSessionStore(store)
	return output.Run(ctx, runner, sessionID)
}

func runSubagentWorkerMode(ctx context.Context, input io.Reader, output io.Writer) error {
	var req subagent.WorkerRequest
	if err := json.NewDecoder(input).Decode(&req); err != nil {
		return fmt.Errorf("decode subagent worker request: %w", err)
	}
	if strings.TrimSpace(req.SessionID) == "" {
		return fmt.Errorf("subagent worker session_id is required")
	}

	runner, sessionID, _, _, _, _, err := buildRunnerWithSubagentContext(ctx, req.SessionID, headless.New(io.Discard), subagentRuntimeContext{
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
		return json.NewEncoder(output).Encode(result)
	}
	result.Content = strings.TrimSpace(msg.Content)
	return json.NewEncoder(output).Encode(result)
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
	registerTools(registry, root, subagentManager, sessionID)

	return runner, sessionID, client, settingsController, subagentManager, store, nil
}

type streamMASubagentAdapter struct {
	manager         *subagent.Manager
	parentSessionID string
}

func (a streamMASubagentAdapter) RunStreamMASubagent(ctx context.Context, req loop.StreamMASubagentRequest) (loop.StreamMASubagentResult, error) {
	if a.manager == nil {
		return loop.StreamMASubagentResult{}, fmt.Errorf("streamma subagent manager is nil")
	}
	result, err := a.manager.Run(ctx, subagent.Request{
		ParentSessionID: a.parentSessionID,
		Prompt:          req.Prompt,
		Description:     req.Description,
		ContextMode:     settings.ContextMode(req.ContextMode),
		RunMode:         settings.RunModeSync,
	})
	if err != nil {
		return loop.StreamMASubagentResult{}, err
	}
	return loop.StreamMASubagentResult{
		Content:        result.Content,
		SessionID:      result.SessionID,
		TranscriptPath: result.TranscriptPath,
		OutputPath:     result.OutputPath,
	}, nil
}

func buildToolRegistry(root string, subagentManager *subagent.Manager, sessionID string) *tool.Registry {
	registry := tool.NewRegistry()
	registerTools(registry, root, subagentManager, sessionID)
	return registry
}

func registerTools(registry *tool.Registry, root string, subagentManager *subagent.Manager, sessionID string) {
	if registry == nil {
		return
	}
	registry.Register(&toolfile.LSTool{Root: root})
	registry.Register(&toolfile.ReadTool{Root: root})
	registry.Register(&toolfile.WriteTool{Root: root})
	registry.Register(&toolfile.GrepTool{Root: root})
	registry.Register(&toolfile.GlobTool{Root: root})
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
