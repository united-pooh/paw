package model

import (
	"context"
	"encoding/json"
	"fmt"
	"gocode/internal/message"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestStreamMessageRequestsAndParsesDeepSeekUsage(t *testing.T) {
	requestChecked := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() { _ = r.Body.Close() }()

		var req ChatCompletionsRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		if req.StreamOptions == nil || !req.StreamOptions.IncludeUsage {
			t.Fatalf("StreamOptions = %#v, want include_usage=true", req.StreamOptions)
		}
		requestChecked = true

		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n")
		_, _ = fmt.Fprint(w, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":100,\"completion_tokens\":2,\"total_tokens\":102,\"prompt_cache_hit_tokens\":40,\"prompt_cache_miss_tokens\":60}}\n\n")
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	client := NewClient(Config{
		Provider:      ProviderDeepSeek,
		APIBaseURL:    server.URL,
		APIPath:       "/chat/completions",
		APIKey:        "sk-test",
		APIKeyEnvName: DeepSeekAPIKeyEnvName,
		Model:         DeepSeekDefaultModel,
		Timeout:       time.Minute,
	})

	events, err := client.StreamMessage(context.Background(), []message.Message{{Role: message.RoleUser, Content: "hello"}})
	if err != nil {
		t.Fatalf("StreamMessage() error = %v", err)
	}

	var gotDelta string
	var gotUsage *Usage
	for ev := range events {
		if ev.Err != nil {
			t.Fatalf("stream error = %v", ev.Err)
		}
		if ev.Delta != "" {
			gotDelta += ev.Delta
		}
		if ev.Usage != nil {
			usage := *ev.Usage
			gotUsage = &usage
		}
	}
	if !requestChecked {
		t.Fatalf("server did not check request")
	}
	if gotDelta != "hi" {
		t.Fatalf("gotDelta = %q, want hi", gotDelta)
	}
	if gotUsage == nil {
		t.Fatalf("gotUsage = nil")
	}
	if gotUsage.CacheHitTokens() != 40 {
		t.Fatalf("CacheHitTokens() = %d, want 40", gotUsage.CacheHitTokens())
	}
	if gotUsage.PromptTokenCount() != 100 {
		t.Fatalf("PromptTokenCount() = %d, want 100", gotUsage.PromptTokenCount())
	}
}
