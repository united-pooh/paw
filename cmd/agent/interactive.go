package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"paw/internal/todo"
	"paw/internal/tokentracer"
	"paw/internal/tool"
	selecttool "paw/internal/tool/select"
	bubbleui "paw/internal/ui/bubble"
	"paw/internal/ui/headless"
	"time"
)

func runSingleTurnMode(ctx context.Context, opts options) error {
	output := headless.New(os.Stdout)
	runner, sessionID, _, _, _, _, mcpManager, err := buildRunner(ctx, opts.sessionID, output, opts.allowOutsideRead)
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
	selectionBroker := selecttool.NewBroker()
	todoBroker := todo.NewBroker()
	defer selectionBroker.Close()
	defer todoBroker.Close()
	output.SetSelectionBroker(selectionBroker)
	output.SetTodoBroker(todoBroker)
	runner, sessionID, client, settingsController, subagentManager, store, mcpManager, err := buildRunner(ctx, opts.sessionID, output, opts.allowOutsideRead, func(registry *tool.Registry) error {
		if err := registerMainAgentTools(registry, todoBroker); err != nil {
			return err
		}
		return registerInteractiveTools(registry, selectionBroker)
	})
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

func clearTerminalWindow(w io.Writer) {
	if w == nil {
		return
	}
	_, _ = io.WriteString(w, "\x1b[H\x1b[2J\x1b[3J")
}
