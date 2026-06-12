package model

import "gocode/internal/message"

// ChatCompletionsRequest 是通用的 Chat Completions 请求结构。
// 命名和字段不绑定具体供应商，便于后续替换 provider。
type ChatCompletionsRequest struct {
	Model         string            `json:"model"`
	Messages      []message.Message `json:"messages"`
	Stream        bool              `json:"stream,omitempty"`
	StreamOptions *StreamOptions    `json:"stream_options,omitempty"`
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
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
	PromptTokens             int `json:"prompt_tokens"`
	CompletionTokens         int `json:"completion_tokens"`
	TotalTokens              int `json:"total_tokens"`
	PromptCacheHitTokens     int `json:"prompt_cache_hit_tokens"`
	PromptCacheMissTokens    int `json:"prompt_cache_miss_tokens"`
}

func (u Usage) CacheHitTokens() int {
	if u.PromptCacheHitTokens != 0 {
		return u.PromptCacheHitTokens
	}
	return u.CacheReadInputTokens
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
