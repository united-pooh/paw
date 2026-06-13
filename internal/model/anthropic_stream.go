package model

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"gocode/internal/message"
	"io"
	"net/http"
	"strings"
)

const (
	anthropicVersion          = "2023-06-01"
	anthropicDefaultMaxTokens = 8192
)

type anthropicMessagesRequest struct {
	Model     string             `json:"model"`
	System    string             `json:"system,omitempty"`
	Messages  []anthropicMessage `json:"messages"`
	MaxTokens int                `json:"max_tokens"`
	Stream    bool               `json:"stream"`
}

type anthropicMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
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

func (c *Client) streamAnthropicMessage(ctx context.Context, cfg Config, messages []message.Message) (<-chan StreamEvent, error) {
	bodyBytes, err := json.Marshal(buildAnthropicMessagesRequest(cfg, messages))
	if err != nil {
		return nil, fmt.Errorf("序列化 Anthropic 请求体失败: %w", err)
	}

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

	resp, err := c.streamHTTPClient().Do(req)
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

func buildAnthropicMessagesRequest(cfg Config, messages []message.Message) anthropicMessagesRequest {
	systemParts := make([]string, 0, 1)
	apiMessages := make([]anthropicMessage, 0, len(messages))
	for _, msg := range messages {
		content := strings.TrimRight(msg.Content, "\n")
		switch msg.Role {
		case message.RoleSystem:
			if strings.TrimSpace(content) != "" {
				systemParts = append(systemParts, content)
			}
		case message.RoleAssistant:
			if strings.TrimSpace(content) != "" {
				apiMessages = append(apiMessages, anthropicMessage{Role: "assistant", Content: content})
			}
		default:
			apiMessages = append(apiMessages, anthropicMessage{Role: "user", Content: content})
		}
	}
	if len(apiMessages) == 0 {
		apiMessages = append(apiMessages, anthropicMessage{Role: "user", Content: "continue"})
	}
	return anthropicMessagesRequest{
		Model:     cfg.Model,
		System:    strings.Join(systemParts, "\n\n"),
		Messages:  apiMessages,
		MaxTokens: anthropicDefaultMaxTokens,
		Stream:    true,
	}
}

func anthropicMessagesURL(cfg Config) string {
	base := strings.TrimRight(strings.TrimSpace(cfg.APIBaseURL), "/")
	lowerBase := strings.ToLower(base)
	switch {
	case strings.HasSuffix(lowerBase, "/anthropic/v1"):
		return base + "/messages"
	case strings.HasSuffix(lowerBase, "/anthropic"):
		return base + "/v1/messages"
	case normalizeProvider(cfg.Provider) == ProviderDeepSeek:
		return strings.TrimRight(strings.TrimSuffix(base, "/v1"), "/") + "/anthropic/v1/messages"
	default:
		apiPath := strings.TrimSpace(cfg.APIPath)
		if apiPath == "" {
			apiPath = "/v1/messages"
		}
		return base + apiPath
	}
}

func setAnthropicRequestHeaders(req *http.Request, cfg Config) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Anthropic-Version", anthropicVersion)
	if strings.TrimSpace(cfg.APIKey) != "" {
		req.Header.Set("X-Api-Key", cfg.APIKey)
	}
}

func (c *Client) consumeAnthropicStream(ctx context.Context, resp *http.Response, events chan<- StreamEvent) {
	defer close(events)
	defer func(Body io.ReadCloser) {
		_ = Body.Close()
	}(resp.Body)

	scanner := newStreamScanner(resp.Body)
	for scanner.Scan() {
		done, err := c.handleAnthropicStreamLine(ctx, scanner.Text(), events)
		if err != nil {
			_ = emitStreamEvent(ctx, events, StreamEvent{Err: err})
			return
		}
		if done {
			return
		}
	}
	if err := scanner.Err(); err != nil {
		_ = emitStreamEvent(ctx, events, StreamEvent{Err: fmt.Errorf("读取 Anthropic 流式响应失败: %w", err)})
		return
	}
	_ = emitStreamEvent(ctx, events, StreamEvent{Done: true})
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
