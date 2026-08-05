package model

import (
	"encoding/json"
	"fmt"

	"paw/internal/message"
)

// ModelAdapter prepares provider-specific tools independently from the wire
// transport, then builds Chat Completions requests from that prepared catalog.
type ModelAdapter interface {
	Name() string
	PrepareTools(tools []ToolDefinition) (PreparedToolSet, error)
	BuildChatCompletionsRequest(cfg Config, messages []message.Message, tools PreparedToolSet, stream bool) (ChatCompletionsRequest, error)
}

func buildOpenAICompatibleChatCompletionsRequest(cfg Config, messages []message.Message, tools PreparedToolSet, stream bool) (ChatCompletionsRequest, error) {
	apiMessages, err := buildOpenAIMessages(messages)
	if err != nil {
		return ChatCompletionsRequest{}, fmt.Errorf("构造 OpenAI 请求消息失败: %w", err)
	}
	req := ChatCompletionsRequest{Model: cfg.Model, Messages: apiMessages, Stream: stream}
	if stream {
		req.StreamOptions = &StreamOptions{IncludeUsage: true}
	}
	for _, tool := range tools {
		parameters := tool.Parameters
		if len(parameters) == 0 {
			parameters = json.RawMessage(`{"type":"object","properties":{},"required":[],"additionalProperties":false}`)
		}
		req.Tools = append(req.Tools, openAITool{Type: "function", Function: openAIToolFunction{
			Name: tool.Name, Description: tool.Description, Parameters: parameters, Strict: tool.Strict,
		}})
	}
	return req, nil
}
