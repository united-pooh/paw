// 本文件集中定义 Bubble Tea TUI 的状态类型、内部消息和模型切换向导数据结构。
package bubble

import (
	"context"
	"encoding/json"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	"paw/internal/loop"
	"paw/internal/model"
	"paw/internal/session"
	"paw/internal/settings"
	"paw/internal/skill"
	"paw/internal/subagent"
	"paw/internal/theme"
	"paw/internal/todo"
	selecttool "paw/internal/tool/select"
	"paw/internal/ui"
	"strings"
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
	// entryTodo 表示 Agent 提交的一次 Todo 完整快照。
	entryTodo
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
	toolInput             json.RawMessage
	fileMutationKnown     bool
	isFileMutation        bool
	toolResult            string
	toolExpanded          bool
	toolGroupPending      bool
	toolGroupOpen         bool
	toolFocused           bool
	toolHovered           bool
	toolResultOnly        bool
	todoSnapshot          *todo.Snapshot
	todoExpanded          bool
	todoLatest            bool
	todoCompletedFold     bool
	todoCleared           bool
	citations             []toolCitation
	// subagentWaitRunning 标记一条 SubagentWait 状态行：运行中显示
	// "子智能体 <名字> 正在运行 Ns"，工具完成/失败后整行从 transcript 移除，
	// 不渲染为可折叠的 Tools 调用块。
	subagentWaitRunning bool
	subagentWaitNames   []string
	createdAt           time.Time
	toolStartedAt       time.Time
	toolFinishedAt      time.Time
	turnMetadata        *session.TurnMetadata
	version             int
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

// selectionMode 表示鼠标拖拽选区时使用的吸附模式。
type selectionMode int

const (
	// selectionModeChar 按单个字符扩展选区（默认拖拽）。
	selectionModeChar selectionMode = iota
	// selectionModeWord 按词边界扩展选区（双击起点）。
	selectionModeWord
	// selectionModeLine 按整行扩展选区（三击起点）。
	selectionModeLine
)

// transcriptClickActionMsg 在双击判定窗口结束后派发延迟的单击动作
// （链接 / todo / 工具行展开）。双击窗口内到达第二次按下时，seq 失配使
// 消息被丢弃，避免「单击已生效、双击又选中」互相冲突。
type transcriptClickActionMsg struct {
	seq   uint64
	point selectionPoint
}

// copyToastExpiredMsg 复制反馈 toast 到期，清除状态栏提示。
type copyToastExpiredMsg struct{}

// translateResultMsg 携带单次翻译请求的返回（原始文本 + 错误）。
// seq 与 appModel.translateSeq 比对，过期响应直接丢弃。
type translateResultMsg struct {
	seq  uint64
	word string
	text string
	err  error
}

// translatePanelState 是翻译面板的展示状态。
type translatePanelState int

const (
	translatePanelLoading translatePanelState = iota
	translatePanelDone
	translatePanelError
)

// translatePanel 保存「双击选中词翻译」面板的临时状态。
type translatePanel struct {
	state       translatePanelState
	word        string // 原始选中词（面板标题）
	phonetic    string // IPA 音标（英文词）
	pos         string // 词性（英文词）
	translation string // 释义
	note        string // 用法/例句备注（可选）
	err         string // state==Error 时的错误描述
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
	settingWizardTranslate
	settingWizardConfirm
)

type settingOption struct {
	label       string
	description string
	// sessionOnly 标记该选项只更新运行期内存、不写入配置文件
	// （/setting 面板里的「动态开关」入口）。
	sessionOnly bool
	apply       func(*settings.Config)
}

type settingWizard struct {
	step     settingWizardStep
	selected map[settingWizardStep]int
	draft    settings.Config
	err      string
	// sessionOnly 为 true 时，confirm 步骤只调用 UpdateRuntime 应用
	// draft，不写配置文件（与 settingOption.sessionOnly 联动）。
	sessionOnly bool
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
	currentTodo     todo.Snapshot
	hasCurrentTodo  bool
	todoWasCleared  bool
	latestTodoIndex int
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
	lastUsedAt     time.Time
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
	ctx                        context.Context
	theme                      theme.Theme
	styles                     StyleSet
	themePicker                *themePickerState
	runner                     Runner
	workspaceRoot              string
	selectionBroker            *selecttool.Broker
	selectionDock              *selectionDock
	todoBroker                 *todo.Broker
	currentTodo                todo.Snapshot
	hasCurrentTodo             bool
	todoWasCleared             bool
	latestTodoIndex            int
	todoPage                   *todoPage
	sessionID                  string
	modelConfig                ModelConfigController
	settingsConfig             SettingsController
	subagents                  SubagentController
	sessionStore               SessionStore
	mcpController              MCPStatusController
	commandRegistry            *CommandRegistry
	skillRegistry              *skill.Registry
	queryGuard                 QueryGuard
	chatQueue                  CommandQueue
	queueMode                  queueInteractionMode
	queueSelectedID            string
	queueEdit                  *queueEditState
	inputSource                inputSource
	cursorAnchor               *terminalCursorAnchor
	input                      textarea.Model
	viewport                   viewport.Model
	width                      int
	height                     int
	ready                      bool
	running                    bool
	runningTerminal            bool
	terminalMode               bool
	terminalPreview            bool
	hasInteracted              bool
	showThinking               bool
	selecting                  bool
	selectionActive            bool
	selectionStart             selectionPoint
	selectionEnd               selectionPoint
	selectionMode              selectionMode
	selectionAnchor            selectionPoint
	selectionMoved             bool
	clickCount                 int
	lastClickAt                time.Time
	lastClickPoint             selectionPoint
	clickActionSeq             uint64
	clickActionPending         bool
	copyToast                  string
	copyToastUntil             time.Time
	translatePanel             *translatePanel
	translateSeq               uint64
	cursorFrameAt              time.Time
	uiAnimationFrameScheduled  bool
	turnStartedAt              time.Time
	turnID                     string
	contextMeter               contextMeterAnimation
	pending                    []inputDraft
	inputTokens                []inputToken
	submittedDraft             inputDraft
	inputHistory               []inputDraft
	historyIndex               int
	historyDraft               inputDraft
	historyDownLock            bool
	inputPasteFoldActive       bool
	transcript                 []transcriptEntry
	transcriptRenderCache      []transcriptRenderCacheEntry
	transcriptRenderSignature  uint64
	transcriptRenderedContent  string
	transcriptContentCached    bool
	transcriptLineCache        []transcriptLineSnapshot
	transcriptLineCacheReady   bool
	transcriptLocationCache    []transcriptEntryLocation
	transcriptLocationsReady   bool
	transcriptRefreshPending   bool
	transcriptRefreshPendingAt time.Time // pending 置位时刻，用于帧窗口判定（不早于 interval 才刷）
	transcriptKeyScrollActive  bool
	newMessageNoticeCycle      uint64
	newMessageNoticeCount      int
	newMessageNoticeHovered    bool
	newMessageNoticePressed    bool
	rawMouseEscapePending      string
	toolInspectActive          bool
	toolInspectIndex           int
	toolHoverIndex             int
	toolGroupExpanded          bool
	toolGroupFullResult        bool
	lastTranscriptRefreshAt    time.Time
	lastToolProgressSecond     int64
	activeAssistant            int
	activeThinking             int
	activeTurnUserEntry        int
	doneAssistant              int
	assistantStream            streamLineBuffer
	thinkingStream             streamLineBuffer
	pendingToolCites           []toolCitation
	isGenerating               bool
	turnHasModelOutput         bool
	modelCancelRequested       bool
	activeModelCancel          context.CancelFunc
	lastCtrlCAt                time.Time // 追踪双击 Ctrl+C 退出
	modelWizard                *modelWizard
	settingWizard              *settingWizard
	sessionPicker              *sessionPicker
	subagentPicker             *subagentPicker
	subagentPreview            *subagentTranscriptPreview
	completion                 *completion
	pipelineState              pipelineState
	pipelineActiveAfter        time.Time
	lastActivityPollAt         time.Time // Activity 面板 ListTasks 刷新的节流时间戳
	spinnerFrameIdx            int       // Activity 中 running 条目的动画帧索引，由 cursorFrameMsg 驱动
	waveAmpTarget              bool      // 均衡器波浪振幅目标态（= isGenerating），翻转时更新
	waveAmpStartedAt           time.Time // 振幅目标态确立时刻，用于缓动进度
	waveAmpFrom                float64   // 过渡起点振幅，反向退场时从此值缓降
	waveAmpCurrent             float64   // 当前振幅（cursorFrameMsg 每帧更新，渲染只读）
	tokenRippleHideAt          time.Time // 回答完成后 Ripple 退场的截止时刻
	worktreeCWD                string
	worktree                   worktreeSnapshot
	worktreeReader             worktreeStatusReader
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
