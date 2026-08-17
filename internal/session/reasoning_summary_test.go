package session

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"paw/internal/message"
)

func TestReasoningSummaryPersistsResumesAndSearchesWithoutProviderData(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	started := time.Unix(100, 0).UTC()
	finished := started.Add(2 * time.Second)
	providerData := json.RawMessage(`{"transport":"openai-responses","version":1,"output_items":[{"type":"reasoning","id":"rs_1","encrypted_content":"cipher-only"}]}`)
	assistant := message.Message{
		Role:    message.RoleAssistant,
		Content: "compatibility answer",
		AssistantParts: []message.AssistantPart{
			{
				Type:   message.AssistantPartReasoning,
				Status: message.AssistantPartCompleted,
				Reasoning: &message.ReasoningPart{
					Text:       "repository summary phrase",
					StartedAt:  &started,
					FinishedAt: &finished,
				},
			},
			{
				Type:   message.AssistantPartText,
				Status: message.AssistantPartCompleted,
				Text:   &message.AssistantTextPart{Text: "visible answer"},
			},
		},
		ProviderData: providerData,
	}
	createTestSession(t, store, "reasoning-summary", []message.Message{
		{Role: message.RoleUser, Content: "inspect repository"},
		assistant,
	})

	history, err := store.LoadResolvedHistory(ctx, "reasoning-summary")
	if err != nil {
		t.Fatalf("LoadResolvedHistory() error = %v", err)
	}
	if len(history) != 2 || len(history[1].AssistantParts) != 2 {
		t.Fatalf("history = %#v, want assistant parts restored", history)
	}
	if got := history[1].AssistantParts[0].Reasoning.Text; got != "repository summary phrase" {
		t.Fatalf("restored reasoning = %q", got)
	}
	if got := string(history[1].ProviderData); got != string(providerData) {
		t.Fatalf("restored provider data = %s, want %s", got, providerData)
	}

	hits, searched, err := store.SearchTranscript(ctx, "reasoning-summary", "repository summary phrase", 20)
	if err != nil {
		t.Fatalf("SearchTranscript(summary) error = %v", err)
	}
	if searched != 2 || len(hits) != 1 || !strings.Contains(hits[0].Content, "repository summary phrase") {
		t.Fatalf("summary search hits=%#v searched=%d", hits, searched)
	}
	secretHits, _, err := store.SearchTranscript(ctx, "reasoning-summary", "cipher-only", 20)
	if err != nil {
		t.Fatalf("SearchTranscript(provider data) error = %v", err)
	}
	if len(secretHits) != 0 {
		t.Fatalf("provider data became searchable: %#v", secretHits)
	}
}
