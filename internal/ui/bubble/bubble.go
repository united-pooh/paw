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

type Runner interface {
	RunTurn(ctx context.Context, input string) (message.Message, error)
	ResetHistory()
}

type ModelConfigController interface {
	CurrentModelConfig() model.Config
	ApplyModelConfig(model.Config) error
}

type ModelConfigSaver interface {
	SaveModelConfig(model.Config) error
}

type UI struct {
	mu                    sync.Mutex
	program               *tea.Program
	modelConfigController ModelConfigController
}

var _ ui.UI = (*UI)(nil)

func New() *UI {
	return &UI{}
}

func (u *UI) SetModelConfigController(controller ModelConfigController) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.modelConfigController = controller
}

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

func (u *UI) OnAssistantDelta(text string) error {
	if text == "" {
		return nil
	}
	return u.send(assistantDeltaMsg(text))
}

func (u *UI) OnToolCall(event ui.ToolCallEvent) error {
	return u.send(toolCallMsg(event))
}

func (u *UI) OnToolResult(event ui.ToolResultEvent) error {
	return u.send(toolResultMsg(event))
}

func (u *UI) OnDone() error {
	return u.send(doneMsg{})
}

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
