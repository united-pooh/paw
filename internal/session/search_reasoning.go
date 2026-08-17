package session

import (
	"paw/internal/message"
	"strings"
)

// searchableMessageText projects only readable message content and reasoning
// summaries. ProviderData is deliberately excluded because it may contain
// encrypted reasoning state that is meant for protocol replay, not search.
func searchableMessageText(msg message.Message) string {
	if len(msg.AssistantParts) == 0 {
		return msg.Content
	}
	parts := make([]string, 0, len(msg.AssistantParts))
	for _, part := range msg.AssistantParts {
		switch part.Type {
		case message.AssistantPartText:
			if part.Text != nil && strings.TrimSpace(part.Text.Text) != "" {
				parts = append(parts, part.Text.Text)
			}
		case message.AssistantPartReasoning:
			if part.Reasoning != nil && !part.Reasoning.Redacted && strings.TrimSpace(part.Reasoning.Text) != "" {
				parts = append(parts, part.Reasoning.Text)
			}
		}
	}
	return strings.Join(parts, " ")
}
