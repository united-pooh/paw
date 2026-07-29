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
	Message *struct {
		Usage *Usage `json:"usage,omitempty"`
	} `json:"message,omitempty"`
	ContentBlock *struct {
		Type     string `json:"type"`
		Text     string `json:"text,omitempty"`
		Thinking string `json:"thinking,omitempty"`
		// tool_use 类型的额外字段
		ID    string          `json:"id,omitempty"`
		Name  string          `json:"name,omitempty"`
		Input json.RawMessage `json:"input,omitempty"`
	} `json:"content_block,omitempty"`
	Delta *struct {
		Type        string  `json:"type"`
		Text        string  `json:"text,omitempty"`
		Thinking    string  `json:"thinking,omitempty"`
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
	bodyBytes, err := json.Marshal(requestBody)
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
		respBytes, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("Anthropic 流式接口返回异常状态 %d，且读取错误响应失败: %w", resp.StatusCode, readErr)
		}
		return nil, fmt.Errorf("Anthropic 流式接口返回异常状态 %d: %s", resp.StatusCode, strings.TrimSpace(string(respBytes)))
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
	// 当前正在累积的原生 tool_use 块（nil 表示当前是文本内容）
	var activeTool *activeAnthropicToolCall

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

		// 原生工具调用：content_block_start type=tool_use
		if chunk.ContentBlock != nil && chunk.ContentBlock.Type == "tool_use" {
			activeTool = &activeAnthropicToolCall{
				id:           chunk.ContentBlock.ID,
				name:         chunk.ContentBlock.Name,
				initialInput: append(json.RawMessage(nil), chunk.ContentBlock.Input...),
			}
			continue
		}

		// 原生工具调用：input_json_delta → 累积参数
		if chunk.Delta != nil && chunk.Delta.Type == "input_json_delta" && activeTool != nil {
			if !activeTool.sawDelta {
				activeTool.args.Reset()
				activeTool.sawDelta = true
			}
			activeTool.args.WriteString(chunk.Delta.PartialJSON)
			continue
		}

		// 原生工具调用：content_block_stop → 发出完整工具调用
		if chunk.Type == "content_block_stop" && activeTool != nil {
			input := activeTool.input()
			if !emitStreamEvent(ctx, events, StreamEvent{ToolCalls: []message.ToolCall{{
				ID:    activeTool.id,
				Name:  activeTool.name,
				Input: input,
			}}}) {
				return
			}
			activeTool = nil
			continue
		}

		// 普通事件：文本、thinking、usage 等
		if done := emitAnthropicChunkEvents(ctx, chunk, events); done {
			return
		}
	}

	if err := scanner.Err(); err != nil {
		_ = emitStreamEvent(ctx, events, StreamEvent{Err: fmt.Errorf("读取 Anthropic 流式响应失败: %w", err)})
		return
	}
	_ = emitStreamEvent(ctx, events, StreamEvent{Done: true})
}

func (t *activeAnthropicToolCall) input() json.RawMessage {
	if t == nil {
		return json.RawMessage(`{}`)
	}
	if t.sawDelta {
		args := t.args.String()
		if input, ok := normalizeAnthropicToolInput(json.RawMessage(args)); ok {
			return input
		}
		return json.RawMessage(`{}`)
	}
	if input, ok := normalizeAnthropicToolInput(t.initialInput); ok {
		return input
	}
	return json.RawMessage(`{}`)
}

func normalizeAnthropicToolInput(raw json.RawMessage) (json.RawMessage, bool) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, false
	}
	if !json.Valid(trimmed) {
		return nil, false
	}
	if len(trimmed) > 0 && trimmed[0] == '"' {
		var nested string
		if err := json.Unmarshal(trimmed, &nested); err != nil {
			return nil, false
		}
		trimmed = bytes.TrimSpace([]byte(nested))
		if len(trimmed) == 0 || !json.Valid(trimmed) {
			return nil, false
		}
	}
	return append(json.RawMessage(nil), trimmed...), true
}

func (c *Client) handleAnthropicStreamLine(ctx context.Context, line string, events chan<- StreamEvent) (done bool, err error) {
	line = strings.TrimSpace(line)
	if line == "" || !strings.HasPrefix(line, "data:") {
		return false, nil
	}
	payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
	if payload == "" {
		return false, nil
	}
	return c.handleAnthropicStreamPayload(ctx, payload, events)
}

func (c *Client) handleAnthropicStreamPayload(ctx context.Context, payload string, events chan<- StreamEvent) (done bool, err error) {
	if payload == "[DONE]" {
		_ = emitStreamEvent(ctx, events, StreamEvent{Done: true})
		return true, nil
	}

	chunk, err := decodeAnthropicStreamChunk(payload)
	if err != nil {
		return false, fmt.Errorf("解析 Anthropic 流式数据失败: %w", err)
	}
	if chunk.Error != nil {
		return false, fmt.Errorf("Anthropic 流式接口返回错误: %s", chunk.Error.Message)
	}
	return emitAnthropicChunkEvents(ctx, chunk, events), nil
}

func decodeAnthropicStreamChunk(payload string) (anthropicStreamResponse, error) {
	var chunk anthropicStreamResponse
	err := json.Unmarshal([]byte(payload), &chunk)
	return chunk, err
}

func emitAnthropicChunkEvents(ctx context.Context, chunk anthropicStreamResponse, events chan<- StreamEvent) (done bool) {
	if chunk.Message != nil && chunk.Message.Usage != nil {
		if !emitStreamEvent(ctx, events, StreamEvent{Usage: chunk.Message.Usage}) {
			return true
		}
	}
	if chunk.Usage != nil {
		if !emitStreamEvent(ctx, events, StreamEvent{Usage: chunk.Usage}) {
			return true
		}
	}
	if chunk.ContentBlock != nil {
		switch chunk.ContentBlock.Type {
		case "text":
			if chunk.ContentBlock.Text != "" && !emitStreamEvent(ctx, events, StreamEvent{Delta: chunk.ContentBlock.Text}) {
				return true
			}
		case "thinking":
			if chunk.ContentBlock.Thinking != "" && !emitStreamEvent(ctx, events, StreamEvent{Thinking: chunk.ContentBlock.Thinking}) {
				return true
			}
		}
	}
	if chunk.Delta != nil {
		switch chunk.Delta.Type {
		case "text_delta":
			if chunk.Delta.Text != "" && !emitStreamEvent(ctx, events, StreamEvent{Delta: chunk.Delta.Text}) {
				return true
			}
		case "thinking_delta":
			if chunk.Delta.Thinking != "" && !emitStreamEvent(ctx, events, StreamEvent{Thinking: chunk.Delta.Thinking}) {
				return true
			}
		case "input_json_delta":
			if chunk.Delta.PartialJSON != "" && !emitStreamEvent(ctx, events, StreamEvent{Delta: chunk.Delta.PartialJSON}) {
				return true
			}
		}
	}
	if chunk.Type == "message_stop" {
		_ = emitStreamEvent(ctx, events, StreamEvent{Done: true})
		return true
	}
	return false
}
