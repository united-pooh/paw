package model

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"paw/internal/message"
	"strings"
)

const (
	anthropicVersion          = "2023-06-01"
	anthropicDefaultMaxTokens = 8192
)

type anthropicMessagesRequest struct {
	Model     string             `json:"model"`
	System    json.RawMessage    `json:"system,omitempty"`
	Messages  []anthropicMessage `json:"messages"`
	MaxTokens int                `json:"max_tokens"`
	Stream    bool               `json:"stream"`
	Tools     []anthropicTool    `json:"tools,omitempty"`
}

// anthropicSystemContent 带 cache_control 的 system prompt 内容块。
type anthropicSystemContent struct {
	Type         string                 `json:"type"`
	Text         string                 `json:"text"`
	CacheControl *anthropicCacheControl `json:"cache_control,omitempty"`
}

// anthropicCacheControl Anthropic prompt cache 控制，type="ephemeral" 表示开启缓存。
type anthropicCacheControl struct {
	Type string `json:"type"`
}

// anthropicTool 是 Anthropic Messages API 的工具定义格式。
type anthropicTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type anthropicMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

type anthropicStreamResponse struct {
	Type    string `json:"type"`
	Index   *int   `json:"index,omitempty"`
	Message *struct {
		Usage *Usage `json:"usage,omitempty"`
	} `json:"message,omitempty"`
	ContentBlock *struct {
		Index int    `json:"index"`
		Type  string `json:"type"`
		Text  string `json:"text,omitempty"`
		// thinking / redacted_thinking
		Thinking string `json:"thinking,omitempty"`
		Data     string `json:"data,omitempty"`
		// tool_use
		ID    string          `json:"id,omitempty"`
		Name  string          `json:"name,omitempty"`
		Input json.RawMessage `json:"input,omitempty"`
	} `json:"content_block,omitempty"`
	Delta *struct {
		Type        string  `json:"type"`
		Text        string  `json:"text,omitempty"`
		Thinking    string  `json:"thinking,omitempty"`
		Signature   string  `json:"signature,omitempty"` // signature_delta
		PartialJSON string  `json:"partial_json,omitempty"`
		StopReason  *string `json:"stop_reason,omitempty"`
	} `json:"delta,omitempty"`
	Usage *Usage `json:"usage,omitempty"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error,omitempty"`
}

func (c *Client) streamAnthropicMessage(ctx context.Context, cfg Config, messages []message.Message, tools []ToolDefinition) (<-chan StreamEvent, error) {
	requestBody, err := buildAnthropicMessagesRequest(cfg, messages, tools)
	if err != nil {
		return nil, fmt.Errorf("构造 Anthropic 请求消息失败: %w", err)
	}
	bodyBytes, err := MarshalRequestBody(requestBody, EffectiveExtraRequestBody(cfg))
	if err != nil {
		return nil, fmt.Errorf("序列化 Anthropic 请求体失败: %w", err)
	}

	resp, err := c.doRequestWithRetry(ctx, cfg, true, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(
			ctx,
			http.MethodPost,
			anthropicMessagesURL(cfg),
			bytes.NewReader(bodyBytes),
		)
		if err != nil {
			return nil, fmt.Errorf("创建 Anthropic HTTP 请求失败: %w", err)
		}
		setAnthropicRequestHeaders(req, cfg)
		return req, nil
	})
	if err != nil {
		return nil, fmt.Errorf("调用 Anthropic 流式接口失败: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, providerHTTPErrorFromResponse(resp, "Anthropic 流式接口")
	}

	events := make(chan StreamEvent)
	go c.consumeAnthropicStream(ctx, resp, events)
	return events, nil
}

func buildAnthropicMessagesRequest(cfg Config, messages []message.Message, tools []ToolDefinition) (anthropicMessagesRequest, error) {
	systemParts := make([]string, 0, 1)
	apiMessages := make([]anthropicMessage, 0, len(messages))
	for _, msg := range messages {
		content, err := anthropicContent(msg)
		if err != nil {
			return anthropicMessagesRequest{}, err
		}
		switch msg.Role {
		case message.RoleSystem:
			if contentText, ok := content.(string); ok && strings.TrimSpace(contentText) != "" {
				systemParts = append(systemParts, contentText)
			}
		case message.RoleAssistant:
			if contentText, ok := content.(string); ok && strings.TrimSpace(contentText) != "" {
				apiMessages = append(apiMessages, anthropicMessage{Role: "assistant", Content: content})
			}
		default:
			apiMessages = append(apiMessages, anthropicMessage{Role: "user", Content: content})
		}
	}
	if len(apiMessages) == 0 {
		apiMessages = append(apiMessages, anthropicMessage{Role: "user", Content: "continue"})
	}
	req := anthropicMessagesRequest{
		Model:     cfg.Model,
		Messages:  apiMessages,
		MaxTokens: anthropicDefaultMaxTokens,
		Stream:    true,
	}
	// 将 system prompt 编码为带 cache_control 的结构体数组，启用 Anthropic prompt caching。
	// 这样 Anthropic API 会在后续相同 system 的请求中命中缓存，cache_read_input_tokens 将反映真实缓存命中。
	if systemText := strings.Join(systemParts, "\n\n"); systemText != "" {
		systemBlocks := []anthropicSystemContent{{
			Type:         "text",
			Text:         systemText,
			CacheControl: &anthropicCacheControl{Type: "ephemeral"},
		}}
		if encoded, err := json.Marshal(systemBlocks); err == nil {
			req.System = encoded
		}
	}
	for _, t := range tools {
		req.Tools = append(req.Tools, anthropicTool{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: t.InputSchema,
		})
	}
	return req, nil
}

func anthropicMessagesURL(cfg Config) string {
	base := strings.TrimRight(strings.TrimSpace(cfg.APIBaseURL), "/")
	apiPath := strings.TrimSpace(cfg.APIPath)
	if base == "" {
		return apiPath
	}
	if apiPath == "" {
		return base
	}
	return base + "/" + strings.TrimLeft(apiPath, "/")
}

func setAnthropicRequestHeaders(req *http.Request, cfg Config) {
	req.Header.Set("Content-Type", "application/json")
	for name, value := range cfg.Headers {
		req.Header.Set(name, value)
	}
	req.Header.Set("Anthropic-Version", anthropicVersion)
	if strings.TrimSpace(cfg.APIKey) != "" {
		req.Header.Set("X-Api-Key", cfg.APIKey)
	}
}

// activeAnthropicToolCall 追踪正在流式接收的原生 tool_use 块。
type activeAnthropicToolCall struct {
	id           string
	name         string
	initialInput json.RawMessage
	args         strings.Builder
	sawDelta     bool
}

func (c *Client) consumeAnthropicStream(ctx context.Context, resp *http.Response, events chan<- StreamEvent) {
	defer close(events)
	defer func(Body io.ReadCloser) {
		_ = Body.Close()
	}(resp.Body)

	scanner := newStreamScanner(resp.Body)
	// 索引映射：block index → part type
	blockTypes := map[int]string{}
	// 追踪每个 block index 的工具调用累积状态
	toolCalls := map[int]*activeAnthropicToolCall{}
	// 按完成顺序记录工具调用
	completedToolIndices := []int{}

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
			break
		}

		chunk, err := decodeAnthropicStreamChunk(payload)
		if err != nil {
			_ = emitStreamEvent(ctx, events, StreamEvent{Err: fmt.Errorf("解析 Anthropic 流式数据失败: %w", err)})
			return
		}
		if chunk.Error != nil {
			_ = emitStreamEvent(ctx, events, StreamEvent{Err: fmt.Errorf("Anthropic 流式接口返回错误: %s", chunk.Error.Message)})
			return
		}

		// content_block_start: 记录类型，发出 part start
		if chunk.ContentBlock != nil {
			idx := 0
			if chunk.Index != nil {
				idx = *chunk.Index
			} else {
				idx = chunk.ContentBlock.Index
			}
			switch chunk.ContentBlock.Type {
			case "text":
				blockTypes[idx] = "text"
				if !emitStreamEvent(ctx, events, anEvent(AssistantPartLifecycleStart, "text", idx, "")) {
					return
				}
				if chunk.ContentBlock.Text != "" {
					if !emitStreamEvent(ctx, events, anEvent(AssistantPartLifecycleDelta, "text", idx, chunk.ContentBlock.Text)) {
						return
					}
				}
			case "thinking":
				blockTypes[idx] = "reasoning"
				if !emitStreamEvent(ctx, events, anEvent(AssistantPartLifecycleStart, "reasoning", idx, "")) {
					return
				}
				if chunk.ContentBlock.Thinking != "" {
					if !emitStreamEvent(ctx, events, anEvent(AssistantPartLifecycleDelta, "reasoning", idx, chunk.ContentBlock.Thinking)) {
						return
					}
				}
			case "redacted_thinking":
				blockTypes[idx] = "reasoning"
				ev := anEvent(AssistantPartLifecycleStart, "reasoning", idx, "")
				ev.AssistantPart.Redacted = true
				if chunk.ContentBlock.Data != "" {
					ev.AssistantPart.OpaqueData = json.RawMessage(chunk.ContentBlock.Data)
				}
				if !emitStreamEvent(ctx, events, ev) {
					return
				}
			case "tool_use":
				blockTypes[idx] = "tool_call"
				toolCalls[idx] = &activeAnthropicToolCall{
					id:           chunk.ContentBlock.ID,
					name:         chunk.ContentBlock.Name,
					initialInput: append(json.RawMessage(nil), chunk.ContentBlock.Input...),
				}
				ev := anEvent(AssistantPartLifecycleStart, "tool_call", idx, "")
				ev.AssistantPart.ToolCallID = chunk.ContentBlock.ID
				ev.AssistantPart.ToolName = chunk.ContentBlock.Name
				if len(chunk.ContentBlock.Input) != 0 {
					ev.AssistantPart.ToolArgs = append(json.RawMessage(nil), chunk.ContentBlock.Input...)
				}
				if !emitStreamEvent(ctx, events, ev) {
					return
				}
			}
			continue
		}

		// content_block_stop: 按已知类型发出 part end
		if chunk.Type == "content_block_stop" {
			idx := 0
			if chunk.Index != nil {
				idx = *chunk.Index
			}
			partType, known := blockTypes[idx]
			if !known {
				partType = "text"
			}
			ev := anEvent(AssistantPartLifecycleEnd, partType, idx, "")
			if tool, exists := toolCalls[idx]; exists && tool != nil && partType == "tool_call" {
				if tool.sawDelta {
					ev.AssistantPart.ToolArgs = json.RawMessage(tool.args.String())
				} else {
					ev.AssistantPart.ToolArgs = append(json.RawMessage(nil), tool.initialInput...)
				}
				completedToolIndices = append(completedToolIndices, idx)
			}
			if !emitStreamEvent(ctx, events, ev) {
				return
			}
			continue
		}

		// message_stop: 整批验证工具调用后结束
		if chunk.Type == "message_stop" {
			if err := finalizeAndFlushToolCalls(events, ctx, toolCalls, completedToolIndices); err != nil {
				_ = emitStreamEvent(ctx, events, StreamEvent{Err: err})
				return
			}
			_ = emitStreamEvent(ctx, events, StreamEvent{Done: true})
			return
		}

		// 增量事件
		if chunk.Delta != nil {
			idx := 0
			if chunk.Index != nil {
				idx = *chunk.Index
			}
			switch chunk.Delta.Type {
			case "text_delta":
				if !emitStreamEvent(ctx, events, anEvent(AssistantPartLifecycleDelta, "text", idx, chunk.Delta.Text)) {
					return
				}
			case "thinking_delta":
				if !emitStreamEvent(ctx, events, anEvent(AssistantPartLifecycleDelta, "reasoning", idx, chunk.Delta.Thinking)) {
					return
				}
			case "signature_delta":
				ev := anEvent(AssistantPartLifecycleDelta, "reasoning", idx, "")
				if chunk.Delta.Signature != "" {
					ev.AssistantPart.OpaqueData = json.RawMessage(chunk.Delta.Signature)
				}
				if !emitStreamEvent(ctx, events, ev) {
					return
				}
			case "input_json_delta":
				if tool, exists := toolCalls[idx]; exists && tool != nil {
					if !tool.sawDelta {
						tool.args.Reset()
						tool.sawDelta = true
					}
					tool.args.WriteString(chunk.Delta.PartialJSON)
				}
				if !emitStreamEvent(ctx, events, anEvent(AssistantPartLifecycleDelta, "tool_call", idx, "")) {
					return
				}
			}
		}

		// Usage
		if chunk.Message != nil && chunk.Message.Usage != nil {
			if !emitStreamEvent(ctx, events, StreamEvent{Usage: chunk.Message.Usage}) {
				return
			}
		}
		if chunk.Usage != nil {
			if !emitStreamEvent(ctx, events, StreamEvent{Usage: chunk.Usage}) {
				return
			}
		}
	}

	if err := scanner.Err(); err != nil {
		_ = emitStreamEvent(ctx, events, StreamEvent{Err: fmt.Errorf("读取 Anthropic 流式响应失败: %w", err)})
		return
	}
	// EOF 前未收到 message_stop: flush 工具调用后发 Done
	if err := finalizeAndFlushToolCalls(events, ctx, toolCalls, completedToolIndices); err != nil {
		_ = emitStreamEvent(ctx, events, StreamEvent{Err: err})
		return
	}
	_ = emitStreamEvent(ctx, events, StreamEvent{Done: true})
}

func anEvent(lifecycle AssistantPartLifecycle, partType string, blockIndex int, delta string) StreamEvent {
	return StreamEvent{
		AssistantPart: &AssistantPartEvent{
			Lifecycle:  lifecycle,
			BlockIndex: blockIndex,
			Type:       partType,
			Delta:      delta,
		},
	}
}

func finalizeAndFlushToolCalls(events chan<- StreamEvent, ctx context.Context, toolCalls map[int]*activeAnthropicToolCall, completedIndices []int) error {
	seen := map[int]bool{}
	callList := make([]*activeAnthropicToolCall, 0, len(completedIndices))
	for _, idx := range completedIndices {
		if seen[idx] {
			continue
		}
		seen[idx] = true
		if tc, exists := toolCalls[idx]; exists {
			callList = append(callList, tc)
		}
	}
	if len(callList) == 0 {
		return nil
	}
	toolCallMessages := make([]message.ToolCall, 0, len(callList))
	for _, tc := range callList {
		if tc == nil || tc.name == "" {
			return fmt.Errorf("Anthropic returned invalid tool call without name")
		}
		input, err := tc.input()
		if err != nil {
			return err
		}
		toolCallMessages = append(toolCallMessages, message.ToolCall{ID: tc.id, Name: tc.name, Input: input})
	}
	if len(toolCallMessages) > 0 {
		if !emitStreamEvent(ctx, events, StreamEvent{ToolCalls: toolCallMessages}) {
			return nil
		}
	}
	return nil
}

// resolveAnthropicToolCalls 整批验证全部工具调用参数，任一非法则整体拒绝。
func resolveAnthropicToolCalls(accumulated []*activeAnthropicToolCall) ([]message.ToolCall, error) {
	if len(accumulated) == 0 {
		return nil, nil
	}
	calls := make([]message.ToolCall, 0, len(accumulated))
	for _, t := range accumulated {
		if t == nil || t.name == "" {
			return nil, fmt.Errorf("Anthropic returned invalid tool call without name")
		}
		input, err := t.input()
		if err != nil {
			return nil, err
		}
		calls = append(calls, message.ToolCall{ID: t.id, Name: t.name, Input: input})
	}
	return calls, nil
}

func (t *activeAnthropicToolCall) input() (json.RawMessage, error) {
	if t == nil {
		return nil, fmt.Errorf("Anthropic returned invalid JSON object arguments for tool %q", "")
	}
	if t.sawDelta {
		return decodeToolArguments("Anthropic", t.id, t.name, []byte(t.args.String()))
	}
	return decodeToolArguments("Anthropic", t.id, t.name, t.initialInput)
}

func (c *Client) handleAnthropicStreamLine(ctx context.Context, line string, events chan<- StreamEvent) (done bool, err error) {
	return false, nil
}

func (c *Client) handleAnthropicStreamPayload(ctx context.Context, payload string, events chan<- StreamEvent) (done bool, err error) {
	return false, nil
}

func decodeAnthropicStreamChunk(payload string) (anthropicStreamResponse, error) {
	var chunk anthropicStreamResponse
	err := json.Unmarshal([]byte(payload), &chunk)
	return chunk, err
}
