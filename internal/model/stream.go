package model

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"paw/internal/message"
	"sort"
	"strings"
)

// StreamEvent 表示一次流式输出事件。
// 一个事件只表达一种状态：
// 1) Delta 非空：收到一段增量文本
// 2) Done=true：流结束
// 3) Err 非空：流中出现错误
type StreamEvent struct {
	Delta     string
	Thinking  string
	ToolCalls []message.ToolCall
	Done      bool
	Err       error
	Usage     *Usage
}

// chatCompletionsStreamResponse 只建模流式响应里当前需要的字段。
// 不需要的字段故意省略，避免上层依赖供应商完整响应结构。
type chatCompletionsStreamResponse struct {
	Choices []struct {
		Delta struct {
			Content   string          `json:"content"`
			ToolCalls []toolCallDelta `json:"tool_calls,omitempty"`
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

	cfg := c.CurrentModelConfig()
	if !cfg.Stream {
		return c.nonStreamingOpenAIMessage(ctx, cfg, messages, tools)
	}
	if shouldAttemptAnthropicStream(cfg) {
		events, err := c.streamAnthropicMessage(ctx, cfg, messages, tools)
		if err == nil {
			return events, nil
		}
	}

	return c.streamOpenAIMessage(ctx, cfg, messages, tools)
}

type nonStreamingChatResponse struct {
	Choices []struct {
		Message struct {
			Content   string `json:"content"`
			ToolCalls []struct {
				ID       string `json:"id"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls,omitempty"`
		} `json:"message"`
	} `json:"choices"`
	Usage *Usage `json:"usage,omitempty"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error,omitempty"`
}

func (c *Client) nonStreamingOpenAIMessage(ctx context.Context, cfg Config, messages []message.Message, tools []ToolDefinition) (<-chan StreamEvent, error) {
	apiMessages, err := buildOpenAIMessages(messages)
	if err != nil {
		return nil, fmt.Errorf("构造 OpenAI 请求消息失败: %w", err)
	}
	reqBody := ChatCompletionsRequest{
		Model:    cfg.Model,
		Messages: apiMessages,
		Stream:   false,
	}
	for _, t := range tools {
		reqBody.Tools = append(reqBody.Tools, openAITool{
			Type: "function",
			Function: openAIToolFunction{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.InputSchema,
			},
		})
	}
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("序列化请求体失败: %w", err)
	}
	resp, err := c.doRequestWithRetry(ctx, cfg, false, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.APIBaseURL+cfg.APIPath, bytes.NewReader(bodyBytes))
		if err != nil {
			return nil, fmt.Errorf("创建 HTTP 请求失败: %w", err)
		}
		c.setRequestHeaders(req)
		return req, nil
	})
	if err != nil {
		return nil, fmt.Errorf("调用模型接口失败: %w", err)
	}
	defer resp.Body.Close()
	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取响应体失败: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("模型接口返回异常状态 %d: %s", resp.StatusCode, strings.TrimSpace(string(respBytes)))
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

	choice := parsed.Choices[0].Message
	events := make(chan StreamEvent, 4)
	if parsed.Usage != nil {
		events <- StreamEvent{Usage: parsed.Usage}
	}
	if content := strings.TrimSpace(choice.Content); content != "" {
		events <- StreamEvent{Delta: content}
	}
	if len(choice.ToolCalls) > 0 {
		calls := make([]message.ToolCall, 0, len(choice.ToolCalls))
		for _, call := range choice.ToolCalls {
			input := json.RawMessage(`{}`)
			if strings.TrimSpace(call.Function.Arguments) != "" && json.Valid([]byte(call.Function.Arguments)) {
				input = json.RawMessage(call.Function.Arguments)
			}
			calls = append(calls, message.ToolCall{ID: call.ID, Name: call.Function.Name, Input: input})
		}
		events <- StreamEvent{ToolCalls: calls}
	}
	if len(events) == 0 {
		close(events)
		return nil, fmt.Errorf("模型接口返回了空内容")
	}
	events <- StreamEvent{Done: true}
	close(events)
	return events, nil
}

func (c *Client) streamOpenAIMessage(ctx context.Context, cfg Config, messages []message.Message, tools []ToolDefinition) (<-chan StreamEvent, error) {
	apiMessages, err := buildOpenAIMessages(messages)
	if err != nil {
		return nil, fmt.Errorf("构造 OpenAI 请求消息失败: %w", err)
	}
	// 启用 stream=true，让服务端按 SSE 增量返回。
	reqBody := ChatCompletionsRequest{
		Model:         cfg.Model,
		Messages:      apiMessages,
		Stream:        true,
		StreamOptions: &StreamOptions{IncludeUsage: true},
	}
	for _, t := range tools {
		reqBody.Tools = append(reqBody.Tools, openAITool{
			Type: "function",
			Function: openAIToolFunction{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.InputSchema,
			},
		})
	}

	// 组装 JSON 请求体。
	bodyBytes, err := json.Marshal(reqBody)
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
		c.setRequestHeaders(req)
		return req, nil
	})
	if err != nil {
		return nil, fmt.Errorf("调用模型接口失败: %w", err)
	}

	// SSE 流建立失败（非 2xx）时，读取错误体后返回。
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBytes, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("模型接口返回异常状态 %d，且读取错误响应失败: %w", resp.StatusCode, readErr)
		}
		return nil, fmt.Errorf("模型接口返回异常状态 %d: %s", resp.StatusCode, strings.TrimSpace(string(respBytes)))
	}

	// 这里开始转入异步消费：
	// - 调用方立即拿到 channel，不阻塞当前 goroutine
	// - 后台 goroutine 负责解析 SSE 并投递事件
	events := make(chan StreamEvent)
	go c.consumeStream(ctx, resp, events)
	return events, nil
}

func shouldAttemptAnthropicStream(cfg Config) bool {
	baseURL := strings.ToLower(strings.TrimSpace(cfg.APIBaseURL))
	apiPath := strings.ToLower(strings.TrimSpace(cfg.APIPath))
	return normalizeProvider(cfg.Provider) == ProviderDeepSeek ||
		strings.Contains(baseURL, "/anthropic") ||
		strings.HasSuffix(apiPath, "/messages")
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
		_ = emitStreamEvent(ctx, events, StreamEvent{Done: true})
		return true, nil
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

	delta := chunk.Choices[0].Delta.Content
	if delta != "" {
		if !emitStreamEvent(ctx, events, StreamEvent{Delta: delta}) {
			return true
		}
	}

	finishReason := chunk.Choices[0].FinishReason
	if finishReason != nil && *finishReason != "" {
		_ = emitStreamEvent(ctx, events, StreamEvent{Done: true})
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

func openAIToolCallsFromAccumulated(accumulated map[int]*activeOpenAIToolCall) []message.ToolCall {
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
		args := call.args.String()
		input := json.RawMessage(`{}`)
		if len(args) > 0 && json.Valid([]byte(args)) {
			input = json.RawMessage(args)
		}
		calls = append(calls, message.ToolCall{
			ID:    call.id,
			Name:  call.name,
			Input: input,
		})
	}
	return calls
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

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" || payload == "[DONE]" {
			break
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
		if len(chunk.Choices) > 0 {
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

		// 文本内容 delta
		if delta := chunk.Choices[0].Delta.Content; delta != "" {
			if !emitStreamEvent(ctx, events, StreamEvent{Delta: delta}) {
				return
			}
		}

		// finish_reason：先 flush 工具调用，再发 Done
		finishReason := chunk.Choices[0].FinishReason
		if finishReason != nil && *finishReason != "" {
			if calls := openAIToolCallsFromAccumulated(accumulated); len(calls) > 0 {
				if !emitStreamEvent(ctx, events, StreamEvent{ToolCalls: calls}) {
					return
				}
			}
			_ = emitStreamEvent(ctx, events, StreamEvent{Done: true})
			return
		}
	}

	if err := scanner.Err(); err != nil {
		_ = emitStreamEvent(ctx, events, StreamEvent{Err: fmt.Errorf("读取流式响应失败: %w", err)})
		return
	}

	// EOF 前未收到 finish_reason：flush 积累的工具调用后发 Done
	if calls := openAIToolCallsFromAccumulated(accumulated); len(calls) > 0 {
		if !emitStreamEvent(ctx, events, StreamEvent{ToolCalls: calls}) {
			return
		}
	}
	_ = emitStreamEvent(ctx, events, StreamEvent{Done: true})
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
