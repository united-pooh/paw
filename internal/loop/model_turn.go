package loop

import (
	"context"
	"encoding/json"
	"fmt"
	"paw/internal/message"
	"paw/internal/model"
	"paw/internal/tokentracer"
	"paw/internal/ui"
	"strings"
)

func (runner *Runner) runModelTurn(ctx context.Context, history []message.Message, turn *TurnState) (message.Message, error) {
	var tools []model.ToolDefinition
	if runner.registry != nil {
		tools = runner.registry.Definitions()
	}
	modelMessages, err := runner.buildModelMessages(ctx, history, turn)
	if err != nil {
		return message.Message{}, err
	}
	if turn != nil {
		turn.PlanEmitted = true
	}
	events, err := runner.model.StreamMessage(ctx, modelMessages, tools)
	if err != nil {
		return message.Message{}, err
	}

	return runner.consumeStream(ctx, events)
}

func (runner *Runner) buildModelMessages(ctx context.Context, history []message.Message, turn *TurnState) ([]message.Message, error) {
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

func (runner *Runner) persistInputAttachments(ctx context.Context, input *message.Message) error {
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

func (runner *Runner) materializeAttachments(ctx context.Context, msg *message.Message) error {
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

func (runner *Runner) buildSystemPrompt() string {
	return runner.buildSystemPromptForTurn(nil)
}

func (runner *Runner) buildSystemPromptForTurn(turn *TurnState) string {
	descriptions := []string{}
	if runner.registry != nil {
		runner.mu.RLock()
		compactToolPrompt := runner.compactToolPrompt
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
			Parts:   append([]message.ContentPart(nil), msg.Parts...),
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

func (runner *Runner) setCurrentUsage(usage model.Usage) {
	runner.mu.Lock()
	defer runner.mu.Unlock()
	runner.usage = usage
	runner.usageKnown = true
}

func (runner *Runner) addSessionUsage(delta tokenUsageTotals) {
	runner.mu.Lock()
	defer runner.mu.Unlock()

	current := usageTotalsFromUsage(runner.sessionUsage, runner.sessionUsageKnown)
	current = current.add(delta)
	runner.sessionUsage = usageFromTotals(current)
	runner.sessionUsageKnown = true
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
		// A model may switch from ordinary prose to a text-encoded tool call
		// after the first visible delta. Do not forward that candidate to the UI;
		// once a delta has been rendered it cannot be retracted from the transcript.
		if looksLikeToolPayloadStart(delta) {
			state.outputMode = outputModeSuppressed
			return nil
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
