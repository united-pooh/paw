// 本文件定义 Bubble Tea TUI 对外暴露的 UI 适配器。
package bubble

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	tea "github.com/charmbracelet/bubbletea"
	"os"
	"path/filepath"
	configv2 "paw/internal/config"
	"paw/internal/loop"
	coremcp "paw/internal/mcp"
	"paw/internal/message"
	"paw/internal/model"
	"paw/internal/session"
	"paw/internal/settings"
	"paw/internal/skill"
	"paw/internal/task"
	"paw/internal/todo"
	selecttool "paw/internal/tool/select"
	"paw/internal/ui"
	"sync"
)

// Runner 描述 TUI 提交一轮用户输入时需要调用的对话运行器。
type Runner interface {
	RunTurn(ctx context.Context, input string) (message.Message, error)
	ResetHistory()
	LoadHistory(ctx context.Context, sessionID string) ([]message.Message, error)
}

// RichInputRunner is an optional runner capability used for clipboard images.
// Runners that do not implement it continue to receive the compatibility text
// via Runner.RunTurn.
type RichInputRunner interface {
	RunRichTurn(ctx context.Context, input message.Message) (message.Message, error)
}

// ForegroundCancelRunner exposes optional cancellation routing for foreground
// work. It is kept separate from Runner so existing integrations and test
// doubles remain source-compatible.
type ForegroundCancelRunner interface {
	CancelCurrentTool() bool
	CancelTurn()
}

// WorkspaceRootProvider is an optional runner capability used to render
// workspace-relative file targets in the transcript.
type WorkspaceRootProvider interface {
	WorkspaceRoot() string
}

// TimedRunner is an optional capability used to persist and display the
// complete foreground model-turn duration. It is deliberately separate from
// Runner so existing integrations and test doubles remain source-compatible.
type TimedRunner interface {
	loop.TimedRunner
}

// SupplementSubmitter describes runners that can accept instructions while a
// model turn is already running.
type SupplementSubmitter interface {
	SubmitSupplement(input string) bool
}

// SessionStore 描述 TUI 列举和管理会话所需的最小接口。
type SessionStore interface {
	ListSessions(ctx context.Context) ([]session.SessionSummary, error)
}

// ResolvedRecordLoader is an optional restore capability that preserves the
// transcript sequence attached to each message.
type ResolvedRecordLoader interface {
	LoadResolvedRecords(ctx context.Context, sessionID string) ([]session.Record, error)
}

// TodoSnapshotLoader is an optional restore capability for the current
// session's durable Todo state. It is separate from transcript projection so
// context compaction cannot make the UI appear to have lost the latest list.
type TodoSnapshotLoader interface {
	LoadLatestTodoSnapshot(ctx context.Context, sessionID string) (todo.Snapshot, bool, error)
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

// ConfigCenterController is the optional config-v2 capability used by the
// unified /setting and /config center.
type ConfigCenterController interface {
	Snapshot() configv2.Snapshot
	ReloadConfig() error
	RefreshModelDiscovery(context.Context) (configv2.Snapshot, error)
	ConfigPath() string
	SetActiveModelID(string) error
	ActivateCatalogSelection(configv2.CatalogSelection) error
	UpdateConfig(context.Context, uint64, []configv2.Operation) (configv2.Snapshot, error)
	CredentialStore() configv2.CredentialStore
}

// SettingsController 描述运行时读取和保存 UI/agent 设置的控制器。
type SettingsController interface {
	CurrentSettings() settings.Config
	SaveSettings(settings.Config) error
	// UpdateRuntime 只更新内存配置、不写配置文件（/setting 指令的动态开关）。
	UpdateRuntime(settings.Config)
}

// TaskController 描述 TUI 手动启动和查看 task 所需的能力。
type TaskController interface {
	Run(context.Context, task.Request) (task.Result, error)
	Launch(context.Context, task.Request) (task.TaskSnapshot, error)
	ListTasks() []task.TaskSnapshot
}

// TaskUpdateSubscriber is an optional live-update capability. It lets
// the TUI wake immediately when a background task changes state instead of
// waiting for the animation/poll interval.
type TaskUpdateSubscriber interface {
	SubscribeTaskUpdates() (<-chan struct{}, func())
}

// MCPStatusController exposes sanitized MCP runtime state to /mcp.
type MCPStatusController interface {
	ConfigPath() string
	Status() []coremcp.ServerStatus
}

// GoalController exposes safe lifecycle controls for the session Goal mode.
type GoalController interface {
	Start(objective string) (string, error)
	Status() string
	Pause() error
	Resume() error
	Cancel() error
	Budget() string
}

// PlanController exposes lifecycle controls for the standalone Plan mode.
// Plans are spec/scope documents independent of Goals; plan mode authors them
// through a clarification → draft → approval workflow.
type PlanController interface {
	Start(requirement string) (string, error)
	Status() string
	List() string
	Show(id string) string
	Resume() error
	Cancel() error
}

type SessionControllerRebinder interface {
	Rebind(sessionID string) error
}

// UI 是基于 Bubble Tea 的交互式终端界面实现。
type UI struct {
	mu                     sync.Mutex
	program                *tea.Program
	modelConfigController  ModelConfigController
	configCenterController ConfigCenterController
	settingsController     SettingsController
	taskController         TaskController
	sessionStore           SessionStore
	mcpController          MCPStatusController
	goalController         GoalController
	planController         PlanController
	selectionBroker        *selecttool.Broker
	todoBroker             *todo.Broker
	sendMsg                func(tea.Msg)
}

// 确保 UI 满足通用终端 UI 接口。
var _ ui.UI = (*UI)(nil)

// New 创建一个尚未启动的 Bubble Tea UI 实例。
func New() *UI {
	return &UI{}
}

func (u *UI) SetSelectionBroker(broker *selecttool.Broker) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.selectionBroker = broker
}

func (u *UI) SetTodoBroker(broker *todo.Broker) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.todoBroker = broker
}

// SetModelConfigController 注入模型配置控制器，供 /model 向导读取和保存配置。
func (u *UI) SetModelConfigController(controller ModelConfigController) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.modelConfigController = controller
}

// SetConfigCenterController injects Paw's v2 provider/model registry.
func (u *UI) SetConfigCenterController(controller ConfigCenterController) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.configCenterController = controller
}

// SetSettingsController 注入 settings 控制器，供 /setting 和状态栏读取。
func (u *UI) SetSettingsController(controller SettingsController) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.settingsController = controller
}

// SetTaskController 注入 task 控制器，供 /task 和 /tasks 使用。
func (u *UI) SetTaskController(controller TaskController) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.taskController = controller
}

// SetSessionStore 注入 session store，供 /resume 命令列举历史会话。
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

// SetGoalController injects the opt-in Goal lifecycle controller.
func (u *UI) SetGoalController(controller GoalController) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.goalController = controller
}

// SetPlanController injects the opt-in Plan lifecycle controller.
func (u *UI) SetPlanController(controller PlanController) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.planController = controller
}

// NotifyPlanFinalized routes a plan-approval event into the running Bubble
// Tea program so the TUI can switch back to chat mode and start execution.
func (u *UI) NotifyPlanFinalized(path string) error {
	return u.send(planFinalizedMsg{path: path})
}

// NotifyPlanStopped routes a non-final plan-session end (paused/failed/
// cancelled) into the TUI so it releases the plan working state.
func (u *UI) NotifyPlanStopped(reason string) error {
	return u.send(planStoppedMsg{reason: reason})
}

// NotifyGoalStopped routes a goal-session end (completed/failed/cancelled/
// paused) into the TUI so it releases the goal working state. Goals run in
// the background, so this is the only path that unblocks the header spinner.
func (u *UI) NotifyGoalStopped(reason string) error {
	return u.send(goalStoppedMsg{reason: reason})
}

// filterIdleMouseMotion drops passive mouse movement that cannot change UI
// state. All-motion mode is required for real hover events, but Bubble Tea
// otherwise runs Update and View for every reported cell crossed by the mouse.
func filterIdleMouseMotion(model tea.Model, msg tea.Msg) tea.Msg {
	mouse, ok := msg.(tea.MouseMsg)
	if !ok || mouse.Action != tea.MouseActionMotion || mouse.Button != tea.MouseButtonNone {
		return msg
	}
	m, ok := model.(appModel)
	if !ok || m.selecting {
		return msg
	}
	inside := m.transcriptNoticeBounds().contains(mouse.X, mouse.Y)
	noticeHoverChanged := inside != m.newMessageNoticeHovered
	toolHoverIndex := -1
	interactionValid := true
	if !inside {
		toolHoverIndex, interactionValid = m.toolHoverIndexAtMouse(mouse.X, mouse.Y)
		if !interactionValid {
			return msg
		}
	}
	toolHoverChanged := toolHoverIndex != m.toolHoverIndex
	if noticeHoverChanged || toolHoverChanged {
		return msg
	}
	return nil
}

// Run 启动 Bubble Tea 主循环，并把输出写入带光标锚点修正的终端流。
func (u *UI) Run(ctx context.Context, runner Runner, sessionID string) error {
	u.mu.Lock()
	controller := u.modelConfigController
	configCenterController := u.configCenterController
	settingsController := u.settingsController
	taskController := u.taskController
	sessionStore := u.sessionStore
	mcpController := u.mcpController
	goalController := u.goalController
	planController := u.planController
	selectionBroker := u.selectionBroker
	todoBroker := u.todoBroker
	u.mu.Unlock()

	anchor := newTerminalCursorAnchor()
	appModel := newModel(ctx, runner, sessionID, controller, settingsController, taskController, sessionStore, anchor)
	appModel.configCenterController = configCenterController
	if configCenterController != nil && configCenterController.ConfigPath() != "" {
		appModel.skillRegistry = skill.NewRegistry([]string{filepath.Join(filepath.Dir(configCenterController.ConfigPath()), "skills")})
	}
	if configCenterController != nil && !configCenterController.Snapshot().Ready {
		appModel.openConfigCenter()
	}
	if subscriber, ok := taskController.(TaskUpdateSubscriber); ok {
		updates, stopUpdates := subscriber.SubscribeTaskUpdates()
		appModel.taskUpdates = updates
		appModel.taskUpdatesStop = stopUpdates
		defer stopUpdates()
	}
	appModel.selectionBroker = selectionBroker
	appModel.todoBroker = todoBroker
	appModel.workspaceRoot = workspaceRootOf(runner)
	appModel.mcpController = mcpController
	appModel.goalController = goalController
	appModel.planController = planController
	// WithInput 包一层 ESC 聚合 reader：在 BubbleTea 解析字节之前，把被读边界
	// 切断的 \x1b[<...M 鼠标序列重新拼合，从源头杜绝 ESC 与 [ 分离导致的
	// [[[[[[[ 泄漏。reader 内嵌 *os.File，MakeRaw 与 kqueue 路径不受影响。
	output := newAnchoredOutput(os.Stdout, anchor)
	defer output.Close()
	eventFilter := newProgramEventFilter(scheduleTranscriptWheelFlush)
	program := tea.NewProgram(
		appModel,
		tea.WithContext(ctx),
		tea.WithInput(newESCCoalescingReader(os.Stdin)),
		tea.WithOutput(output),
		tea.WithAltScreen(),
		tea.WithMouseAllMotion(),
		tea.WithFilter(eventFilter.Filter),
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

// OnReasoningStart 开始一个有序 reasoning 块。redacted 块只显示占位标题，
// 不会把 provider 的不透明内容暴露给 transcript。
func (u *UI) OnReasoningStart(partIndex int, redacted bool) error {
	return u.send(assistantPartMsg{
		lifecycle:  "start",
		blockIndex: partIndex,
		partType:   "reasoning",
		redacted:   redacted,
	})
}

// OnReasoningDelta 转发一个 reasoning 文本增量。
func (u *UI) OnReasoningDelta(partIndex int, text string) error {
	if text == "" {
		return nil
	}
	return u.send(assistantPartMsg{
		lifecycle:  "delta",
		blockIndex: partIndex,
		partType:   "reasoning",
		delta:      text,
	})
}

// OnReasoningEnd 完成一个 reasoning 块，使其折叠为时长标题。
func (u *UI) OnReasoningEnd(partIndex int) error {
	return u.send(assistantPartMsg{
		lifecycle:  "end",
		blockIndex: partIndex,
		partType:   "reasoning",
	})
}

// OnToolCall 接收工具调用事件，并转发给 Bubble Tea 状态机展示。
func (u *UI) OnToolCall(event ui.ToolCallEvent) error {
	return u.send(cloneToolCallMsg(event))
}

func cloneToolCallMsg(event ui.ToolCallEvent) toolCallMsg {
	event.Input = append(json.RawMessage(nil), event.Input...)
	event.FileMutation = cloneFileMutationSnapshot(event.FileMutation)
	return toolCallMsg(event)
}

// OnToolResult 接收工具结果事件，并转发给 Bubble Tea 状态机展示。
func (u *UI) OnToolResult(event ui.ToolResultEvent) error {
	return u.send(cloneToolResultMsg(event))
}

func cloneToolResultMsg(event ui.ToolResultEvent) toolResultMsg {
	event.FileMutation = cloneFileMutationSnapshot(event.FileMutation)
	return toolResultMsg(event)
}

func cloneFileMutationSnapshot(snapshot *ui.FileMutationSnapshot) *ui.FileMutationSnapshot {
	if snapshot == nil {
		return nil
	}
	copied := *snapshot
	return &copied
}

// OnDone 通知 TUI 当前 assistant 响应已经结束。
func (u *UI) OnDone() error {
	return u.send(doneMsg{})
}

// ConsumesFileMutations opts Bubble into before/after file snapshots.
func (u *UI) ConsumesFileMutations() bool { return true }

// OnSystemMessage 接收后台任务等系统事件，并转发给 Bubble Tea 状态机展示。
func (u *UI) OnSystemMessage(event ui.SystemEvent) error {
	return u.send(systemEventMsg(event))
}

// send 将外部事件安全地投递到正在运行的 Bubble Tea program。
func (u *UI) send(msg tea.Msg) error {
	u.mu.Lock()
	program := u.program
	sendMsg := u.sendMsg
	u.mu.Unlock()
	if sendMsg != nil {
		sendMsg(msg)
		return nil
	}
	if program == nil {
		return fmt.Errorf("bubble UI program is not running")
	}
	program.Send(msg)
	return nil
}
