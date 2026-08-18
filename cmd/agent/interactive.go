package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"paw/internal/goal"
	"paw/internal/loop"
	"paw/internal/plan"
	"paw/internal/session"
	"paw/internal/settings"
	"paw/internal/todo"
	"paw/internal/tokentracer"
	"paw/internal/tool"
	selecttool "paw/internal/tool/select"
	bubbleui "paw/internal/ui/bubble"
	"paw/internal/ui/headless"
	"strings"
	"time"
)

// finalizeTool is the plan_finalize tool registered in interactive mode. Its
// hook is wired once the session plan controller exists.
var finalizeTool = plan.NewFinalizeTool(nil)

func runSingleTurnMode(ctx context.Context, opts options) error {
	output := headless.New(os.Stdout)
	todoBroker := todo.NewBroker()
	defer todoBroker.Close()
	app, err := buildRunner(ctx, opts.sessionID, output, opts.allowOutsideRead, false, func(registry *tool.Registry) error {
		return registerMainAgentTools(registry, todoBroker)
	})
	if err != nil {
		return err
	}
	defer func() { _ = app.Close() }()
	wireSessionTools(app.Runner, app.Store, todoBroker, app.SessionID)
	applyCompressionSettings(app.Runner, app.SettingsController)
	app.Runner.SetStreamMAEnabled(opts.streamMA)

	fmt.Fprintf(os.Stderr, "session: %s\n", app.SessionID)
	_, err = app.Runner.RunTurn(ctx, opts.prompt)
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
	app, err := buildRunner(ctx, opts.sessionID, output, opts.allowOutsideRead, true, func(registry *tool.Registry) error {
		if err := registerMainAgentTools(registry, todoBroker); err != nil {
			return err
		}
		if err := registerInteractiveTools(registry, selectionBroker); err != nil {
			return err
		}
		registry.Register(finalizeTool)
		return nil
	})
	if err != nil {
		return err
	}
	defer func() { _ = app.Close() }()
	runner := app.Runner
	sessionID := app.SessionID
	wireSessionTools(runner, app.Store, todoBroker, sessionID)
	runner.SetSessionLoadedHook(func(sid string) {
		// /resume 切换会话后：会话相关工具、todo 事件、状态块全部跟随新会话。
		wireSessionTools(runner, app.Store, todoBroker, sid)
	})
	applyCompressionSettings(runner, app.SettingsController)
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
		app.TaskManager.SetTokenTracer(tracer)
	}

	output.SetModelConfigController(app.ConfigController)
	output.SetConfigCenterController(app.ConfigController)
	output.SetSettingsController(app.SettingsController)
	output.SetTaskController(app.TaskManager)
	output.SetSessionStore(app.Store)
	output.SetMCPStatusController(app.MCPManager)
	// store 目录作为 goal/plan 事件流的根；无 store 时两控制器自行降级。
	sessionBase := ""
	if app.Store != nil {
		sessionBase = app.Store.Dir()
	}
	goalController := goal.NewSessionController(sessionID, runner, todoBroker, sessionBase)
	goalController.SetStopped(func(reason string) {
		_ = output.NotifyGoalStopped(reason)
	})
	output.SetGoalController(goalController)
	planController := plan.NewSessionController(sessionID, runner, plansDir(runner), sessionBase)
	planController.SetNotify(func(doc plan.PlanDoc) {
		_ = output.NotifyPlanFinalized(doc.Path)
	})
	planController.SetStopped(func(reason string) {
		_ = output.NotifyPlanStopped(reason)
	})
	output.SetPlanController(planController)
	finalizeTool.SetHook(planController.Finalize)
	return output.Run(ctx, runner, sessionID)
}

// wireSessionTools 把会话相关工具与状态块绑定到指定 sessionID。
// 启动时绑定一次，/resume 切换会话后经 SessionLoadedHook 重新绑定。
func wireSessionTools(runner *loop.Runner, store *session.JSONLStore, todoBroker *todo.Broker, sessionID string) {
	if runner == nil || store == nil {
		return
	}
	restoreTodoBroker(store, todoBroker, sessionID)
	wireTodoEvents(store, sessionID, filepath.Join(runner.WorkspaceRoot(), "memory", "progress.md"))
	wireSearchTranscript(store, sessionID)
	wireStateTools(store, sessionID)
	runner.SetTodoBroker(todoBroker)
	runner.SetStateBlockProvider(stateBlockProviderFor(sessionID, store, todoBroker, plansDir(runner)))
}

func restoreTodoBroker(store *session.JSONLStore, broker *todo.Broker, sessionID string) {
	if store == nil || broker == nil || strings.TrimSpace(sessionID) == "" {
		return
	}
	snapshot, ok, err := store.LoadLatestTodoSnapshot(context.Background(), sessionID)
	if err != nil {
		// A damaged Todo projection must not leave the previous session's state
		// visible to the new session. Clear it and keep the transcript error
		// observable in logs for diagnosis.
		broker.Restore(todo.Snapshot{}, false)
		fmt.Fprintf(os.Stderr, "restore todo snapshot for %s: %v\n", sessionID, err)
		return
	}
	broker.Restore(snapshot, ok)
}

// plansDir resolves the plan document directory under the workspace root.
func plansDir(runner *loop.Runner) string {
	root := ""
	if runner != nil {
		root = runner.WorkspaceRoot()
	}
	if strings.TrimSpace(root) == "" {
		root = "."
	}
	return filepath.Join(root, "docs", "superpowers", "plans")
}

// applyCompressionSettings 把 settings 的 context_compression 应用到 runner。
func applyCompressionSettings(runner *loop.Runner, settingsController *settings.Controller) {
	if runner == nil || settingsController == nil {
		return
	}
	cfg := settingsController.CurrentSettings()
	runner.SetContextMode(string(cfg.ContextCompression.Mode))
	runner.SetResumeRecentTurns(cfg.ContextCompression.ResumeRecentTurns)
	runner.SetStateCompactionRatio(cfg.ContextCompression.StateCompactionRatio)
}

// stateBlockProviderFor 构建模式 B 状态块提供者。组件级容错：
// plan 事件库不可用/会话存储缺失时对应组件跳过。
func stateBlockProviderFor(sessionID string, store *session.JSONLStore, broker *todo.Broker, plansDir string) *stateBlockProvider {
	var docStore plan.DocStore
	var sessionBase string
	if store != nil {
		sessionBase = store.Dir()
		if esStore, err := plan.NewEventStore(plansDir, store.Dir()); err == nil {
			docStore = esStore
		}
	}
	home, _ := os.UserHomeDir()
	memoryPath := filepath.Join(home, ".paw", "memory.md")
	provider := newStateBlockProvider(docStore, broker, sessionID, sessionBase, memoryPath)
	provider.todoStore = store
	return provider
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
