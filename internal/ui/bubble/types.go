// 本文件集中定义 Bubble Tea TUI 的状态类型、内部消息和模型切换向导数据结构。
package bubble

import (
	"context"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	"paw/internal/loop"
	"paw/internal/model"
	"paw/internal/session"
	"paw/internal/settings"
	"paw/internal/skill"
	"paw/internal/subagent"
	"paw/internal/theme"
	"paw/internal/ui"
	"strings"
	"time"
)

// entryKind 表示 transcript 中一条消息的来源类别。
type entryKind int

type transcriptRenderMode uint8

const (
	transcriptRenderFormatted transcriptRenderMode = iota
	transcriptRenderStreamingPlain
	transcriptRenderPlain
)

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
	kind                  entryKind
	title                 string
	body                  string
	newMessageNoticeCycle uint64       // UI-only marker; 不参与 transcript 文本渲染。
	inputTokens           []inputToken // visual-only metadata; body remains the raw submitted/session text
	color                 string       // 可选：标题颜色（lipgloss 颜色字符串），与 subagents 面板保持一致
	isError               bool         // true for tool results with IsError=true
	toolUseID             string
	toolName              string
	toolStatus            string
	toolTarget            string
	toolResult            string
	toolExpanded          bool
	toolFocused           bool
	toolHovered           bool
	toolResultOnly        bool
	citations             []toolCitation
	createdAt             time.Time
	toolStartedAt         time.Time
	toolFinishedAt        time.Time
	turnMetadata          *session.TurnMetadata
	version               int
	renderMode            transcriptRenderMode
}

type toolCitation struct {
	toolUseID string
	name      string
	target    string
	status    string
	preview   string
	isError   bool
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
// restoreDraft is populated for failed rich-image turns so the user can fix
// the model/endpoint or retry without losing the clipboard image.
type contextCompactionFinishedMsg struct {
	result loop.ContextCompactionResult
	err    error
}

type turnFinishedMsg struct {
	err              error
	metadata         *session.TurnMetadata
	metadataErr      error
	restoreDraft     *inputDraft
	interruptedDraft *inputDraft
}

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
	// modelWizardModel 表示正在选择 provider 下的具体模型。
	modelWizardModel
	// modelWizardConfirm 表示正在确认将要应用的模型配置。
	modelWizardConfirm
)

// modelProviderOption 描述 /model 向导中的一个 provider 选项。
type modelProviderOption struct {
	id          string
	label       string
	description string
	profile     model.Profile
}

// modelWizard 保存 /model 交互向导的临时 UI 状态。
type modelWizard struct {
	step            modelWizardStep
	providerOptions []modelProviderOption
	selectedIndex   int
	selectedModel   int
	modelOptions    []string
	err             string
}

type settingWizardStep int

const (
	settingWizardContext settingWizardStep = iota
	settingWizardRunMode
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
	options := modelProviderOptionsForConfig(current)
	selected := 0
	for i, option := range options {
		if option.id == current.ProfileID || option.id == current.Provider {
			selected = i
			break
		}
	}
	return &modelWizard{
		step:            modelWizardProvider,
		providerOptions: options,
		selectedIndex:   selected,
	}
}

func modelProviderOptionsForConfig(current model.Config) []modelProviderOption {
	profiles := model.ConfiguredProfiles(current)
	options := make([]modelProviderOption, 0, len(profiles))
	for _, profile := range profiles {
		label := strings.TrimSpace(profile.Name)
		if label == "" {
			label = strings.TrimSpace(profile.Provider)
		}
		if label == "" {
			label = strings.TrimSpace(profile.ID)
		}
		description := strings.TrimSpace(profile.Transport)
		if description == "" {
			description = strings.TrimSpace(profile.APIBaseURL)
		}
		options = append(options, modelProviderOption{
			id:          profile.ID,
			label:       label,
			description: description,
			profile:     profile,
		})
	}
	return options
}

// selectedProvider 返回当前选中的 provider，并在索引越界时进行纠正。
func (w *modelWizard) selectedProvider() modelProviderOption {
	if w == nil || len(w.providerOptions) == 0 {
		return modelProviderOption{}
	}
	if w.selectedIndex < 0 {
		w.selectedIndex = 0
	}
	if w.selectedIndex >= len(w.providerOptions) {
		w.selectedIndex = len(w.providerOptions) - 1
	}
	return w.providerOptions[w.selectedIndex]
}

func (w *modelWizard) selectedModelName() string {
	if w == nil || w.selectedModel < 0 || w.selectedModel >= len(w.modelOptions) {
		return ""
	}
	return w.modelOptions[w.selectedModel]
}

func (w *modelWizard) selectedModelNameOr(fallback string) string {
	if selected := w.selectedModelName(); selected != "" {
		return selected
	}
	return fallback
}

// sessionsLoadedMsg 携带异步加载完成的会话列表。
type sessionsLoadedMsg struct {
	sessions []sessionSummaryItem
	err      error
}

// sessionRestoredMsg 携带历史恢复完成的结果。
type sessionRestoredMsg struct {
	sessionID       string
	entries         []transcriptEntry
	source          sessionRestoreSource
	subagentPreview *subagentTranscriptPreview
	err             error
}

type sessionRestoreSource int

const (
	sessionRestorePicker sessionRestoreSource = iota
	sessionRestoreSubagentEnter
)

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

type subagentPicker struct {
	tasks         []subagent.TaskSnapshot
	selectedIndex int
	tab           activityTab
}

type subagentTranscriptPreview struct {
	task             subagent.TaskSnapshot
	sessionID        string
	parentSessionID  string
	parentTranscript []transcriptEntry
	entries          []transcriptEntry
	liveContent      string
}

// appModel 是 Bubble Tea TUI 的唯一状态中心。
type appModel struct {
	ctx                       context.Context
	theme                     theme.Theme
	styles                    StyleSet
	themePicker               *themePickerState
	runner                    Runner
	sessionID                 string
	modelConfig               ModelConfigController
	settingsConfig            SettingsController
	subagents                 SubagentController
	sessionStore              SessionStore
	mcpController             MCPStatusController
	commandRegistry           *CommandRegistry
	skillRegistry             *skill.Registry
	queryGuard                QueryGuard
	chatQueue                 CommandQueue
	cursorAnchor              *terminalCursorAnchor
	input                     textarea.Model
	viewport                  viewport.Model
	width                     int
	height                    int
	ready                     bool
	running                   bool
	runningTerminal           bool
	terminalMode              bool
	terminalPreview           bool
	hasInteracted             bool
	showThinking              bool
	selecting                 bool
	selectionActive           bool
	selectionStart            selectionPoint
	selectionEnd              selectionPoint
	cursorFrameAt             time.Time
	uiAnimationFrameScheduled bool
	turnStartedAt             time.Time
	turnID                    string
	contextMeter              contextMeterAnimation
	pending                   []inputDraft
	inputTokens               []inputToken
	submittedDraft            inputDraft
	inputHistory              []inputDraft
	historyIndex              int
	historyDraft              inputDraft
	historyDownLock           bool
	inputPasteFoldActive      bool
	transcript                []transcriptEntry
	transcriptRenderCache     []transcriptRenderCacheEntry
	transcriptRefreshPending  bool
	transcriptKeyScrollActive bool
	newMessageNoticeCycle     uint64
	newMessageNoticeCount     int
	newMessageNoticeHovered   bool
	newMessageNoticePressed   bool
	rawMouseEscapePending     string
	toolInspectActive         bool
	toolInspectIndex          int
	toolHoverIndex            int
	lastTranscriptRefreshAt   time.Time
	lastToolProgressSecond    int64
	activeAssistant           int
	activeThinking            int
	activeTurnUserEntry       int
	doneAssistant             int
	assistantStream           streamLineBuffer
	thinkingStream            streamLineBuffer
	pendingToolCites          []toolCitation
	isGenerating              bool
	turnHasModelOutput        bool
	modelCancelRequested      bool
	activeModelCancel         context.CancelFunc
	lastCtrlCAt               time.Time // 追踪双击 Ctrl+C 退出
	modelWizard               *modelWizard
	settingWizard             *settingWizard
	sessionPicker             *sessionPicker
	subagentPicker            *subagentPicker
	subagentPreview           *subagentTranscriptPreview
	completion                *completion
	pipelineState             pipelineState
	pipelineActiveAfter       time.Time
	spinnerFrameIdx           int       // Activity 中 running 条目的动画帧索引，由 cursorFrameMsg 驱动
	waveAmpTarget             bool      // 均衡器波浪振幅目标态（= isGenerating），翻转时更新
	waveAmpStartedAt          time.Time // 振幅目标态确立时刻，用于缓动进度
	waveAmpFrom               float64   // 过渡起点振幅，反向退场时从此值缓降
	waveAmpCurrent            float64   // 当前振幅（cursorFrameMsg 每帧更新，渲染只读）
	tokenRippleHideAt         time.Time // 回答完成后 Ripple 退场的截止时刻
	worktreeCWD               string
	worktree                  worktreeSnapshot
	worktreeReader            worktreeStatusReader
}

type activityTab int

const (
	activityTabSubagents activityTab = iota
	activityTabPipeline
)

// pipelinePhaseStatus 标记单个 pipeline 阶段的状态。
type pipelinePhaseStatus int

const (
	phaseStatusPending pipelinePhaseStatus = iota
	phaseStatusDone
	phaseStatusActive
	phaseStatusRetry
)

// pipelinePhaseEntry 单个阶段的状态快照。
type pipelinePhaseEntry struct {
	name      string
	artifact  string // 相对 .pipeline-workspace/ 的文件名
	status    pipelinePhaseStatus
	iteration int
}

// pipelineState 完整 pipeline 状态快照（每 500ms 轮询更新）。
type pipelineState struct {
	detected   bool // 本次 TUI 会话检测到活跃 pipeline
	activeIdx  int  // 当前 active 阶段索引（-1 = none）
	globalIter int  // 全局迭代（来自 execution-report.json.iteration）
	doneCount  int  // 已完成阶段数
	phases     [18]pipelinePhaseEntry
}

// pipelineStateUpdatedMsg 由后台轮询 cmd 发回，携带最新 pipelineState。
type pipelineStateUpdatedMsg struct {
	state pipelineState
}

type contextStatsProvider interface {
	ContextStats(limitTokens int, draft string) loop.ContextStats
}

type contextCompactor interface {
	CompactContext(context.Context, string) (loop.ContextCompactionResult, error)
}

type tokenTracerURLProvider interface {
	TokenTracerURL() string
}
