package model

import (
	"context"
	"encoding/json"
	"fmt"
	"codex-agent-go/internal/message"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestStreamMessageUsesAnthropicStreamForDeepSeekEarlyUsageAndThinking(t *testing.T) {
	requestChecked := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() { _ = r.Body.Close() }()

		if r.URL.Path != "/anthropic/v1/messages" {
			t.Fatalf("path = %q, want /anthropic/v1/messages", r.URL.Path)
		}
		if got := r.Header.Get("X-Api-Key"); got != "sk-test" {
			t.Fatalf("X-Api-Key = %q, want sk-test", got)
		}
		if got := r.Header.Get("Anthropic-Version"); got != anthropicVersion {
			t.Fatalf("Anthropic-Version = %q, want %s", got, anthropicVersion)
		}

		var req anthropicMessagesRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		if !req.Stream || req.MaxTokens == 0 {
			t.Fatalf("request = %#v, want stream with max tokens", req)
		}
		if len(req.System) == 0 || len(req.Messages) != 1 || req.Messages[0].Role != "user" {
			t.Fatalf("request = %#v, want system plus one user message", req)
		}
		requestChecked = true

		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "event: message_start\n")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":100,\"cache_creation_input_tokens\":4,\"cache_read_input_tokens\":40,\"output_tokens\":0}}}\n\n")
		_, _ = fmt.Fprint(w, "event: content_block_delta\n")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"thinking_delta\",\"thinking\":\"plan\"}}\n\n")
		_, _ = fmt.Fprint(w, "event: content_block_delta\n")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"hi\"}}\n\n")
		_, _ = fmt.Fprint(w, "event: message_delta\n")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"message_delta\",\"usage\":{\"input_tokens\":100,\"cache_creation_input_tokens\":4,\"cache_read_input_tokens\":40,\"output_tokens\":2}}\n\n")
		_, _ = fmt.Fprint(w, "event: message_stop\n")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"message_stop\"}\n\n")
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

	events, err := client.StreamMessage(context.Background(), []message.Message{
		{Role: message.RoleSystem, Content: "system prompt"},
		{Role: message.RoleUser, Content: "hello"},
	}, nil)
	if err != nil {
		t.Fatalf("StreamMessage() error = %v", err)
	}

	var gotDelta string
	var gotThinking string
	var firstUsage *Usage
	var lastUsage *Usage
	for ev := range events {
		if ev.Err != nil {
			t.Fatalf("stream error = %v", ev.Err)
		}
		if ev.Usage != nil {
			usage := *ev.Usage
			if firstUsage == nil {
				firstUsage = &usage
			}
			lastUsage = &usage
		}
		gotDelta += ev.Delta
		gotThinking += ev.Thinking
	}
	if !requestChecked {
		t.Fatalf("server did not check request")
	}
	if gotThinking != "plan" {
		t.Fatalf("gotThinking = %q, want plan", gotThinking)
	}
	if gotDelta != "hi" {
		t.Fatalf("gotDelta = %q, want hi", gotDelta)
	}
	if firstUsage == nil || firstUsage.PromptTokenCount() != 100 || firstUsage.ContextTokenCount() != 144 {
		t.Fatalf("firstUsage = %#v, want early input/cache usage", firstUsage)
	}
	if lastUsage == nil || lastUsage.CompletionTokenCount() != 2 || lastUsage.ContextTokenCount() != 146 {
		t.Fatalf("lastUsage = %#v, want final output usage", lastUsage)
	}
}

func TestStreamMessageEmitsAnthropicToolCalls(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() { _ = r.Body.Close() }()
		if r.URL.Path != "/anthropic/v1/messages" {
			t.Fatalf("path = %q, want /anthropic/v1/messages", r.URL.Path)
		}

		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "event: content_block_start\n")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"content_block_start\",\"content_block\":{\"type\":\"tool_use\",\"id\":\"call_subagent\",\"name\":\"Subagent\"}}\n\n")
		_, _ = fmt.Fprint(w, "event: content_block_delta\n")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"prompt\\\":\\\"work\\\"}\"}}\n\n")
		_, _ = fmt.Fprint(w, "event: content_block_stop\n")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"content_block_stop\"}\n\n")
		_, _ = fmt.Fprint(w, "event: message_stop\n")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"message_stop\"}\n\n")
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

	events, err := client.StreamMessage(context.Background(), []message.Message{{Role: message.RoleUser, Content: "launch"}}, nil)
	if err != nil {
		t.Fatalf("StreamMessage() error = %v", err)
	}

	var calls []message.ToolCall
	var delta strings.Builder
	for ev := range events {
		if ev.Err != nil {
			t.Fatalf("stream error = %v", ev.Err)
		}
		delta.WriteString(ev.Delta)
		calls = append(calls, ev.ToolCalls...)
	}
	if delta.String() != "" {
		t.Fatalf("delta = %q, want no textual tool JSON", delta.String())
	}
	if len(calls) != 1 || calls[0].ID != "call_subagent" || calls[0].Name != "Subagent" {
		t.Fatalf("tool calls = %#v, want Subagent call", calls)
	}
	if string(calls[0].Input) != `{"prompt":"work"}` {
		t.Fatalf("tool call input = %s", calls[0].Input)
	}
}

func TestStreamMessageFallsBackToOpenAIAndParsesDeepSeekUsage(t *testing.T) {
	anthropicChecked := false
	openAIChecked := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() { _ = r.Body.Close() }()
		if r.URL.Path == "/anthropic/v1/messages" {
			anthropicChecked = true
			http.Error(w, "anthropic unavailable", http.StatusNotFound)
			return
		}
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("path = %q, want /chat/completions", r.URL.Path)
		}

		var req ChatCompletionsRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		if req.StreamOptions == nil || !req.StreamOptions.IncludeUsage {
			t.Fatalf("StreamOptions = %#v, want include_usage=true", req.StreamOptions)
		}
		openAIChecked = true

		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n")
		_, _ = fmt.Fprint(w, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":100,\"completion_tokens\":2,\"total_tokens\":102,\"prompt_cache_hit_tokens\":40,\"prompt_cache_miss_tokens\":60,\"prompt_tokens_details\":{\"cached_tokens\":30}}}\n\n")
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

	events, err := client.StreamMessage(context.Background(), []message.Message{{Role: message.RoleUser, Content: "hello"}}, nil)
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
	if !anthropicChecked {
		t.Fatalf("server did not receive anthropic attempt")
	}
	if !openAIChecked {
		t.Fatalf("server did not check OpenAI fallback request")
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

func TestStreamMessageEmitsOpenAIToolCalls(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() { _ = r.Body.Close() }()
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("path = %q, want /chat/completions", r.URL.Path)
		}

		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_read","function":{"name":"Read","arguments":"{\"file_path\":\"go.mod\"}"}},{"index":1,"id":"call_ls","function":{"name":"LS","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`+"\n\n")
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	client := NewClient(Config{
		Provider:      ProviderCustom,
		APIBaseURL:    server.URL,
		APIPath:       "/chat/completions",
		APIKey:        "sk-test",
		APIKeyEnvName: CustomAPIKeyEnvName,
		Model:         CustomDefaultModel,
		Timeout:       time.Minute,
	})

	events, err := client.StreamMessage(context.Background(), []message.Message{{Role: message.RoleUser, Content: "inspect"}}, nil)
	if err != nil {
		t.Fatalf("StreamMessage() error = %v", err)
	}

	var calls []message.ToolCall
	var delta strings.Builder
	for ev := range events {
		if ev.Err != nil {
			t.Fatalf("stream error = %v", ev.Err)
		}
		delta.WriteString(ev.Delta)
		calls = append(calls, ev.ToolCalls...)
	}
	if delta.String() != "" {
		t.Fatalf("delta = %q, want no textual tool JSON", delta.String())
	}
	if len(calls) != 2 {
		t.Fatalf("tool calls = %#v, want two calls", calls)
	}
	if calls[0].ID != "call_read" || calls[0].Name != "Read" || string(calls[0].Input) != `{"file_path":"go.mod"}` {
		t.Fatalf("first call = %#v", calls[0])
	}
	if calls[1].ID != "call_ls" || calls[1].Name != "LS" || string(calls[1].Input) != `{}` {
		t.Fatalf("second call = %#v", calls[1])
	}
}

func TestUsageParsesOpenAINestedCachedTokens(t *testing.T) {
	usage := Usage{
		PromptTokens: 100,
		PromptTokensDetails: TokenDetails{
			CachedTokens: 64,
		},
	}
	if got := usage.CacheHitTokens(); got != 64 {
		t.Fatalf("CacheHitTokens() = %d, want nested cached_tokens", got)
	}

	usage = Usage{
		InputTokens: 100,
		InputTokensDetails: TokenDetails{
			CachedTokens: 72,
		},
	}
	if got := usage.CacheHitTokens(); got != 72 {
		t.Fatalf("CacheHitTokens() = %d, want input_tokens_details cached_tokens", got)
	}
}

func TestStreamMessageReadsSSELineLongerThanLegacyScannerLimit(t *testing.T) {
	longDelta := strings.Repeat("x", 1024*1024+1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":\"%s\"}}]}\n\n", longDelta)
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	client := NewClient(Config{
		Provider:      ProviderCustom,
		APIBaseURL:    server.URL,
		APIPath:       "/chat/completions",
		APIKey:        "sk-test",
		APIKeyEnvName: CustomAPIKeyEnvName,
		Model:         CustomDefaultModel,
		Timeout:       time.Minute,
	})

	events, err := client.StreamMessage(context.Background(), []message.Message{{Role: message.RoleUser, Content: "hello"}}, nil)
	if err != nil {
		t.Fatalf("StreamMessage() error = %v", err)
	}

	var gotDelta strings.Builder
	for ev := range events {
		if ev.Err != nil {
			t.Fatalf("stream error = %v", ev.Err)
		}
		gotDelta.WriteString(ev.Delta)
	}
	if gotDelta.String() != longDelta {
		t.Fatalf("gotDelta length = %d, want %d", gotDelta.Len(), len(longDelta))
	}
}

func TestStreamMessageDoesNotTimeoutWhileReadingBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"slow\"}}]}\n\n")
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		time.Sleep(50 * time.Millisecond)
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	client := NewClient(Config{
		Provider:      ProviderCustom,
		APIBaseURL:    server.URL,
		APIPath:       "/chat/completions",
		APIKey:        "sk-test",
		APIKeyEnvName: CustomAPIKeyEnvName,
		Model:         CustomDefaultModel,
		Timeout:       10 * time.Millisecond,
	})

	events, err := client.StreamMessage(context.Background(), []message.Message{{Role: message.RoleUser, Content: "hello"}}, nil)
	if err != nil {
		t.Fatalf("StreamMessage() error = %v", err)
	}

	var gotDelta string
	for ev := range events {
		if ev.Err != nil {
			t.Fatalf("stream error = %v", ev.Err)
		}
		gotDelta += ev.Delta
	}
	if gotDelta != "slow" {
		t.Fatalf("gotDelta = %q, want slow", gotDelta)
	}
}
