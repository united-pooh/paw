// 本文件集中定义 Bubble Tea TUI 的状态类型、内部消息和模型切换向导数据结构。
package bubble

import (
	"context"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	"gocode/internal/loop"
	"gocode/internal/model"
	"gocode/internal/settings"
	"gocode/internal/subagent"
	"gocode/internal/ui"
	"time"
)

// entryKind 表示 transcript 中一条消息的来源类别。
type entryKind int

// transcript 条目类型常量。
const (
	// entrySystem 表示系统状态或命令提示消息。
	entrySystem entryKind = iota
	// entryUser 表示用户输入消息。
	entryUser
	// entryAssistant 表示 assistant 的模型输出消息。
	entryAssistant
	// entryThinking 表示模型 thinking 过程消息。
	entryThinking
	// entryTool 表示工具调用、工具结果或终端命令消息。
	entryTool
	// entryError 表示错误消息。
	entryError
)

// transcriptEntry 是聊天历史区的一条可渲染记录。
type transcriptEntry struct {
	kind      entryKind
	title     string
	body      string
	createdAt time.Time
}

// selectionPoint 表示 transcript 渲染结果中的一个显示单元格坐标。
type selectionPoint struct {
	row int
	col int
}

// assistantDeltaMsg 表示模型流式输出的一段文本增量。
type assistantDeltaMsg string

// thinkingDeltaMsg 表示模型 thinking 流式输出的一段文本增量。
type thinkingDeltaMsg string

// toolCallMsg 表示工具调用事件。
type toolCallMsg ui.ToolCallEvent

// toolResultMsg 表示工具结果事件。
type toolResultMsg ui.ToolResultEvent

// systemEventMsg 表示后台任务等系统事件。
type systemEventMsg ui.SystemEvent

// doneMsg 表示当前 assistant 输出已经结束。
type doneMsg struct{}

// turnFinishedMsg 表示一轮模型调用已经结束，并携带可能的错误。
type turnFinishedMsg struct{ err error }

// shellFinishedMsg 表示一次终端命令执行完成后的结果。
type shellFinishedMsg struct {
	command string
	stdout  string
	stderr  string
	err     error
}

type subagentFinishedMsg struct {
	result subagent.Result
	err    error
}

// cursorFrameMsg 驱动自定义光标动画更新一帧。
type cursorFrameMsg time.Time

// modelWizardStep 表示 /model 向导当前所处的步骤。
type modelWizardStep int

// /model 向导步骤常量。
const (
	// modelWizardProvider 表示正在选择 provider。
	modelWizardProvider modelWizardStep = iota
	// modelWizardConfirm 表示正在确认将要应用的模型配置。
	modelWizardConfirm
)

// modelProviderOption 描述 /model 向导中的一个 provider 选项。
type modelProviderOption struct {
	id          string
	label       string
	description string
}

// modelProviderOptions 是当前 TUI 支持切换的 provider 列表。
var modelProviderOptions = []modelProviderOption{
	{
		id:          model.ProviderCustom,
		label:       "custom",
		description: "Local or custom OpenAI-compatible endpoint",
	},
	{
		id:          model.ProviderDeepSeek,
		label:       "deepseek",
		description: "DeepSeek official OpenAI-compatible endpoint",
	},
}

// modelWizard 保存 /model 交互向导的临时 UI 状态。
type modelWizard struct {
	step          modelWizardStep
	selectedIndex int
	err           string
}

type settingWizardStep int

const (
	settingWizardContext settingWizardStep = iota
	settingWizardRunMode
	settingWizardMeterLocation
	settingWizardLimit
	settingWizardConfirm
)

type settingOption struct {
	label       string
	description string
	apply       func(*settings.Config)
}

type settingWizard struct {
	step     settingWizardStep
	selected map[settingWizardStep]int
	draft    settings.Config
	err      string
}

// contextMeterAnimation 记录 context 进度条从旧 token 用量跳到新用量的短时动画。
type contextMeterAnimation struct {
	initialized bool
	startedAt   time.Time
	fromUsed    float64
	fromCache   float64
	targetUsed  int
	targetCache int
}

// newModelWizard 根据当前配置创建 provider 选择向导，并默认选中当前 provider。
func newModelWizard(current model.Config) *modelWizard {
	selected := 0
	for i, option := range modelProviderOptions {
		if option.id == current.Provider {
			selected = i
			break
		}
	}
	return &modelWizard{step: modelWizardProvider, selectedIndex: selected}
}

// selectedProvider 返回当前选中的 provider，并在索引越界时进行纠正。
func (w *modelWizard) selectedProvider() modelProviderOption {
	if w == nil || len(modelProviderOptions) == 0 {
		return modelProviderOption{}
	}
	if w.selectedIndex < 0 {
		w.selectedIndex = 0
	}
	if w.selectedIndex >= len(modelProviderOptions) {
		w.selectedIndex = len(modelProviderOptions) - 1
	}
	return modelProviderOptions[w.selectedIndex]
}

// sessionsLoadedMsg 携带异步加载完成的会话列表。
type sessionsLoadedMsg struct {
	sessions []sessionSummaryItem
	err      error
}

// sessionRestoredMsg 携带历史恢复完成的结果。
type sessionRestoredMsg struct {
	sessionID string
	err       error
}

// fileCompletionLoadedMsg 携带异步加载完成的文件补全列表。
type fileCompletionLoadedMsg struct {
	searchDir string   // 对应的搜索目录，用于丢弃过期结果
	items     []string // 目录内的全部条目（未过滤）
	filtered  []string // 按前缀过滤后的结果
	err       error
}

// sessionSummaryItem 是 session picker 中的一行显示项。
type sessionSummaryItem struct {
	sessionID      string
	createdAt      time.Time
	firstMessage   string
	transcriptSize int64
}

// appModel 是 Bubble Tea TUI 的唯一状态中心。
type appModel struct {
	ctx             context.Context
	runner          Runner
	sessionID       string
	modelConfig     ModelConfigController
	settingsConfig  SettingsController
	subagents       SubagentController
	sessionStore    SessionStore
	commandRegistry *CommandRegistry
	queryGuard      QueryGuard
	chatQueue       CommandQueue
	cursorAnchor    *terminalCursorAnchor
	input           textarea.Model
	viewport        viewport.Model
	width           int
	height          int
	ready           bool
	running         bool
	runningTerminal bool
	terminalMode    bool
	terminalPreview bool
	showThinking    bool
	selecting       bool
	selectionActive bool
	selectionStart  selectionPoint
	selectionEnd    selectionPoint
	cursorFrameAt   time.Time
	turnStartedAt   time.Time
	contextMeter    contextMeterAnimation
	pending         []string
	inputHistory    []string
	historyIndex    int
	historyDraft    string
	historyDownLock bool
	transcript      []transcriptEntry
	activeAssistant int
	isGenerating    bool
	modelWizard     *modelWizard
	settingWizard   *settingWizard
	sessionPicker   *sessionPicker
	completion      *completion
}

type contextStatsProvider interface {
	ContextStats(limitTokens int, draft string) loop.ContextStats
}
