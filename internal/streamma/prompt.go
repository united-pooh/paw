package streamma

import (
	"fmt"
	"gocode/internal/message"
	"strings"
)

const defaultSystemPrompt = "You are a StreamMA agent. Produce natural-language steps and close each step with END_STEP on its own line."

func BuildPrompt(transcript *Transcript) []message.Message {
	segments := BuildPromptSegments(transcript)
	if len(segments) == 0 {
		return nil
	}
	messages := make([]message.Message, 0, len(segments))
	for _, segment := range segments {
		messages = append(messages, message.Message{
			Role:    segment.Role,
			Content: segment.Content,
		})
	}
	return messages
}

func BuildPromptSegments(transcript *Transcript) []PromptSegment {
	if transcript == nil {
		return nil
	}
	system := strings.TrimSpace(transcript.System)
	if system == "" {
		system = defaultSystemPrompt
	}

	segments := []PromptSegment{
		{Key: "system", Role: message.RoleSystem, Content: system, CacheStable: true},
		{Key: "problem", Role: message.RoleUser, Content: "Problem:\n" + transcript.Problem, CacheStable: true},
	}
	for i, entry := range transcript.entries {
		switch entry.Kind {
		case TranscriptInbound:
			segments = append(segments, PromptSegment{
				Key:         fmt.Sprintf("transcript:%06d:inbound:%s", i+1, entry.From),
				Role:        message.RoleUser,
				Content:     fmt.Sprintf("Inbound step from %s:\n%s", entry.From, entry.Text),
				CacheStable: true,
			})
		case TranscriptOwn:
			segments = append(segments, PromptSegment{
				Key:         fmt.Sprintf("transcript:%06d:own", i+1),
				Role:        message.RoleAssistant,
				Content:     "Own step:\n" + entry.Text,
				CacheStable: true,
			})
		}
	}
	return segments
}
