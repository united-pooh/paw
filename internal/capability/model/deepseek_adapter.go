package model

import (
	"encoding/json"
	"fmt"
	"strings"

	"paw/internal/message"
)

type DeepSeekAdapter struct{}

func (DeepSeekAdapter) Name() string { return "deepseek" }

func (DeepSeekAdapter) PrepareTools(tools []ToolDefinition) (PreparedToolSet, error) {
	return prepareDeepSeekTools(tools)
}

func (DeepSeekAdapter) BuildChatCompletionsRequest(cfg Config, messages []message.Message, tools PreparedToolSet, stream bool) (ChatCompletionsRequest, error) {
	origin := messageOriginForConfig(cfg, "openai-compatible")
	wire := make([]openAIMessage, 0, len(messages))
	for _, msg := range messages {
		if results := messageToolResults(msg); len(results) != 0 {
			hasUnpairedResult := false
			for _, result := range results {
				if strings.TrimSpace(result.ToolUseID) == "" {
					hasUnpairedResult = true
					continue
				}
				wire = append(wire, openAIMessage{Role: "tool", Content: result.Content, ToolCallID: result.ToolUseID})
			}
			if !hasUnpairedResult && (msg.Content == "" ||
				strings.HasPrefix(msg.Content, "TOOL_RESULT:\n") || strings.HasPrefix(msg.Content, "TOOL_RESULTS:\n")) {
				continue
			}
		}

		content, err := openAIContent(msg)
		if err != nil {
			return ChatCompletionsRequest{}, fmt.Errorf("构造 DeepSeek 请求消息失败: %w", err)
		}
		item := openAIMessage{Role: string(msg.Role), Content: content}
		if msg.Role == message.RoleAssistant {
			sameOrigin := msg.GeneratedBy != nil && *msg.GeneratedBy == *origin
			if err := applyDeepSeekAssistantHistory(&item, msg, sameOrigin, len(tools) > 0); err != nil {
				return ChatCompletionsRequest{}, err
			}
		}
		wire = append(wire, item)
	}
	return buildChatCompletionsRequest(cfg.Model, wire, tools, stream), nil
}

func applyDeepSeekAssistantHistory(item *openAIMessage, msg message.Message, sameOrigin, hasTools bool) error {
	calls := messageToolCalls(msg)
	var reasoning, text strings.Builder
	for _, part := range msg.AssistantParts {
		if sameOrigin && hasTools && part.Reasoning != nil && !part.Reasoning.Redacted {
			reasoning.WriteString(part.Reasoning.Text)
		}
		if len(calls) != 0 && part.Text != nil {
			text.WriteString(part.Text.Text)
		}
	}
	item.ReasoningContent = reasoning.String()
	if len(calls) == 0 {
		return nil
	}

	item.Content = nil
	if text.Len() != 0 {
		item.Content = text.String()
	}
	for _, call := range calls {
		arguments := string(call.Input)
		if sameOrigin && call.ProviderArguments != "" {
			arguments = call.ProviderArguments
		}
		if !json.Valid([]byte(arguments)) {
			return fmt.Errorf("DeepSeek 工具 %q 的历史参数不是有效 JSON", call.Name)
		}
		item.ToolCalls = append(item.ToolCalls, openAIToolCall{
			ID: call.ID, Type: "function", Function: openAIToolCallFunction{Name: call.Name, Arguments: arguments},
		})
	}
	return nil
}

var _ ModelAdapter = DeepSeekAdapter{}
