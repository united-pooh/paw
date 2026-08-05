package model

import "paw/internal/message"

type DeepSeekAdapter struct{}

func (DeepSeekAdapter) Name() string { return "deepseek" }

func (DeepSeekAdapter) PrepareTools(tools []ToolDefinition) (PreparedToolSet, error) {
	return prepareDeepSeekTools(tools)
}

func (DeepSeekAdapter) BuildChatCompletionsRequest(cfg Config, messages []message.Message, tools PreparedToolSet, stream bool) (ChatCompletionsRequest, error) {
	return buildOpenAICompatibleChatCompletionsRequest(cfg, messages, tools, stream)
}

var _ ModelAdapter = DeepSeekAdapter{}
