package loop

import (
	"codex-agent-go/internal/message"
	"codex-agent-go/internal/model"
	"codex-agent-go/internal/skill"
	"codex-agent-go/internal/tokentracer"
	"codex-agent-go/internal/tool"
	"codex-agent-go/internal/ui"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

const (
	maxToolRounds       = 500
	toolUseResponseType = "tool_use"
)

// ModelStreamer 是 loop 层依赖的最小模型流式接口。
type ModelStreamer interface {
	StreamMessage(ctx context.Context, messages []message.Message, tools []model.ToolDefinition) (<-chan model.StreamEvent, error)
}

// HistoryStore 是 Runner 依赖的最小历史存储接口。
// loop 层只关心“加载历史”和“追加历史”，不需要知道完整 session 能力。
type HistoryStore interface {
	LoadResolvedHistory(ctx context.Context, sessionID string) ([]message.Message, error)
	Append(ctx context.Context, sessionID string, msgs ...message.Message) error
}

type historyExistenceStore interface {
	Exists(ctx context.Context, sessionID string) (bool, error)
}

// Runner 负责单轮工具闭环调度。
// SubagentTokensProvider returns the cumulative token usage for all subagents
// spawned by a given parent session.
type SubagentTokensProvider interface {
	TotalSubagentTokens(parentSessionID string) int
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
	// history 保存”已经成功完成”的多轮对话消息（in-memory fallback when eventStore is nil）。
	history           []message.Message
	usage             model.Usage
	usageKnown        bool
	sessionUsage      model.Usage
	sessionUsageKnown bool
	// pendingSupplements holds ephemeral supplements (not event-sourced, transient per session).
	pendingSupplements []string
	// systemSupplement is a configuration value set at construction time.
	systemSupplement       string
	subagentTokensProvider SubagentTokensProvider
	// caps bundles optional capability dependencies.
	caps RunnerCapabilities
	// activeSkillContext is per-session ephemeral state, not event-sourced.
	activeSkillContext string
	// traceState groups the trace IDs that were previously top-level fields.
	traceState struct {
		stageID string
		agentID string
	}
	// Event sourcing (optional — nil means fall back to in-memory behavior).
	eventStore    SessionEventStore
	snapshotStore SnapshotStore
}

// RunnerCapabilities holds optional capability dependencies for Runner.
// All fields are nil-safe; Runner checks for nil before use.
type RunnerCapabilities struct {
	CompactToolPrompt bool
	StreamMAEnabled   bool
	StreamMASubagents StreamMASubagentRunner // may be nil
	SkillRegistry     *skill.Registry        // may be nil; overrides default
	TokenTracer       *tokentracer.Tracer    // may be nil
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
}

type outputMode int

const (
	outputModeUndecided outputMode = iota
	outputModeVisible
	outputModeSuppressed
)

type turnState struct {
	content      strings.Builder
	pending      strings.Builder
	toolCalls    []message.ToolCall
	wroteOutput  bool
	outputMode   outputMode
	usage        model.Usage
	usageKnown   bool
	traceStageID string
	traceAgentID string
}

type toolUseEnvelope struct {
	Type  string          `json:"type"`
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

// NewRunner 创建调度器。
func NewRunner(model ModelStreamer, output ui.UI, registry *tool.Registry, store HistoryStore, sessionID string) *Runner {
	return NewRunnerWithInstructionRoot(model, output, registry, store, sessionID, "")
}

// NewRunnerWithInstructionRoot 创建带项目指令根目录的调度器。
func NewRunnerWithInstructionRoot(model ModelStreamer, output ui.UI, registry *tool.Registry, store HistoryStore, sessionID, instructionRoot string) *Runner {
	return &Runner{
		model:     model,
		ui:        output,
		registry:  registry,
		store:     store,
		sessionID: sessionID,
		workRoot:  instructionRoot,
		prompt:    NewPromptBuilder(NewInstructionManager(instructionRoot)),
		caps: RunnerCapabilities{
			SkillRegistry:   skill.NewRegistry(skill.DefaultRoots(instructionRoot)),
			StreamMAEnabled: true,
		},
	}
}

// RunTurn 执行一次最小工具闭环流程，并返回最终 assistant 消息。
func (runner *Runner) RunTurn(ctx context.Context, input string) (msg message.Message, err error) {
	if err := runner.validate(); err != nil {
		return message.Message{}, err
	}
	runner.activateSkillContext(input)
	defer runner.clearActiveSkillContext()

	// Audit: record the user input that started this turn.
	runner.emitAuditEvent(ctx, SessionEvent{
		Kind:      EventKindUserInput,
		UserInput: &SessionUserInputPayload{Text: input},
	})

	if runner.historyIsNil() && runner.store != nil {
		messages, err := runner.store.LoadResolvedHistory(ctx, runner.sessionID)
		if err != nil {
			if existsStore, ok := runner.store.(historyExistenceStore); ok {
				exists, existsErr := existsStore.Exists(ctx, runner.sessionID)
				if existsErr != nil {
					return message.Message{}, existsErr
				}
				if !exists {
					runner.setHistoryIfNil(nil)
				} else {
					return message.Message{}, err
				}
			} else {
				return message.Message{}, err
			}
		} else {
			runner.setHistoryIfNil(messages)
		}
	}

	if invocation, ok := parseStreamMAInvocation(input); ok {
		if !runner.currentStreamMAEnabled() {
			return message.Message{}, fmt.Errorf("streamma is disabled; start with -streamma=true or set GOCODE_STREAMMA=1 to enable /streamma")
		}
		return runner.runStreamMATurn(ctx, input, invocation)
	}

	trace := runner.beginTraceTurn(input, "conversation")
	defer func() {
		runner.finishTraceTurn(trace, err)
	}()

	// 每一轮都基于“已提交的历史副本”工作。
	// 这样即使当前轮中途失败，也不会污染下一轮上下文。
	history, injectedSupplements := runner.buildTurnHistory(input)
	committed := false
	defer func() {
		if !committed && len(injectedSupplements) > 0 {
			runner.prependSupplements(injectedSupplements)
		}
	}()
	for round := 0; round < maxToolRounds; round++ {
		var injected []string
		history, injected = runner.appendPendingSupplements(history)
		injectedSupplements = append(injectedSupplements, injected...)

		assistantMessage, err := runner.runModelTurn(ctx, history)
		if err != nil {
			return message.Message{}, err
		}
		history = append(history, assistantMessage)

		toolCalls := toolCallsFromMessage(assistantMessage)
		if len(toolCalls) == 0 {
			// 只有当本轮完整结束时，才把本轮消息提交为新的会话历史。
			if err := runner.commitHistory(ctx, history); err != nil {
				return message.Message{}, err
			}
			committed = true
			return assistantMessage, nil
		}

		toolResult, err := runner.runToolCalls(ctx, toolCalls)
		if err != nil {
			return message.Message{}, err
		}
		history = append(history, toolResult)
	}

	return message.Message{}, fmt.Errorf("tool loop exceeded max rounds: %d", maxToolRounds)
}

func (runner *Runner) validate() error {
	if runner == nil || runner.model == nil || runner.ui == nil {
		return fmt.Errorf("runner 未初始化: model 或 ui 为空")
	}
	return nil
}

// SetSkillRegistry overrides the local skill registry. It is primarily useful
// for tests and embedded callers that want a constrained skill search path.
func (runner *Runner) SetSkillRegistry(registry *skill.Registry) {
	if runner == nil {
		return
	}
	runner.mu.Lock()
	defer runner.mu.Unlock()
	runner.caps.SkillRegistry = registry
}

// SkillRoots returns the active skill search roots for tools that need to read
// selected skill references without opening write access outside the workspace.
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
	contextText, loaded, errs := registry.InstructionContext(input)
	runner.mu.Lock()
	runner.activeSkillContext = contextText
	runner.mu.Unlock()
	if len(loaded) > 0 {
		names := make([]string, 0, len(loaded))
		for _, sk := range loaded {
			names = append(names, sk.Name)
		}
		runner.notifySystem("skills", "loaded "+strings.Join(names, ", "))
	}
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
	return runner.caps.SkillRegistry
}

func (runner *Runner) currentSkillContext() string {
	if runner == nil {
		return ""
	}
	runner.mu.RLock()
	defer runner.mu.RUnlock()
	return runner.activeSkillContext
}

func (runner *Runner) runModelTurn(ctx context.Context, history []message.Message) (message.Message, error) {
	var tools []model.ToolDefinition
	if runner.registry != nil {
		tools = runner.registry.Definitions()
	}
	events, err := runner.model.StreamMessage(ctx, runner.buildModelMessages(history), tools)
	if err != nil {
		return message.Message{}, err
	}

	return runner.consumeStream(ctx, events)
}

func buildUserMessages(input string) []message.Message {
	return []message.Message{
		{
			Role:    message.RoleUser,
			Content: input,
		},
	}
}

// SubmitSupplement records a user supplement that should be injected into the
// next available model step. It is safe to call while RunTurn is streaming.
func (runner *Runner) SubmitSupplement(input string) bool {
	input = strings.TrimSpace(input)
	if runner == nil || input == "" {
		return false
	}
	runner.mu.Lock()
	runner.pendingSupplements = append(runner.pendingSupplements, input)
	runner.mu.Unlock()
	return true
}

// PendingSupplementCount returns supplements that have not yet been injected.
func (runner *Runner) PendingSupplementCount() int {
	if runner == nil {
		return 0
	}
	runner.mu.RLock()
	defer runner.mu.RUnlock()
	return len(runner.pendingSupplements)
}

// buildTurnHistory 复制已完成历史，并在尾部追加当前用户输入。
// 这样单轮执行可以安全地在本地 history 上反复追加 tool_use / tool_result。
func (runner *Runner) buildTurnHistory(input string) ([]message.Message, []string) {
	runner.mu.RLock()
	history := append([]message.Message(nil), runner.history...)
	runner.mu.RUnlock()
	supplements := runner.drainSupplements()
	history = append(history, buildSupplementMessages(supplements)...)
	history = append(history, buildUserMessages(input)...)
	return history, supplements
}

func (runner *Runner) appendPendingSupplements(history []message.Message) ([]message.Message, []string) {
	supplements := runner.drainSupplements()
	if len(supplements) == 0 {
		return history, nil
	}
	return append(history, buildSupplementMessages(supplements)...), supplements
}

func (runner *Runner) drainSupplements() []string {
	if runner == nil {
		return nil
	}
	runner.mu.Lock()
	defer runner.mu.Unlock()
	if len(runner.pendingSupplements) == 0 {
		return nil
	}
	supplements := append([]string(nil), runner.pendingSupplements...)
	runner.pendingSupplements = nil
	return supplements
}

func (runner *Runner) prependSupplements(supplements []string) {
	if runner == nil || len(supplements) == 0 {
		return
	}
	runner.mu.Lock()
	defer runner.mu.Unlock()
	combined := make([]string, 0, len(supplements)+len(runner.pendingSupplements))
	combined = append(combined, supplements...)
	combined = append(combined, runner.pendingSupplements...)
	runner.pendingSupplements = combined
}

func buildSupplementMessages(supplements []string) []message.Message {
	messages := make([]message.Message, 0, len(supplements))
	for _, supplement := range supplements {
		supplement = strings.TrimSpace(supplement)
		if supplement == "" {
			continue
		}
		messages = append(messages, message.Message{
			Role:    message.RoleUser,
			Content: "Supplemental instruction submitted while this turn was running:\n" + supplement,
		})
	}
	return messages
}

// commitHistory 在一次 turn 成功结束后提交历史。
// 先计算新增消息，再更新内存、持久化磁盘，两步顺序不能颠倒。
func (runner *Runner) commitHistory(ctx context.Context, history []message.Message) error {
	runner.mu.RLock()
	prevLen := len(runner.history)
	runner.mu.RUnlock()

	newMsgs := history[prevLen:]

	if runner.store != nil && len(newMsgs) > 0 {
		if err := runner.store.Append(ctx, runner.sessionID, newMsgs...); err != nil {
			return fmt.Errorf("保存会话历史失败: %w", err)
		}
	}

	// Emit event-sourcing events for each new message.
	if runner.eventStore != nil && len(newMsgs) > 0 {
		events := make([]SessionEvent, 0, len(newMsgs))
		for i := range newMsgs {
			msg := newMsgs[i]
			events = append(events, SessionEvent{
				Kind:    EventKindHistoryMessage,
				Message: &msg,
			})
		}
		if err := runner.eventStore.Append(ctx, runner.sessionID, events...); err != nil {
			return fmt.Errorf("保存事件历史失败: %w", err)
		}
	}

	// 持久化成功后再更新内存副本。
	runner.mu.Lock()
	runner.history = append([]message.Message(nil), history...)
	runner.mu.Unlock()

	// Audit: mark the turn as fully committed.
	if len(newMsgs) > 0 {
		runner.emitAuditEvent(ctx, SessionEvent{
			Kind:       EventKindTurnCommitted,
			TurnCommit: &SessionTurnCommitPayload{MessageCount: len(newMsgs)},
		})
	}
	return nil
}

// ResetHistory 清空当前对话上下文。
// 交互式 REPL 可以用它实现 /clear 这类命令。
func (runner *Runner) ResetHistory() {
	runner.mu.Lock()
	defer runner.mu.Unlock()
	runner.history = []message.Message{}
	runner.usage = model.Usage{}
	runner.usageKnown = false
	runner.sessionUsage = model.Usage{}
	runner.sessionUsageKnown = false
	runner.pendingSupplements = nil
}

// LoadHistory 从 store 加载指定 session 的历史，并替换当前 runner 的历史。
// 同时更新 runner.sessionID 以指向新会话。
func (runner *Runner) LoadHistory(ctx context.Context, sessionID string) error {
	if runner.store == nil {
		return fmt.Errorf("runner store is nil")
	}
	messages, err := runner.store.LoadResolvedHistory(ctx, sessionID)
	if err != nil {
		return err
	}
	runner.mu.Lock()
	defer runner.mu.Unlock()
	runner.history = append([]message.Message(nil), messages...)
	runner.sessionID = sessionID
	runner.usage = model.Usage{}
	runner.usageKnown = false
	runner.sessionUsage = model.Usage{}
	runner.sessionUsageKnown = false
	runner.pendingSupplements = nil
	return nil
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
	return ContextStats{
		UsedTokens:  current.used + subagentTokens,
		CacheTokens: current.cache,
		LimitTokens: limitTokens,
	}
}

func (runner *Runner) historyIsNil() bool {
	runner.mu.RLock()
	defer runner.mu.RUnlock()
	return runner.history == nil
}

func (runner *Runner) setHistoryIfNil(history []message.Message) {
	runner.mu.Lock()
	defer runner.mu.Unlock()
	if runner.history == nil {
		runner.history = append([]message.Message(nil), history...)
	}
}

// loadSessionState loads the projected history, usage, and supplements from
// the event store (with optional snapshot acceleration). When eventStore is nil
// it returns the current in-memory values instead.
func (runner *Runner) loadSessionState(ctx context.Context) ([]message.Message, UsageState, []string) {
	runner.mu.RLock()
	es := runner.eventStore
	ss := runner.snapshotStore
	sid := runner.sessionID
	runner.mu.RUnlock()

	if es == nil {
		// Fall back to in-memory state.
		runner.mu.RLock()
		hist := append([]message.Message(nil), runner.history...)
		us := UsageState{
			Usage:             runner.usage,
			UsageKnown:        runner.usageKnown,
			SessionUsage:      runner.sessionUsage,
			SessionUsageKnown: runner.sessionUsageKnown,
		}
		runner.mu.RUnlock()
		return hist, us, nil
	}

	var snap *SessionSnapshot
	if ss != nil {
		if s, ok, err := ss.Load(ctx, sid); err == nil && ok {
			snap = &s
		}
	}

	events, err := es.Load(ctx, sid)
	if err != nil {
		// On error, fall back to in-memory state.
		runner.mu.RLock()
		hist := append([]message.Message(nil), runner.history...)
		us := UsageState{
			Usage:             runner.usage,
			UsageKnown:        runner.usageKnown,
			SessionUsage:      runner.sessionUsage,
			SessionUsageKnown: runner.sessionUsageKnown,
		}
		runner.mu.RUnlock()
		return hist, us, nil
	}

	history := ApplyHistoryProjection(snap, events)
	usageState := ApplyUsageProjection(snap, events)

	var supplements []string
	if snap != nil {
		supplements = append(supplements, snap.Supplements...)
	}

	return history, usageState, supplements
}

func buildSystemMessage(content string) message.Message {
	return message.Message{
		Role:    message.RoleSystem,
		Content: content,
	}
}

func buildAssistantMessage(content string) message.Message {
	return message.Message{
		Role:    message.RoleAssistant,
		Content: content,
	}
}

func buildToolResultMessage(toolUseID, content string, isError bool) message.Message {
	return message.Message{
		Role: message.RoleUser,
		ToolResult: &message.ToolResult{
			ToolUseID: toolUseID,
			Content:   content,
			IsError:   isError,
		},
	}
}

func buildToolResultsMessage(results []message.ToolResult) message.Message {
	msg := message.Message{Role: message.RoleUser}
	if len(results) == 0 {
		return msg
	}
	if len(results) == 1 {
		result := results[0]
		msg.ToolResult = &result
		return msg
	}
	msg.ToolResults = append([]message.ToolResult(nil), results...)
	return msg
}

func toolCallsFromMessage(msg message.Message) []message.ToolCall {
	if len(msg.ToolUses) > 0 {
		return append([]message.ToolCall(nil), msg.ToolUses...)
	}
	if msg.ToolUse == nil {
		return nil
	}
	return []message.ToolCall{*msg.ToolUse}
}

func toolResultsFromMessage(msg message.Message) []message.ToolResult {
	if len(msg.ToolResults) > 0 {
		return append([]message.ToolResult(nil), msg.ToolResults...)
	}
	if msg.ToolResult == nil {
		return nil
	}
	return []message.ToolResult{*msg.ToolResult}
}

func (runner *Runner) buildModelMessages(history []message.Message) []message.Message {
	messages := make([]message.Message, 0, len(history)+1)
	messages = append(messages, buildSystemMessage(runner.buildSystemPrompt()))
	for _, msg := range history {
		rendered := renderMessageForModel(msg)
		// 跳过空 content 的 assistant 消息，避免发送 {"role":"assistant"} 触发 API 400 错误。
		// 空 assistant 消息可能来自模型返回空流式内容的边界情况。
		if rendered.Role == message.RoleAssistant && rendered.Content == "" {
			continue
		}
		messages = append(messages, rendered)
	}
	return messages
}

// 构建系统提示词
func (runner *Runner) buildSystemPrompt() string {
	descriptions := []string{}
	if runner.registry != nil {
		runner.mu.RLock()
		compactToolPrompt := runner.caps.CompactToolPrompt
		runner.mu.RUnlock()
		if compactToolPrompt {
			descriptions = runner.registry.DescribeBrief()
		} else {
			descriptions = runner.registry.Describe()
		}
	}
	if runner.prompt == nil {
		runner.prompt = NewPromptBuilder(NewInstructionManager(""))
	}
	prompt := runner.prompt.Build(descriptions)
	runner.mu.RLock()
	supplement := runner.systemSupplement
	skillContext := runner.activeSkillContext
	runner.mu.RUnlock()
	var sections []string
	if strings.TrimSpace(skillContext) != "" {
		sections = append(sections, "Selected skill instructions:\n"+strings.TrimSpace(skillContext))
	}
	if strings.TrimSpace(supplement) != "" {
		sections = append(sections, "Additional system instructions:\n"+strings.TrimSpace(supplement))
	}
	if len(sections) == 0 {
		return prompt
	}
	return strings.TrimRight(prompt, "\n") + "\n\n" + strings.Join(sections, "\n\n") + "\n"
}

func renderMessageForModel(msg message.Message) message.Message {
	switch {
	case len(toolCallsFromMessage(msg)) > 0:
		calls := toolCallsFromMessage(msg)
		parts := make([]string, 0, len(calls))
		for _, call := range calls {
			parts = append(parts, marshalJSON(toolUseEnvelope{Type: toolUseResponseType, ID: call.ID, Name: call.Name, Input: call.Input}))
		}
		return message.Message{
			Role:    message.RoleAssistant,
			Content: strings.Join(parts, "\n"),
		}
	case len(toolResultsFromMessage(msg)) > 0:
		results := toolResultsFromMessage(msg)
		parts := make([]string, 0, len(results))
		for _, result := range results {
			parts = append(parts, marshalJSON(result))
		}
		label := "TOOL_RESULT:\n"
		if len(results) > 1 {
			label = "TOOL_RESULTS:\n"
		}
		return message.Message{
			Role:    message.RoleUser,
			Content: label + strings.Join(parts, "\n"),
		}
	default:
		return message.Message{
			Role:    msg.Role,
			Content: msg.Content,
		}
	}
}

func marshalJSON(v any) string {
	data, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(data)
}

func (runner *Runner) consumeStream(ctx context.Context, events <-chan model.StreamEvent) (message.Message, error) {
	var state turnState
	state.traceStageID, state.traceAgentID = runner.currentTraceIDs()

	for ev := range events {
		msg, done, err := runner.handleEvent(&state, ev)
		if err != nil {
			return message.Message{}, err
		}
		if done {
			return msg, nil
		}
	}

	return runner.finishWithoutDone(ctx, state)
}

func (runner *Runner) handleEvent(state *turnState, ev model.StreamEvent) (message.Message, bool, error) {
	if ev.Err != nil {
		return message.Message{}, false, runner.failAfterPartialOutput(state.wroteOutput, ev.Err)
	}
	if ev.Usage != nil {
		runner.recordUsageEvent(state, *ev.Usage)
	}
	if err := runner.appendThinking(ev.Thinking); err != nil {
		return message.Message{}, false, err
	}

	if err := runner.appendDelta(state, ev.Delta); err != nil {
		return message.Message{}, false, err
	}
	appendStreamToolCalls(state, ev.ToolCalls)

	if !ev.Done {
		return message.Message{}, false, nil
	}

	msg, err := runner.finalizeAssistantMessage(state)
	return msg, true, err
}

func (runner *Runner) recordUsageEvent(state *turnState, usage model.Usage) {
	previousTrace := tokentracer.UsageFromModelUsage(state.usage)
	previous := usageTotalsFromUsage(state.usage, state.usageKnown)
	state.usage = mergeUsageSnapshot(state.usage, usage)
	state.usageKnown = true
	current := usageTotalsFromUsage(state.usage, true)
	runner.setCurrentUsage(state.usage)
	runner.addSessionUsage(current.delta(previous))
	runner.emitModelUsage(state.usage)
	currentTrace := tokentracer.UsageFromModelUsage(state.usage)
	runner.recordTraceUsage(state.traceStageID, state.traceAgentID, currentTrace.Delta(previousTrace), map[string]any{
		"source": "model_stream",
	})
}

func (runner *Runner) emitModelUsage(usage model.Usage) {
	if runner == nil || runner.ui == nil {
		return
	}
	receiver, ok := runner.ui.(modelUsageReceiver)
	if !ok {
		return
	}
	receiver.OnModelUsage(usage)
}

// SetEventStore injects an event store for event sourcing.
// If nil, the runner falls back to pure in-memory behavior.
func (runner *Runner) SetEventStore(store SessionEventStore) {
	if runner == nil {
		return
	}
	runner.mu.Lock()
	defer runner.mu.Unlock()
	runner.eventStore = store
}

// SetSnapshotStore injects a snapshot store for event sourcing.
func (runner *Runner) SetSnapshotStore(store SnapshotStore) {
	if runner == nil {
		return
	}
	runner.mu.Lock()
	defer runner.mu.Unlock()
	runner.snapshotStore = store
}

// emitAuditEvent appends a single best-effort audit event to the event store.
// It is intentionally non-blocking and silently drops errors — audit events
// must not disrupt the main turn flow.
func (runner *Runner) emitAuditEvent(ctx context.Context, event SessionEvent) {
	if runner == nil {
		return
	}
	runner.mu.RLock()
	es := runner.eventStore
	sid := runner.sessionID
	runner.mu.RUnlock()
	if es == nil {
		return
	}
	event.SessionID = sid
	_ = es.Append(ctx, sid, event)
}

func (runner *Runner) setCurrentUsage(usage model.Usage) {
	runner.mu.Lock()
	runner.usage = usage
	runner.usageKnown = true
	es := runner.eventStore
	sid := runner.sessionID
	runner.mu.Unlock()

	if es != nil {
		_ = es.Append(context.Background(), sid, SessionEvent{
			Kind:  EventKindUsageUpdate,
			Usage: &SessionUsagePayload{Usage: usage, IsSession: false},
		})
	}
}

func (runner *Runner) addSessionUsage(delta tokenUsageTotals) {
	runner.mu.Lock()
	current := usageTotalsFromUsage(runner.sessionUsage, runner.sessionUsageKnown)
	current = current.add(delta)
	runner.sessionUsage = usageFromTotals(current)
	runner.sessionUsageKnown = true
	accumulated := runner.sessionUsage
	es := runner.eventStore
	sid := runner.sessionID
	runner.mu.Unlock()

	if es != nil {
		_ = es.Append(context.Background(), sid, SessionEvent{
			Kind:  EventKindUsageUpdate,
			Usage: &SessionUsagePayload{Usage: accumulated, IsSession: true},
		})
	}
}

func mergeUsageSnapshot(current, next model.Usage) model.Usage {
	if next.InputTokens != 0 {
		current.InputTokens = next.InputTokens
	}
	if next.OutputTokens != 0 {
		current.OutputTokens = next.OutputTokens
	}
	if next.CacheCreationInputTokens != 0 {
		current.CacheCreationInputTokens = next.CacheCreationInputTokens
	}
	if next.CacheReadInputTokens != 0 {
		current.CacheReadInputTokens = next.CacheReadInputTokens
	}
	if next.PromptTokens != 0 {
		current.PromptTokens = next.PromptTokens
	}
	if next.CompletionTokens != 0 {
		current.CompletionTokens = next.CompletionTokens
	}
	if next.TotalTokens != 0 {
		current.TotalTokens = next.TotalTokens
	}
	if next.PromptCacheHitTokens != 0 {
		current.PromptCacheHitTokens = next.PromptCacheHitTokens
	}
	if next.PromptCacheMissTokens != 0 {
		current.PromptCacheMissTokens = next.PromptCacheMissTokens
	}
	current.PromptTokensDetails = mergeTokenDetails(current.PromptTokensDetails, next.PromptTokensDetails)
	current.InputTokensDetails = mergeTokenDetails(current.InputTokensDetails, next.InputTokensDetails)
	return current
}

func mergeTokenDetails(current, next model.TokenDetails) model.TokenDetails {
	if next.CachedTokens != 0 {
		current.CachedTokens = next.CachedTokens
	}
	if next.CacheReadTokens != 0 {
		current.CacheReadTokens = next.CacheReadTokens
	}
	if next.CacheReadInputTokens != 0 {
		current.CacheReadInputTokens = next.CacheReadInputTokens
	}
	if next.CacheCreationTokens != 0 {
		current.CacheCreationTokens = next.CacheCreationTokens
	}
	if next.CacheCreationInputTokens != 0 {
		current.CacheCreationInputTokens = next.CacheCreationInputTokens
	}
	return current
}

func usageTotalsFromUsage(usage model.Usage, known bool) tokenUsageTotals {
	if !known {
		return tokenUsageTotals{}
	}
	used := usage.ContextTokenCount()
	cache := usage.CacheHitTokens()
	output := usage.CompletionTokenCount()
	if used < 0 {
		used = 0
	}
	if cache < 0 {
		cache = 0
	}
	if output < 0 {
		output = 0
	}
	if used < cache {
		used = cache
	}
	if cache > used {
		cache = used
	}
	if used < output {
		used = output
	}
	return tokenUsageTotals{used: used, cache: cache, output: output}
}

func usageFromTotals(t tokenUsageTotals) model.Usage {
	t = t.normalized()
	return model.Usage{
		PromptTokens:         maxInt(0, t.used-t.output),
		CompletionTokens:     t.output,
		TotalTokens:          t.used,
		PromptCacheHitTokens: t.cache,
	}
}

func (t tokenUsageTotals) add(other tokenUsageTotals) tokenUsageTotals {
	return tokenUsageTotals{
		used:   maxInt(0, t.used) + maxInt(0, other.used),
		cache:  maxInt(0, t.cache) + maxInt(0, other.cache),
		output: maxInt(0, t.output) + maxInt(0, other.output),
	}.normalized()
}

func (t tokenUsageTotals) delta(previous tokenUsageTotals) tokenUsageTotals {
	return tokenUsageTotals{
		used:   maxInt(0, t.used-previous.used),
		cache:  maxInt(0, t.cache-previous.cache),
		output: maxInt(0, t.output-previous.output),
	}
}

func (t tokenUsageTotals) normalized() tokenUsageTotals {
	if t.used < 0 {
		t.used = 0
	}
	if t.cache < 0 {
		t.cache = 0
	}
	if t.output < 0 {
		t.output = 0
	}
	if t.used < t.cache {
		t.used = t.cache
	}
	if t.cache > t.used {
		t.cache = t.used
	}
	if t.used < t.output {
		t.used = t.output
	}
	return t
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func (runner *Runner) appendDelta(state *turnState, delta string) error {
	if delta == "" {
		return nil
	}

	state.content.WriteString(delta)

	switch state.outputMode {
	case outputModeVisible:
		return runner.writeDelta(state, delta)
	case outputModeSuppressed:
		return nil
	}

	state.pending.WriteString(delta)
	state.outputMode = detectOutputMode(state.content.String())
	if state.outputMode == outputModeUndecided {
		return nil
	}
	if state.outputMode == outputModeSuppressed {
		state.pending.Reset()
		return nil
	}

	pending := state.pending.String()
	state.pending.Reset()
	return runner.writeDelta(state, pending)
}

func detectOutputMode(content string) outputMode {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return outputModeUndecided
	}

	// 只要当前累计内容里已经能提取出合法 tool_use，就直接压制输出。
	if len(extractToolUseEnvelopes(trimmed)) > 0 {
		return outputModeSuppressed
	}

	// 对“看起来像工具前置说明”的内容持续保持观望，避免模型先输出大段
	// “我将使用工具……”，随后再跟 fenced tool_use JSON。
	if looksLikeToolPreamble(trimmed) {
		return outputModeUndecided
	}

	// 模型有时会把 tool_use JSON 包在 Markdown fence 里返回。
	// 这里一旦看到 “{” 或 “`” 开头，就先抑制输出，等 finalize 再判定
	// 它到底是工具调用还是普通文本，避免把 tool_use JSON 泄漏到终端。
	if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") || strings.HasPrefix(trimmed, "`") {
		return outputModeSuppressed
	}

	// <tool_call>、<tool> 系列 以及 <简单标签名> 开头时直接抑制，等 finalize 确认
	lowerTrimmed := strings.ToLower(trimmed)
	if strings.HasPrefix(lowerTrimmed, "<tool_call") ||
		strings.HasPrefix(lowerTrimmed, "<tool ") ||
		strings.HasPrefix(lowerTrimmed, "<tool>") {
		return outputModeSuppressed
	}
	// <glob>、<bash>、<read> 等简单标签名开头（<toolname>JSON 格式）
	if strings.HasPrefix(trimmed, "<") && !strings.HasPrefix(trimmed, "</") {
		end := strings.IndexByte(trimmed[1:], '>')
		if end != -1 && isSimpleXMLTagName(trimmed[1:end+1]) {
			return outputModeSuppressed
		}
	}

	return outputModeVisible
}

func looksLikeToolPreamble(trimmed string) bool {
	lower := strings.ToLower(trimmed)
	hints := []string{
		"我将",
		"我会",
		"我来",
		"好的，我来",
		"让我",
		"先使用",
		"首先使用",
		"读取",
		"读出",
		"遍历",
		"列出",
		"查看",
		"i will",
		"i'll",
		"let me",
		"tool_use",
		"<invoke",
		"<tool_call",
		"<tool ",
		"<tool>",
		"工具",
		"调用",
		"使用",
		"ls",
		"read",
		"write",
		"grep",
		"glob",
		"bash",
		"webfetch",
		"```",
	}
	for _, hint := range hints {
		if strings.Contains(lower, strings.ToLower(hint)) {
			return true
		}
	}
	return false
}

func (runner *Runner) appendThinking(thinking string) error {
	if thinking == "" {
		return nil
	}
	runner.recordTraceEvent("thinking_delta", map[string]any{
		"text":  thinking,
		"bytes": len([]byte(thinking)),
	})
	if sink, ok := runner.ui.(ui.ThinkingDeltaReceiver); ok {
		if err := sink.OnThinkingDelta(thinking); err != nil {
			return err
		}
	}
	return nil
}

func (runner *Runner) writeDelta(state *turnState, delta string) error {
	if delta == "" {
		return nil
	}
	runner.recordTraceEvent("assistant_delta", map[string]any{
		"text":  delta,
		"bytes": len([]byte(delta)),
	})

	if err := runner.ui.OnAssistantDelta(delta); err != nil {
		return runner.failAfterPartialOutput(state.wroteOutput, err)
	}
	state.wroteOutput = true
	return nil
}

func (runner *Runner) finalizeAssistantMessage(state *turnState) (message.Message, error) {
	// Audit: record the full streamed assistant text (once per turn, not per chunk).
	if text := state.content.String(); text != "" {
		runner.emitAuditEvent(context.Background(), SessionEvent{
			Kind:           EventKindAssistantDelta,
			AssistantDelta: &SessionAssistantDeltaPayload{Text: text},
		})
	}

	if len(state.toolCalls) > 0 {
		return buildAssistantToolCallMessage(state.toolCalls), nil
	}

	msg := parseAssistantMessage(state.content.String())
	if len(toolCallsFromMessage(msg)) > 0 {
		return msg, nil
	}

	if !state.wroteOutput {
		if err := runner.writeDelta(state, msg.Content); err != nil {
			return message.Message{}, err
		}
	}
	if err := runner.ui.OnDone(); err != nil {
		return message.Message{}, err
	}

	return msg, nil
}

func parseAssistantMessage(content string) message.Message {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return buildAssistantMessage("")
	}

	envelopes := extractToolUseEnvelopes(trimmed)
	if len(envelopes) == 0 {
		return buildAssistantMessage(content)
	}

	calls := make([]message.ToolCall, 0, len(envelopes))
	for _, envelope := range envelopes {
		calls = append(calls, message.ToolCall{
			ID:    envelope.ID,
			Name:  envelope.Name,
			Input: envelope.Input,
		})
	}

	return buildAssistantToolCallMessage(calls)
}

func appendStreamToolCalls(state *turnState, calls []message.ToolCall) {
	if state == nil || len(calls) == 0 {
		return
	}
	state.toolCalls = append(state.toolCalls, calls...)
	state.pending.Reset()
	state.outputMode = outputModeSuppressed
}

func buildAssistantToolCallMessage(calls []message.ToolCall) message.Message {
	calls = normalizeToolCalls(calls)
	msg := message.Message{Role: message.RoleAssistant}
	if len(calls) == 1 {
		call := calls[0]
		msg.ToolUse = &call
	} else if len(calls) > 1 {
		msg.ToolUses = calls
		call := calls[0]
		msg.ToolUse = &call
	}
	return msg
}

func normalizeToolCalls(calls []message.ToolCall) []message.ToolCall {
	normalized := make([]message.ToolCall, 0, len(calls))
	for i, call := range calls {
		if strings.TrimSpace(call.Name) == "" {
			continue
		}
		if call.ID == "" {
			if len(calls) == 1 {
				call.ID = call.Name
			} else {
				call.ID = fmt.Sprintf("%s_%d", call.Name, i+1)
			}
		}
		if len(call.Input) == 0 {
			call.Input = json.RawMessage(`{}`)
		} else {
			call.Input = append(json.RawMessage(nil), call.Input...)
		}
		normalized = append(normalized, call)
	}
	return normalized
}

func extractToolUseEnvelope(trimmed string) (toolUseEnvelope, bool) {
	envelopes := extractToolUseEnvelopes(trimmed)
	if len(envelopes) == 0 {
		return toolUseEnvelope{}, false
	}
	return envelopes[0], true
}

// extractToolUseEnvelopes 从 assistant 输出中提取一个或多个合法的 tool_use 对象。
// 当前兼容七类格式：
// 1) 整段内容就是裸 JSON
// 2) 整段内容就是 fenced JSON
// 3) 说明文字中嵌入 fenced JSON 或裸 JSON
// 4) Claude/DeepSeek 兼容的 <invoke> + DSML 参数块
// 5) <tool_call><tool name="...">...</tool></tool_call> 格式
// 6) <tool><tool_call><name>X</name><input>JSON</input></tool_call></tool> 格式
// 7) <toolname>JSON</toolname> 格式（标签名即工具名）
func extractToolUseEnvelopes(trimmed string) []toolUseEnvelope {
	if envelopes, ok := decodeToolUseEnvelopes(trimmed); ok {
		return envelopes
	}

	if payload, ok := extractFencedToolUsePayload(trimmed); ok {
		if envelopes, matched := decodeToolUseEnvelopes(payload); matched {
			return envelopes
		}
	}

	if envelopes := extractEmbeddedToolUseEnvelopes(trimmed); len(envelopes) > 0 {
		return envelopes
	}

	if envelope, ok := extractInvokeToolUseEnvelope(trimmed); ok {
		return []toolUseEnvelope{envelope}
	}

	if envelope, ok := extractToolCallEnvelope(trimmed); ok {
		return []toolUseEnvelope{envelope}
	}

	if envelope, ok := extractTagNameToolEnvelope(trimmed); ok {
		return []toolUseEnvelope{envelope}
	}

	return nil
}

// extractTagNameToolEnvelope 处理 <toolname>JSON</toolname> 格式。
// 标签名即工具名，标签体为合法的 JSON 对象。
// 仅匹配标签名由字母/数字/下划线/连字符构成（无属性）的简单标签。
func extractTagNameToolEnvelope(content string) (toolUseEnvelope, bool) {
	for start := 0; start < len(content); {
		openIdx := strings.IndexByte(content[start:], '<')
		if openIdx == -1 {
			break
		}
		openIdx += start

		// 跳过结束标签
		if openIdx+1 < len(content) && content[openIdx+1] == '/' {
			start = openIdx + 2
			continue
		}

		// 找到标签结束 >
		closeAngle := strings.IndexByte(content[openIdx:], '>')
		if closeAngle == -1 {
			break
		}
		closeAngle += openIdx

		tagName := content[openIdx+1 : closeAngle]
		if !isSimpleXMLTagName(tagName) {
			start = closeAngle + 1
			continue
		}

		// 查找对应的结束标签
		closeTag := "</" + tagName + ">"
		closeIdx := strings.Index(strings.ToLower(content[closeAngle+1:]), strings.ToLower(closeTag))
		if closeIdx == -1 {
			start = closeAngle + 1
			continue
		}
		closeIdx += closeAngle + 1

		body := strings.TrimSpace(content[closeAngle+1 : closeIdx])
		// 标签体必须是有效的 JSON 对象
		if !strings.HasPrefix(body, "{") || !json.Valid([]byte(body)) {
			start = closeAngle + 1
			continue
		}

		return toolUseEnvelope{
			Type:  toolUseResponseType,
			Name:  tagName,
			Input: json.RawMessage(body),
		}, true
	}
	return toolUseEnvelope{}, false
}

// isSimpleXMLTagName 验证字符串是否为仅含字母/数字/下划线/连字符的简单标签名（无属性、无空格）。
func isSimpleXMLTagName(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '_' || r == '-') {
			return false
		}
	}
	return true
}

// extractToolCallEnvelope 处理以下三类 XML 工具调用格式：
//
//	格式 A：<tool_call><tool name="X">body</tool></tool_call>
//	        （body 可以是 JSON 或 XML 参数，</tool> 可省略由 </tool_call> 关闭）
//
//	格式 B：<tool><tool_call><name>X</name><input>JSON</input></tool_call></tool>
//	        （DeepSeek 部分模型输出的格式）
//
//	格式 C：<tool_call><name>X</name><input>JSON</input></tool_call>
//	        （同格式 B 但无外层 <tool> 包装）
func extractToolCallEnvelope(content string) (toolUseEnvelope, bool) {
	lower := strings.ToLower(content)

	// 格式 B / C：通过 <name>...</name> + <input>...</input> 提取
	if envelope, ok := extractNameInputEnvelope(content, lower); ok {
		return envelope, true
	}

	// 格式 A：通过 <tool name="..."> 提取
	toolIdx := strings.Index(lower, "<tool ")
	if toolIdx == -1 {
		return toolUseEnvelope{}, false
	}

	tagEndRel := strings.IndexByte(content[toolIdx:], '>')
	if tagEndRel == -1 {
		return toolUseEnvelope{}, false
	}
	tagEnd := toolIdx + tagEndRel

	openTag := content[toolIdx : tagEnd+1]
	name := extractTagAttribute(openTag, "name")
	if name == "" {
		return toolUseEnvelope{}, false
	}

	bodyStart := tagEnd + 1
	bodyEnd := len(content)
	restLower := strings.ToLower(content[bodyStart:])
	if closeIdx := strings.Index(restLower, "</tool>"); closeIdx != -1 {
		bodyEnd = bodyStart + closeIdx
	} else if closeIdx := strings.Index(restLower, "</tool_call>"); closeIdx != -1 {
		bodyEnd = bodyStart + closeIdx
	}

	body := strings.TrimSpace(content[bodyStart:bodyEnd])
	input := decodeInvokeInput(body)

	return toolUseEnvelope{
		Type:  toolUseResponseType,
		Name:  name,
		Input: input,
	}, true
}

// extractNameInputEnvelope 提取 <name>X</name><input>JSON</input> 格式的工具调用。
func extractNameInputEnvelope(content, lower string) (toolUseEnvelope, bool) {
	nameStart := strings.Index(lower, "<name>")
	if nameStart == -1 {
		return toolUseEnvelope{}, false
	}
	nameEnd := strings.Index(lower[nameStart:], "</name>")
	if nameEnd == -1 {
		return toolUseEnvelope{}, false
	}
	nameEnd += nameStart
	name := strings.TrimSpace(content[nameStart+len("<name>") : nameEnd])
	if name == "" {
		return toolUseEnvelope{}, false
	}

	inputStart := strings.Index(lower, "<input>")
	if inputStart == -1 {
		return toolUseEnvelope{}, false
	}
	inputEnd := strings.Index(lower[inputStart:], "</input>")
	if inputEnd == -1 {
		return toolUseEnvelope{}, false
	}
	inputEnd += inputStart
	inputBody := strings.TrimSpace(content[inputStart+len("<input>") : inputEnd])

	input := decodeInvokeInput(inputBody)
	return toolUseEnvelope{
		Type:  toolUseResponseType,
		Name:  name,
		Input: input,
	}, true
}

func decodeToolUseEnvelope(payload string) (toolUseEnvelope, bool) {
	payload = strings.TrimSpace(payload)
	if !strings.HasPrefix(payload, "{") {
		return toolUseEnvelope{}, false
	}

	var envelope toolUseEnvelope
	if err := json.Unmarshal([]byte(payload), &envelope); err != nil {
		return toolUseEnvelope{}, false
	}
	if envelope.Type != toolUseResponseType || envelope.Name == "" {
		return toolUseEnvelope{}, false
	}
	return envelope, true
}

func decodeToolUseEnvelopes(payload string) ([]toolUseEnvelope, bool) {
	payload = strings.TrimSpace(payload)
	if payload == "" {
		return nil, false
	}
	if envelope, ok := decodeToolUseEnvelope(payload); ok {
		return []toolUseEnvelope{envelope}, true
	}
	if !strings.HasPrefix(payload, "[") {
		return nil, false
	}
	var envelopes []toolUseEnvelope
	if err := json.Unmarshal([]byte(payload), &envelopes); err != nil {
		return nil, false
	}
	if len(envelopes) == 0 {
		return nil, false
	}
	for _, envelope := range envelopes {
		if envelope.Type != toolUseResponseType || envelope.Name == "" {
			return nil, false
		}
	}
	return envelopes, true
}

func extractMarkdownFenceBody(trimmed string) (string, bool) {
	rest := strings.TrimPrefix(trimmed, "```")
	newlineIndex := strings.IndexByte(rest, '\n')
	if newlineIndex == -1 {
		return "", false
	}

	bodyWithClose := rest[newlineIndex+1:]
	closeIndex := strings.LastIndex(bodyWithClose, "```")
	if closeIndex == -1 {
		return "", false
	}

	return bodyWithClose[:closeIndex], true
}

// extractFencedToolUsePayload 在整段文本中搜索 fenced code block，
// 并返回第一个看起来像 tool_use JSON 的 body。
func extractFencedToolUsePayload(trimmed string) (string, bool) {
	searchFrom := 0
	for {
		start := strings.Index(trimmed[searchFrom:], "```")
		if start == -1 {
			return "", false
		}
		start += searchFrom

		body, next, ok := extractFenceBodyAt(trimmed, start)
		if !ok {
			return "", false
		}
		body = strings.TrimSpace(body)
		if _, ok := decodeToolUseEnvelope(body); ok {
			return body, true
		}
		searchFrom = next
	}
}

func extractFenceBodyAt(content string, start int) (body string, next int, ok bool) {
	rest := content[start:]
	if !strings.HasPrefix(rest, "```") {
		return "", len(content), false
	}

	rest = strings.TrimPrefix(rest, "```")
	newlineIndex := strings.IndexByte(rest, '\n')
	if newlineIndex == -1 {
		return "", len(content), false
	}

	bodyWithClose := rest[newlineIndex+1:]
	closeIndex := strings.Index(bodyWithClose, "```")
	if closeIndex == -1 {
		return "", len(content), false
	}

	consumed := start + 3 + newlineIndex + 1 + closeIndex + 3
	return bodyWithClose[:closeIndex], consumed, true
}

// extractEmbeddedJSONObject 在整段文本中搜索平衡的 JSON 对象，
// 并返回第一个可解析为 tool_use 的对象。
func extractEmbeddedJSONObject(trimmed string) (string, bool) {
	for start := strings.IndexByte(trimmed, '{'); start != -1; {
		payload, next, ok := extractBalancedJSONObject(trimmed, start)
		if ok {
			if _, matched := decodeToolUseEnvelope(payload); matched {
				return payload, true
			}
		}

		if next >= len(trimmed) {
			break
		}
		relative := strings.IndexByte(trimmed[next:], '{')
		if relative == -1 {
			break
		}
		start = next + relative
	}
	return "", false
}

func extractEmbeddedToolUseEnvelopes(trimmed string) []toolUseEnvelope {
	var envelopes []toolUseEnvelope
	for start := strings.IndexByte(trimmed, '{'); start != -1; {
		payload, next, ok := extractBalancedJSONObject(trimmed, start)
		if ok {
			if envelope, matched := decodeToolUseEnvelope(payload); matched {
				envelopes = append(envelopes, envelope)
			}
		}

		if next >= len(trimmed) {
			break
		}
		relative := strings.IndexByte(trimmed[next:], '{')
		if relative == -1 {
			break
		}
		start = next + relative
	}
	return envelopes
}

func extractBalancedJSONObject(content string, start int) (string, int, bool) {
	if start < 0 || start >= len(content) || content[start] != '{' {
		return "", len(content), false
	}

	depth := 0
	inString := false
	escaped := false

	for i := start; i < len(content); i++ {
		ch := content[i]

		if inString {
			if escaped {
				escaped = false
				continue
			}
			if ch == '\\' {
				escaped = true
				continue
			}
			if ch == '"' {
				inString = false
			}
			continue
		}

		switch ch {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return content[start : i+1], i + 1, true
			}
		}
	}

	return "", len(content), false
}

func extractInvokeToolUseEnvelope(content string) (toolUseEnvelope, bool) {
	lower := strings.ToLower(content)
	start := strings.Index(lower, "<invoke")
	if start == -1 {
		return toolUseEnvelope{}, false
	}

	tagEnd := strings.Index(content[start:], ">")
	if tagEnd == -1 {
		return toolUseEnvelope{}, false
	}
	tagEnd += start

	openTag := content[start : tagEnd+1]
	name := extractTagAttribute(openTag, "name")
	if name == "" {
		return toolUseEnvelope{}, false
	}

	bodyStart := tagEnd + 1
	closeRel := strings.Index(strings.ToLower(content[bodyStart:]), "</invoke>")
	if closeRel == -1 {
		return toolUseEnvelope{}, false
	}

	input := decodeInvokeInput(content[bodyStart : bodyStart+closeRel])
	return toolUseEnvelope{
		Type:  toolUseResponseType,
		Name:  name,
		Input: input,
	}, true
}

func decodeInvokeInput(body string) json.RawMessage {
	body = strings.TrimSpace(body)
	if strings.HasPrefix(body, "{") && json.Valid([]byte(body)) {
		return json.RawMessage(body)
	}

	params := make(map[string]any)
	searchFrom := 0
	for searchFrom < len(body) {
		openRel := strings.Index(body[searchFrom:], "<")
		if openRel == -1 {
			break
		}
		open := searchFrom + openRel
		if open+1 < len(body) && body[open+1] == '/' {
			searchFrom = open + 2
			continue
		}

		tagEndRel := strings.Index(body[open:], ">")
		if tagEndRel == -1 {
			break
		}
		tagEnd := open + tagEndRel
		openTag := body[open : tagEnd+1]
		paramName := extractTagAttribute(openTag, "name")
		tagName := extractTagName(openTag)
		if tagName == "" {
			searchFrom = tagEnd + 1
			continue
		}
		// 支持 <file_path>value</file_path> 风格（tag 名即参数名）
		if paramName == "" {
			paramName = tagName
		}

		closeTag := "</" + tagName + ">"
		valueStart := tagEnd + 1
		closeRel := strings.Index(body[valueStart:], closeTag)
		if closeRel == -1 {
			searchFrom = tagEnd + 1
			continue
		}
		valueEnd := valueStart + closeRel
		params[paramName] = decodeInvokeParamValue(openTag, body[valueStart:valueEnd])
		searchFrom = valueEnd + len(closeTag)
	}

	if len(params) == 0 {
		return json.RawMessage(`{}`)
	}
	raw, err := json.Marshal(params)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return raw
}

func decodeInvokeParamValue(openTag, body string) any {
	value := extractTagAttribute(openTag, "value")
	if value == "" {
		value = html.UnescapeString(strings.TrimSpace(body))
	}

	stringAttr := strings.ToLower(strings.TrimSpace(extractTagAttribute(openTag, "string")))
	if stringAttr != "false" {
		return value
	}

	if value == "" {
		return ""
	}
	var decoded any
	if err := json.Unmarshal([]byte(value), &decoded); err == nil {
		return decoded
	}
	if intValue, err := strconv.Atoi(value); err == nil {
		return intValue
	}
	if floatValue, err := strconv.ParseFloat(value, 64); err == nil {
		return floatValue
	}
	if boolValue, err := strconv.ParseBool(value); err == nil {
		return boolValue
	}
	return value
}

func extractTagName(tag string) string {
	tag = strings.TrimSpace(strings.TrimPrefix(tag, "<"))
	tag = strings.TrimSuffix(tag, ">")
	tag = strings.TrimSpace(tag)
	if tag == "" || strings.HasPrefix(tag, "/") {
		return ""
	}
	for i, r := range tag {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == '/' {
			return tag[:i]
		}
	}
	return tag
}

func extractTagAttribute(tag, name string) string {
	for _, quote := range []byte{'"', '\''} {
		prefix := name + "=" + string(quote)
		start := strings.Index(tag, prefix)
		if start == -1 {
			continue
		}
		valueStart := start + len(prefix)
		valueEndRel := strings.IndexByte(tag[valueStart:], quote)
		if valueEndRel == -1 {
			return ""
		}
		return html.UnescapeString(tag[valueStart : valueStart+valueEndRel])
	}
	return ""
}

func (runner *Runner) finishWithoutDone(ctx context.Context, state turnState) (message.Message, error) {
	if state.wroteOutput {
		_ = runner.ui.OnDone()
	}
	if ctx.Err() != nil {
		return message.Message{}, ctx.Err()
	}

	return message.Message{}, fmt.Errorf("模型流在未发送完成事件时结束")
}

func (runner *Runner) failAfterPartialOutput(wroteOutput bool, err error) error {
	if wroteOutput {
		_ = runner.ui.OnDone()
	}
	return err
}

func (runner *Runner) runToolCalls(ctx context.Context, calls []message.ToolCall) (message.Message, error) {
	results := make([]message.ToolResult, 0, len(calls))
	for start := 0; start < len(calls); {
		if runner.isToolCallConcurrencySafe(calls[start]) {
			end := start + 1
			for end < len(calls) && runner.isToolCallConcurrencySafe(calls[end]) {
				end++
			}
			batchResults, err := runner.runToolCallBatch(ctx, calls[start:end])
			if err != nil {
				return message.Message{}, err
			}
			results = append(results, batchResults...)
			start = end
			continue
		}

		result, err := runner.runToolCall(ctx, calls[start])
		if err != nil {
			return message.Message{}, err
		}
		results = append(results, result)
		start++
	}
	return buildToolResultsMessage(results), nil
}

func (runner *Runner) runToolCallBatch(ctx context.Context, calls []message.ToolCall) ([]message.ToolResult, error) {
	for _, call := range calls {
		if err := runner.emitToolCall(call); err != nil {
			return nil, err
		}
	}

	results := make([]message.ToolResult, len(calls))
	var wg sync.WaitGroup
	for i := range calls {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = runner.executeToolCall(ctx, calls[i])
		}()
	}
	wg.Wait()

	for i, result := range results {
		if err := runner.emitToolResult(calls[i], result); err != nil {
			return nil, err
		}
	}
	return results, nil
}

func (runner *Runner) runToolCall(ctx context.Context, call message.ToolCall) (message.ToolResult, error) {
	if err := runner.emitToolCall(call); err != nil {
		return message.ToolResult{}, err
	}

	result := runner.executeToolCall(ctx, call)
	if err := runner.emitToolResult(call, result); err != nil {
		return message.ToolResult{}, err
	}

	return result, nil
}

func (runner *Runner) emitToolCall(call message.ToolCall) error {
	event := ui.ToolCallEvent{
		ID:    call.ID,
		Name:  call.Name,
		Input: append(json.RawMessage(nil), call.Input...),
	}
	// Write/Edit 工具执行前同步读旧文件，供 UI diff 展示使用。
	// 只有 UI 声明会消费 OldContent 时才读取，避免 sinkUI 等无显示 UI 浪费 I/O。
	if consumer, ok := runner.ui.(ui.OldContentConsumer); ok && consumer.ConsumesOldContent() {
		switch strings.ToLower(strings.TrimSpace(call.Name)) {
		case "write", "edit", "update":
			var input struct {
				FilePath string `json:"file_path"`
				Path     string `json:"path"`
			}
			if err := json.Unmarshal(call.Input, &input); err == nil {
				path := input.FilePath
				if path == "" {
					path = input.Path
				}
				if path != "" {
					if !filepath.IsAbs(path) && runner.workRoot != "" {
						path = filepath.Join(runner.workRoot, path)
					}
					if data, err := os.ReadFile(path); err == nil {
						event.OldContent = string(data)
					}
				}
			}
		}
	}
	runner.recordTraceEvent("tool_call", map[string]any{
		"tool_use_id": call.ID,
		"name":        call.Name,
		"input":       json.RawMessage(append([]byte(nil), call.Input...)),
	})
	// Audit: record the tool call as an event.
	runner.emitAuditEvent(context.Background(), SessionEvent{
		Kind: EventKindToolCallFired,
		ToolCall: &SessionToolCallPayload{
			ID:    call.ID,
			Name:  call.Name,
			Input: append(json.RawMessage(nil), call.Input...),
		},
	})
	return runner.ui.OnToolCall(event)
}

func (runner *Runner) emitToolResult(call message.ToolCall, result message.ToolResult) error {
	runner.recordTraceEvent("tool_result", map[string]any{
		"tool_use_id": result.ToolUseID,
		"name":        call.Name,
		"is_error":    result.IsError,
		"content":     result.Content,
	})
	// Audit: record the tool result as an event.
	runner.emitAuditEvent(context.Background(), SessionEvent{
		Kind: EventKindToolResult,
		ToolResult: &SessionToolResultPayload{
			ToolUseID: result.ToolUseID,
			Name:      call.Name,
			Content:   result.Content,
			IsError:   result.IsError,
		},
	})
	return runner.ui.OnToolResult(ui.ToolResultEvent{
		ToolUseID: result.ToolUseID,
		Name:      call.Name,
		Content:   result.Content,
		IsError:   result.IsError,
	})
}

func (runner *Runner) isToolCallConcurrencySafe(call message.ToolCall) bool {
	return runner.registry != nil && runner.registry.IsConcurrencySafe(call.Name, call.Input)
}

func (runner *Runner) executeToolCall(ctx context.Context, call message.ToolCall) message.ToolResult {
	if runner.registry == nil {
		return message.ToolResult{ToolUseID: call.ID, Content: "tool registry is nil", IsError: true}
	}

	selectedTool, ok := runner.registry.Get(call.Name)
	if !ok {
		return message.ToolResult{ToolUseID: call.ID, Content: fmt.Sprintf("unknown tool: %s", call.Name), IsError: true}
	}

	output, err := selectedTool.Run(ctx, call.Input)
	if err != nil {
		return message.ToolResult{ToolUseID: call.ID, Content: err.Error(), IsError: true}
	}

	return message.ToolResult{ToolUseID: call.ID, Content: output}
}
