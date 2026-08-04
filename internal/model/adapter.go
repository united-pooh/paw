package model

import (
	"encoding/json"
	"fmt"

	"paw/internal/message"
)

// ModelAdapter builds a provider-specific Chat Completions request.
type ModelAdapter interface {
	Name() string
	BuildChatCompletionsRequest(cfg Config, messages []message.Message, tools []ToolDefinition, stream bool) (ChatCompletionsRequest, error)
}

func buildOpenAICompatibleChatCompletionsRequest(cfg Config, messages []message.Message, tools []ToolDefinition, stream bool, strict bool) (ChatCompletionsRequest, error) {
	apiMessages, err := buildOpenAIMessages(messages)
	if err != nil {
		return ChatCompletionsRequest{}, fmt.Errorf("构造 OpenAI 请求消息失败: %w", err)
	}
	req := ChatCompletionsRequest{Model: cfg.Model, Messages: apiMessages, Stream: stream}
	if stream {
		req.StreamOptions = &StreamOptions{IncludeUsage: true}
	}
	for _, tool := range tools {
		parameters := tool.InputSchema
		if len(parameters) == 0 {
			parameters = json.RawMessage(`{"type":"object","properties":{},"required":[],"additionalProperties":false}`)
		}
		req.Tools = append(req.Tools, openAITool{Type: "function", Function: openAIToolFunction{
			Name: tool.Name, Description: tool.Description, Parameters: parameters, Strict: strict,
		}})
	}
	return req, nil
}
