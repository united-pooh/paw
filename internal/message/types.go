package message

import "encoding/json"

type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

type Message struct {
	Role    Role   `json:"role"`
	Content string `json:"content,omitempty"`
	// Parts preserves the ordered text/image representation of a rich user
	// message. Content remains the compatibility/debug representation and is
	// intentionally kept for old transcripts and text-only providers.
	Parts          []ContentPart   `json:"parts,omitempty"`
	AssistantParts []AssistantPart `json:"assistant_parts,omitempty"`
	GeneratedBy    *MessageOrigin  `json:"generated_by,omitempty"`
	ToolUse        *ToolCall       `json:"tool_use,omitempty"`
	ToolUses       []ToolCall      `json:"tool_uses,omitempty"`
	ToolResult     *ToolResult     `json:"tool_result,omitempty"`
	ToolResults    []ToolResult    `json:"tool_results,omitempty"`
	ProviderData   json.RawMessage `json:"provider_data,omitempty"`
}

// ContentPartType identifies one ordered part in a multimodal message.
type ContentPartType string

const (
	ContentPartText  ContentPartType = "text"
	ContentPartImage ContentPartType = "image"
)

// ContentPart is an ordered text or image fragment. Image bytes are transient
// and omitted from JSON; persisted messages contain only Image.Attachment.
type ContentPart struct {
	Type  ContentPartType `json:"type"`
	Text  string          `json:"text,omitempty"`
	Image *ImagePart      `json:"image,omitempty"`
}

// ImagePart describes an image attachment. Data is populated only while a
// message is being submitted or materialized for a provider request.
type ImagePart struct {
	MIMEType   string `json:"mime_type"`
	Attachment string `json:"attachment,omitempty"`
	Data       []byte `json:"-"`
}

type ToolCall struct {
	ID         string          `json:"id"`
	Name       string          `json:"name"`
	Input      json.RawMessage `json:"input"`
	InputError string          `json:"input_error,omitempty"`
}

type ToolResult struct {
	ToolUseID string `json:"tool_use_id"`
	Content   string `json:"content"`
	IsError   bool   `json:"is_error"`
}
