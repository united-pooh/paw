package app

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	configv2 "paw/internal/config"
	"paw/internal/mcp"
	"paw/internal/model"
	"paw/internal/session"
	"paw/internal/sessionactor"
	"paw/internal/settings"
	"paw/internal/task"
)

const defaultRuntimeCloseTimeout = 5 * time.Second

type runtimeCloseStage struct {
	name  string
	close func(context.Context) error
}

type WorkspaceRuntime struct {
	Root               string
	Runner             *sessionactor.Host
	SessionID          string
	Model              *model.Client
	ConfigController   *configv2.Controller
	SettingsController *settings.Controller
	TaskManager        *task.Manager
	Store              *session.JSONLStore
	MCPManager         *mcp.Manager
	ControllerLease    *ControllerLease
	Toolset            *Toolset

	configManager *configv2.Manager
	taskLauncher  *task.ProcessPoolLauncher
	closeTimeout  time.Duration
	closeStages   []runtimeCloseStage
	closeOnce     sync.Once
	closeErr      error
}

func (r *WorkspaceRuntime) Close() error {
	if r == nil {
		return nil
	}
	r.closeOnce.Do(func() {
		timeout := r.closeTimeout
		if timeout <= 0 {
			timeout = defaultRuntimeCloseTimeout
		}
		var errs []error
		for _, stage := range r.closeStages {
			if stage.close == nil {
				continue
			}
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			err := runRuntimeCloseStage(ctx, stage.close)
			cancel()
			if err != nil {
				errs = append(errs, fmt.Errorf("close %s: %w", stage.name, err))
			}
		}
		r.closeErr = errors.Join(errs...)
	})
	return r.closeErr
}

func runRuntimeCloseStage(ctx context.Context, closeStage func(context.Context) error) error {
	result := make(chan error, 1)
	go func() {
		result <- closeStage(ctx)
	}()
	select {
	case err := <-result:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *WorkspaceRuntime) initializeCloseStages() {
	r.closeStages = []runtimeCloseStage{
		{name: "mcp", close: func(ctx context.Context) error {
			if r.MCPManager == nil {
				return nil
			}
			return r.MCPManager.Close(ctx)
		}},
		{name: "task", close: func(context.Context) error {
			var errs []error
			if r.TaskManager != nil {
				errs = append(errs, r.TaskManager.Close())
			}
			if r.taskLauncher != nil {
				errs = append(errs, r.taskLauncher.Close())
			}
			return errors.Join(errs...)
		}},
		{name: "runner", close: func(context.Context) error {
			if r.Runner != nil {
				r.Runner.Close()
			}
			return nil
		}},
		{name: "config", close: func(context.Context) error {
			if r.ConfigController != nil {
				return r.ConfigController.Close()
			}
			if r.configManager != nil {
				return r.configManager.Close()
			}
			return nil
		}},
		{name: "lease", close: func(context.Context) error {
			if r.ControllerLease == nil {
				return nil
			}
			return r.ControllerLease.Close()
		}},
	}
}
