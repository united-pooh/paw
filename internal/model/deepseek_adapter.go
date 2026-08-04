package model

import "paw/internal/message"

type DeepSeekAdapter struct{}

func (DeepSeekAdapter) Name() string { return "deepseek" }

func (DeepSeekAdapter) BuildChatCompletionsRequest(cfg Config, messages []message.Message, tools []ToolDefinition, stream bool) (ChatCompletionsRequest, error) {
	req, err := buildOpenAICompatibleChatCompletionsRequest(cfg, messages, nil, stream, false)
	if err != nil {
		return ChatCompletionsRequest{}, err
	}
	for _, tool := range tools {
		schema, err := normalizeDeepSeekToolSchema(tool.Name, tool.InputSchema)
		if err != nil {
			return ChatCompletionsRequest{}, err
		}
		req.Tools = append(req.Tools, openAITool{Type: "function", Function: openAIToolFunction{
			Name: tool.Name, Description: tool.Description, Parameters: schema, Strict: true,
		}})
	}
	return req, nil
}

var _ ModelAdapter = DeepSeekAdapter{}
