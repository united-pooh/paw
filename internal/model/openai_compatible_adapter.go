package model

import "paw/internal/message"

type OpenAICompatibleAdapter struct{}

func (OpenAICompatibleAdapter) Name() string { return "openai-compatible" }

func (OpenAICompatibleAdapter) PrepareTools(tools []ToolDefinition) (PreparedToolSet, error) {
	return preparePassthroughTools(tools), nil
}

func (OpenAICompatibleAdapter) BuildChatCompletionsRequest(cfg Config, messages []message.Message, tools PreparedToolSet, stream bool) (ChatCompletionsRequest, error) {
	return buildOpenAICompatibleChatCompletionsRequest(cfg, messages, tools, stream)
}

func (OpenAICompatibleAdapter) BuildResponsesRequest(cfg Config, messages []message.Message, tools PreparedToolSet, stream bool) (responsesRequest, error) {
	return buildResponsesRequest(cfg, messages, tools, stream)
}

var _ ModelAdapter = OpenAICompatibleAdapter{}
var _ ResponsesRequestBuilder = OpenAICompatibleAdapter{}
