// 本文件集中定义 Bubble Tea TUI 的状态类型、内部消息和模型切换向导数据结构。
package bubble

import (
	"context"
	"encoding/json"
	"github.com/charmbracelet/bubbles/textarea"
	configv2 "paw/internal/config"
	"paw/internal/loop"
	"paw/internal/model"
	"paw/internal/session"
	"paw/internal/settings"
	"paw/internal/skill"
	"paw/internal/task"
	"paw/internal/theme"
	"paw/internal/todo"
	selecttool "paw/internal/tool/select"
	"paw/internal/ui"
	"paw/internal/ui/bubble/viewportx"
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
	// entryThinking 表示模型 thinking 过程消息（旧路径，逐步被 entryReasoning 取代）。
	entryThinking
	// entryReasoning 表示模型思考过程消息（有序 assistant part 路径）。
	entryReasoning
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
	color                 string       // 可选：标题颜色（lipgloss 颜色字符串），与 taskController 面板保持一致
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
	citations             []toolCitation
	// taskWaitRunning 标记一条 TaskWait 状态行：运行中显示
	// "子智能体 <名字> 正在运行 Ns"，工具完成/失败后整行从 transcript 移除，
	// 不渲染为可折叠的 Tools 调用块。
	taskWaitRunning bool
	taskWaitNames   []string
	createdAt       time.Time
	toolStartedAt   time.Time
	toolFinishedAt  time.Time
	turnMetadata    *session.TurnMetadata
	version         int
	// reasoning 特定字段
	redacted              bool
	reasoningPartIndex    int // provider block index
	reasoningStartedAt    *time.Time
	reasoningFinishedAt   *time.Time
	reasoningExpansionSet bool // UI-only local override; never persisted
	reasoningExpanded     bool // value used when reasoningExpansionSet is true
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

// assistantPartMsg 表示有序助理 part 生命周期事件（从模型流接收）。
type assistantPartMsg struct {
	lifecycle  string // "start" | "delta" | "end"
	blockIndex int
	partType   string // "reasoning" | "text" | "tool_call"
	delta      string
	redacted   bool
}

// toolCallMsg 表示工具调用事件。
type toolCallMsg ui.ToolCallEvent

// toolResultMsg 表示工具结果事件。
type toolResultMsg ui.ToolResultEvent

// systemEventMsg 表示后台任务等系统事件。
type systemEventMsg ui.SystemEvent

// doneMsg 表示当前 assistant 输出已经结束。
type doneMsg struct{}

// planFinalizedMsg 表示 plan 会话已定稿（用户选择"执行"且文档已落盘）。
// Path 指向已批准的 plan 文档，UI 收到后切回 chat 模式并自动开始执行轮。
type planFinalizedMsg struct {
	path string
}

// planStoppedMsg 表示 plan 会话非定稿结束（暂停/失败/取消），UI 释放
// planWorking 并回到 chat 模式。
type planStoppedMsg struct {
	reason string
}

// goalStoppedMsg 表示 goal 会话结束（完成/失败/取消/暂停），UI 释放
// goalWorking 并回到 chat 模式。goal 在后台长期运行，UI 无法自行感知
// 结束时刻，必须由 goal 控制器经 NotifyGoalStopped 投递。
type goalStoppedMsg struct {
	reason string
}

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

type taskFinishedMsg struct {
	result task.Result
	err    error
}

type taskUpdateMsg struct {
	closed bool
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
	step              modelWizardStep
	providerOptions   []modelProviderOption
	selectedIndex     int
	selectedModel     int
	modelOptions      []string
	modelSelections   []configv2.CatalogSelection
	catalogSelections []configv2.CatalogSelection
	catalogActiveID   string
	catalogBound      bool
	err               string
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

// newModelWizard 根据当前配置创建 provider 选择向导，并默认选中当前 provider。
// When a config-v2 Snapshot is supplied, the wizard pins every displayed model
// to the exact catalog identity, source, and revision observed at open time.
func newModelWizard(current model.Config, snapshots ...configv2.Snapshot) *modelWizard {
	options := modelProviderOptionsForConfig(current)
	selected := 0
	for i, option := range options {
		if option.id == current.ProfileID || option.id == current.Provider {
			selected = i
			break
		}
	}
	wizard := &modelWizard{
		step:            modelWizardProvider,
		providerOptions: options,
		selectedIndex:   selected,
	}
	if len(snapshots) == 0 {
		return wizard
	}
	snapshot := snapshots[0]
	wizard.catalogBound = true
	wizard.catalogActiveID = snapshot.ActiveModelID
	for _, id := range sortedCatalogModelIDs(snapshot.EffectiveModels) {
		selection, err := snapshot.CatalogSelection(id)
		if err == nil {
			wizard.catalogSelections = append(wizard.catalogSelections, selection)
		}
	}
	return wizard
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

func (w *modelWizard) selectedCatalogSelection() (configv2.CatalogSelection, bool) {
	if w == nil || w.selectedModel < 0 || w.selectedModel >= len(w.modelSelections) {
		return configv2.CatalogSelection{}, false
	}
	selection := w.modelSelections[w.selectedModel]
	return selection, selection.ID != ""
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
	sessionID      string
	entries        []transcriptEntry
	source         sessionRestoreSource
	taskPreview    *taskTranscriptPreview
	currentTodo    todo.Snapshot
	hasCurrentTodo bool
	todoWasCleared bool
	err            error
}

type sessionRestoreSource int

const (
	sessionRestorePicker sessionRestoreSource = iota
	sessionRestoreTaskEnter
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

type taskPicker struct {
	tasks         []task.TaskSnapshot
	selectedIndex int
	tab           activityTab
}

type taskTranscriptPreview struct {
	task             task.TaskSnapshot
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
	sessionID                  string
	modelConfig                ModelConfigController
	configCenterController     ConfigCenterController
	configCenter               *configCenterState
	settingsConfig             SettingsController
	taskController             TaskController
	taskUpdates                <-chan struct{}
	taskUpdatesStop            func()
	sessionStore               SessionStore
	mcpController              MCPStatusController
	goalController             GoalController
	planController             PlanController
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
	viewport                   viewportx.Model
	width                      int
	height                     int
	ready                      bool
	running                    bool
	runningTerminal            bool
	terminalMode               bool
	goalMode                   bool
	planMode                   bool
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
	clockTickScheduled         bool      // 空闲时钟链去重标志
	lastKeyEventAt             time.Time // 最后键盘输入时刻（IME 安全窗口用）
	turnStartedAt              time.Time
	turnID                     string
	goalWorking                bool
	planWorking                bool
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
	transcriptContentCached    bool
	transcriptLines            []string              // 增量渲染行缓存：与 viewport 行同步（末尾保留一行空隙）
	transcriptEntrySpans       []transcriptEntrySpan // 每条目渲染行区间，与 transcript 等长；startRow<0 表示未渲染
	transcriptLinesValid       bool                  // 行缓存与 transcript 结构对齐（条目数一致）
	viewportShowsSelection     bool                  // viewport 当前显示的是选择高亮版内容
	lastSelectionRenderSig     uint64                // 上次实际渲染进 viewport 的选区签名（selectionRenderSignature）
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
	activeReasoning            int
	activeTurnUserEntry        int
	doneAssistant              int
	assistantStream            streamLineBuffer
	thinkingStream             streamLineBuffer
	pendingToolCites           []toolCitation
	isGenerating               bool
	turnHasModelOutput         bool
	modelCancelRequested       bool
	activeModelCancel          context.CancelFunc
	toolCancelRequested        bool
	lastCtrlCAt                time.Time // 追踪双击 Ctrl+C 退出
	modelWizard                *modelWizard
	settingWizard              *settingWizard
	sessionPicker              *sessionPicker
	taskPicker                 *taskPicker
	taskPreview                *taskTranscriptPreview
	completion                 *completion
	lastActivityPollAt         time.Time // Activity 面板 ListTasks 刷新的节流时间戳
	spinnerFrameIdx            int       // Activity 中 running 条目的动画帧索引，由 cursorFrameMsg 驱动
	waveAmpTarget              bool      // 均衡器波浪振幅目标态（= isGenerating），翻转时更新
	waveAmpStartedAt           time.Time // 振幅目标态确立时刻，用于缓动进度
	waveAmpFrom                float64   // 过渡起点振幅，反向退场时从此值缓降
	waveAmpCurrent             float64   // 当前振幅（cursorFrameMsg 每帧更新，渲染只读）
	tokenRippleExitAt          time.Time // 任务结束后 Ripple 越界退场的起始时刻
	worktreeCWD                string
	worktree                   worktreeSnapshot
	worktreeReader             worktreeStatusReader
}

type activityTab int

const (
	activityTabTasks activityTab = iota
	activityTabTodo
)

type contextStatsProvider interface {
	ContextStats(limitTokens int, draft string) loop.ContextStats
}

type contextCompactor interface {
	CompactContext(context.Context, string) (loop.ContextCompactionResult, error)
}

type tokenTracerURLProvider interface {
	TokenTracerURL() string
}
