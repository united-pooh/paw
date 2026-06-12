// 本文件定义 Bubble Tea TUI 对外暴露的 UI 适配器。
package bubble

import (
	"context"
	"errors"
	"fmt"
	tea "github.com/charmbracelet/bubbletea"
	"gocode/internal/message"
	"gocode/internal/model"
	"gocode/internal/ui"
	"os"
	"sync"
)

// Runner 描述 TUI 提交一轮用户输入时需要调用的对话运行器。
type Runner interface {
	RunTurn(ctx context.Context, input string) (message.Message, error)
	ResetHistory()
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

// UI 是基于 Bubble Tea 的交互式终端界面实现。
type UI struct {
	mu                    sync.Mutex
	program               *tea.Program
	modelConfigController ModelConfigController
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

// Run 启动 Bubble Tea 主循环，并把输出写入带光标锚点修正的终端流。
func (u *UI) Run(ctx context.Context, runner Runner, sessionID string) error {
	u.mu.Lock()
	controller := u.modelConfigController
	u.mu.Unlock()

	anchor := newTerminalCursorAnchor()
	appModel := newModel(ctx, runner, sessionID, controller, anchor)
	program := tea.NewProgram(
		appModel,
		tea.WithContext(ctx),
		tea.WithOutput(newAnchoredOutput(os.Stdout, anchor)),
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
