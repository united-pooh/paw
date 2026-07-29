// 本文件定义 Bubble Tea TUI 对外暴露的 UI 适配器。
package bubble

import (
	"context"
	"errors"
	"fmt"
	tea "github.com/charmbracelet/bubbletea"
	"os"
	coremcp "paw/internal/mcp"
	"paw/internal/message"
	"paw/internal/model"
	"paw/internal/session"
	"paw/internal/settings"
	"paw/internal/subagent"
	"paw/internal/ui"
	"sync"
)

// Runner 描述 TUI 提交一轮用户输入时需要调用的对话运行器。
type Runner interface {
	RunTurn(ctx context.Context, input string) (message.Message, error)
	ResetHistory()
	LoadHistory(ctx context.Context, sessionID string) ([]message.Message, error)
}

// SupplementSubmitter describes runners that can accept instructions while a
// model turn is already running.
type SupplementSubmitter interface {
	SubmitSupplement(input string) bool
}

// SupplementStatsProvider exposes pending supplement counts for status UI.
type SupplementStatsProvider interface {
	PendingSupplementCount() int
}

// SessionStore 描述 TUI 列举和管理会话所需的最小接口。
type SessionStore interface {
	ListSessions(ctx context.Context) ([]session.SessionSummary, error)
}

// ModelConfigController 描述运行时读取和应用模型配置的控制器。
type ModelConfigController interface {
	CurrentModelConfig() model.Config
	ApplyModelConfig(model.Config) error
}

// ModelConfigSaver 描述可将模型配置持久化到磁盘的控制器扩展。
type ModelConfigSaver interface {
	SaveModelConfig(model.Config) error
}

// SettingsController 描述运行时读取和保存 UI/agent 设置的控制器。
type SettingsController interface {
	CurrentSettings() settings.Config
	SaveSettings(settings.Config) error
}

// SubagentController 描述 TUI 手动启动和查看 subagent 所需的能力。
type SubagentController interface {
	Run(context.Context, subagent.Request) (subagent.Result, error)
	Launch(context.Context, subagent.Request) (subagent.TaskSnapshot, error)
	ListTasks() []subagent.TaskSnapshot
}

// MCPStatusController exposes sanitized MCP runtime state to /mcp.
type MCPStatusController interface {
	ConfigPath() string
	Status() []coremcp.ServerStatus
}

// UI 是基于 Bubble Tea 的交互式终端界面实现。
type UI struct {
	mu                    sync.Mutex
	program               *tea.Program
	modelConfigController ModelConfigController
	settingsController    SettingsController
	subagentController    SubagentController
	sessionStore          SessionStore
	mcpController         MCPStatusController
}

// 确保 UI 满足通用终端 UI 接口。
var _ ui.UI = (*UI)(nil)

// New 创建一个尚未启动的 Bubble Tea UI 实例。
func New() *UI {
	return &UI{}
}

// SetModelConfigController 注入模型配置控制器，供 /model 向导读取和保存配置。
func (u *UI) SetModelConfigController(controller ModelConfigController) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.modelConfigController = controller
}

// SetSettingsController 注入 settings 控制器，供 /setting 和状态栏读取。
func (u *UI) SetSettingsController(controller SettingsController) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.settingsController = controller
}

// SetSubagentController 注入 subagent 控制器，供 /subagent 和 /tasks 使用。
func (u *UI) SetSubagentController(controller SubagentController) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.subagentController = controller
}

// SetSessionStore 注入 session store，供 /sessions 命令列举历史会话。
func (u *UI) SetSessionStore(store SessionStore) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.sessionStore = store
}

// SetMCPStatusController injects MCP runtime state for the /mcp command.
func (u *UI) SetMCPStatusController(controller MCPStatusController) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.mcpController = controller
}

// Run 启动 Bubble Tea 主循环，并把输出写入带光标锚点修正的终端流。
func (u *UI) Run(ctx context.Context, runner Runner, sessionID string) error {
	u.mu.Lock()
	controller := u.modelConfigController
	settingsController := u.settingsController
	subagentController := u.subagentController
	sessionStore := u.sessionStore
	mcpController := u.mcpController
	u.mu.Unlock()

	anchor := newTerminalCursorAnchor()
	appModel := newModel(ctx, runner, sessionID, controller, settingsController, subagentController, sessionStore, anchor)
	appModel.mcpController = mcpController
	// WithInput 包一层 ESC 聚合 reader：在 BubbleTea 解析字节之前，把被读边界
	// 切断的 \x1b[<...M 鼠标序列重新拼合，从源头杜绝 ESC 与 [ 分离导致的
	// [[[[[[[ 泄漏。reader 内嵌 *os.File，MakeRaw 与 kqueue 路径不受影响。
	program := tea.NewProgram(
		appModel,
		tea.WithContext(ctx),
		tea.WithInput(newESCCoalescingReader(os.Stdin)),
		tea.WithOutput(newAnchoredOutput(os.Stdout, anchor)),
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
	)

	u.mu.Lock()
	u.program = program
	u.mu.Unlock()

	_, err := program.Run()

	u.mu.Lock()
	if u.program == program {
		u.program = nil
	}
	u.mu.Unlock()

	if errors.Is(err, tea.ErrInterrupted) {
		return nil
	}
	return err
}

// OnAssistantDelta 接收模型流式文本增量，并转发给 Bubble Tea 状态机。
func (u *UI) OnAssistantDelta(text string) error {
	if text == "" {
		return nil
	}
	return u.send(assistantDeltaMsg(text))
}

// OnThinkingDelta 接收模型 thinking 流，并转发给 Bubble Tea 状态机。
func (u *UI) OnThinkingDelta(text string) error {
	if text == "" {
		return nil
	}
	return u.send(thinkingDeltaMsg(text))
}

// OnToolCall 接收工具调用事件，并转发给 Bubble Tea 状态机展示。
func (u *UI) OnToolCall(event ui.ToolCallEvent) error {
	return u.send(toolCallMsg(event))
}

// OnToolResult 接收工具结果事件，并转发给 Bubble Tea 状态机展示。
func (u *UI) OnToolResult(event ui.ToolResultEvent) error {
	return u.send(toolResultMsg(event))
}

// OnDone 通知 TUI 当前 assistant 响应已经结束。
func (u *UI) OnDone() error {
	return u.send(doneMsg{})
}

// ConsumesOldContent 声明 bubble UI 会消费 OldContent 用于 diff 展示。
func (u *UI) ConsumesOldContent() bool { return true }

// OnSystemMessage 接收后台任务等系统事件，并转发给 Bubble Tea 状态机展示。
func (u *UI) OnSystemMessage(event ui.SystemEvent) error {
	return u.send(systemEventMsg(event))
}

// send 将外部事件安全地投递到正在运行的 Bubble Tea program。
func (u *UI) send(msg tea.Msg) error {
	u.mu.Lock()
	program := u.program
	u.mu.Unlock()
	if program == nil {
		return fmt.Errorf("bubble UI program is not running")
	}
	program.Send(msg)
	return nil
}
