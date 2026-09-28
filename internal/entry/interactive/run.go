package interactive

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	appcore "paw/internal/app"
	selecttool "paw/internal/capability/tool/select"
	"paw/internal/runtime/goal"
	"paw/internal/runtime/loop"
	"paw/internal/runtime/plan"
	"paw/internal/todo"
	"paw/internal/tokentracer"
	bubbleui "paw/internal/ui/bubble"
	"time"
)

func Run(ctx context.Context, opts Options) error {
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	restoreLogs, err := redirectDiagnosticLogs(root)
	if err != nil {
		return err
	}
	defer restoreLogs()

	clearTerminalWindow(os.Stdout)

	output := bubbleui.New()
	selectionBroker := selecttool.NewBroker()
	todoBroker := todo.NewBroker()
	defer todoBroker.Close()
	output.SetSelectionBroker(selectionBroker)
	output.SetTodoBroker(todoBroker)
	app, err := appcore.BuildWorkspaceRuntime(ctx, appcore.WorkspaceRuntimeOptions{
		Root: root, SessionID: opts.SessionID, Output: output,
		AllowOutsideRead: opts.AllowOutsideRead, AllowIncomplete: true,
		TodoBroker: todoBroker, SelectionBroker: selectionBroker,
		ControllerMode: appcore.ControllerModeTUI,
	})
	if err != nil {
		selectionBroker.Close()
		return err
	}
	defer func() {
		selectionBroker.Close()
		_ = app.Close()
	}()
	host := app.SessionHost
	host.SetPermissionPrompter(selectionPermissionPrompter{broker: selectionBroker})
	host.RepublishPendingPermissions(app.SessionID)
	sessionID := app.SessionID
	appcore.BindSessionTools(app, todoBroker, sessionID)
	host.SetSessionLoadedHook(func(sid string) {
		// /resume 切换会话后：会话相关工具、todo 事件、状态块全部跟随新会话。
		appcore.BindSessionTools(app, todoBroker, sid)
	})
	appcore.ApplyCompressionSettings(host.Engine, app.SettingsController)
	host.SetStreamMAEnabled(opts.StreamMA)
	if opts.TokenTracer {
		tracer, server, err := startTokenTracer(ctx, sessionID, opts)
		if err != nil {
			return err
		}
		defer func() {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_ = server.Shutdown(shutdownCtx)
		}()
		host.SetTokenTracer(tracer)
		host.SetSessionLoadedHook(func(sid string) {
			tracer.SetSessionID(sid)
		})
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
	goalController := goal.NewSessionController(host, todoBroker, sessionBase)
	if err := app.BindGoalController(goalController); err != nil {
		return fmt.Errorf("bind goal tools: %w", err)
	}
	if err := goalController.Rebind(sessionID); err != nil {
		return fmt.Errorf("restore goal controller: %w", err)
	}
	goalController.SetStopped(func(reason string) {
		_ = output.NotifyGoalStopped(reason)
	})
	output.SetGoalController(goalController)
	planController := plan.NewSessionController(host, appcore.PlansDir(host.Engine), sessionBase)
	if err := planController.Rebind(sessionID); err != nil {
		return fmt.Errorf("restore plan controller: %w", err)
	}
	planController.SetNotify(func(doc plan.PlanDoc) {
		_ = output.NotifyPlanFinalized(doc.Path)
	})
	planController.SetStopped(func(reason string) {
		_ = output.NotifyPlanStopped(reason)
	})
	output.SetPlanController(planController)
	app.Toolset.Finalize().SetHook(planController.Finalize)
	return output.Run(ctx, host, sessionID)
}

type selectionPermissionPrompter struct {
	broker *selecttool.Broker
}

func (p selectionPermissionPrompter) PromptPermission(ctx context.Context, request loop.PermissionRequest) (loop.PermissionDecision, error) {
	if p.broker == nil {
		return loop.PermissionDeny, nil
	}
	result, err := p.broker.Ask(ctx, selecttool.Request{
		Prompt:      "Read requests access outside the workspace:\n" + request.CanonicalPath,
		Mode:        selecttool.ModeSingle,
		OptionsOnly: true,
		Options: []selecttool.Option{
			{ID: string(loop.PermissionAllowOnce), Label: "Allow once", Description: "Allow only this exact Read path for this tool call."},
			{ID: string(loop.PermissionDeny), Label: "Deny", Description: "Return a normal tool error without reading the file."},
		},
		MinSelect: 1,
		MaxSelect: 1,
	})
	if err != nil {
		return "", err
	}
	if result.Cancelled || len(result.SelectedOptions) == 0 {
		return loop.PermissionDeny, nil
	}
	if result.SelectedOptions[0].ID == string(loop.PermissionAllowOnce) {
		return loop.PermissionAllowOnce, nil
	}
	return loop.PermissionDeny, nil
}

func startTokenTracer(ctx context.Context, sessionID string, opts Options) (*tokentracer.Tracer, *tokentracer.Server, error) {
	tracer := tokentracer.New("Paw")
	tracer.SetSessionID(sessionID)
	if cwd, err := os.Getwd(); err == nil {
		tracer.SetWorkspace(cwd)
	}
	port := opts.TokenTracerPort
	server := tokentracer.NewServer(tracer, tokentracer.ServerConfig{
		Host:        "127.0.0.1",
		Port:        port,
		OpenBrowser: opts.TokenTracerOpen,
	})
	if err := server.Start(ctx); err != nil {
		if port == 0 {
			return nil, nil, err
		}
		// 请求的端口被占（常见：另一个 Paw 会话已在跑）时回退到随机空闲
		// 端口继续，而不是杀死整个会话；显式端口仍大声失败。
		log.Printf("paw: token tracer port %d unavailable (%v); falling back to a free port", port, err)
		server = tokentracer.NewServer(tracer, tokentracer.ServerConfig{
			Host:        "127.0.0.1",
			Port:        0,
			OpenBrowser: opts.TokenTracerOpen,
		})
		if err := server.Start(ctx); err != nil {
			return nil, nil, err
		}
	}
	return tracer, server, nil
}

func clearTerminalWindow(w io.Writer) {
	if w == nil {
		return
	}
	_, _ = io.WriteString(w, "\x1b[H\x1b[2J\x1b[3J")
}
