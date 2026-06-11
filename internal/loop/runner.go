package loop

import (
	"context"
	"encoding/json"
	"fmt"
	"gocode/internal/message"
	"gocode/internal/model"
	"gocode/internal/tool"
	"gocode/internal/ui"
	"strings"
)

const (
	maxToolRounds       = 20
	toolUseResponseType = "tool_use"
)

// ModelStreamer 是 loop 层依赖的最小模型流式接口。
type ModelStreamer interface {
	StreamMessage(ctx context.Context, messages []message.Message) (<-chan model.StreamEvent, error)
}

// HistoryStore 是 Runner 依赖的最小历史存储接口。
// loop 层只关心“加载历史”和“追加历史”，不需要知道完整 session 能力。
type HistoryStore interface {
	LoadResolvedHistory(ctx context.Context, sessionID string) ([]message.Message, error)
	Append(ctx context.Context, sessionID string, msgs ...message.Message) error
}

// Runner 负责单轮工具闭环调度。
type Runner struct {
	model     ModelStreamer
	ui        ui.UI
	registry  *tool.Registry
	store     HistoryStore
	sessionID string
	// history 保存“已经成功完成”的多轮对话消息。
	// 当前调用路径是串行的，因此这里先不引入锁。
	// TODO: 如果后续要把 Runner 暴露给并发调用方，需要为 history 增加互斥保护。
	history []message.Message
}

type outputMode int

const (
	outputModeUndecided outputMode = iota
	outputModeVisible
	outputModeSuppressed
)

type turnState struct {
	content     strings.Builder
	pending     strings.Builder
	wroteOutput bool
	outputMode  outputMode
}

type toolUseEnvelope struct {
	Type  string          `json:"type"`
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

// NewRunner 创建调度器。
func NewRunner(model ModelStreamer, output ui.UI, registry *tool.Registry, store HistoryStore, sessionID string) *Runner {
	return &Runner{
		model:     model,
		ui:        output,
		registry:  registry,
		store:     store,
		sessionID: sessionID,
	}
}

// RunTurn 执行一次最小工具闭环流程，并返回最终 assistant 消息。
func (runner *Runner) RunTurn(ctx context.Context, input string) (message.Message, error) {
	if err := runner.validate(); err != nil {
		return message.Message{}, err
	}

	if runner.history == nil && runner.store != nil {
		messages, err := runner.store.LoadResolvedHistory(ctx, runner.sessionID)
		if err != nil {
			return message.Message{}, err
		}
		runner.history = messages
	}

	// 每一轮都基于“已提交的历史副本”工作。
	// 这样即使当前轮中途失败，也不会污染下一轮上下文。
	history := runner.buildTurnHistory(input)
	for round := 0; round < maxToolRounds; round++ {
		assistantMessage, err := runner.runModelTurn(ctx, history)
		if err != nil {
			return message.Message{}, err
		}
		history = append(history, assistantMessage)

		if assistantMessage.ToolUse == nil {
			// 只有当本轮完整结束时，才把本轮消息提交为新的会话历史。
			if err := runner.commitHistory(ctx, history); err != nil {
				return message.Message{}, err
			}
			return assistantMessage, nil
		}

		toolResult, err := runner.runToolCall(ctx, assistantMessage.ToolUse)
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

func (runner *Runner) runModelTurn(ctx context.Context, history []message.Message) (message.Message, error) {
	events, err := runner.model.StreamMessage(ctx, runner.buildModelMessages(history))
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

// buildTurnHistory 复制已完成历史，并在尾部追加当前用户输入。
// 这样单轮执行可以安全地在本地 history 上反复追加 tool_use / tool_result。
func (runner *Runner) buildTurnHistory(input string) []message.Message {
	history := append([]message.Message(nil), runner.history...)
	history = append(history, buildUserMessages(input)...)
	return history
}

// commitHistory 在一次 turn 成功结束后提交历史。
// 先计算新增消息，再更新内存、持久化磁盘，两步顺序不能颠倒。
func (runner *Runner) commitHistory(ctx context.Context, history []message.Message) error {
	if runner.store != nil {
		// 在更新 runner.history 之前，先用旧长度算出本轮新增的消息。
		// runner.history 是上一轮结束时的状态，history 是本轮完整状态。
		prevLen := len(runner.history)
		newMsgs := history[prevLen:]
		if len(newMsgs) > 0 {
			if err := runner.store.Append(ctx, runner.sessionID, newMsgs...); err != nil {
				return fmt.Errorf("保存会话历史失败: %w", err)
			}
		}
	}

	// 持久化成功后再更新内存副本。
	runner.history = append([]message.Message(nil), history...)
	return nil
}

// ResetHistory 清空当前对话上下文。
// 交互式 REPL 可以用它实现 /clear 这类命令。
// TODO: 接入会话持久化后，这里还需要同步清理对应的 session 数据。
func (runner *Runner) ResetHistory() {
	runner.history = []message.Message{}
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
	// TODO: 后续工具多了以后肯定需要优化的
	var prompt strings.Builder
	prompt.WriteString("You are a helpful coding assistant.\n")

	descriptions := []string{}
	if runner.registry != nil {
		descriptions = runner.registry.Describe()
	}

	if len(descriptions) == 0 {
		prompt.WriteString("Answer with plain text.\n")
		return prompt.String()
	}

	prompt.WriteString("You can use tools.\n")
	prompt.WriteString("Available tools:\n")
	for _, description := range descriptions {
		prompt.WriteString("- ")
		prompt.WriteString(description)
		prompt.WriteByte('\n')
	}
	prompt.WriteString("When you need a tool, respond with ONLY a JSON object in this format:\n")
	prompt.WriteString(`{"type":"tool_use","id":"call_1","name":"tool_name","input":{}}`)
	prompt.WriteByte('\n')
	prompt.WriteString("Make sure the input object matches the tool input_schema exactly.\n")
	prompt.WriteString("Do not wrap the JSON in markdown fences.\n")
	prompt.WriteString("After you receive a TOOL_RESULT message:\n")
	prompt.WriteString("- If the result contains useful information, use it to continue reasoning or call another tool.\n")
	prompt.WriteString("- If the result shows an error or failure (e.g. test failures, compile errors), analyze it and try to fix the problem by calling more tools.\n")
	prompt.WriteString("- Only provide a plain-text final answer when you have fully resolved the task or determined it cannot be completed.\n")
	return prompt.String()
}

func renderMessageForModel(msg message.Message) message.Message {
	switch {
	case msg.ToolUse != nil:
		return message.Message{
			Role:    message.RoleAssistant,
			Content: marshalJSON(toolUseEnvelope{Type: toolUseResponseType, ID: msg.ToolUse.ID, Name: msg.ToolUse.Name, Input: msg.ToolUse.Input}),
		}
	case msg.ToolResult != nil:
		return message.Message{
			Role:    message.RoleUser,
			Content: "TOOL_RESULT:\n" + marshalJSON(msg.ToolResult),
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

	if err := runner.appendDelta(state, ev.Delta); err != nil {
		return message.Message{}, false, err
	}

	if !ev.Done {
		return message.Message{}, false, nil
	}

	msg, err := runner.finalizeAssistantMessage(state)
	return msg, true, err
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
	if _, ok := extractToolUseEnvelope(trimmed); ok {
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
	if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "`") {
		return outputModeSuppressed
	}

	return outputModeVisible
}

func looksLikeToolPreamble(trimmed string) bool {
	lower := strings.ToLower(trimmed)
	hints := []string{
		"我将",
		"我会",
		"让我",
		"先使用",
		"首先使用",
		"i will",
		"i'll",
		"let me",
		"tool_use",
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

func (runner *Runner) writeDelta(state *turnState, delta string) error {
	if delta == "" {
		return nil
	}

	if err := runner.ui.OnAssistantDelta(delta); err != nil {
		return runner.failAfterPartialOutput(state.wroteOutput, err)
	}
	state.wroteOutput = true
	return nil
}

func (runner *Runner) finalizeAssistantMessage(state *turnState) (message.Message, error) {
	msg := parseAssistantMessage(state.content.String())
	if msg.ToolUse != nil {
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

	envelope, ok := extractToolUseEnvelope(trimmed)
	if !ok {
		return buildAssistantMessage(content)
	}
	if envelope.ID == "" {
		envelope.ID = envelope.Name
	}

	return message.Message{
		Role: message.RoleAssistant,
		ToolUse: &message.ToolCall{
			ID:    envelope.ID,
			Name:  envelope.Name,
			Input: envelope.Input,
		},
	}
}

// extractToolUseEnvelope 从 assistant 输出中提取第一个合法的 tool_use 对象。
// 当前兼容三类格式：
// 1) 整段内容就是裸 JSON
// 2) 整段内容就是 fenced JSON
// 3) 说明文字中嵌入 fenced JSON 或裸 JSON
func extractToolUseEnvelope(trimmed string) (toolUseEnvelope, bool) {
	if envelope, ok := decodeToolUseEnvelope(trimmed); ok {
		return envelope, true
	}

	if payload, ok := extractFencedToolUsePayload(trimmed); ok {
		return decodeToolUseEnvelope(payload)
	}

	if payload, ok := extractEmbeddedJSONObject(trimmed); ok {
		return decodeToolUseEnvelope(payload)
	}

	return toolUseEnvelope{}, false
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

func (runner *Runner) runToolCall(ctx context.Context, call *message.ToolCall) (message.Message, error) {
	if call == nil {
		return buildToolResultMessage("", "tool call is nil", true), nil
	}

	if err := runner.ui.OnToolCall(ui.ToolCallEvent{
		ID:    call.ID,
		Name:  call.Name,
		Input: append(json.RawMessage(nil), call.Input...),
	}); err != nil {
		return message.Message{}, err
	}

	result := runner.executeToolCall(ctx, call)
	if err := runner.ui.OnToolResult(ui.ToolResultEvent{
		ToolUseID: result.ToolResult.ToolUseID,
		Name:      call.Name,
		Content:   result.ToolResult.Content,
		IsError:   result.ToolResult.IsError,
	}); err != nil {
		return message.Message{}, err
	}

	return result, nil
}

func (runner *Runner) executeToolCall(ctx context.Context, call *message.ToolCall) message.Message {
	if runner.registry == nil {
		return buildToolResultMessage(call.ID, "tool registry is nil", true)
	}

	selectedTool, ok := runner.registry.Get(call.Name)
	if !ok {
		return buildToolResultMessage(call.ID, fmt.Sprintf("unknown tool: %s", call.Name), true)
	}

	output, err := selectedTool.Run(ctx, call.Input)
	if err != nil {
		return buildToolResultMessage(call.ID, err.Error(), true)
	}

	return buildToolResultMessage(call.ID, output, false)
}
