package model

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"paw/internal/message"
	"sort"
	"strings"
)

type AssistantPartLifecycle string

const (
	AssistantPartLifecycleStart AssistantPartLifecycle = "start"
	AssistantPartLifecycleDelta AssistantPartLifecycle = "delta"
	AssistantPartLifecycleEnd   AssistantPartLifecycle = "end"
)

// AssistantPartEvent 表示一条有序助理 part 的生命周期事件。
// 官方传输层应当使用这些事件，而非直接发送 Delta/Thinking/ToolCalls。
// 旧字段 Delta/Thinking/ToolCalls/ProviderData 暂保留，由 Runner 兼容层转换为 part 事件。
type AssistantPartEvent struct {
	Lifecycle  AssistantPartLifecycle
	BlockIndex int
	Type       string // "reasoning" | "text" | "tool_call"
	Delta      string
	Redacted   bool
	ToolCallID string
	ToolName   string
	ToolArgs   json.RawMessage
	OpaqueData json.RawMessage
}

// StreamEvent 表示一次流式输出事件。
// 一个事件只表达一种状态：
// 1) AssistantPart 非空：part 生命周期事件
// 2) Delta 非空：收到一段增量文本（旧路径，由 Runner 转换为 AssistantPart）
// 3) Thinking 非空：thinking 增量（旧路径，由 Runner 转换为 AssistantPart）
// 4) Done=true：当前协议响应结束
// 5) Err 非空：流中出现错误
type StreamEvent struct {
	AssistantPart *AssistantPartEvent
	GeneratedBy   *message.MessageOrigin
	Delta         string
	Thinking      string
	ToolCalls     []message.ToolCall
	ProviderData  json.RawMessage
	Done          bool
	FinishReason  FinishReason
	Err           error
	Usage         *Usage
}

// FinishReason is the typed Chat Completions finish_reason value surfaced to
// the runner. Non-Chat transports may continue to emit Done with the zero value.
type FinishReason string

const (
	FinishReasonStop          FinishReason = "stop"
	FinishReasonToolCalls     FinishReason = "tool_calls"
	FinishReasonLength        FinishReason = "length"
	FinishReasonContentFilter FinishReason = "content_filter"
)

// chatCompletionsStreamResponse 只建模流式响应里当前需要的字段。
// 不需要的字段故意省略，避免上层依赖供应商完整响应结构。
type chatCompletionsStreamResponse struct {
	Choices []struct {
		Delta struct {
			Content          string          `json:"content"`
			ReasoningContent string          `json:"reasoning_content,omitempty"`
			Reasoning        string          `json:"reasoning,omitempty"`
			ToolCalls        []toolCallDelta `json:"tool_calls,omitempty"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Usage *Usage `json:"usage,omitempty"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error,omitempty"`
}

// toolCallDelta 是 OpenAI 兼容接口流式 tool_calls 的单个增量。
type toolCallDelta struct {
	Index    int    `json:"index"`
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// StreamMessage 以 SSE 方式返回模型增量输出。
// 调用方拿到 channel 后持续 range：
// - 处理 Delta
// - 处理 Err
// - 收到 Done 后结束当前轮次
func (c *Client) StreamMessage(ctx context.Context, messages []message.Message, tools []ToolDefinition) (<-chan StreamEvent, error) {
	// 模型接口要求至少有一条输入消息。
	if len(messages) == 0 {
		return nil, fmt.Errorf("messages 不能为空")
	}

	// 出站前修复工具调用配对（对齐 CodeWhale prepare_model_bound_request）：
	// 隔离孤儿 tool result、给悬空 tool_use 补合成错误结果，从源头避免
	// "No tool call found for tool output with call_id ..." 类 400。
	// 幂等且无问题时零拷贝；修复统计在此层不暴露，由上层另行记录。
	messages, _ = RepairToolCallPairs(messages)

	cfg := c.CurrentModelConfig()
	transport := "openai-compatible"
	if shouldUseResponsesAPI(cfg) {
		transport = "openai-responses"
	} else if shouldAttemptAnthropicStream(cfg) {
		transport = "anthropic-compatible"
	}
	origin := &message.MessageOrigin{Transport: transport, Model: cfg.Model}
	adapter := SelectModelAdapter(cfg)
	prepared, err := c.prepareTools(adapter, tools)
	if err != nil {
		return nil, fmt.Errorf("准备 %s 工具失败: %w", adapter.Name(), err)
	}
	if shouldUseResponsesAPI(cfg) {
		if !cfg.Stream {
			events, err := c.nonStreamingResponsesMessage(ctx, cfg, messages, prepared)
			if err != nil {
				return nil, err
			}
			return wrapWithOrigin(adaptPreparedToolEvents(ctx, events, prepared), origin), nil
		}
		events, err := c.streamResponsesMessage(ctx, cfg, messages, prepared)
		if err != nil {
			return nil, err
		}
		return wrapWithOrigin(adaptPreparedToolEvents(ctx, events, prepared), origin), nil
	}
	if !cfg.Stream {
		events, err := c.nonStreamingOpenAIMessage(ctx, cfg, adapter, messages, prepared)
		if err != nil {
			return nil, err
		}
		return wrapWithOrigin(adaptPreparedToolEvents(ctx, events, prepared), origin), nil
	}
	if shouldAttemptAnthropicStream(cfg) {
		events, err := c.streamAnthropicMessage(ctx, cfg, messages, tools)
		if err == nil {
			return wrapWithOrigin(events, origin), nil
		}
		var providerErr *ProviderHTTPError
		if errors.As(err, &providerErr) && providerErr.StatusCode != http.StatusNotFound && providerErr.StatusCode != http.StatusMethodNotAllowed {
			return nil, err
		}
	}

	events, err := c.streamOpenAIMessage(ctx, cfg, adapter, messages, prepared)
	if err != nil {
		return nil, err
	}
	return wrapWithOrigin(adaptPreparedToolEvents(ctx, events, prepared), origin), nil
}

func wrapWithOrigin(events <-chan StreamEvent, origin *message.MessageOrigin) <-chan StreamEvent {
	out := make(chan StreamEvent)
	go func() {
		defer close(out)
		out <- StreamEvent{GeneratedBy: origin}
		for ev := range events {
			out <- ev
		}
	}()
	return out
}

type nonStreamingChatResponse struct {
	Choices []struct {
		Message struct {
			Content          string `json:"content"`
			ReasoningContent string `json:"reasoning_content,omitempty"`
			ToolCalls        []struct {
				ID       string `json:"id"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls,omitempty"`
		} `json:"message"`
		FinishReason *string `json:"finish_reason,omitempty"`
	} `json:"choices"`
	Usage *Usage `json:"usage,omitempty"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error,omitempty"`
}

func (c *Client) nonStreamingOpenAIMessage(ctx context.Context, cfg Config, adapter ModelAdapter, messages []message.Message, tools PreparedToolSet) (<-chan StreamEvent, error) {
	reqBody, err := adapter.BuildChatCompletionsRequest(cfg, messages, tools, false)
	if err != nil {
		return nil, fmt.Errorf("构造 %s 请求失败: %w", adapter.Name(), err)
	}
	bodyBytes, err := MarshalRequestBody(reqBody, EffectiveChatCompletionsExtraRequestBody(cfg))
	if err != nil {
		return nil, fmt.Errorf("序列化请求体失败: %w", err)
	}
	resp, err := c.doRequestWithRetry(ctx, cfg, false, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.APIBaseURL+cfg.APIPath, bytes.NewReader(bodyBytes))
		if err != nil {
			return nil, fmt.Errorf("创建 HTTP 请求失败: %w", err)
		}
		c.setRequestHeadersForConfig(req, cfg)
		return req, nil
	})
	if err != nil {
		return nil, fmt.Errorf("调用模型接口失败: %w", err)
	}
	defer resp.Body.Close()
	respBytes, err := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, newProviderHTTPErrorWithReadError(resp.StatusCode, resp.Header, respBytes, err, "模型接口")
	}
	if err != nil {
		return nil, fmt.Errorf("读取响应体失败: %w", err)
	}
	var parsed nonStreamingChatResponse
	if err := json.Unmarshal(respBytes, &parsed); err != nil {
		return nil, fmt.Errorf("解析响应 JSON 失败: %w", err)
	}
	if parsed.Error != nil {
		return nil, fmt.Errorf("模型接口返回错误: %s", parsed.Error.Message)
	}
	if len(parsed.Choices) == 0 {
		return nil, fmt.Errorf("模型接口未返回任何 choices")
	}

	finishReason, err := chatCompletionFinishReason(parsed.Choices[0].FinishReason, true)
	if err != nil {
		return nil, err
	}
	choice := parsed.Choices[0].Message
	if finishReason == FinishReasonToolCalls && len(choice.ToolCalls) == 0 {
		return nil, errChatCompletionToolCallsWithoutCalls()
	}
	events := make(chan StreamEvent, 4)
	if parsed.Usage != nil {
		events <- StreamEvent{Usage: parsed.Usage}
	}
	if reasoning := strings.TrimSpace(choice.ReasoningContent); reasoning != "" {
		events <- StreamEvent{Thinking: reasoning}
	}
	if content := strings.TrimSpace(choice.Content); content != "" {
		events <- StreamEvent{Delta: content}
	}
	if len(choice.ToolCalls) > 0 {
		calls := make([]message.ToolCall, 0, len(choice.ToolCalls))
		for _, call := range choice.ToolCalls {
			input, err := decodeToolArguments("Chat Completions", call.ID, call.Function.Name, []byte(call.Function.Arguments))
			if err != nil {
				close(events)
				return nil, err
			}
			calls = append(calls, message.ToolCall{ID: call.ID, Name: call.Function.Name, Input: input})
		}
		events <- StreamEvent{ToolCalls: calls}
	}
	if len(events) == 0 {
		close(events)
		return nil, fmt.Errorf("模型接口返回了空内容")
	}
	events <- StreamEvent{Done: true, FinishReason: finishReason}
	close(events)
	return events, nil
}

func (c *Client) streamOpenAIMessage(ctx context.Context, cfg Config, adapter ModelAdapter, messages []message.Message, tools PreparedToolSet) (<-chan StreamEvent, error) {
	reqBody, err := adapter.BuildChatCompletionsRequest(cfg, messages, tools, true)
	if err != nil {
		return nil, fmt.Errorf("构造 %s 请求失败: %w", adapter.Name(), err)
	}

	// 组装 JSON 请求体。
	bodyBytes, err := MarshalRequestBody(reqBody, EffectiveChatCompletionsExtraRequestBody(cfg))
	if err != nil {
		return nil, fmt.Errorf("序列化请求体失败: %w", err)
	}

	// 将上层 context 透传到底层 HTTP 请求，支持超时/取消；请求建立失败时
	// 按配置重试，响应一旦建立则交给 SSE 消费器继续读取。
	resp, err := c.doRequestWithRetry(ctx, cfg, true, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(
			ctx,
			http.MethodPost,
			cfg.APIBaseURL+cfg.APIPath,
			bytes.NewReader(bodyBytes),
		)
		if err != nil {
			return nil, fmt.Errorf("创建 HTTP 请求失败: %w", err)
		}
		// 当前本地网关沿用标准 JSON 请求体和 Bearer 鉴权头。
		c.setRequestHeadersForConfig(req, cfg)
		return req, nil
	})
	if err != nil {
		return nil, fmt.Errorf("调用模型接口失败: %w", err)
	}

	// SSE 流建立失败（非 2xx）时，读取错误体后返回。
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, providerHTTPErrorFromResponse(resp, "模型接口")
	}

	// 这里开始转入异步消费：
	// - 调用方立即拿到 channel，不阻塞当前 goroutine
	// - 后台 goroutine 负责解析 SSE 并投递事件
	events := make(chan StreamEvent)
	go c.consumeStream(ctx, resp, events)
	return events, nil
}

func shouldAttemptAnthropicStream(cfg Config) bool {
	transport := strings.ToLower(strings.TrimSpace(cfg.Transport))
	return strings.Contains(transport, "anthropic")
}

const (
	streamScannerInitialBufferBytes = 64 * 1024
	streamScannerMaxTokenBytes      = 32 * 1024 * 1024
)

func newStreamScanner(body io.Reader) *bufio.Scanner {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, streamScannerInitialBufferBytes), streamScannerMaxTokenBytes)
	return scanner
}

func (c *Client) handleStreamLine(ctx context.Context, line string, events chan<- StreamEvent) (done bool, err error) {
	line = strings.TrimSpace(line)
	if line == "" {
		return false, nil
	}
	if !strings.HasPrefix(line, "data:") {
		return false, nil
	}

	payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
	return c.handleStreamPayload(ctx, payload, events)
}

func (c *Client) handleStreamPayload(ctx context.Context, payload string, events chan<- StreamEvent) (done bool, err error) {
	if payload == "" {
		return false, nil
	}
	if payload == "[DONE]" {
		return true, fmt.Errorf("Chat Completions stream sent [DONE] before finish_reason")
	}

	chunk, err := decodeStreamChunk(payload)
	if err != nil {
		return false, fmt.Errorf("解析流式数据失败: %w", err)
	}
	if chunk.Error != nil {
		return false, fmt.Errorf("模型接口返回错误: %s", chunk.Error.Message)
	}

	return emitChunkEvents(ctx, chunk, events), nil
}

func decodeStreamChunk(payload string) (chatCompletionsStreamResponse, error) {
	var chunk chatCompletionsStreamResponse
	err := json.Unmarshal([]byte(payload), &chunk)
	return chunk, err
}

func emitChunkEvents(ctx context.Context, chunk chatCompletionsStreamResponse, events chan<- StreamEvent) (done bool) {
	if chunk.Usage != nil {
		if !emitStreamEvent(ctx, events, StreamEvent{Usage: chunk.Usage}) {
			return true
		}
	}
	if len(chunk.Choices) == 0 {
		return false
	}

	// 推理/思考内容 delta
	reasoningText := chunk.Choices[0].Delta.ReasoningContent
	if reasoningText == "" {
		reasoningText = chunk.Choices[0].Delta.Reasoning
	}
	if reasoningText != "" {
		if !emitStreamEvent(ctx, events, StreamEvent{Thinking: reasoningText}) {
			return true
		}
	}

	delta := chunk.Choices[0].Delta.Content
	if delta != "" {
		if !emitStreamEvent(ctx, events, StreamEvent{Delta: delta}) {
			return true
		}
	}

	finishReason := chunk.Choices[0].FinishReason
	if finishReason != nil && *finishReason != "" {
		reason, err := chatCompletionFinishReason(finishReason, false)
		if err != nil {
			_ = emitStreamEvent(ctx, events, StreamEvent{Err: err})
			return true
		}
		_ = emitStreamEvent(ctx, events, StreamEvent{Done: true, FinishReason: reason})
		return true
	}

	return false
}

// activeOpenAIToolCall 追踪正在流式累积的 OpenAI tool_call 增量。
type activeOpenAIToolCall struct {
	id   string
	name string
	args strings.Builder
}

func openAIToolCallsFromAccumulated(accumulated map[int]*activeOpenAIToolCall) ([]message.ToolCall, error) {
	calls := make([]message.ToolCall, 0, len(accumulated))
	indexes := make([]int, 0, len(accumulated))
	for index := range accumulated {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	for _, index := range indexes {
		call := accumulated[index]
		if call == nil || call.name == "" {
			continue
		}
		input, err := decodeToolArguments("Chat Completions", call.id, call.name, []byte(call.args.String()))
		if err != nil {
			return nil, err
		}
		calls = append(calls, message.ToolCall{
			ID:    call.id,
			Name:  call.name,
			Input: input,
		})
	}
	return calls, nil
}

func chatCompletionFinishReason(raw *string, allowMissing bool) (FinishReason, error) {
	if raw == nil || strings.TrimSpace(*raw) == "" {
		if allowMissing {
			return FinishReasonStop, nil
		}
		return "", fmt.Errorf("Chat Completions stream ended before finish_reason")
	}
	reason := FinishReason(strings.TrimSpace(*raw))
	switch reason {
	case FinishReasonStop, FinishReasonToolCalls, FinishReasonLength:
		return reason, nil
	case FinishReasonContentFilter:
		return "", fmt.Errorf("Chat Completions response stopped by content_filter")
	default:
		return "", fmt.Errorf("Chat Completions response returned unknown finish_reason %q", string(reason))
	}
}

func errChatCompletionToolCallsWithoutCalls() error {
	return fmt.Errorf("Chat Completions finish_reason tool_calls without tool calls")
}

// consumeStream 负责把 SSE 文本行转换为 StreamEvent，并累积原生 tool_calls。
func (c *Client) consumeStream(ctx context.Context, resp *http.Response, events chan<- StreamEvent) {
	defer close(events)
	defer func(Body io.ReadCloser) {
		_ = Body.Close()
	}(resp.Body)

	scanner := newStreamScanner(resp.Body)
	// 按 index 累积 tool_calls，支持单次响应中多个工具调用
	accumulated := map[int]*activeOpenAIToolCall{}
	var finishReason FinishReason
	finishSeen := false

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" {
			continue
		}
		if payload == "[DONE]" {
			if !finishSeen {
				_ = emitStreamEvent(ctx, events, StreamEvent{Err: fmt.Errorf("Chat Completions stream sent [DONE] before finish_reason")})
				return
			}
			_ = emitStreamEvent(ctx, events, StreamEvent{Done: true, FinishReason: finishReason})
			return
		}

		chunk, err := decodeStreamChunk(payload)
		if err != nil {
			_ = emitStreamEvent(ctx, events, StreamEvent{Err: fmt.Errorf("解析流式数据失败: %w", err)})
			return
		}
		if chunk.Error != nil {
			_ = emitStreamEvent(ctx, events, StreamEvent{Err: fmt.Errorf("模型接口返回错误: %s", chunk.Error.Message)})
			return
		}
		// 累积 delta.tool_calls
		if len(chunk.Choices) > 0 && !finishSeen {
			for _, tc := range chunk.Choices[0].Delta.ToolCalls {
				call, ok := accumulated[tc.Index]
				if !ok {
					call = &activeOpenAIToolCall{}
					accumulated[tc.Index] = call
				}
				if tc.ID != "" {
					call.id = tc.ID
				}
				if tc.Function.Name != "" {
					call.name = tc.Function.Name
				}
				call.args.WriteString(tc.Function.Arguments)
			}
		}

		// Usage
		if chunk.Usage != nil {
			if !emitStreamEvent(ctx, events, StreamEvent{Usage: chunk.Usage}) {
				return
			}
		}

		if len(chunk.Choices) == 0 {
			continue
		}

		// 推理/思考内容 delta（DeepSeek reasoning_content / 通用 reasoning）
		reasoningText := chunk.Choices[0].Delta.ReasoningContent
		if reasoningText == "" {
			reasoningText = chunk.Choices[0].Delta.Reasoning
		}
		if reasoningText != "" && !finishSeen {
			if !emitStreamEvent(ctx, events, StreamEvent{Thinking: reasoningText}) {
				return
			}
		}

		// 文本内容 delta
		if delta := chunk.Choices[0].Delta.Content; delta != "" && !finishSeen {
			if !emitStreamEvent(ctx, events, StreamEvent{Delta: delta}) {
				return
			}
		}

		// finish_reason：先验证并 flush 工具调用，但继续读取可能存在的 usage-only 尾块。
		rawFinishReason := chunk.Choices[0].FinishReason
		if rawFinishReason != nil && *rawFinishReason != "" {
			if finishSeen {
				_ = emitStreamEvent(ctx, events, StreamEvent{Err: fmt.Errorf("Chat Completions stream returned duplicate finish_reason")})
				return
			}
			reason, err := chatCompletionFinishReason(rawFinishReason, false)
			if err != nil {
				_ = emitStreamEvent(ctx, events, StreamEvent{Err: err})
				return
			}
			calls, err := openAIToolCallsFromAccumulated(accumulated)
			if err != nil {
				_ = emitStreamEvent(ctx, events, StreamEvent{Err: err})
				return
			}
			if reason == FinishReasonToolCalls && len(calls) == 0 {
				_ = emitStreamEvent(ctx, events, StreamEvent{Err: errChatCompletionToolCallsWithoutCalls()})
				return
			}
			if len(calls) > 0 {
				if !emitStreamEvent(ctx, events, StreamEvent{ToolCalls: calls}) {
					return
				}
				accumulated = map[int]*activeOpenAIToolCall{}
			}
			finishReason = reason
			finishSeen = true
			continue
		}
	}

	if err := scanner.Err(); err != nil {
		_ = emitStreamEvent(ctx, events, StreamEvent{Err: fmt.Errorf("读取流式响应失败: %w", err)})
		return
	}

	if !finishSeen {
		_ = emitStreamEvent(ctx, events, StreamEvent{Err: fmt.Errorf("Chat Completions stream ended before finish_reason")})
		return
	}
	_ = emitStreamEvent(ctx, events, StreamEvent{Done: true, FinishReason: finishReason})
}

// emitStreamEvent 在 ctx 取消时主动放弃发送，避免 goroutine 卡在 channel send 上，
// 从而保证 resp.Body 的 defer Close 可以执行。
func emitStreamEvent(ctx context.Context, events chan<- StreamEvent, ev StreamEvent) bool {
	select {
	case events <- ev:
		return true
	case <-ctx.Done():
		return false
	}
}
