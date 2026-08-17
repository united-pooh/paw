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

// ResponsesRequestBuilder constructs the provider-independent Responses API
// request body. It is separate from ModelAdapter because Chat Completions and
// Responses are different wire protocols even when they share model adapters.
type ResponsesRequestBuilder interface {
	BuildResponsesRequest(cfg Config, messages []message.Message, tools PreparedToolSet, stream bool) (responsesRequest, error)
}

// selectResponsesAdapter keeps GPT Responses on the OpenAI-compatible adapter
// boundary while leaving other Responses providers on their existing path.
func selectResponsesAdapter(cfg Config, fallback ModelAdapter) ModelAdapter {
	if isGPTResponsesConfig(cfg) {
		return OpenAICompatibleAdapter{}
	}
	return fallback
}

func buildResponsesRequestForAdapter(cfg Config, adapter ModelAdapter, messages []message.Message, tools PreparedToolSet, stream bool) (responsesRequest, error) {
	adapter = selectResponsesAdapter(cfg, adapter)
	if builder, ok := adapter.(ResponsesRequestBuilder); ok {
		return builder.BuildResponsesRequest(cfg, messages, tools, stream)
	}
	return buildResponsesRequest(cfg, messages, tools, stream)
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
