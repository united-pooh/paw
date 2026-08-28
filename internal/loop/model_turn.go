package loop

import (
	"context"
	"encoding/json"
	"fmt"
	"paw/internal/message"
	"paw/internal/model"
	"paw/internal/tokentracer"
	"strings"
)

const (
	maxLengthContinuationRequests = 3
	lengthContinuationInstruction = "从截断位置继续，不重复、不重启回答"
	maxContinuationOverlapRunes   = 512
)

func (runner *Engine) runModelTurn(ctx context.Context, history []message.Message, turn *TurnState) (message.Message, error) {
	var tools []model.ToolDefinition
	if runner.registry != nil {
		tools = runner.registry.Definitions()
		if filter := runner.currentToolFilter(); filter != nil {
			filtered := make([]model.ToolDefinition, 0, len(tools))
			for _, definition := range tools {
				if filter(definition.Name, nil) == nil {
					filtered = append(filtered, definition)
				}
			}
			tools = filtered
		}
	}
	baseModelMessages, err := runner.buildModelMessages(ctx, history, turn)
	if err != nil {
		return message.Message{}, err
	}

	var state turnState
	state.traceStageID, state.traceAgentID = runner.currentTraceIDs()
	state.outer = turn

	for attempt := 0; attempt < maxLengthContinuationRequests; attempt++ {
		modelMessages := baseModelMessages
		if attempt > 0 {
			state.beginContinuationOverlap(state.content.String())
			modelMessages = buildLengthContinuationMessages(baseModelMessages, state.content.String())
		}
		runner.beginUsageRequest(&state)
		contentBefore := state.content.Len()
		toolCallsBefore := len(state.toolCalls)
		events, err := runner.model.StreamMessage(ctx, modelMessages, tools)
		if err != nil {
			return runner.failModelTurnWithPartial(&state, err)
		}
		if turn != nil {
			turn.PlanEmitted = true
		}

		finalizeOnDone := attempt == 0
		msg, finishReason, err := runner.consumeStream(ctx, events, &state, finalizeOnDone)
		if err != nil {
			return msg, err
		}
		if attempt > 0 && len(state.toolCalls) > toolCallsBefore {
			return runner.failModelTurnWithPartial(&state, fmt.Errorf("length 续写响应包含工具调用，已停止以避免执行不完整工具"))
		}

		switch finishReason {
		case model.FinishReasonLength:
			if state.content.Len() == contentBefore {
				return runner.failModelTurnWithPartial(&state, fmt.Errorf("模型响应因 length 截断但没有返回可续写文本"))
			}
			if turnStateHasToolCalls(&state) {
				return runner.failModelTurnWithPartial(&state, fmt.Errorf("模型响应因 length 截断且包含工具调用，已停止以避免执行不完整工具"))
			}
			if attempt == maxLengthContinuationRequests-1 {
				return runner.failModelTurnWithPartial(&state, fmt.Errorf("模型响应连续 %d 次以 length 截断，已停止", maxLengthContinuationRequests))
			}
			continue
		case model.FinishReasonToolCalls:
			if attempt > 0 {
				return runner.failModelTurnWithPartial(&state, fmt.Errorf("length 续写响应包含工具调用，已停止以避免执行不完整工具"))
			}
			return msg, nil
		default:
			if attempt > 0 {
				if state.content.Len() == contentBefore {
					return runner.failModelTurnWithPartial(&state, fmt.Errorf("length 续写响应没有返回新文本"))
				}
				if len(toolCallsFromMessage(msg)) > 0 {
					return runner.failModelTurnWithPartial(&state, fmt.Errorf("length 续写响应包含工具调用，已停止以避免执行不完整工具"))
				}
				finalMsg, finalizeErr := runner.finalizeAssistantMessage(&state)
				if finalizeErr != nil {
					return runner.failModelTurnWithPartial(&state, finalizeErr)
				}
				return finalMsg, nil
			}
			return msg, nil
		}
	}

	return runner.failModelTurnWithPartial(&state, fmt.Errorf("模型响应连续 %d 次以 length 截断，已停止", maxLengthContinuationRequests))
}

func buildLengthContinuationMessages(base []message.Message, accumulated string) []message.Message {
	messages := make([]message.Message, 0, len(base)+2)
	messages = append(messages, base...)
	messages = append(messages,
		message.Message{Role: message.RoleAssistant, Content: accumulated},
		message.Message{Role: message.RoleUser, Content: lengthContinuationInstruction},
	)
	return messages
}

func (runner *Engine) buildModelMessages(ctx context.Context, history []message.Message, turn *TurnState) ([]message.Message, error) {
	messages := make([]message.Message, 0, len(history)+1)
	messages = append(messages, buildSystemMessage(runner.buildSystemPromptForTurn(turn)))
	for _, msg := range history {
		if err := runner.materializeAttachments(ctx, &msg); err != nil {
			return nil, err
		}
		rendered := renderMessageForModel(msg)
		// 跳过空 content 的 assistant 消息，避免发送 {"role":"assistant"} 触发 API 400 错误。
		// 空 assistant 消息可能来自模型返回空流式内容的边界情况。
		if rendered.Role == message.RoleAssistant && rendered.Content == "" {
			continue
		}
		messages = append(messages, rendered)
	}
	return messages, nil
}

func (runner *Engine) persistInputAttachments(ctx context.Context, input *message.Message) error {
	if input == nil || len(input.Parts) == 0 {
		return nil
	}
	store, ok := runner.store.(AttachmentStore)
	for i := range input.Parts {
		part := &input.Parts[i]
		if part.Type != message.ContentPartImage || part.Image == nil {
			continue
		}
		if len(part.Image.Data) == 0 {
			if strings.TrimSpace(part.Image.Attachment) == "" {
				return fmt.Errorf("图片附件缺少数据或引用")
			}
			continue
		}
		if !ok {
			return fmt.Errorf("图片输入需要可用的附件存储")
		}
		reference, err := store.SaveAttachment(ctx, part.Image.MIMEType, part.Image.Data)
		if err != nil {
			return fmt.Errorf("保存图片附件失败: %w", err)
		}
		part.Image.Attachment = reference
	}
	return nil
}

func (runner *Engine) materializeAttachments(ctx context.Context, msg *message.Message) error {
	if msg == nil || len(msg.Parts) == 0 {
		return nil
	}
	store, ok := runner.store.(AttachmentStore)
	for i := range msg.Parts {
		part := &msg.Parts[i]
		if part.Type != message.ContentPartImage || part.Image == nil {
			continue
		}
		if len(part.Image.Data) > 0 {
			continue
		}
		reference := strings.TrimSpace(part.Image.Attachment)
		if reference == "" {
			return fmt.Errorf("图片消息缺少附件引用")
		}
		if !ok {
			return fmt.Errorf("无法读取图片附件 %q：当前会话存储不支持附件", reference)
		}
		mimeType, data, err := store.ReadAttachment(ctx, reference)
		if err != nil {
			return fmt.Errorf("读取图片附件 %q 失败: %w", reference, err)
		}
		part.Image.MIMEType = mimeType
		part.Image.Data = data
	}
	return nil
}

func (runner *Engine) buildSystemPrompt() string {
	return runner.buildSystemPromptForTurn(nil)
}

func (runner *Engine) buildSystemPromptForTurn(turn *TurnState) string {
	descriptions := []string{}
	if runner.registry != nil {
		compactToolPrompt := runner.compact.toolPrompt()
		filter := runner.toolGate.currentTurnFilter()
		if compactToolPrompt {
			descriptions = runner.registry.DescribeBrief()
		} else {
			descriptions = runner.registry.Describe()
		}
		if filter != nil {
			allowed := map[string]bool{}
			for _, definition := range runner.registry.Definitions() {
				if filter(definition.Name, nil) == nil {
					allowed[definition.Name] = true
				}
			}
			filtered := make([]string, 0, len(descriptions))
			for _, description := range descriptions {
				name := strings.TrimSpace(description)
				if idx := strings.IndexByte(name, ':'); idx > 0 {
					name = strings.TrimSpace(name[:idx])
				}
				if allowed[name] {
					filtered = append(filtered, description)
				}
			}
			descriptions = filtered
		}
	}
	if runner.prompt == nil {
		runner.prompt = NewPromptBuilder(NewInstructionManager(""))
	}
	prompt := runner.prompt.Build(descriptions)
	supplement := runner.promptCtx.currentSystemSupplement()
	skillContext := runner.currentSkillContext()
	if turn != nil {
		if turn.SkillContext != "" {
			skillContext = turn.SkillContext
		}
		if turn.PlanEmitted {
			skillContext = "The selected skill instructions are already active for this turn. Continue from the latest tool result; do not restart the investigation plan or repeat its checklist."
		}
	}
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
		rendered := buildAssistantToolCallMessage(calls)
		rendered.Content = strings.Join(parts, "\n")
		rendered.ProviderData = append(json.RawMessage(nil), msg.ProviderData...)
		return rendered
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
		rendered := buildToolResultsMessage(results)
		rendered.Content = label + strings.Join(parts, "\n")
		rendered.ProviderData = append(json.RawMessage(nil), msg.ProviderData...)
		return rendered
	default:
		return message.Message{
			Role:         msg.Role,
			Content:      msg.Content,
			Parts:        append([]message.ContentPart(nil), msg.Parts...),
			ProviderData: append(json.RawMessage(nil), msg.ProviderData...),
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

func (runner *Engine) consumeStream(ctx context.Context, events <-chan model.StreamEvent, state *turnState, finalizeOnDone bool) (message.Message, model.FinishReason, error) {
	for ev := range events {
		state.streamEstablished = true
		msg, finishReason, done, err := runner.handleEvent(state, ev, finalizeOnDone)
		if err != nil {
			return msg, "", err
		}
		if done {
			return msg, finishReason, nil
		}
	}

	if err := runner.flushContinuationOverlap(state); err != nil {
		return runner.partialAssistantMessage(state), "", err
	}
	msg, err := runner.finishWithoutDone(ctx, state)
	return msg, "", err
}

func (runner *Engine) handleEvent(state *turnState, ev model.StreamEvent, finalizeOnDone bool) (message.Message, model.FinishReason, bool, error) {
	if ev.Err != nil {
		if err := runner.flushContinuationOverlap(state); err != nil {
			return runner.partialAssistantMessage(state), "", false, err
		}
		msg, err := runner.failModelTurnWithPartial(state, ev.Err)
		return msg, "", false, err
	}
	if ev.Usage != nil {
		runner.recordUsageEvent(state, *ev.Usage)
	}

	// 捕获请求 origin
	if ev.GeneratedBy != nil {
		state.generatedBy = ev.GeneratedBy
	}

	// 新路径：AssistantPart 生命周期事件
	if ev.AssistantPart != nil {
		state.structuredPartsSeen = true
		if err := runner.handleAssistantPartEvent(state, ev.AssistantPart); err != nil {
			msg, failErr := runner.failModelTurnWithPartial(state, err)
			return msg, "", false, failErr
		}
		if !ev.Done {
			return message.Message{}, "", false, nil
		}
	} else {
		// 旧路径：转换 Delta/Thinking/ToolCalls 为兼容合成 parts
		if !state.structuredPartsSeen && (ev.Thinking != "" || ev.Delta != "" || len(ev.ToolCalls) > 0 || len(ev.ProviderData) != 0) {
			state.legacyEvents = true
		}
		if err := runner.appendThinking(state, ev.Thinking); err != nil {
			msg, failErr := runner.failModelTurnWithPartial(state, err)
			return msg, "", false, failErr
		}
		if err := runner.appendDelta(state, ev.Delta); err != nil {
			return runner.partialAssistantMessage(state), "", false, err
		}
		if len(ev.ToolCalls) > 0 {
			state.resetToolPayloadCandidate()
		}
		appendStreamToolCalls(state, ev.ToolCalls)
		if len(ev.ProviderData) != 0 {
			state.providerData = append(json.RawMessage(nil), ev.ProviderData...)
		}
		if !ev.Done {
			return message.Message{}, "", false, nil
		}
	}

	if err := runner.flushContinuationOverlap(state); err != nil {
		return runner.partialAssistantMessage(state), "", false, err
	}

	if ev.FinishReason == model.FinishReasonLength {
		return message.Message{}, ev.FinishReason, true, nil
	}

	if !finalizeOnDone {
		return runner.protocolAssistantMessage(state), ev.FinishReason, true, nil
	}

	msg, err := runner.finalizeAssistantMessage(state)
	if err != nil {
		msg, failErr := runner.failModelTurnWithPartial(state, err)
		return msg, ev.FinishReason, false, failErr
	}
	return msg, ev.FinishReason, true, nil
}

func (runner *Engine) handleAssistantPartEvent(state *turnState, event *model.AssistantPartEvent) error {
	if state.parts == nil {
		state.parts = newPartAccumulator()
	}

	switch event.Lifecycle {
	case model.AssistantPartLifecycleStart:
		state.parts.openPart(event.BlockIndex, message.AssistantPartType(event.Type))
		switch event.Type {
		case "reasoning":
			if event.Redacted {
				state.parts.setReasoningRedacted(event.BlockIndex)
			}
			runner.sendReasoningStart(state, event.BlockIndex, event.Redacted)
		case "tool_call":
			state.parts.setToolCallIdentity(event.BlockIndex, event.ToolCallID, event.ToolName)
			// 工具参数开始流式生成的时刻：工具耗时的计费起点（参数生成→执行
			// 完成合并口径），回写到外层 TurnState 供执行阶段换算。
			if state.outer != nil {
				state.outer.markToolArgsGenStarted(event.ToolCallID, runner.now())
			}
			if len(event.ToolArgs) != 0 {
				state.parts.appendToolArgs(event.BlockIndex, string(event.ToolArgs))
			}
		}

	case model.AssistantPartLifecycleDelta:
		switch event.Type {
		case "reasoning":
			state.parts.appendText(event.BlockIndex, event.Delta)
			runner.sendReasoningDelta(state, event.BlockIndex, event.Delta)
		case "text":
			state.parts.appendText(event.BlockIndex, event.Delta)
			// 仍通过旧路径渲染文本，保证 Bubble 可见
			return runner.appendDelta(state, event.Delta)
		case "tool_call":
			state.parts.appendToolArgs(event.BlockIndex, string(event.ToolArgs))
			// 如果 tool 的文本可见性发生变化，可能需要重置 tool payload 状态
		}

		if len(event.OpaqueData) != 0 && event.Type == "reasoning" {
			state.parts.setReasoningProviderData(event.BlockIndex, event.OpaqueData)
		}

	case model.AssistantPartLifecycleEnd:
		switch event.Type {
		case "reasoning":
			runner.sendReasoningEnd(state, event.BlockIndex)
			if len(event.OpaqueData) != 0 {
				state.parts.setReasoningProviderData(event.BlockIndex, event.OpaqueData)
			}
		case "tool_call":
			// 将累积的工具调用加入 state.toolCalls，保持与现有工具执行兼容
			if p, exists := state.parts.active[event.BlockIndex]; exists && p.ToolCall != nil {
				state.toolCalls = append(state.toolCalls, *p.ToolCall)
				state.resetToolPayloadCandidate()
			}
		}
		state.parts.closePart(event.BlockIndex)
	}

	return nil
}

func (runner *Engine) beginUsageRequest(state *turnState) {
	state.requestUsage = model.Usage{}
	state.requestUsageKnown = false
}

func (runner *Engine) recordUsageEvent(state *turnState, usage model.Usage) {
	previousTrace := tokentracer.UsageFromModelUsage(state.usage)
	requestPrevious := usageTotalsFromUsage(state.requestUsage, state.requestUsageKnown)
	state.requestUsage = mergeUsageSnapshot(state.requestUsage, usage)
	state.requestUsageKnown = true
	requestCurrent := usageTotalsFromUsage(state.requestUsage, true)
	delta := requestCurrent.delta(requestPrevious)
	currentTotals := usageTotalsFromUsage(state.usage, state.usageKnown).add(delta)
	state.usage = usageFromTotals(currentTotals)
	state.usageKnown = true
	runner.setCurrentUsage(state.usage)
	runner.addSessionUsage(delta)
	runner.emitModelUsage(state.usage)
	currentTrace := tokentracer.UsageFromModelUsage(state.usage)
	runner.recordTraceUsage(state.traceStageID, state.traceAgentID, currentTrace.Delta(previousTrace), map[string]any{
		"source": "model_stream",
	})
}

func (runner *Engine) emitModelUsage(usage model.Usage) {
	if runner == nil || runner.display == nil {
		return
	}
	_ = runner.display.Publish(DisplayEvent{Kind: DisplayModelUsage, Usage: usage})
}

func (runner *Engine) setCurrentUsage(usage model.Usage) {
	runner.usage.setCurrent(usage)
}

func (runner *Engine) addSessionUsage(delta tokenUsageTotals) {
	runner.usage.addSession(delta)
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

func (runner *Engine) appendDelta(state *turnState, delta string) error {
	if delta == "" {
		return nil
	}
	if state.overlap.active {
		state.overlap.buffer.WriteString(delta)
		if continuationBufferRuneLen(&state.overlap) < state.overlap.maxRunes {
			return nil
		}
		return runner.flushContinuationOverlap(state)
	}

	return runner.appendResolvedDelta(state, delta)
}

func (runner *Engine) appendResolvedDelta(state *turnState, delta string) error {
	if delta == "" {
		return nil
	}

	state.content.WriteString(delta)
	if state.toolPayload.active {
		return runner.appendToolPayloadCandidateDelta(state, delta)
	}

	switch state.outputMode {
	case outputModeVisible:
		// A model may switch from ordinary prose to a text-encoded tool call
		// after the first visible delta. Do not forward that candidate to the UI;
		// once a delta has been rendered it cannot be retracted from the transcript.
		if looksLikeToolPayloadStart(delta) {
			state.toolPayload.active = true
			state.toolPayload.buffer.WriteString(delta)
			return runner.resolveToolPayloadCandidate(state, false)
		}
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

type toolPayloadCandidateDecision int

const (
	toolPayloadCandidatePending toolPayloadCandidateDecision = iota
	toolPayloadCandidateFlush
	toolPayloadCandidateSuppress
)

func (runner *Engine) appendToolPayloadCandidateDelta(state *turnState, delta string) error {
	state.toolPayload.buffer.WriteString(delta)
	return runner.resolveToolPayloadCandidate(state, false)
}

func (runner *Engine) resolveToolPayloadCandidate(state *turnState, final bool) error {
	if state == nil || !state.toolPayload.active {
		return nil
	}

	candidate := state.toolPayload.buffer.String()
	switch classifyToolPayloadCandidate(candidate, final) {
	case toolPayloadCandidatePending:
		return nil
	case toolPayloadCandidateSuppress:
		state.resetToolPayloadCandidate()
		state.outputMode = outputModeSuppressed
		return nil
	case toolPayloadCandidateFlush:
		state.resetToolPayloadCandidate()
		return runner.writeDelta(state, candidate)
	default:
		return nil
	}
}

func classifyToolPayloadCandidate(candidate string, final bool) toolPayloadCandidateDecision {
	trimmed := strings.TrimSpace(candidate)
	if trimmed == "" {
		if final {
			return toolPayloadCandidateFlush
		}
		return toolPayloadCandidatePending
	}

	if len(extractToolUseEnvelopes(trimmed)) > 0 {
		return toolPayloadCandidateSuppress
	}

	switch {
	case strings.HasPrefix(trimmed, "```"):
		if _, _, ok := extractFenceBodyAt(trimmed, 0); ok {
			return toolPayloadCandidateFlush
		}
	case strings.HasPrefix(trimmed, "{"):
		if _, _, ok := extractBalancedJSONObject(trimmed, 0); ok {
			return toolPayloadCandidateFlush
		}
	case strings.HasPrefix(trimmed, "["):
		if json.Valid([]byte(trimmed)) {
			return toolPayloadCandidateFlush
		}
	case startsToolTagCandidate(trimmed):
		if toolTagCandidateClosed(trimmed) {
			return toolPayloadCandidateFlush
		}
	default:
		return toolPayloadCandidateFlush
	}

	if final {
		return toolPayloadCandidateFlush
	}
	return toolPayloadCandidatePending
}

func startsToolTagCandidate(trimmed string) bool {
	lower := strings.ToLower(trimmed)
	return strings.HasPrefix(lower, "<invoke") ||
		strings.HasPrefix(lower, "<tool_call") ||
		strings.HasPrefix(lower, "<tool ") ||
		strings.HasPrefix(lower, "<tool>")
}

func toolTagCandidateClosed(trimmed string) bool {
	lower := strings.ToLower(trimmed)
	switch {
	case strings.HasPrefix(lower, "<invoke"):
		return strings.Contains(lower, "</invoke>")
	case strings.HasPrefix(lower, "<tool_call"):
		return strings.Contains(lower, "</tool_call>")
	case strings.HasPrefix(lower, "<tool "):
		return strings.Contains(lower, "</tool>")
	case strings.HasPrefix(lower, "<tool>"):
		return strings.Contains(lower, "</tool>")
	default:
		return false
	}
}

func (state *turnState) resetToolPayloadCandidate() {
	if state == nil {
		return
	}
	state.toolPayload.active = false
	state.toolPayload.buffer.Reset()
}

func (state *turnState) beginContinuationOverlap(existing string) {
	state.overlap = continuationOverlapState{}
	existingRunes := []rune(existing)
	limit := minInt(maxContinuationOverlapRunes, len(existingRunes))
	if limit <= 0 {
		return
	}
	state.overlap.active = true
	state.overlap.existing = string(existingRunes[len(existingRunes)-limit:])
	state.overlap.maxRunes = limit
}

func continuationBufferRuneLen(overlap *continuationOverlapState) int {
	if overlap == nil {
		return 0
	}
	return len([]rune(overlap.buffer.String()))
}

func (runner *Engine) flushContinuationOverlap(state *turnState) error {
	if state == nil || !state.overlap.active {
		return nil
	}
	buffered := state.overlap.buffer.String()
	existing := state.overlap.existing
	state.overlap = continuationOverlapState{}
	remaining := trimContinuationOverlap(existing, buffered)
	if remaining == "" {
		return nil
	}
	return runner.appendResolvedDelta(state, remaining)
}

func looksLikeToolPayloadStart(delta string) bool {
	trimmed := strings.TrimSpace(delta)
	if trimmed == "" {
		return false
	}
	if strings.HasPrefix(trimmed, "{") ||
		strings.HasPrefix(trimmed, "[") ||
		strings.HasPrefix(trimmed, "```") {
		return true
	}

	lower := strings.ToLower(trimmed)
	return strings.HasPrefix(lower, "<invoke") ||
		strings.HasPrefix(lower, "<tool_call") ||
		strings.HasPrefix(lower, "<tool ") ||
		strings.HasPrefix(lower, "<tool>")
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

func (runner *Engine) appendThinking(state *turnState, thinking string) error {
	if thinking == "" {
		return nil
	}
	if state != nil {
		runner.ensureLegacyAssistantPart(state, message.AssistantPartReasoning)
		state.parts.appendText(state.legacyActiveIndex, thinking)
	}
	runner.recordTraceEvent("thinking_delta", map[string]any{
		"text":  thinking,
		"bytes": len([]byte(thinking)),
	})
	return runner.display.Publish(DisplayEvent{Kind: DisplayThinkingDelta, Text: thinking})
}

func (runner *Engine) ensureLegacyAssistantPart(state *turnState, partType message.AssistantPartType) {
	if state == nil {
		return
	}
	if state.parts == nil {
		state.parts = newPartAccumulator()
	}
	if state.legacyActiveType == partType {
		if _, exists := state.parts.active[state.legacyActiveIndex]; exists {
			return
		}
	}
	if state.legacyActiveType != "" {
		state.parts.closePart(state.legacyActiveIndex)
	}
	state.legacyNextPartIndex++
	state.legacyActiveIndex = -state.legacyNextPartIndex
	state.legacyActiveType = partType
	state.parts.openPart(state.legacyActiveIndex, partType)
}

func (runner *Engine) finalizeLegacyAssistantPart(state *turnState) {
	if state == nil || state.parts == nil || state.legacyActiveType == "" {
		return
	}
	state.parts.closePart(state.legacyActiveIndex)
	state.legacyActiveType = ""
}

func (runner *Engine) sendReasoningStart(state *turnState, blockIndex int, redacted bool) {
	if runner.display != nil {
		_ = runner.display.Publish(DisplayEvent{Kind: DisplayReasoningStart, PartIndex: blockIndex, Redacted: redacted})
	}
}

func (runner *Engine) sendReasoningDelta(state *turnState, blockIndex int, text string) {
	if text == "" {
		return
	}
	runner.recordTraceEvent("thinking_delta", map[string]any{
		"text":  text,
		"bytes": len([]byte(text)),
	})
	if runner.display != nil {
		_ = runner.display.Publish(DisplayEvent{Kind: DisplayReasoningDelta, PartIndex: blockIndex, Text: text})
	}
}

func (runner *Engine) sendReasoningEnd(state *turnState, blockIndex int) {
	if runner.display != nil {
		_ = runner.display.Publish(DisplayEvent{Kind: DisplayReasoningEnd, PartIndex: blockIndex})
	}
}

func (runner *Engine) writeDelta(state *turnState, delta string) error {
	if delta == "" {
		return nil
	}
	if state != nil && state.legacyEvents {
		runner.ensureLegacyAssistantPart(state, message.AssistantPartText)
		state.parts.appendText(state.legacyActiveIndex, delta)
	}
	runner.recordTraceEvent("assistant_delta", map[string]any{
		"text":  delta,
		"bytes": len([]byte(delta)),
	})

	if err := runner.display.Publish(DisplayEvent{Kind: DisplayAssistantDelta, Text: delta}); err != nil {
		return runner.failAfterPartialOutputForState(state, err)
	}
	state.visibleContent.WriteString(delta)
	state.wroteOutput = true
	return nil
}

func (runner *Engine) finalizeAssistantMessage(state *turnState) (message.Message, error) {
	if err := runner.resolveToolPayloadCandidate(state, true); err != nil {
		return message.Message{}, err
	}
	if len(state.toolCalls) > 0 {
		msg := buildAssistantToolCallMessage(state.toolCalls)
		msg.ProviderData = append(json.RawMessage(nil), state.providerData...)
		msg.AssistantParts = runner.completedAssistantParts(state, state.toolCalls)
		msg.GeneratedBy = state.generatedBy
		return msg, nil
	}

	msg := parseAssistantMessage(state.content.String())
	if calls := toolCallsFromMessage(msg); len(calls) > 0 {
		msg.AssistantParts = runner.completedAssistantParts(state, calls)
		msg.GeneratedBy = state.generatedBy
		return msg, nil
	}

	if !state.wroteOutput {
		if err := runner.writeDelta(state, msg.Content); err != nil {
			return message.Message{}, err
		}
	}
	state.uiFinalized = true
	if err := runner.display.Publish(DisplayEvent{Kind: DisplayDone}); err != nil {
		return message.Message{}, err
	}

	msg.ProviderData = append(json.RawMessage(nil), state.providerData...)
	msg.AssistantParts = runner.completedAssistantParts(state, nil)
	msg.GeneratedBy = state.generatedBy
	return msg, nil
}

func (runner *Engine) completedAssistantParts(state *turnState, calls []message.ToolCall) []message.AssistantPart {
	if state == nil {
		return nil
	}
	var parts []message.AssistantPart
	if state.parts != nil {
		parts = state.parts.finalize()
	}
	for _, call := range calls {
		if assistantPartsContainToolCall(parts, call) {
			continue
		}
		copied := call
		copied.Input = append(json.RawMessage(nil), call.Input...)
		parts = append(parts, message.AssistantPart{
			Type:     message.AssistantPartToolCall,
			Status:   message.AssistantPartCompleted,
			ToolCall: &copied,
		})
	}
	return parts
}

func assistantPartsContainToolCall(parts []message.AssistantPart, want message.ToolCall) bool {
	for _, part := range parts {
		if part.ToolCall == nil {
			continue
		}
		if want.ID != "" || part.ToolCall.ID != "" {
			if want.ID == part.ToolCall.ID {
				return true
			}
			continue
		}
		if want.Name == part.ToolCall.Name && string(want.Input) == string(part.ToolCall.Input) {
			return true
		}
	}
	return false
}

func (runner *Engine) finishWithoutDone(ctx context.Context, state *turnState) (message.Message, error) {
	if ctx.Err() != nil {
		return runner.failModelTurnWithPartial(state, ctx.Err())
	}

	return runner.failModelTurnWithPartial(state, fmt.Errorf("模型流在未发送完成事件时结束"))
}

func (runner *Engine) failAfterPartialOutput(wroteOutput bool, err error) error {
	if wroteOutput {
		_ = runner.display.Publish(DisplayEvent{Kind: DisplayDone})
	}
	return err
}

func (runner *Engine) failModelTurnWithPartial(state *turnState, err error) (message.Message, error) {
	if flushErr := runner.resolveToolPayloadCandidate(state, true); flushErr != nil {
		err = flushErr
	}
	return runner.partialAssistantMessage(state), runner.failAfterPartialOutputForState(state, err)
}

func (runner *Engine) failAfterPartialOutputForState(state *turnState, err error) error {
	if state == nil {
		return err
	}
	if state.uiFinalized {
		return err
	}
	if state.wroteOutput {
		state.uiFinalized = true
	}
	return runner.failAfterPartialOutput(state.wroteOutput, err)
}

func (runner *Engine) partialAssistantMessage(state *turnState) message.Message {
	if state == nil {
		return message.Message{}
	}
	content := state.visibleContent.String()
	if !state.streamEstablished && content == "" && len(state.toolCalls) == 0 && len(state.providerData) == 0 {
		return message.Message{}
	}
	msg := buildAssistantToolCallMessage(state.toolCalls)
	msg.Role = message.RoleAssistant
	msg.Content = content
	msg.ProviderData = append(json.RawMessage(nil), state.providerData...)
	if state.parts != nil {
		msg.AssistantParts = state.parts.finalize()
		for i := range msg.AssistantParts {
			if msg.AssistantParts[i].Status == "" {
				msg.AssistantParts[i].Status = message.AssistantPartPartial
			}
		}
	}
	return msg
}

func (runner *Engine) protocolAssistantMessage(state *turnState) message.Message {
	if state == nil {
		return message.Message{}
	}
	if len(state.toolCalls) > 0 {
		msg := buildAssistantToolCallMessage(state.toolCalls)
		msg.ProviderData = append(json.RawMessage(nil), state.providerData...)
		msg.AssistantParts = runner.completedAssistantParts(state, state.toolCalls)
		msg.GeneratedBy = state.generatedBy
		return msg
	}
	msg := parseAssistantMessage(state.content.String())
	msg.ProviderData = append(json.RawMessage(nil), state.providerData...)
	msg.AssistantParts = runner.completedAssistantParts(state, toolCallsFromMessage(msg))
	msg.GeneratedBy = state.generatedBy
	return msg
}

func turnStateHasToolCalls(state *turnState) bool {
	if state == nil {
		return false
	}
	if len(state.toolCalls) > 0 {
		return true
	}
	msg := parseAssistantMessage(state.content.String())
	return len(toolCallsFromMessage(msg)) > 0
}

func trimContinuationOverlap(existing, next string) string {
	if existing == "" || next == "" {
		return next
	}
	existingRunes := []rune(existing)
	nextRunes := []rune(next)
	limit := minInt(maxContinuationOverlapRunes, len(existingRunes))
	limit = minInt(limit, len(nextRunes))
	for n := limit; n > 0; n-- {
		if string(existingRunes[len(existingRunes)-n:]) == string(nextRunes[:n]) {
			return string(nextRunes[n:])
		}
	}
	return next
}
