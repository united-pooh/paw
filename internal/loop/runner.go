package loop

import (
	"context"
	"encoding/json"
	"fmt"
	"paw/internal/message"
	"paw/internal/model"
	"paw/internal/session"
	"paw/internal/skill"
	"paw/internal/todo"
	"paw/internal/tokentracer"
	"paw/internal/tool"
	"paw/internal/ui"
	"strings"
	"sync"
	"time"
)

const (
	maxToolRounds       = 500
	toolUseResponseType = "tool_use"
)

// ToolFilter restricts which tools a turn may advertise and execute. A nil
// filter allows all registered tools. The filter receives the tool name and
// the raw input when available (nil input while building the prompt).
type ToolFilter func(name string, input json.RawMessage) error

// ToolFilterApplier scopes a runner's tool set and system supplement for the
// turns of a higher-level runtime (e.g. plan mode). It is implemented by the
// runner's turn executor so runtimes stay testable with fake executors.
type ToolFilterApplier interface {
	SetTurnToolFilter(ToolFilter)
	SystemSupplement() string
	SetSystemSupplement(string)
}

type ModelStreamer interface {
	StreamMessage(ctx context.Context, messages []message.Message, tools []model.ToolDefinition) (<-chan model.StreamEvent, error)
}

type HistoryStore interface {
	LoadResolvedHistory(ctx context.Context, sessionID string) ([]message.Message, error)
	Append(ctx context.Context, sessionID string, msgs ...message.Message) error
}

type SessionLoadResult struct {
	Messages []message.Message
	Recovery *session.RecoveryState
}

type AttachmentStore interface {
	SaveAttachment(ctx context.Context, mimeType string, data []byte) (string, error)
	ReadAttachment(ctx context.Context, reference string) (string, []byte, error)
}

type historyExistenceStore interface {
	Exists(ctx context.Context, sessionID string) (bool, error)
}

type SubagentTokensProvider interface {
	TotalSubagentTokens(parentSessionID string) int
}

// TurnOwnedTaskCleaner stops background work owned by an exact outer turn.
type TurnOwnedTaskCleaner interface {
	StopOwnedTasks(ctx context.Context, parentSessionID, parentTurnID, reason string)
}

type modelUsageReceiver interface {
	OnModelUsage(usage model.Usage)
}

type Runner struct {
	mu        sync.RWMutex
	model     ModelStreamer
	ui        ui.UI
	registry  *tool.Registry
	store     HistoryStore
	sessionID string
	workRoot  string // 工具使用的 workspace 根目录，用于解析相对文件路径
	prompt    *PromptBuilder
	// history 保存”已经成功完成”的多轮对话消息。
	// 当前调用路径是串行的，因此这里先不引入锁。
	// TODO: 如果后续要把 Runner 暴露给并发调用方，需要为 history 增加互斥保护。
	history                []message.Message
	usage                  model.Usage
	usageKnown             bool
	sessionUsage           model.Usage
	sessionUsageKnown      bool
	supplements            []string
	systemSupplement       string
	compactToolPrompt      bool
	contextLimitTokens     int
	contextMaintenance     contextMaintenanceConfig
	compactionArchive      *compactionArchive
	softCompactNoticed     bool
	consecutiveCompacts    int
	compactStuck           bool
	streamMAEnabled        bool
	streamMASubagents      StreamMASubagentRunner
	subagentTokensProvider SubagentTokensProvider
	turnOwnedTaskCleaner   TurnOwnedTaskCleaner
	recovery               *session.RecoveryState
	skillRegistry          *skill.Registry
	activeSkillContext     string
	tokenTracer            *tokentracer.Tracer
	traceStageID           string
	traceAgentID           string
	yoloModeHandler        func(bool)
	nowFn                  func() time.Time
	autoContinueConfig     AutoContinueConfig
	todoBroker             *todo.Broker
	lastProgressHash       string
	activeTool             activeToolState
	activeTurnCancel       context.CancelFunc
	turnToolFilter         ToolFilter
}

type activeToolState struct {
	id     string
	name   string
	cancel context.CancelFunc
	stream bool
}

type tokenUsageTotals struct {
	used   int
	cache  int
	output int
}

type ContextStats struct {
	UsedTokens  int
	CacheTokens int
	LimitTokens int
	// SessionUsedTokens is the cumulative token usage across the whole session
	// (sum of every turn's delta), distinct from UsedTokens which reflects the
	// current context window. Exposed for display; the UI must not reach into
	// runner internals.
	SessionUsedTokens int
}

// TurnState is the lifecycle state for one outer user turn. Tool calls and
// their results stay inside the same TurnState; a new user turn gets a fresh
// instance. Keeping this state separate from the per-stream turnState prevents
// turn-scoped instructions from being re-initialized on every tool round.
type TurnState struct {
	// PlanEmitted indicates that the initial skill/investigation instructions
	// have already been presented to the model during this outer turn.
	PlanEmitted bool
	// ToolRounds counts completed assistant/tool iterations in this turn.
	ToolRounds int
	// SkillContext is the immutable skill snapshot captured at turn start.
	SkillContext string
}

type outputMode int

const (
	outputModeUndecided outputMode = iota
	outputModeVisible
	outputModeSuppressed
)

type turnState struct {
	content           strings.Builder
	visibleContent    strings.Builder
	streamEstablished bool
	pending           strings.Builder
	toolPayload       toolPayloadCandidateState
	toolCalls         []message.ToolCall
	providerData      json.RawMessage
	wroteOutput       bool
	outputMode        outputMode
	usage             model.Usage
	usageKnown        bool
	requestUsage      model.Usage
	requestUsageKnown bool
	overlap           continuationOverlapState
	uiFinalized       bool
	traceStageID      string
	traceAgentID      string
}

type toolPayloadCandidateState struct {
	active bool
	buffer strings.Builder
}

type continuationOverlapState struct {
	active   bool
	existing string
	maxRunes int
	buffer   strings.Builder
}

type toolUseEnvelope struct {
	Type  string          `json:"type"`
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

func NewRunner(model ModelStreamer, output ui.UI, registry *tool.Registry, store HistoryStore, sessionID string) *Runner {
	return NewRunnerWithInstructionRoot(model, output, registry, store, sessionID, "")
}

func NewRunnerWithInstructionRoot(model ModelStreamer, output ui.UI, registry *tool.Registry, store HistoryStore, sessionID, instructionRoot string) *Runner {
	maintenance := defaultContextMaintenanceConfig()
	archive, _ := newCompactionArchive(instructionRoot, sessionID, maintenance.archiveEnabled)
	return &Runner{
		model:              model,
		ui:                 output,
		registry:           registry,
		store:              store,
		sessionID:          sessionID,
		workRoot:           instructionRoot,
		prompt:             NewPromptBuilder(NewInstructionManager(instructionRoot)),
		skillRegistry:      skill.NewRegistry(skill.DefaultRoots(instructionRoot)),
		contextLimitTokens: initialContextLimitTokens(model),
		contextMaintenance: maintenance,
		compactionArchive:  archive,
		streamMAEnabled:    true,
		nowFn:              time.Now,
		autoContinueConfig: DefaultAutoContinueConfig(),
	}
}

func (runner *Runner) registerActiveTool(id, name string, cancel context.CancelFunc) {
	if runner == nil {
		return
	}
	runner.mu.Lock()
	runner.activeTool = activeToolState{id: id, name: name, cancel: cancel, stream: true}
	runner.mu.Unlock()
}

func (runner *Runner) clearActiveTool(id string) {
	if runner == nil {
		return
	}
	runner.mu.Lock()
	if id == "" || runner.activeTool.id == id {
		runner.activeTool = activeToolState{}
	}
	runner.mu.Unlock()
}

// CancelCurrentTool cancels the active streaming tool, if one is registered.
func (runner *Runner) CancelCurrentTool() bool {
	if runner == nil {
		return false
	}
	runner.mu.RLock()
	active := runner.activeTool
	runner.mu.RUnlock()
	if !active.stream || active.cancel == nil {
		return false
	}
	active.cancel()
	return true
}

// CancelTurn cancels the active turn context, if one is registered.
func (runner *Runner) CancelTurn() {
	if runner == nil {
		return
	}
	runner.mu.RLock()
	cancel := runner.activeTurnCancel
	runner.mu.RUnlock()
	if cancel != nil {
		cancel()
	}
}

func (runner *Runner) beginActiveTurn(ctx context.Context) (context.Context, func()) {
	if ctx == nil {
		ctx = context.Background()
	}
	turnCtx, cancel := context.WithCancel(ctx)
	runner.mu.Lock()
	runner.activeTurnCancel = cancel
	runner.mu.Unlock()
	return turnCtx, func() {
		cancel()
		runner.mu.Lock()
		runner.activeTurnCancel = nil
		runner.mu.Unlock()
	}
}

func (runner *Runner) WorkspaceRoot() string {
	if runner == nil {
		return ""
	}
	return runner.workRoot
}
func (runner *Runner) ReplaceToolNamespace(namespace string, tools []tool.Tool) error {
	if runner == nil || runner.registry == nil {
		return fmt.Errorf("runner tool registry is unavailable")
	}
	return runner.registry.ReplaceNamespace(namespace, tools)
}

func (runner *Runner) SetYoloModeHandler(handler func(bool)) {
	if runner == nil {
		return
	}
	runner.mu.Lock()
	runner.yoloModeHandler = handler
	runner.mu.Unlock()
}

// SetTurnToolFilter scopes the tool set for subsequent turns. A nil filter
// restores unrestricted access. Higher-level runtimes set it around their own
// turns and restore the previous value afterwards.
func (runner *Runner) SetTurnToolFilter(filter ToolFilter) {
	if runner == nil {
		return
	}
	runner.mu.Lock()
	defer runner.mu.Unlock()
	runner.turnToolFilter = filter
}

// currentToolFilter returns the active turn tool filter, if any.
func (runner *Runner) currentToolFilter() ToolFilter {
	if runner == nil {
		return nil
	}
	runner.mu.RLock()
	defer runner.mu.RUnlock()
	return runner.turnToolFilter
}

// SystemSupplement returns the current additional system instructions.
func (runner *Runner) SystemSupplement() string {
	if runner == nil {
		return ""
	}
	runner.mu.RLock()
	defer runner.mu.RUnlock()
	return runner.systemSupplement
}

func (runner *Runner) SetYoloMode(enabled bool) (bool, error) {
	if runner == nil || runner.registry == nil {
		return false, fmt.Errorf("runner tool registry is unavailable")
	}
	registered, ok := runner.registry.Get("Read")
	if !ok {
		return false, fmt.Errorf("Read tool is unavailable")
	}
	controller, ok := registered.(interface {
		SetAllowOutsideRoot(bool)
		OutsideRootAllowed() bool
	})
	if !ok {
		return false, fmt.Errorf("Read tool does not support yolo mode")
	}
	controller.SetAllowOutsideRoot(enabled)
	runner.mu.RLock()
	handler := runner.yoloModeHandler
	runner.mu.RUnlock()
	if handler != nil {
		handler(enabled)
	}
	return controller.OutsideRootAllowed(), nil
}

func (runner *Runner) YoloMode() bool {
	if runner == nil || runner.registry == nil {
		return false
	}
	registered, ok := runner.registry.Get("Read")
	if !ok {
		return false
	}
	controller, ok := registered.(interface{ OutsideRootAllowed() bool })
	return ok && controller.OutsideRootAllowed()
}

func (runner *Runner) validate() error {
	if runner == nil || runner.model == nil || runner.ui == nil {
		return fmt.Errorf("runner 未初始化: model 或 ui 为空")
	}
	return nil
}

func (runner *Runner) SetSkillRegistry(registry *skill.Registry) {
	if runner == nil {
		return
	}
	runner.mu.Lock()
	defer runner.mu.Unlock()
	runner.skillRegistry = registry
}

func (runner *Runner) SkillRoots() []string {
	registry := runner.currentSkillRegistry()
	if registry == nil {
		return nil
	}
	return registry.Roots()
}

func (runner *Runner) activateSkillContext(input string) {
	if runner == nil {
		return
	}
	registry := runner.currentSkillRegistry()
	if registry == nil {
		runner.clearActiveSkillContext()
		return
	}
	contextText, _, errs := registry.InstructionContext(input)
	runner.mu.Lock()
	runner.activeSkillContext = contextText
	runner.mu.Unlock()
	for _, err := range errs {
		if err != nil {
			runner.notifySystem("skills", err.Error())
		}
	}
}

func (runner *Runner) clearActiveSkillContext() {
	if runner == nil {
		return
	}
	runner.mu.Lock()
	defer runner.mu.Unlock()
	runner.activeSkillContext = ""
}

func (runner *Runner) currentSkillRegistry() *skill.Registry {
	if runner == nil {
		return nil
	}
	runner.mu.RLock()
	defer runner.mu.RUnlock()
	return runner.skillRegistry
}

func (runner *Runner) currentSkillContext() string {
	if runner == nil {
		return ""
	}
	runner.mu.RLock()
	defer runner.mu.RUnlock()
	return runner.activeSkillContext
}

func (runner *Runner) ContextStats(limitTokens int, _ string) ContextStats {
	if limitTokens <= 0 {
		limitTokens = 1024 * 1024
	}
	if runner == nil {
		return ContextStats{LimitTokens: limitTokens}
	}

	runner.mu.RLock()
	usage := runner.usage
	usageKnown := runner.usageKnown
	sessionID := runner.sessionID
	provider := runner.subagentTokensProvider
	runner.mu.RUnlock()

	current := usageTotalsFromUsage(usage, usageKnown)
	subagentTokens := 0
	if provider != nil {
		subagentTokens = provider.TotalSubagentTokens(sessionID)
	}

	runner.mu.RLock()
	sessionTotals := usageTotalsFromUsage(runner.sessionUsage, runner.sessionUsageKnown)
	runner.mu.RUnlock()

	return ContextStats{
		UsedTokens:        current.used + subagentTokens,
		CacheTokens:       current.cache,
		LimitTokens:       limitTokens,
		SessionUsedTokens: sessionTotals.used + subagentTokens,
	}
}
