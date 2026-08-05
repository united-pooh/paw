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
