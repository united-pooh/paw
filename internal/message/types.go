package message

import "encoding/json"

type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

type Message struct {
	Role        Role         `json:"role"`
	Content     string       `json:"content,omitempty"`
	ToolUse     *ToolCall    `json:"tool_use,omitempty"`
	ToolUses    []ToolCall   `json:"tool_uses,omitempty"`
	ToolResult  *ToolResult  `json:"tool_result,omitempty"`
	ToolResults []ToolResult `json:"tool_results,omitempty"`
}

type ToolCall struct {
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

type ToolResult struct {
	ToolUseID string `json:"tool_use_id"`
	Content   string `json:"content"`
	IsError   bool   `json:"is_error"`
}
