package loop

import (
	"context"
	"encoding/json"
	"fmt"
	"paw/internal/message"
	"paw/internal/model"
	"paw/internal/session"
	"paw/internal/skill"
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
	Modes    *SessionModeSnapshot
}

type SessionModeSnapshot struct {
	ActiveGoalID        string
	GoalStatus          string
	ActivePlanID        string
	PlanStatus          string
	PendingPermissionID string
}

type AttachmentStore interface {
	SaveAttachment(ctx context.Context, mimeType string, data []byte) (string, error)
	ReadAttachment(ctx context.Context, reference string) (string, []byte, error)
}

type historyExistenceStore interface {
	Exists(ctx context.Context, sessionID string) (bool, error)
}

type TaskTokensProvider interface {
	TotalTaskTokens(parentSessionID string) int
}

// TurnOwnedTaskCleaner stops background work owned by an exact outer turn.
type TurnOwnedTaskCleaner interface {
	StopOwnedTasks(ctx context.Context, parentSessionID, parentTurnID, reason string)
}

type modelUsageReceiver interface {
	OnModelUsage(usage model.Usage)
}

type enginePorts struct {
	model    ModelStreamer
	display  *DisplayBus
	registry *tool.Registry
	store    HistoryStore
	prompt   *PromptBuilder
	nowFn    func() time.Time
}

type engineSessionState struct {
	mu        sync.RWMutex
	sessionID string
	workRoot  string // 工具使用的 workspace 根目录，用于解析相对文件路径
	// history 保存“已经成功完成”的多轮对话消息。
	// 当前调用路径是串行的，因此这里先不引入锁。
	// TODO: 如果后续要把 Engine 暴露给并发调用方，需要为 history 增加互斥保护。
	history  []message.Message
	recovery *session.RecoveryState
}

// Engine 是会话执行内核：驱动“模型 → 工具 → 模型”循环。构造依赖与
// 会话身份收进两个内聚状态，顶层只保留横切协作者。
type Engine struct {
	enginePorts
	engineSessionState

	usage     usageMeter      // token 用量账本（上下文/会话两本）
	hooks     hookChain       // 生命周期钩子链（会话切换等）
	trace     traceSession    // tokentracer 会话（tracer + 当前 stage/agent）
	gate      progressGate    // 完成门：自动续跑配置 + todo 进展/陈旧追踪
	skills    skillState      // 技能注册表与激活上下文
	streamMA  streamMAState   // StreamMA 开关与任务运行器
	compact   compactionState // 上下文压缩：配置/归档/窗口限值/卡死检测/工具提示压缩
	stateCfg  stateConfig     // 模式 B 恢复：压缩模式/近邻轮数/压缩比/状态块提供者
	promptCtx promptState     // 系统提示增补与轮间 supplements
	taskEnv   taskEnv         // 后台任务适配（token 统计 / turn 归属清理）
	turnCtl   turnCtl         // 活动工具与 turn 取消句柄
	toolGate  toolGateState   // 工具门：yolo 通知回调 + turn 工具过滤器

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
	// ToolArgsGenAt 记录每个工具调用参数开始流式生成的时刻（tool_call block
	// start），工具执行时换算「参数生成→执行完成」的合并耗时；非流式或不带
	// call ID 的 provider 无此窗口，回退为纯执行耗时。
	ToolArgsGenAt map[string]time.Time
	// ToolPhases 是本 turn 已执行工具的阶段耗时聚合（tracer 按 turn 上报）。
	ToolPhases ToolPhaseTotals
}

// ToolPhaseTotals 聚合一个 turn 内全部工具调用的阶段耗时（毫秒）。
type ToolPhaseTotals struct {
	ArgsGenMS int64 `json:"args_gen_ms"`
	ExecMS    int64 `json:"exec_ms"`
	Calls     int   `json:"calls"`
}

// markToolArgsGenStarted 记录工具参数开始生成的时刻（只取第一次，重试或
// 续流不重置起点）。
func (s *TurnState) markToolArgsGenStarted(callID string, at time.Time) {
	if s == nil || strings.TrimSpace(callID) == "" || at.IsZero() {
		return
	}
	if s.ToolArgsGenAt == nil {
		s.ToolArgsGenAt = make(map[string]time.Time)
	}
	if _, exists := s.ToolArgsGenAt[callID]; !exists {
		s.ToolArgsGenAt[callID] = at
	}
}

type outputMode int

const (
	outputModeUndecided outputMode = iota
	outputModeVisible
	outputModeSuppressed
)

type turnState struct {
	content             strings.Builder
	visibleContent      strings.Builder
	streamEstablished   bool
	pending             strings.Builder
	toolPayload         toolPayloadCandidateState
	toolCalls           []message.ToolCall
	providerData        json.RawMessage
	wroteOutput         bool
	outputMode          outputMode
	usage               model.Usage
	usageKnown          bool
	requestUsage        model.Usage
	requestUsageKnown   bool
	overlap             continuationOverlapState
	uiFinalized         bool
	traceStageID        string
	traceAgentID        string
	parts               *partAccumulator
	generatedBy         *message.MessageOrigin
	structuredPartsSeen bool
	legacyEvents        bool
	legacyActiveType    message.AssistantPartType
	legacyActiveIndex   int
	legacyNextPartIndex int
	// outer 指向本回合外层 TurnState（工具参数生成时刻回写于此，生命周期与
	// 整个用户 turn 一致）；测试构造的裸 turnState 为 nil。
	outer *TurnState
}

// partAccumulator 按 provider block index 收集有序助理 parts。
// active 存正在接收的事件，order 记录插入顺序，completedParts 存储已关闭的 part。
type partAccumulator struct {
	active         map[int]message.AssistantPart
	order          []int
	completedParts []message.AssistantPart
	origin         *message.MessageOrigin
}

func newPartAccumulator() *partAccumulator {
	return &partAccumulator{
		active: make(map[int]message.AssistantPart),
	}
}

func (pa *partAccumulator) openPart(blockIndex int, partType message.AssistantPartType) {
	if _, exists := pa.active[blockIndex]; exists {
		return
	}
	now := time.Now()
	p := message.AssistantPart{
		Type:   partType,
		Status: message.AssistantPartPartial,
	}
	switch partType {
	case message.AssistantPartReasoning:
		p.Reasoning = &message.ReasoningPart{
			ProviderStateComplete: false,
			StartedAt:             &now,
		}
	case message.AssistantPartText:
		p.Text = &message.AssistantTextPart{}
	case message.AssistantPartToolCall:
		p.ToolCall = &message.ToolCall{}
	}
	pa.active[blockIndex] = p
	pa.order = append(pa.order, blockIndex)
}

func (pa *partAccumulator) closePart(blockIndex int) {
	p, exists := pa.active[blockIndex]
	if !exists {
		return
	}
	now := time.Now()
	switch p.Type {
	case message.AssistantPartReasoning:
		if p.Reasoning != nil {
			p.Reasoning.FinishedAt = &now
		}
	case message.AssistantPartText:
		// text parts carry no extra timestamp
	}
	p.Status = message.AssistantPartCompleted
	delete(pa.active, blockIndex)
	pa.completedParts = append(pa.completedParts, p)
}

func (pa *partAccumulator) appendText(blockIndex int, delta string) {
	p, exists := pa.active[blockIndex]
	if !exists {
		return
	}
	switch p.Type {
	case message.AssistantPartReasoning:
		if p.Reasoning != nil {
			p.Reasoning.Text += delta
		}
	case message.AssistantPartText:
		if p.Text != nil {
			p.Text.Text += delta
		}
	}
	pa.active[blockIndex] = p
}

func (pa *partAccumulator) setToolCallIdentity(blockIndex int, id, name string) {
	p, exists := pa.active[blockIndex]
	if !exists || p.ToolCall == nil {
		return
	}
	if id != "" {
		p.ToolCall.ID = id
	}
	if name != "" {
		p.ToolCall.Name = name
	}
	pa.active[blockIndex] = p
}

func (pa *partAccumulator) appendToolArgs(blockIndex int, args string) {
	p, exists := pa.active[blockIndex]
	if !exists || p.ToolCall == nil {
		return
	}
	if p.ToolCall.Input == nil {
		p.ToolCall.Input = json.RawMessage(args)
	} else {
		p.ToolCall.Input = append(p.ToolCall.Input, []byte(args)...)
	}
	pa.active[blockIndex] = p
}

func (pa *partAccumulator) setReasoningProviderData(blockIndex int, data json.RawMessage) {
	p, exists := pa.active[blockIndex]
	if !exists || p.Reasoning == nil {
		return
	}
	p.Reasoning.ProviderData = append(json.RawMessage(nil), data...)
	p.Reasoning.ProviderStateComplete = true
	pa.active[blockIndex] = p
}

func (pa *partAccumulator) setReasoningRedacted(blockIndex int) {
	p, exists := pa.active[blockIndex]
	if !exists || p.Reasoning == nil {
		return
	}
	p.Reasoning.Redacted = true
	pa.active[blockIndex] = p
}

func (pa *partAccumulator) finalize() []message.AssistantPart {
	// 关闭所有仍活跃的 parts
	for _, idx := range pa.order {
		if _, exists := pa.active[idx]; exists {
			pa.closePart(idx)
		}
	}
	return message.CloneAssistantParts(pa.completedParts)
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

func NewEngine(model ModelStreamer, output ui.UI, registry *tool.Registry, store HistoryStore, sessionID string) *Engine {
	return NewEngineWithInstructionRoot(model, output, registry, store, sessionID, "")
}

func NewEngineWithInstructionRoot(model ModelStreamer, output ui.UI, registry *tool.Registry, store HistoryStore, sessionID, instructionRoot string) *Engine {
	maintenance := defaultContextMaintenanceConfig()
	archive, _ := newCompactionArchive(instructionRoot, sessionID, maintenance.archiveEnabled)
	return &Engine{
		enginePorts: enginePorts{
			model:    model,
			display:  newUIDisplayBus(output),
			registry: registry,
			store:    store,
			prompt:   NewPromptBuilder(NewInstructionManager(instructionRoot)),
			nowFn:    time.Now,
		},
		engineSessionState: engineSessionState{sessionID: sessionID, workRoot: instructionRoot},
		skills:             skillState{registry: skill.NewRegistry(skill.DefaultRoots(instructionRoot))},
		compact:            newCompactionState(maintenance, archive, initialContextLimitTokens(model)),
		gate:               progressGate{autoContinue: DefaultAutoContinueConfig()},
		streamMA:           streamMAState{enabled: true},
	}
}

// turnCtl 收敛 turn 生命周期的活动工具与取消句柄，自带锁。
type turnCtl struct {
	mu         sync.Mutex
	activeTool activeToolState
	turnCancel context.CancelFunc
}

func (t *turnCtl) registerTool(id, name string, cancel context.CancelFunc) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.activeTool = activeToolState{id: id, name: name, cancel: cancel, stream: true}
}

func (t *turnCtl) clearTool(id string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if id == "" || t.activeTool.id == id {
		t.activeTool = activeToolState{}
	}
}

func (t *turnCtl) cancelActiveTool() bool {
	t.mu.Lock()
	active := t.activeTool
	t.mu.Unlock()
	if !active.stream || active.cancel == nil {
		return false
	}
	active.cancel()
	return true
}

func (t *turnCtl) cancelTurn() {
	t.mu.Lock()
	cancel := t.turnCancel
	t.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (t *turnCtl) beginTurn(ctx context.Context) (context.Context, func()) {
	if ctx == nil {
		ctx = context.Background()
	}
	turnCtx, cancel := context.WithCancel(ctx)
	t.mu.Lock()
	t.turnCancel = cancel
	t.mu.Unlock()
	return turnCtx, func() {
		cancel()
		t.mu.Lock()
		t.turnCancel = nil
		t.mu.Unlock()
	}
}

func (runner *Engine) registerActiveTool(id, name string, cancel context.CancelFunc) {
	if runner == nil {
		return
	}
	runner.turnCtl.registerTool(id, name, cancel)
}

func (runner *Engine) clearActiveTool(id string) {
	if runner == nil {
		return
	}
	runner.turnCtl.clearTool(id)
}

// CancelCurrentTool cancels the active streaming tool, if one is registered.
func (runner *Engine) CancelCurrentTool() bool {
	if runner == nil {
		return false
	}
	return runner.turnCtl.cancelActiveTool()
}

// CancelTurn cancels the active turn context, if one is registered.
func (runner *Engine) CancelTurn() {
	if runner == nil {
		return
	}
	runner.turnCtl.cancelTurn()
}

func (runner *Engine) beginActiveTurn(ctx context.Context) (context.Context, func()) {
	return runner.turnCtl.beginTurn(ctx)
}

func (runner *Engine) WorkspaceRoot() string {
	if runner == nil {
		return ""
	}
	return runner.workRoot
}
func (runner *Engine) ReplaceToolNamespace(namespace string, tools []tool.Tool) error {
	if runner == nil || runner.registry == nil {
		return fmt.Errorf("runner tool registry is unavailable")
	}
	return runner.registry.ReplaceNamespace(namespace, tools)
}

// toolGateState 收敛工具门控：yolo 模式通知回调与 turn 级工具过滤器。
type toolGateState struct {
	mu             sync.RWMutex
	yoloHandler    func(bool)
	turnFilter     ToolFilter
	permissionGate PermissionGate
	toolLifecycle  ToolLifecycle
}

func (g *toolGateState) setYoloHandler(handler func(bool)) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.yoloHandler = handler
}

func (g *toolGateState) currentYoloHandler() func(bool) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.yoloHandler
}

func (g *toolGateState) setTurnFilter(filter ToolFilter) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.turnFilter = filter
}

func (g *toolGateState) currentTurnFilter() ToolFilter {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.turnFilter
}

func (g *toolGateState) setPermissionGate(gate PermissionGate) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.permissionGate = gate
}

func (g *toolGateState) currentPermissionGate() PermissionGate {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.permissionGate
}

func (g *toolGateState) setToolLifecycle(lifecycle ToolLifecycle) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.toolLifecycle = lifecycle
}

func (g *toolGateState) currentToolLifecycle() ToolLifecycle {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.toolLifecycle
}

func (runner *Engine) SetYoloModeHandler(handler func(bool)) {
	if runner == nil {
		return
	}
	runner.toolGate.setYoloHandler(handler)
}

// SessionLoadedHook 在 LoadSession 切换会话后回调（cmd/agent 用它重绑
// 会话相关工具：/resume 切换后 wire* 与状态文件跟随新会话）。
type SessionLoadedHook func(sessionID string)

// SetSessionLoadedHook 注册会话切换回调（可多次调用，全部执行）。
// P2 起为 hookChain 上的兼容糖；新代码优先实现 Hook 能力接口并经
// RegisterHook 挂载（钩子间禁止互调，按注册顺序执行）。
func (runner *Engine) SetSessionLoadedHook(hook SessionLoadedHook) {
	if runner == nil || hook == nil {
		return
	}
	runner.hooks.register(sessionLoadedFunc(hook))
}

// RegisterHook 挂载生命周期钩子（会话切换等能力接口按需实现）。
func (runner *Engine) RegisterHook(hook Hook) {
	if runner == nil {
		return
	}
	runner.hooks.register(hook)
}

func (runner *Engine) SetStateBlockProvider(provider StateBlockProvider) {
	if runner == nil {
		return
	}
	runner.stateCfg.setBlockProvider(provider)
}

// SetContextMode 设置上下文压缩模式："summary"（对话摘要，现状）或
// "state"（状态压缩）。空值视为 summary。
func (runner *Engine) SetContextMode(mode string) {
	if runner == nil {
		return
	}
	runner.stateCfg.setMode(mode)
}

// contextModeState 返回是否处于状态压缩模式。
func (runner *Engine) contextModeState() bool {
	if runner == nil {
		return false
	}
	return runner.stateCfg.isStateMode()
}

// buildStateContext 通过 provider 获取状态块（模式 B 恢复/压缩用）。
func (runner *Engine) buildStateContext(ctx context.Context) (string, error) {
	if runner == nil {
		return "", nil
	}
	return runner.stateCfg.buildBlock(ctx)
}

// SetTurnToolFilter scopes the tool set for subsequent turns. A nil filter
// restores unrestricted access. Higher-level runtimes set it around their own
// turns and restore the previous value afterwards.
func (runner *Engine) SetTurnToolFilter(filter ToolFilter) {
	if runner == nil {
		return
	}
	runner.toolGate.setTurnFilter(filter)
}

// currentToolFilter returns the active turn tool filter, if any.
func (runner *Engine) currentToolFilter() ToolFilter {
	if runner == nil {
		return nil
	}
	return runner.toolGate.currentTurnFilter()
}

// SystemSupplement returns the current additional system instructions.
func (runner *Engine) SystemSupplement() string {
	if runner == nil {
		return ""
	}
	return runner.promptCtx.currentSystemSupplement()
}

func (runner *Engine) SetYoloMode(enabled bool) (bool, error) {
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
	if handler := runner.toolGate.currentYoloHandler(); handler != nil {
		handler(enabled)
	}
	return controller.OutsideRootAllowed(), nil
}

func (runner *Engine) YoloMode() bool {
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

func (runner *Engine) validate() error {
	if runner == nil || runner.model == nil || runner.display == nil {
		return fmt.Errorf("engine 未初始化: model 或 display 为空")
	}
	return nil
}

// skillState 收敛技能注册表与本 turn 激活的技能上下文，自带锁。
type skillState struct {
	mu            sync.RWMutex
	registry      *skill.Registry
	activeContext string
}

func (s *skillState) setRegistry(registry *skill.Registry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.registry = registry
}

func (s *skillState) currentRegistry() *skill.Registry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.registry
}

func (s *skillState) setActiveContext(contextText string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.activeContext = contextText
}

func (s *skillState) clearActiveContext() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.activeContext = ""
}

func (s *skillState) currentActiveContext() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.activeContext
}

func (runner *Engine) SetSkillRegistry(registry *skill.Registry) {
	if runner == nil {
		return
	}
	runner.skills.setRegistry(registry)
}

func (runner *Engine) SkillRoots() []string {
	registry := runner.currentSkillRegistry()
	if registry == nil {
		return nil
	}
	return registry.Roots()
}

func (runner *Engine) activateSkillContext(input string) {
	if runner == nil {
		return
	}
	registry := runner.currentSkillRegistry()
	if registry == nil {
		runner.skills.clearActiveContext()
		return
	}
	contextText, _, errs := registry.InstructionContext(input)
	runner.skills.setActiveContext(contextText)
	for _, err := range errs {
		if err != nil {
			runner.notifySystem("skills", err.Error())
		}
	}
}

func (runner *Engine) clearActiveSkillContext() {
	if runner == nil {
		return
	}
	runner.skills.clearActiveContext()
}

func (runner *Engine) currentSkillRegistry() *skill.Registry {
	if runner == nil {
		return nil
	}
	return runner.skills.currentRegistry()
}

func (runner *Engine) currentSkillContext() string {
	if runner == nil {
		return ""
	}
	return runner.skills.currentActiveContext()
}

func (runner *Engine) ContextStats(limitTokens int, _ string) ContextStats {
	if limitTokens <= 0 {
		limitTokens = 1024 * 1024
	}
	if runner == nil {
		return ContextStats{LimitTokens: limitTokens}
	}

	runner.mu.RLock()
	sessionID := runner.sessionID
	runner.mu.RUnlock()

	contextUsage, contextKnown := runner.usage.contextUsage()
	current := usageTotalsFromUsage(contextUsage, contextKnown)
	taskTokens := 0
	if provider := runner.taskEnv.tokens(); provider != nil {
		taskTokens = provider.TotalTaskTokens(sessionID)
	}

	sessionUsage, sessionKnown := runner.usage.sessionUsage()
	sessionTotals := usageTotalsFromUsage(sessionUsage, sessionKnown)

	return ContextStats{
		UsedTokens:        current.used + taskTokens,
		CacheTokens:       current.cache,
		LimitTokens:       limitTokens,
		SessionUsedTokens: sessionTotals.used + taskTokens,
	}
}
