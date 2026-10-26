package loop

import (
	"strings"
	"testing"

	"paw/internal/capability/model"
	"paw/internal/message"
)

func TestContextEstimateCountsRichMessageTextOnce(t *testing.T) {
	text := strings.Repeat("look ", 100)
	msg := message.Message{Role: message.RoleUser, Content: text + "[Image 1]", Parts: []message.ContentPart{
		{Type: message.ContentPartText, Text: text},
		{Type: message.ContentPartImage, Image: &message.ImagePart{Attachment: "image.png"}},
	}}
	if got, want := estimateMessageTokens([]message.Message{msg}), 4+estimateTextTokens(text)+256; got != want {
		t.Fatalf("rich message estimate = %d, want %d without compatibility text", got, want)
	}
}

func TestContextEstimateSourceTransitions(t *testing.T) {
	runner := NewEngineWithInstructionRoot(nil, nil, nil, nil, "estimate-source", t.TempDir())
	history := []message.Message{{Role: message.RoleUser, Content: "restored history"}}
	runner.syncContextUsageFromHistory(history)
	if stats := runner.ContextStats(10000, ""); !stats.Estimated || !stats.TotalKnown || stats.UsedTokens == 0 {
		t.Fatalf("restored history must be marked estimated: %#v", stats)
	}
	runner.setCurrentUsage(model.Usage{PromptTokens: 100, CompletionTokens: 20})
	if stats := runner.ContextStats(10000, ""); stats.Estimated || stats.UsedTokens != 120 {
		t.Fatalf("provider report must replace estimate: %#v", stats)
	}
	runner.syncContextUsageFromHistory(history)
	runner.syncContextUsageFromHistory(nil)
	if stats := runner.ContextStats(10000, ""); stats.Estimated || stats.Known || stats.UsedTokens != 0 {
		t.Fatalf("empty history must clear estimated capacity: %#v", stats)
	}
	runner.syncContextUsageFromHistory(history)
	runner.usage.resetAll()
	if stats := runner.ContextStats(10000, ""); stats.Estimated || stats.Known {
		t.Fatalf("session reset must clear estimate source: %#v", stats)
	}
}

func TestContextEstimateTextFallbackMatchesProviderContent(t *testing.T) {
	msg := message.Message{Role: message.RoleUser, Content: "canonical text", Parts: []message.ContentPart{
		{Type: message.ContentPartText, Text: "compatibility text"},
	}}
	if got, want := estimateMessageTokens([]message.Message{msg}), 4+estimateTextTokens(msg.Content); got != want {
		t.Fatalf("text-only provider estimate = %d, want %d", got, want)
	}
}
