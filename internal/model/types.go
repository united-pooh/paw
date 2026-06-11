package model

import "gocode/internal/message"

// ChatCompletionsRequest 是通用的 Chat Completions 请求结构。
// 命名和字段不绑定具体供应商，便于后续替换 provider。
type ChatCompletionsRequest struct {
	Model    string            `json:"model"`
	Messages []message.Message `json:"messages"`
	Stream   bool              `json:"stream,omitempty"`
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
