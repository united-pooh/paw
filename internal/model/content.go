package model

import (
	"encoding/base64"
	"fmt"
	"paw/internal/message"
	"strings"
)

// openAIMessage keeps text-only content as a JSON string while encoding rich
// messages as the provider's ordered content-part array.
type openAIMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

type openAIContentPart struct {
	Type     string          `json:"type"`
	Text     string          `json:"text,omitempty"`
	ImageURL *openAIImageURL `json:"image_url,omitempty"`
}

type openAIImageURL struct {
	URL string `json:"url"`
}

type anthropicContentPart struct {
	Type   string                `json:"type"`
	Text   string                `json:"text,omitempty"`
	Source *anthropicImageSource `json:"source,omitempty"`
}

type anthropicImageSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
}

func buildOpenAIMessages(messages []message.Message) ([]openAIMessage, error) {
	result := make([]openAIMessage, 0, len(messages))
	for _, msg := range messages {
		content, err := openAIContent(msg)
		if err != nil {
			return nil, err
		}
		result = append(result, openAIMessage{Role: string(msg.Role), Content: content})
	}
	return result, nil
}

func openAIContent(msg message.Message) (any, error) {
	if !hasImagePart(msg.Parts) {
		return msg.Content, nil
	}
	parts := make([]openAIContentPart, 0, len(msg.Parts))
	for _, part := range msg.Parts {
		switch part.Type {
		case message.ContentPartText:
			parts = append(parts, openAIContentPart{Type: "text", Text: part.Text})
		case message.ContentPartImage:
			if part.Image == nil || len(part.Image.Data) == 0 {
				return nil, fmt.Errorf("OpenAI 图片消息缺少已加载的附件")
			}
			mimeType := strings.TrimSpace(part.Image.MIMEType)
			if mimeType == "" {
				return nil, fmt.Errorf("OpenAI 图片消息缺少 MIME 类型")
			}
			encoded := base64.StdEncoding.EncodeToString(part.Image.Data)
			parts = append(parts, openAIContentPart{
				Type:     "image_url",
				ImageURL: &openAIImageURL{URL: "data:" + mimeType + ";base64," + encoded},
			})
		default:
			return nil, fmt.Errorf("OpenAI 消息包含未知内容块类型: %q", part.Type)
		}
	}
	return parts, nil
}

func anthropicContent(msg message.Message) (any, error) {
	if !hasImagePart(msg.Parts) {
		return strings.TrimRight(msg.Content, "\n"), nil
	}
	parts := make([]anthropicContentPart, 0, len(msg.Parts))
	for _, part := range msg.Parts {
		switch part.Type {
		case message.ContentPartText:
			parts = append(parts, anthropicContentPart{Type: "text", Text: part.Text})
		case message.ContentPartImage:
			if part.Image == nil || len(part.Image.Data) == 0 {
				return nil, fmt.Errorf("Anthropic 图片消息缺少已加载的附件")
			}
			mimeType := strings.TrimSpace(part.Image.MIMEType)
			if mimeType == "" {
				return nil, fmt.Errorf("Anthropic 图片消息缺少 MIME 类型")
			}
			parts = append(parts, anthropicContentPart{
				Type: "image",
				Source: &anthropicImageSource{
					Type:      "base64",
					MediaType: mimeType,
					Data:      base64.StdEncoding.EncodeToString(part.Image.Data),
				},
			})
		default:
			return nil, fmt.Errorf("Anthropic 消息包含未知内容块类型: %q", part.Type)
		}
	}
	return parts, nil
}

func hasImagePart(parts []message.ContentPart) bool {
	for _, part := range parts {
		if part.Type == message.ContentPartImage && part.Image != nil {
			return true
		}
	}
	return false
}
