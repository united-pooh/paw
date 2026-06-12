package main

import (
	"context"
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
	"log"
	"os"
)

type options struct {
	prompt    string
	sessionID string
}

func parseOptions() options {
	prompt := flag.String("p", "", "single-turn prompt; omit to start Bubble Tea UI")
	sessionID := flag.String("s", "", "session ID; omit to resume/create by cwd")
	flag.Parse()

	return options{
		prompt:    *prompt,
		sessionID: *sessionID,
	}
}

func main() {
	opts := parseOptions()
	ctx := context.Background()

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
	runner, sessionID, _, _, _, err := buildRunner(ctx, opts.sessionID, output)
	if err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "session: %s\n", sessionID)
	_, err = runner.RunTurn(ctx, opts.prompt)
	return err
}

func runInteractiveMode(ctx context.Context, opts options) error {
	output := bubbleui.New()
	runner, sessionID, client, settingsController, subagentManager, err := buildRunner(ctx, opts.sessionID, output)
	if err != nil {
		return err
	}

	output.SetModelConfigController(client)
	output.SetSettingsController(settingsController)
	output.SetSubagentController(subagentManager)
	fmt.Fprintf(os.Stderr, "session: %s\n", sessionID)
	return output.Run(ctx, runner, sessionID)
}

func buildRunner(ctx context.Context, sessionIDFlag string, output uiiface.UI) (*loop.Runner, string, *model.Client, *settings.Controller, *subagent.Manager, error) {
	cfg, err := model.LoadConfigFromEnv()
	if err != nil {
		return nil, "", nil, nil, nil, err
	}

	root, err := os.Getwd()
	if err != nil {
		return nil, "", nil, nil, nil, err
	}

	store, err := session.NewJSONLStoreInCwd()
	if err != nil {
		return nil, "", nil, nil, nil, fmt.Errorf("初始化 session store 失败: %w", err)
	}

	sessionID, err := resolveSessionID(ctx, store, sessionIDFlag, root)
	if err != nil {
		return nil, "", nil, nil, nil, err
	}

	client := model.NewClient(cfg)
	settingsController, err := settings.NewControllerInCwd()
	if err != nil {
		return nil, "", nil, nil, nil, err
	}
	var notifier subagent.Notifier
	if n, ok := output.(subagent.Notifier); ok {
		notifier = n
	}
	subagentManager := subagent.NewManager(subagent.Config{
		Model:    client,
		Store:    store,
		Root:     root,
		Settings: settingsController,
		Notifier: notifier,
	})
	registry := tool.NewRegistry()
	registry.Register(&toolfile.LSTool{Root: root})
	registry.Register(&toolfile.ReadTool{Root: root})
	registry.Register(&toolfile.WriteTool{Root: root})
	registry.Register(&toolfile.GrepTool{Root: root})
	registry.Register(&toolfile.GlobTool{Root: root})
	registry.Register(&toolexec.BashTool{Root: root})
	registry.Register(&toolwebfetch.Tool{})
	registry.Register(subagent.NewTool(subagentManager, sessionID))
	registry.Register(subagent.NewStatusTool(subagentManager))

	return loop.NewRunnerWithInstructionRoot(client, output, registry, store, sessionID, root), sessionID, client, settingsController, subagentManager, nil
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

	return store.OpenOrCreate(ctx, cwd)
}
