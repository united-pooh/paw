package model

import (
	"encoding/json"
	"paw/internal/message"
	"testing"
)

func TestOpenAIContentPreservesTextOnlyStringAndEncodesImages(t *testing.T) {
	text, err := openAIContent(message.Message{Role: message.RoleUser, Content: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := text.(string); !ok {
		t.Fatalf("text-only content type = %T, want string", text)
	}
	textParts, err := openAIContent(message.Message{
		Role:    message.RoleUser,
		Content: "hello",
		Parts:   []message.ContentPart{{Type: message.ContentPartText, Text: "hello"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := textParts.(string); !ok || got != "hello" {
		t.Fatalf("text-only parts content = %#v, want string hello", textParts)
	}

	content, err := openAIContent(message.Message{
		Role:    message.RoleUser,
		Content: "look [Image 1]",
		Parts: []message.ContentPart{
			{Type: message.ContentPartText, Text: "look "},
			{Type: message.ContentPartImage, Image: &message.ImagePart{MIMEType: "image/png", Data: []byte("png")}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(content)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(encoded); got != `[{"type":"text","text":"look "},{"type":"image_url","image_url":{"url":"data:image/png;base64,cG5n"}}]` {
		t.Fatalf("OpenAI rich content = %s", got)
	}
}

func TestAnthropicContentEncodesBase64ImageSource(t *testing.T) {
	content, err := anthropicContent(message.Message{
		Role: message.RoleUser,
		Parts: []message.ContentPart{
			{Type: message.ContentPartText, Text: "inspect"},
			{Type: message.ContentPartImage, Image: &message.ImagePart{MIMEType: "image/jpeg", Data: []byte("jpg")}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(content)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(encoded); got != `[{"type":"text","text":"inspect"},{"type":"image","source":{"type":"base64","media_type":"image/jpeg","data":"anBn"}}]` {
		t.Fatalf("Anthropic rich content = %s", got)
	}
}
