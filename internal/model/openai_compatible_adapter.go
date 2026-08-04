package model

import "paw/internal/message"

type OpenAICompatibleAdapter struct{}

func (OpenAICompatibleAdapter) Name() string { return "openai-compatible" }

func (OpenAICompatibleAdapter) BuildChatCompletionsRequest(cfg Config, messages []message.Message, tools []ToolDefinition, stream bool) (ChatCompletionsRequest, error) {
	return buildOpenAICompatibleChatCompletionsRequest(cfg, messages, tools, stream, false)
}
