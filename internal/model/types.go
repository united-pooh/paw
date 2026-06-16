package model

import (
	"encoding/json"

	"gocode/internal/message"
)

// ToolDefinition 描述一个可被模型调用的工具，格式兼容 Anthropic 和 OpenAI。
type ToolDefinition struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"` // JSON Schema object
}

// ChatCompletionsRequest 是通用的 Chat Completions 请求结构。
// 命名和字段不绑定具体供应商，便于后续替换 provider。
type ChatCompletionsRequest struct {
	Model         string            `json:"model"`
	Messages      []message.Message `json:"messages"`
	Stream        bool              `json:"stream,omitempty"`
	StreamOptions *StreamOptions    `json:"stream_options,omitempty"`
	Tools         []openAITool      `json:"tools,omitempty"`
}

// openAITool 是 OpenAI 兼容接口的工具定义格式。
type openAITool struct {
	Type     string          `json:"type"` // "function"
	Function ToolDefinition  `json:"function"`
}

type StreamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

// ChatCompletionsResponse 是通用的最小响应结构。
// 这里只提取第一阶段必需的字段：choices[].message.content。
type ChatCompletionsResponse struct {
	Choices []struct {
		Message message.Message `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error,omitempty"`
}

type Usage struct {
	InputTokens              int          `json:"input_tokens"`
	OutputTokens             int          `json:"output_tokens"`
	CacheCreationInputTokens int          `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int          `json:"cache_read_input_tokens"`
	PromptTokens             int          `json:"prompt_tokens"`
	CompletionTokens         int          `json:"completion_tokens"`
	TotalTokens              int          `json:"total_tokens"`
	PromptCacheHitTokens     int          `json:"prompt_cache_hit_tokens"`
	PromptCacheMissTokens    int          `json:"prompt_cache_miss_tokens"`
	PromptTokensDetails      TokenDetails `json:"prompt_tokens_details"`
	InputTokensDetails       TokenDetails `json:"input_tokens_details"`
}

type TokenDetails struct {
	CachedTokens             int `json:"cached_tokens"`
	CacheReadTokens          int `json:"cache_read_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
	CacheCreationTokens      int `json:"cache_creation_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
}

func (u Usage) CacheHitTokens() int {
	if u.PromptCacheHitTokens != 0 {
		return u.PromptCacheHitTokens
	}
	if u.PromptTokensDetails.CachedTokens != 0 {
		return u.PromptTokensDetails.CachedTokens
	}
	if u.InputTokensDetails.CachedTokens != 0 {
		return u.InputTokensDetails.CachedTokens
	}
	if u.PromptTokensDetails.CacheReadTokens != 0 {
		return u.PromptTokensDetails.CacheReadTokens
	}
	if u.InputTokensDetails.CacheReadTokens != 0 {
		return u.InputTokensDetails.CacheReadTokens
	}
	if u.PromptTokensDetails.CacheReadInputTokens != 0 {
		return u.PromptTokensDetails.CacheReadInputTokens
	}
	if u.InputTokensDetails.CacheReadInputTokens != 0 {
		return u.InputTokensDetails.CacheReadInputTokens
	}
	return u.CacheReadInputTokens
}

func (u Usage) CacheCreationTokens() int {
	if u.CacheCreationInputTokens != 0 {
		return u.CacheCreationInputTokens
	}
	if u.PromptTokensDetails.CacheCreationTokens != 0 {
		return u.PromptTokensDetails.CacheCreationTokens
	}
	if u.InputTokensDetails.CacheCreationTokens != 0 {
		return u.InputTokensDetails.CacheCreationTokens
	}
	if u.PromptTokensDetails.CacheCreationInputTokens != 0 {
		return u.PromptTokensDetails.CacheCreationInputTokens
	}
	if u.InputTokensDetails.CacheCreationInputTokens != 0 {
		return u.InputTokensDetails.CacheCreationInputTokens
	}
	return u.PromptCacheMissTokens
}

func (u Usage) PromptTokenCount() int {
	if u.PromptTokens != 0 {
		return u.PromptTokens
	}
	return u.InputTokens
}

func (u Usage) CompletionTokenCount() int {
	if u.CompletionTokens != 0 {
		return u.CompletionTokens
	}
	return u.OutputTokens
}

func (u Usage) ContextTokenCount() int {
	if u.TotalTokens != 0 {
		return u.TotalTokens
	}
	if u.PromptTokens != 0 || u.CompletionTokens != 0 {
		return u.PromptTokens + u.CompletionTokens
	}
	return u.InputTokens + u.CacheCreationInputTokens + u.CacheReadInputTokens + u.OutputTokens
}
