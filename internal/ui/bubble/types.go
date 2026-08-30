// 本文件集中定义 Bubble Tea TUI 的状态类型、内部消息和模型切换向导数据结构。
package bubble

import (
	"context"
	"encoding/json"
	"fmt"
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
	"paw/internal/ui/bubble/textareax"
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
	// entryWorkSegment 表示一个折叠的工作段视图条目（reasoning + 工具事务
	// 的连续运行），由 foldWorkSegments 在视图层生成，不进入原始 transcript。
	entryWorkSegment
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
	// responseClock 仅存在于视图副本（transcript 原条目永不置位）：该响应紧跟
	// 一个工作运行时由折叠层标记，渲染时在响应结束的下一行输出暗色时钟行，
	// 而不是把时间挂在 Thought 段标题上（避免时间跑到响应上方）。
	responseClock bool
	// segment 仅在 kind == entryWorkSegment 时非空，持有收编的子条目与聚合统计。
	segment *workSegmentData
}

// workSegmentData 是一个工作段的视图数据：子条目为原始 reasoning/tool 条目
// 的视图引用，统计口径见 foldWorkSegments。
type workSegmentData struct {
	children     []transcriptEntry
	toolCalls    int // 不含 update_todo 等簿记工具
	failed       int
	hasReasoning bool
	startedAt    time.Time
	finishedAt   time.Time
	header       bool // 展开态下作为段标题行内联在视图中（子条目跟随其后平铺）
	live         bool // foldLive 下仅尾部段为 true
}

// workSegmentKey 生成段的稳定身份：用于跨视图重建保留展开状态。只用段
// 起点（纳秒精度，逐段唯一）：live 段在流式中 children/toolCalls/failed
// 会持续增长，把这些计数纳入 key 会让展开态在段成长时丢失。
func workSegmentKey(data *workSegmentData) string {
	if data == nil {
		return ""
	}
	return fmt.Sprintf("%d/%t", data.startedAt.UnixNano(), data.hasReasoning)
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
	search            string
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
	forkedFrom     string // source == sessionRestoreFork 时的父会话 ID
	taskPreview    *taskTranscriptPreview
	currentTodo    todo.Snapshot
	hasCurrentTodo bool
	todoWasCleared bool
	modes          *loop.SessionModeSnapshot
	err            error
}

type sessionRestoreSource int

const (
	sessionRestorePicker sessionRestoreSource = iota
	sessionRestoreTaskEnter
	sessionRestoreNew
	sessionRestoreFork
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
	ctx                       context.Context
	theme                     theme.Theme
	styles                    StyleSet
	themePicker               *themePickerState
	runner                    Runner
	workspaceRoot             string
	selectionBroker           *selecttool.Broker
	selectionDock             *selectionDock
	todoBroker                *todo.Broker
	currentTodo               todo.Snapshot
	hasCurrentTodo            bool
	todoWasCleared            bool
	latestTodoIndex           int
	sessionID                 string
	modelConfig               ModelConfigController
	configCenterController    ConfigCenterController
	configCenter              *configCenterState
	settingsConfig            SettingsController
	taskController            TaskController
	taskUpdates               <-chan struct{}
	taskUpdatesStop           func()
	sessionStore              SessionStore
	mcpController             MCPStatusController
	goalController            GoalController
	planController            PlanController
	commandRegistry           *CommandRegistry
	skillRegistry             *skill.Registry
	queryGuard                QueryGuard
	chatQueue                 CommandQueue
	queueMode                 queueInteractionMode
	queueSelectedID           string
	queueEdit                 *queueEditState
	inputSource               inputSource
	cursorAnchor              *terminalCursorAnchor
	input                     textareax.Model
	viewport                  viewportx.Model
	width                     int
	height                    int
	ready                     bool
	running                   bool
	runningTerminal           bool
	terminalMode              bool
	goalMode                  bool
	planMode                  bool
	terminalPreview           bool
	hasInteracted             bool
	showThinking              bool
	selecting                 bool
	selectionActive           bool
	selectionStart            selectionPoint
	selectionEnd              selectionPoint
	selectionMode             selectionMode
	selectionAnchor           selectionPoint
	selectionMoved            bool
	clickCount                int
	lastClickAt               time.Time
	lastClickPoint            selectionPoint
	clickActionSeq            uint64
	clickActionPending        bool
	copyToast                 string
	copyToastUntil            time.Time
	translatePanel            *translatePanel
	translateSeq              uint64
	cursorFrameAt             time.Time
	uiAnimationFrameScheduled bool
	clockTickScheduled        bool      // 空闲时钟链去重标志
	lastKeyEventAt            time.Time // 最后键盘输入时刻（IME 安全窗口用）
	turnStartedAt             time.Time
	turnID                    string
	// lastModelVisibleActivityAt 是本轮最近一次模型可见活动（正文 delta、
	// 工具调用/结果、思考块收尾）的时刻。与 turnStartedAt 一起界定当前
	// reasoning 块的计时窗口起点：思考内容被网关缓冲、最后才一次性到达时，
	// 时长仍能覆盖从请求发出到思考结束的真实等待。
	lastModelVisibleActivityAt time.Time
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
	// viewEntries 是 transcript 的视图投影（工作段折叠后的渲染序列），由
	// recomputeViewEntries 在渲染前重建；渲染管线（render cache/spans/交互
	// 坐标）一律基于 viewEntries 下标，transcript 保持原始条目不动。
	viewEntries         []transcriptEntry
	transcriptToViewMap []int // transcript 下标 → 视图下标（被收编条目指向所属段）
	viewToTranscriptMap []int // 视图下标 → transcript 下标（段条目为 -1）
	foldModeSet         bool
	lastFoldMode        foldMode
	segmentExpanded     map[string]bool // 工作段身份（workSegmentKey）→ 手动展开态
	// viewProjectionSig 是上次渲染的视图投影结构签名：折叠结构变化（如 live
	// 段解散、段重组）会让同一视图槽位装上不同条目，而 transcript 条目本身
	// 未变、失效下标发现不了；签名分歧点作为额外的视图空间失效起点。
	viewProjectionSig                   []viewSlotSignature
	viewStructDirtyFrom                 int // 视图空间结构失效起点；-1 表示无（零值 0 由 viewStructDirtySet 区分）
	viewStructDirtySet                  bool
	transcriptRenderCache               []transcriptRenderCacheEntry
	transcriptRenderConfig              transcriptRenderConfig
	transcriptRenderConfigSet           bool
	transcriptInvalidation              transcriptInvalidation
	transcriptContentCached             bool
	transcriptLines                     []string              // 增量渲染行缓存：与 viewport 行同步（末尾保留一行空隙）
	transcriptEntrySpans                []transcriptEntrySpan // 每条目渲染行区间，与 transcript 等长；startRow<0 表示未渲染
	transcriptInteraction               transcriptInteractionIndex
	transcriptLinesValid                bool   // 行缓存与 transcript 结构对齐（条目数一致）
	viewportShowsSelection              bool   // viewport 当前显示的是选择高亮版内容
	lastSelectionRenderSig              uint64 // 上次实际渲染进 viewport 的选区签名（selectionRenderSignature）
	transcriptLineCache                 []transcriptLineSnapshot
	transcriptLineCacheReady            bool
	transcriptLocationCache             []transcriptEntryLocation
	transcriptLocationsReady            bool
	transcriptRefreshPending            bool
	transcriptRefreshPendingAt          time.Time // pending 置位时刻，用于帧窗口判定（不早于 interval 才刷）
	transcriptRefreshDeferred           bool
	transcriptRefreshDeferredGeneration uint64
	transcriptKeyScrollActive           bool
	newMessageNoticeCycle               uint64
	newMessageNoticeCount               int
	newMessageNoticeHovered             bool
	newMessageNoticePressed             bool
	rawMouseEscapePending               string
	toolInspectActive                   bool
	toolInspectIndex                    int
	toolHoverIndex                      int
	transcriptHoverPatch                transcriptHoverPatchCache
	toolGroupExpanded                   bool
	toolGroupFullResult                 bool
	lastTranscriptRefreshAt             time.Time
	transcriptRefreshCount              uint64
	transcriptRefreshBatchDepth         int
	transcriptRefreshBatched            bool
	transcriptRenderVisits              int
	transcriptPrefixAnchorVisits        int
	transcriptSegmentRangeCalls         int
	transcriptInteractionVisits         int
	transcriptInteractionRangeCalls     int
	transcriptHoverPatchRenderSpy       *int
	toolProgressVisits                  int
	toolRuntimeRebuildVisits            int
	taskToolUpdateVisits                int
	toolRuntime                         transcriptToolRuntimeIndex
	activeAssistant                     int
	activeThinking                      int
	activeReasoning                     int
	activeTurnUserEntry                 int
	doneAssistant                       int
	assistantStream                     streamLineBuffer
	thinkingStream                      streamLineBuffer
	pendingToolCites                    []toolCitation
	isGenerating                        bool
	turnHasModelOutput                  bool
	modelCancelRequested                bool
	activeModelCancel                   context.CancelFunc
	toolCancelRequested                 bool
	lastCtrlCAt                         time.Time // 追踪双击 Ctrl+C 退出
	modelWizard                         *modelWizard
	settingWizard                       *settingWizard
	sessionPicker                       *sessionPicker
	taskPicker                          *taskPicker
	taskPreview                         *taskTranscriptPreview
	completion                          *completion
	lastActivityPollAt                  time.Time // Activity 面板 ListTasks 刷新的节流时间戳
	spinnerFrameIdx                     int       // Activity 中 running 条目的动画帧索引，由 cursorFrameMsg 驱动
	waveAmpTarget                       bool      // 均衡器波浪振幅目标态（= isGenerating），翻转时更新
	waveAmpStartedAt                    time.Time // 振幅目标态确立时刻，用于缓动进度
	waveAmpFrom                         float64   // 过渡起点振幅，反向退场时从此值缓降
	waveAmpCurrent                      float64   // 当前振幅（cursorFrameMsg 每帧更新，渲染只读）
	tokenRippleExitAt                   time.Time // 任务结束后 Ripple 越界退场的起始时刻
	worktreeCWD                         string
	worktree                            worktreeSnapshot
	worktreeReader                      worktreeStatusReader
}

type activityLayoutMode uint8

const (
	activityLayoutHidden activityLayoutMode = iota
	activityLayoutDocked
	activityLayoutFullscreen
)

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
