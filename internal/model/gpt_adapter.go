package model

import "paw/internal/message"

type GPTAdapter struct{}

func (GPTAdapter) Name() string { return "gpt" }

func (GPTAdapter) PrepareTools(tools []ToolDefinition) (PreparedToolSet, error) {
	return preparePassthroughTools(tools), nil
}

func (GPTAdapter) BuildChatCompletionsRequest(cfg Config, messages []message.Message, tools PreparedToolSet, stream bool) (ChatCompletionsRequest, error) {
	return buildOpenAICompatibleChatCompletionsRequest(cfg, messages, tools, stream)
}
