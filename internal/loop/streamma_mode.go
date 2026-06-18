package loop

import (
	"context"
	"fmt"
	"gocode/internal/message"
	"gocode/internal/model"
	"gocode/internal/streamma"
	"gocode/internal/ui"
	"strings"
	"sync"
	"time"
)

const (
	streamMACommand      = "/streamma"
	streamMATraceCommand = "/streamma-trace"
)

type StreamMASubagentRequest struct {
	RunID           string
	SessionID       string
	SessionKey      string
	InvocationIndex int
	AgentID         string
	Role            string
	Description     string
	SystemPrompt    string
	Problem         string
	InboundFrom     string
	InboundStep     *streamma.StepPacket
	Boundary        string
	RequireBoundary bool
	Prompt          string
	ContextMode     string
}

type StreamMASubagentStream struct {
	Events         <-chan model.StreamEvent
	SessionID      string
	TranscriptPath string
	OutputPath     string
}

type StreamMASubagentRunner interface {
	StreamSubagent(ctx context.Context, req StreamMASubagentRequest) (StreamMASubagentStream, error)
}

func (runner *Runner) SetStreamMASubagentRunner(subagents StreamMASubagentRunner) {
	if runner == nil {
		return
	}
	runner.mu.Lock()
	defer runner.mu.Unlock()
	runner.streamMASubagents = subagents
}

func (runner *Runner) SetSubagentTokensProvider(p SubagentTokensProvider) {
	if runner == nil {
		return
	}
	runner.mu.Lock()
	defer runner.mu.Unlock()
	runner.subagentTokensProvider = p
}

func (runner *Runner) SetSystemSupplement(supplement string) {
	if runner == nil {
		return
	}
	runner.mu.Lock()
	defer runner.mu.Unlock()
	runner.systemSupplement = strings.TrimSpace(supplement)
}

type streamMAInvocation struct {
	Task  string
	Trace bool
}

func parseStreamMAInvocation(input string) (streamMAInvocation, bool) {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return streamMAInvocation{}, false
	}
	for _, command := range []string{streamMACommand, streamMATraceCommand} {
		if trimmed == command {
			return streamMAInvocation{Trace: command == streamMATraceCommand}, true
		}
		prefix := command + " "
		if strings.HasPrefix(trimmed, prefix) {
			return streamMAInvocation{
				Task:  strings.TrimSpace(strings.TrimPrefix(trimmed, prefix)),
				Trace: command == streamMATraceCommand,
			}, true
		}
	}
	return streamMAInvocation{}, false
}

func (runner *Runner) runStreamMATurn(ctx context.Context, input string, invocation streamMAInvocation) (message.Message, error) {
	task := invocation.Task
	if strings.TrimSpace(task) == "" {
		if invocation.Trace {
			return message.Message{}, fmt.Errorf("usage: /streamma-trace <task>")
		}
		return message.Message{}, fmt.Errorf("usage: /streamma <task>")
	}
	subagents := runner.currentStreamMASubagents()
	if subagents == nil {
		return message.Message{}, fmt.Errorf("streamma requires subagent backend")
	}

	history, injectedSupplements := runner.buildTurnHistory(input)
	committed := false
	defer func() {
		if !committed && len(injectedSupplements) > 0 {
			runner.prependSupplements(injectedSupplements)
		}
	}()

	spec := defaultStreamMAGraph(task)
	if invocation.Trace {
		runner.notifySystem("streamma-trace", fmt.Sprintf("running %s with live runtime events", describeStreamMAGraph(spec)))
	} else {
		runner.notifySystem("streamma", fmt.Sprintf("running %s with subagent-backed Exact+END_STEP workers", describeStreamMAGraph(spec)))
	}
	result, err := streamma.RunGraphWithAgentEventSink(
		ctx,
		spec,
		newStreamMASubagentModel(subagents, runner.notifySystem, invocation.Trace),
		buildStreamMAProblem(task, history),
		runner.streamMATraceSink(invocation.Trace),
	)
	if err != nil {
		return message.Message{}, err
	}
	if result.Status != streamma.RunCompleted {
		return message.Message{}, fmt.Errorf("streamma run %s ended with status %s: %s", result.RunID, result.Status, result.Error)
	}

	finalText := finalStreamMAText(result)
	assistant := buildAssistantMessage(finalText)
	history = append(history, assistant)
	if err := runner.commitHistory(ctx, history); err != nil {
		return message.Message{}, err
	}
	committed = true

	if err := runner.ui.OnAssistantDelta(finalText); err != nil {
		return message.Message{}, err
	}
	if err := runner.ui.OnDone(); err != nil {
		return message.Message{}, err
	}
	return assistant, nil
}

func (runner *Runner) streamMATraceSink(enabled bool) func(streamma.Event) {
	if !enabled {
		return nil
	}
	started := time.Now()
	return func(event streamma.Event) {
		body := formatStreamMATraceEvent(started, event)
		if strings.TrimSpace(body) != "" {
			runner.notifySystem("streamma-trace", body)
		}
	}
}

func formatStreamMATraceEvent(started time.Time, event streamma.Event) string {
	elapsed := time.Since(started).Truncate(time.Millisecond)
	parts := []string{
		fmt.Sprintf("+%s", elapsed),
		fmt.Sprintf("seq=%d", event.Seq),
		"type=" + string(event.Type),
	}
	if event.ProducerAgentID != "" {
		parts = append(parts, "producer="+event.ProducerAgentID)
	}
	if event.TargetAgentID != "" {
		parts = append(parts, "target="+event.TargetAgentID)
	}
	if event.EdgeID != "" {
		parts = append(parts, "edge="+event.EdgeID)
	}
	if event.Step != nil {
		parts = append(parts,
			"step="+event.Step.StepID,
			fmt.Sprintf("index=%d", event.Step.StepIndex),
			fmt.Sprintf("recovered=%t", event.Step.Boundary.BoundaryRecovered),
		)
	}
	if event.Control != nil {
		parts = append(parts,
			"control="+event.Control.ControlType,
			fmt.Sprintf("final_step=%d", event.Control.FinalStep),
			"reason="+event.Control.Reason,
		)
	}
	if event.Final != nil {
		parts = append(parts, "final_agent="+event.Final.AgentID)
	}
	if event.Error != "" {
		parts = append(parts, "error="+event.Error)
	}
	return strings.Join(parts, " ")
}

func (runner *Runner) currentStreamMASubagents() StreamMASubagentRunner {
	if runner == nil {
		return nil
	}
	runner.mu.RLock()
	defer runner.mu.RUnlock()
	return runner.streamMASubagents
}

func defaultStreamMAGraph(task string) streamma.GraphSpec {
	if streamMATaskLooksImplementationHeavy(task) {
		return streamma.GraphSpec{
			RunID:    fmt.Sprintf("streamma-%d", time.Now().UTC().UnixNano()),
			Protocol: streamma.ProtocolStream,
			StepPolicy: streamma.StepPolicy{
				Boundary:        streamma.DefaultBoundary,
				MaxStepBytes:    64 * 1024,
				RequireBoundary: true,
			},
			Agents: []streamma.AgentSpec{
				{ID: "planner", Role: "source_planner", SystemPrompt: streamMAAgentPrompt("planner", "source_planner", "Create a concise implementation plan and success criteria. Do not modify files.")},
				{ID: "scout", Role: "workspace_scout", SystemPrompt: streamMAAgentPrompt("scout", "workspace_scout", "Inspect the workspace and identify the safest place and stack for the requested implementation. Prefer read-only investigation.")},
				{ID: "builder", Role: "implementation_builder", SystemPrompt: streamMAAgentPrompt("builder", "implementation_builder", "Use the inbound plan and workspace facts to implement the requested artifact with available tools. For temporary demos, create files under a clearly named temporary workspace directory.")},
				{ID: "verifier", Role: "runtime_verifier", SystemPrompt: streamMAAgentPrompt("verifier", "runtime_verifier", "Run focused verification for the builder output, report failures, and fix only when necessary.")},
				{ID: "finalizer", Role: "final_response", SystemPrompt: streamMAAgentPrompt("finalizer", "final_response", "Synthesize the current implemented state, paths, commands, verification results, and remaining caveats for the user.")},
			},
			Edges: []streamma.EdgeSpec{
				{From: "planner", To: "builder"},
				{From: "scout", To: "builder"},
				{From: "builder", To: "verifier"},
				{From: "builder", To: "finalizer"},
				{From: "verifier", To: "finalizer"},
			},
		}
	}
	return streamma.GraphSpec{
		RunID:    fmt.Sprintf("streamma-%d", time.Now().UTC().UnixNano()),
		Protocol: streamma.ProtocolStream,
		StepPolicy: streamma.StepPolicy{
			Boundary:        streamma.DefaultBoundary,
			MaxStepBytes:    32 * 1024,
			RequireBoundary: true,
		},
		Agents: []streamma.AgentSpec{
			{ID: "planner", Role: "source_planner", SystemPrompt: streamMAAgentPrompt("planner", "source_planner", "Break down the task into early useful reasoning steps.")},
			{ID: "solver", Role: "reasoning_solver", SystemPrompt: streamMAAgentPrompt("solver", "reasoning_solver", "Consume planner steps and solve the task incrementally.")},
			{ID: "critic", Role: "quality_critic", SystemPrompt: streamMAAgentPrompt("critic", "quality_critic", "Check solver steps for gaps, contradictions, and missing evidence.")},
			{ID: "finalizer", Role: "final_response", SystemPrompt: streamMAAgentPrompt("finalizer", "final_response", "Produce the final user-facing answer from the best available inbound steps.")},
		},
		Edges: []streamma.EdgeSpec{
			{From: "planner", To: "solver"},
			{From: "planner", To: "critic"},
			{From: "solver", To: "critic"},
			{From: "critic", To: "finalizer"},
			{From: "solver", To: "finalizer"},
		},
	}
}

func streamMATaskLooksImplementationHeavy(task string) bool {
	lower := strings.ToLower(task)
	for _, marker := range []string{
		"implement", "build", "create", "make", "code", "app", "game", "test", "run",
		"实现", "制作", "创建", "写", "代码", "项目", "游戏", "测试", "运行", "修复", "临时",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func streamMAAgentPrompt(agentID, role, mission string) string {
	return fmt.Sprintf("streamma_agent_id=%s\nstreamma_role=%s\n%s\n%s", agentID, role, mission, streamMAOutputContract)
}

const streamMAOutputContract = `Output contract:
- Use natural language only.
- End every reasoning step with a line that contains exactly END_STEP.
- END_STEP must be on its own line with no spaces, punctuation, indentation, JSON, or Markdown fences.
- The StreamMA runtime is strict: if your final answer does not include an exact END_STEP line, this agent invocation fails instead of being propagated.
- The final non-whitespace line of every assistant message must be exactly END_STEP. Never write any text after a closing END_STEP line.
- You are a real subagent worker with your normal tools available.
- Use tools only when your role needs them and after you have enough verified plan/context.
- Keep each step public and concise; do not reveal private chain-of-thought.`

func buildStreamMAProblem(task string, history []message.Message) string {
	var builder strings.Builder
	builder.WriteString("Current user task:\n")
	builder.WriteString(strings.TrimSpace(task))
	if context := streamMAConversationContext(history); context != "" {
		builder.WriteString("\n\nConversation context:\n")
		builder.WriteString(context)
	}
	return builder.String()
}

func streamMAConversationContext(history []message.Message) string {
	const maxRunes = 6000
	var lines []string
	for _, msg := range history {
		content := strings.TrimSpace(msg.Content)
		if content == "" {
			continue
		}
		if strings.HasPrefix(content, streamMACommand) || strings.HasPrefix(content, streamMATraceCommand) {
			continue
		}
		switch msg.Role {
		case message.RoleUser:
			lines = append(lines, "User: "+content)
		case message.RoleAssistant:
			lines = append(lines, "Assistant: "+content)
		}
	}
	if len(lines) == 0 {
		return ""
	}
	text := strings.Join(lines, "\n")
	runes := []rune(text)
	if len(runes) <= maxRunes {
		return text
	}
	return string(runes[len(runes)-maxRunes:])
}

func finalStreamMAText(result streamma.RunResult) string {
	if result.Final != nil {
		if text := cleanStreamMAFinalText(result.Final.Answer.Text); text != "" {
			return text
		}
	}
	for i := len(result.Events) - 1; i >= 0; i-- {
		event := result.Events[i]
		if event.Step != nil {
			if text := cleanStreamMAFinalText(event.Step.Content.Text); text != "" {
				return text
			}
		}
	}
	return "StreamMA completed without a final answer."
}

func cleanStreamMAFinalText(text string) string {
	cleaned := strings.TrimSpace(text)
	for {
		trimmed := strings.TrimSpace(strings.TrimPrefix(cleaned, "Own step:"))
		if trimmed == cleaned {
			break
		}
		cleaned = trimmed
	}
	return cleaned
}

func (runner *Runner) notifySystem(title, body string) {
	if runner == nil || runner.ui == nil {
		return
	}
	notifier, ok := runner.ui.(ui.SystemNotifier)
	if !ok {
		return
	}
	_ = notifier.OnSystemMessage(ui.SystemEvent{Title: title, Body: body})
}

type streamMASubagentModel struct {
	subagents StreamMASubagentRunner
	notify    func(string, string)
	trace     bool

	mu       sync.Mutex
	sessions map[string]string
}

func newStreamMASubagentModel(subagents StreamMASubagentRunner, notify func(string, string), trace bool) *streamMASubagentModel {
	return &streamMASubagentModel{
		subagents: subagents,
		notify:    notify,
		trace:     trace,
		sessions:  map[string]string{},
	}
}

func (m *streamMASubagentModel) StreamAgent(ctx context.Context, invocation streamma.AgentInvocation) (<-chan model.StreamEvent, error) {
	if m == nil || m.subagents == nil {
		return nil, fmt.Errorf("streamma subagent backend is nil")
	}
	agentID := strings.TrimSpace(invocation.AgentID)
	if agentID == "" {
		agentID = "agent"
	}
	role := strings.TrimSpace(invocation.Role)
	if role == "" {
		role = "worker"
	}
	sessionKey := streamMASessionKey(invocation.RunID, agentID)
	sessionID := m.sessionID(sessionKey)
	firstInvocation := sessionID == ""
	prompt := buildStreamMAIncrementalPrompt(invocation, firstInvocation)
	stream, err := m.subagents.StreamSubagent(ctx, StreamMASubagentRequest{
		RunID:           invocation.RunID,
		SessionID:       sessionID,
		SessionKey:      sessionKey,
		InvocationIndex: invocation.InvocationIndex,
		AgentID:         agentID,
		Role:            role,
		Description:     "streamma " + agentID + " " + role,
		SystemPrompt:    invocation.SystemPrompt,
		Problem:         invocation.Problem,
		InboundFrom:     invocation.InboundFrom,
		InboundStep:     cloneLoopStreamMAStep(invocation.InboundStep),
		Boundary:        invocation.Boundary,
		RequireBoundary: invocation.RequireBoundary,
		Prompt:          prompt,
		ContextMode:     "empty",
	})
	if err != nil {
		return nil, err
	}
	if assigned := strings.TrimSpace(stream.SessionID); assigned != "" {
		sessionID = m.rememberSession(sessionKey, assigned)
	}
	if m.notify != nil {
		if m.trace {
			m.notify("streamma-trace", fmt.Sprintf("subagent.started agent=%s role=%s invocation=%d session=%s", agentID, role, invocation.InvocationIndex, sessionID))
		} else {
			m.notify(agentID, fmt.Sprintf("(%s) started", role))
		}
	}
	return m.wrapStream(ctx, invocation, stream), nil
}

func (m *streamMASubagentModel) sessionID(sessionKey string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sessions[sessionKey]
}

func (m *streamMASubagentModel) rememberSession(sessionKey, sessionID string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if existing := strings.TrimSpace(m.sessions[sessionKey]); existing != "" {
		return existing
	}
	m.sessions[sessionKey] = sessionID
	return sessionID
}

func (m *streamMASubagentModel) wrapStream(ctx context.Context, invocation streamma.AgentInvocation, stream StreamMASubagentStream) <-chan model.StreamEvent {
	out := make(chan model.StreamEvent)
	go func() {
		defer close(out)
		var lastUsage *model.Usage
		defer func() {
			if m.notify == nil {
				return
			}
			agentID := strings.TrimSpace(invocation.AgentID)
			role := strings.TrimSpace(invocation.Role)
			detail := fmt.Sprintf("(%s) finished", role)
			if strings.TrimSpace(stream.SessionID) != "" {
				detail += " session=" + stream.SessionID
			}
			if m.trace {
				traceDetail := fmt.Sprintf("subagent.finished agent=%s role=%s invocation=%d", agentID, role, invocation.InvocationIndex)
				if strings.TrimSpace(stream.SessionID) != "" {
					traceDetail += " session=" + stream.SessionID
				}
				traceDetail += formatStreamMAUsageTrace(lastUsage)
				m.notify("streamma-trace", traceDetail)
			} else {
				m.notify(agentID, detail)
			}
		}()
		for ev := range stream.Events {
			if ev.Usage != nil {
				usage := *ev.Usage
				lastUsage = &usage
			}
			if !sendStreamMAEvent(ctx, out, ev) {
				drainStreamMAEvents(stream.Events, &lastUsage)
				return
			}
		}
	}()
	return out
}

func drainStreamMAEvents(events <-chan model.StreamEvent, lastUsage **model.Usage) {
	for ev := range events {
		if ev.Usage != nil && lastUsage != nil {
			usage := *ev.Usage
			*lastUsage = &usage
		}
	}
}

func sendStreamMAEvent(ctx context.Context, out chan<- model.StreamEvent, ev model.StreamEvent) bool {
	select {
	case out <- ev:
		return true
	case <-ctx.Done():
		return false
	}
}

func streamMASessionKey(runID, agentID string) string {
	runID = strings.TrimSpace(runID)
	agentID = strings.TrimSpace(agentID)
	if runID == "" {
		runID = "run"
	}
	if agentID == "" {
		agentID = "agent"
	}
	return runID + ":" + agentID
}

func buildStreamMAIncrementalPrompt(invocation streamma.AgentInvocation, firstInvocation bool) string {
	var builder strings.Builder
	boundary := strings.TrimSpace(invocation.Boundary)
	if boundary == "" {
		boundary = streamma.DefaultBoundary
	}
	if firstInvocation {
		builder.WriteString("StreamMA logical agent bootstrap. Treat this subagent session as the persistent ctx_a for this logical agent.\n")
		builder.WriteString("Future invocations will append only new inbound steps to this same session; keep your prior session history as prefix context.\n\n")
	} else {
		builder.WriteString("StreamMA incremental invocation. Continue from the existing ctx_a in this same subagent session.\n")
		builder.WriteString("Do not restate the original problem or earlier inbound steps unless needed for a concise next step.\n\n")
	}
	builder.WriteString("Run ID: ")
	builder.WriteString(strings.TrimSpace(invocation.RunID))
	builder.WriteString("\nInvocation: ")
	builder.WriteString(fmt.Sprintf("%d", invocation.InvocationIndex))
	builder.WriteString("\n")
	builder.WriteString("Agent ID: ")
	builder.WriteString(strings.TrimSpace(invocation.AgentID))
	builder.WriteString("\nRole: ")
	builder.WriteString(strings.TrimSpace(invocation.Role))
	builder.WriteString("\n\n")
	if firstInvocation {
		builder.WriteString("Original problem:\n")
		builder.WriteString(strings.TrimSpace(invocation.Problem))
		builder.WriteString("\n\n")
	}
	if invocation.InboundStep != nil {
		builder.WriteString("New inbound step from ")
		builder.WriteString(strings.TrimSpace(invocation.InboundFrom))
		builder.WriteString(":\n")
		builder.WriteString(strings.TrimSpace(invocation.InboundStep.Content.Text))
		builder.WriteString("\n\n")
	} else if firstInvocation {
		builder.WriteString("Initial problem delivery. Start producing useful public reasoning steps for this agent role.\n\n")
	} else {
		builder.WriteString("No new inbound step is attached; continue only if this role can make useful progress from existing ctx_a.\n\n")
	}
	builder.WriteString("Produce one or more public StreamMA steps. Each step must end with a standalone ")
	builder.WriteString(boundary)
	builder.WriteString(" line. The boundary line must contain exactly ")
	builder.WriteString(boundary)
	builder.WriteString(".")
	if invocation.RequireBoundary {
		builder.WriteString(" This runtime is strict and will fail this invocation if the final emitted step is not closed by the exact boundary. The last non-whitespace line of your assistant message must be exactly ")
		builder.WriteString(boundary)
		builder.WriteString(", with no text after it.")
	}
	builder.WriteString("\n")
	return builder.String()
}

func cloneLoopStreamMAStep(step *streamma.StepPacket) *streamma.StepPacket {
	if step == nil {
		return nil
	}
	cloned := *step
	if cloned.Dependencies.InputEvents != nil {
		cloned.Dependencies.InputEvents = append([]string(nil), cloned.Dependencies.InputEvents...)
	}
	return &cloned
}

func formatStreamMAUsageTrace(usage *model.Usage) string {
	if usage == nil {
		return " usage=unknown"
	}
	parts := []string{
		fmt.Sprintf("input=%d", usage.PromptTokenCount()),
		fmt.Sprintf("output=%d", usage.CompletionTokenCount()),
	}
	cacheKnown := usageHasCacheSignal(*usage)
	if cacheKnown {
		parts = append(parts, fmt.Sprintf("cache_hit=%d", usage.CacheHitTokens()))
	} else {
		parts = append(parts, "cache_hit=unknown")
	}
	if usage.PromptCacheMissTokens != 0 {
		parts = append(parts, fmt.Sprintf("cache_miss=%d", usage.PromptCacheMissTokens))
	} else {
		parts = append(parts, "cache_miss=unknown")
	}
	if cacheKnown && usage.PromptCacheMissTokens != 0 {
		denominator := usage.CacheHitTokens() + usage.PromptCacheMissTokens
		if denominator > 0 {
			parts = append(parts, fmt.Sprintf("hit_rate=%.1f%%", float64(usage.CacheHitTokens())*100/float64(denominator)))
		} else {
			parts = append(parts, "hit_rate=unknown")
		}
	} else {
		parts = append(parts, "hit_rate=unknown")
	}
	return " usage(" + strings.Join(parts, " ") + ")"
}

func usageHasCacheSignal(usage model.Usage) bool {
	return usage.PromptCacheHitTokens != 0 ||
		usage.PromptCacheMissTokens != 0 ||
		usage.CacheReadInputTokens != 0 ||
		usage.CacheCreationInputTokens != 0 ||
		usage.PromptTokensDetails.CachedTokens != 0 ||
		usage.InputTokensDetails.CachedTokens != 0 ||
		usage.PromptTokensDetails.CacheReadTokens != 0 ||
		usage.InputTokensDetails.CacheReadTokens != 0 ||
		usage.PromptTokensDetails.CacheReadInputTokens != 0 ||
		usage.InputTokensDetails.CacheReadInputTokens != 0 ||
		usage.PromptTokensDetails.CacheCreationTokens != 0 ||
		usage.InputTokensDetails.CacheCreationTokens != 0 ||
		usage.PromptTokensDetails.CacheCreationInputTokens != 0 ||
		usage.InputTokensDetails.CacheCreationInputTokens != 0
}

func describeStreamMAGraph(spec streamma.GraphSpec) string {
	ids := make([]string, 0, len(spec.Agents))
	for _, agent := range spec.Agents {
		ids = append(ids, agent.ID)
	}
	return strings.Join(ids, " -> ")
}
