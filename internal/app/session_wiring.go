package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"paw/internal/platform/pawpath"
	"paw/internal/platform/settings"
	"paw/internal/runtime/goal"
	"paw/internal/runtime/loop"
	"paw/internal/runtime/plan"
	"paw/internal/storage/session"
	"paw/internal/todo"
	"strings"
)

// BindSessionTools 把会话相关工具与状态块绑定到指定 sessionID。
// 启动时绑定一次，/resume 切换会话后经 SessionLoadedHook 重新绑定。
func BindSessionTools(app *WorkspaceRuntime, todoBroker *todo.Broker, sessionID string) {
	if app == nil || app.SessionHost == nil || app.Store == nil || app.Toolset == nil {
		return
	}
	engine := app.SessionHost.Engine
	restoreTodoBroker(app.Store, todoBroker, sessionID)
	if err := app.Toolset.BindSession(app.Store, sessionID, filepath.Join(engine.WorkspaceRoot(), "memory", "progress.md")); err != nil {
		fmt.Fprintf(os.Stderr, "bind session tools for %s: %v\n", sessionID, err)
	}
	engine.SetTodoBroker(todoBroker)
	engine.SetStateBlockProvider(stateBlockProviderFor(sessionID, app.Store, todoBroker, PlansDir(engine)))
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
		if errors.Is(err, session.ErrSessionNotFound) {
			// 会话文件已被删除（如清理旧会话后重启）：属正常状态，静默空恢复。
			return
		}
		fmt.Fprintf(os.Stderr, "restore todo snapshot for %s: %v\n", sessionID, err)
		return
	}
	broker.Restore(snapshot, ok)
}

// PlansDir resolves the plan document directory under the workspace root.
func PlansDir(engine *loop.Engine) string {
	root := ""
	if engine != nil {
		root = engine.WorkspaceRoot()
	}
	if strings.TrimSpace(root) == "" {
		root = "."
	}
	return filepath.Join(root, filepath.FromSlash(plan.DefaultDirectory))
}

// ApplyCompressionSettings 把 settings 的 context_compression 应用到引擎。
func ApplyCompressionSettings(engine *loop.Engine, settingsController *settings.Controller) {
	if engine == nil || settingsController == nil {
		return
	}
	cfg := settingsController.CurrentSettings()
	engine.SetContextMode(string(cfg.ContextCompression.Mode))
	engine.SetResumeRecentTurns(cfg.ContextCompression.ResumeRecentTurns)
	engine.SetStateCompactionRatio(cfg.ContextCompression.StateCompactionRatio)
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
	memoryPath := ""
	if home, err := pawpath.Home(); err == nil {
		memoryPath = filepath.Join(home, "memory.md")
	}
	provider := newStateBlockProvider(docStore, broker, sessionID, sessionBase, memoryPath)
	provider.todoStore = store
	if store != nil {
		if goals, err := goal.NewEventStore(sessionBase); err == nil {
			provider.goalStore = goals
		}
	}
	return provider
}
