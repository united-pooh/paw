package model

import "paw/internal/message"

type GPTAdapter struct{}

func (GPTAdapter) Name() string { return "gpt" }

func (GPTAdapter) BuildChatCompletionsRequest(cfg Config, messages []message.Message, tools []ToolDefinition, stream bool) (ChatCompletionsRequest, error) {
	return buildOpenAICompatibleChatCompletionsRequest(cfg, messages, tools, stream, false)
}
