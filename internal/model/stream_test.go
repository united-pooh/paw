package model

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"paw/internal/message"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestStreamMessageUsesAnthropicStreamForDeepSeekEarlyUsageAndThinking(t *testing.T) {
	requestChecked := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() { _ = r.Body.Close() }()

		if r.URL.Path != "/v1/messages" {
			t.Fatalf("path = %q, want /v1/messages", r.URL.Path)
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
		_, _ = fmt.Fprint(w, "event: content_block_start\n")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"thinking\",\"thinking\":\"plan\"}}\n\n")
		_, _ = fmt.Fprint(w, "event: content_block_start\n")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"content_block_start\",\"index\":1,\"content_block\":{\"type\":\"text\",\"text\":\"hi\"}}\n\n")
		_, _ = fmt.Fprint(w, "event: message_delta\n")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"message_delta\",\"usage\":{\"input_tokens\":100,\"cache_creation_input_tokens\":4,\"cache_read_input_tokens\":40,\"output_tokens\":2}}\n\n")
		_, _ = fmt.Fprint(w, "event: content_block_stop\n")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"content_block_stop\",\"index\":0}\n\n")
		_, _ = fmt.Fprint(w, "event: content_block_stop\n")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"content_block_stop\",\"index\":1}\n\n")
		_, _ = fmt.Fprint(w, "event: message_stop\n")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"message_stop\"}\n\n")
	}))
	defer server.Close()

	client := NewClient(Config{
		Provider:      "anthropic-gateway",
		Transport:     "anthropic-compatible",
		APIBaseURL:    server.URL,
		APIPath:       "/v1/messages",
		APIKey:        "sk-test",
		APIKeyEnvName: "TEST_ANTHROPIC_API_KEY",
		Model:         "anthropic-model",
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
		if ev.AssistantPart != nil && ev.AssistantPart.Type == "reasoning" {
			gotThinking += ev.AssistantPart.Delta
		}
		if ev.AssistantPart != nil && ev.AssistantPart.Lifecycle == AssistantPartLifecycleDelta && ev.AssistantPart.Type == "text" {
			gotDelta += ev.AssistantPart.Delta
		}
		gotDelta += ev.Delta
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
		if r.URL.Path != "/v1/messages" {
			t.Fatalf("path = %q, want /v1/messages", r.URL.Path)
		}

		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "event: content_block_start\n")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"content_block_start\",\"content_block\":{\"type\":\"tool_use\",\"id\":\"call_task\",\"name\":\"Task\"}}\n\n")
		_, _ = fmt.Fprint(w, "event: content_block_delta\n")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"prompt\\\":\\\"work\\\"}\"}}\n\n")
		_, _ = fmt.Fprint(w, "event: content_block_stop\n")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"content_block_stop\"}\n\n")
		_, _ = fmt.Fprint(w, "event: message_stop\n")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"message_stop\"}\n\n")
	}))
	defer server.Close()

	client := NewClient(Config{
		Provider:      "anthropic-gateway",
		Transport:     "anthropic-compatible",
		APIBaseURL:    server.URL,
		APIPath:       "/v1/messages",
		APIKey:        "sk-test",
		APIKeyEnvName: "TEST_ANTHROPIC_API_KEY",
		Model:         "anthropic-model",
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
	if len(calls) != 1 || calls[0].ID != "call_task" || calls[0].Name != "Task" {
		t.Fatalf("tool calls = %#v, want Task call", calls)
	}
	if string(calls[0].Input) != `{"prompt":"work"}` {
		t.Fatalf("tool call input = %s", calls[0].Input)
	}
}

func TestStreamMessageEmitsAnthropicToolCallInputFromStartBlock(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() { _ = r.Body.Close() }()
		if r.URL.Path != "/v1/messages" {
			t.Fatalf("path = %q, want /v1/messages", r.URL.Path)
		}

		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "event: content_block_start\n")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"content_block_start\",\"content_block\":{\"type\":\"tool_use\",\"id\":\"call_read\",\"name\":\"Read\",\"input\":{\"file_path\":\"go.mod\"}}}\n\n")
		_, _ = fmt.Fprint(w, "event: content_block_stop\n")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"content_block_stop\"}\n\n")
		_, _ = fmt.Fprint(w, "event: message_stop\n")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"message_stop\"}\n\n")
	}))
	defer server.Close()

	client := NewClient(Config{
		Provider:      "anthropic-gateway",
		Transport:     "anthropic-compatible",
		APIBaseURL:    server.URL,
		APIPath:       "/v1/messages",
		APIKey:        "sk-test",
		APIKeyEnvName: "TEST_ANTHROPIC_API_KEY",
		Model:         "anthropic-model",
		Timeout:       time.Minute,
	})

	events, err := client.StreamMessage(context.Background(), []message.Message{{Role: message.RoleUser, Content: "read"}}, nil)
	if err != nil {
		t.Fatalf("StreamMessage() error = %v", err)
	}

	var calls []message.ToolCall
	for ev := range events {
		if ev.Err != nil {
			t.Fatalf("stream error = %v", ev.Err)
		}
		calls = append(calls, ev.ToolCalls...)
	}
	if len(calls) != 1 || calls[0].ID != "call_read" || calls[0].Name != "Read" {
		t.Fatalf("tool calls = %#v, want Read call", calls)
	}
	if string(calls[0].Input) != `{"file_path":"go.mod"}` {
		t.Fatalf("tool call input = %s", calls[0].Input)
	}
}

func TestStreamMessageRejectsAnthropicToolCallInputFromStringStartBlock(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() { _ = r.Body.Close() }()
		if r.URL.Path != "/v1/messages" {
			t.Fatalf("path = %q, want /v1/messages", r.URL.Path)
		}

		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "event: content_block_start\n")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"content_block_start\",\"content_block\":{\"type\":\"tool_use\",\"id\":\"call_read\",\"name\":\"Read\",\"input\":\"{\\\"file_path\\\":\\\"go.mod\\\"}\"}}\n\n")
		_, _ = fmt.Fprint(w, "event: content_block_stop\n")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"content_block_stop\"}\n\n")
	}))
	defer server.Close()

	client := NewClient(Config{
		Provider:      "anthropic-gateway",
		Transport:     "anthropic-compatible",
		APIBaseURL:    server.URL,
		APIPath:       "/v1/messages",
		APIKey:        "sk-test",
		APIKeyEnvName: "TEST_ANTHROPIC_API_KEY",
		Model:         "anthropic-model",
		Timeout:       time.Minute,
	})

	events, err := client.StreamMessage(context.Background(), []message.Message{{Role: message.RoleUser, Content: "read"}}, nil)
	if err != nil {
		t.Fatalf("StreamMessage() error = %v", err)
	}

	var gotErr error
	for ev := range events {
		if len(ev.ToolCalls) != 0 {
			t.Fatalf("nested-string input emitted tool calls: %#v", ev.ToolCalls)
		}
		if ev.Err != nil {
			gotErr = ev.Err
		}
	}
	if gotErr == nil || !strings.Contains(gotErr.Error(), "invalid JSON object arguments") {
		t.Fatalf("error = %v, want invalid arguments error", gotErr)
	}
}

func TestStreamMessageAnthropicToolCallDeltaOverridesStartBlockInput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() { _ = r.Body.Close() }()
		if r.URL.Path != "/v1/messages" {
			t.Fatalf("path = %q, want /v1/messages", r.URL.Path)
		}

		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "event: content_block_start\n")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"content_block_start\",\"content_block\":{\"type\":\"tool_use\",\"id\":\"call_read\",\"name\":\"Read\",\"input\":{\"file_path\":\"stale.go\"}}}\n\n")
		_, _ = fmt.Fprint(w, "event: content_block_delta\n")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"file_path\\\":\"}}\n\n")
		_, _ = fmt.Fprint(w, "event: content_block_delta\n")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"\\\"fresh.go\\\"}\"}}\n\n")
		_, _ = fmt.Fprint(w, "event: content_block_stop\n")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"content_block_stop\"}\n\n")
		_, _ = fmt.Fprint(w, "event: message_stop\n")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"message_stop\"}\n\n")
	}))
	defer server.Close()

	client := NewClient(Config{
		Provider:      "anthropic-gateway",
		Transport:     "anthropic-compatible",
		APIBaseURL:    server.URL,
		APIPath:       "/v1/messages",
		APIKey:        "sk-test",
		APIKeyEnvName: "TEST_ANTHROPIC_API_KEY",
		Model:         "anthropic-model",
		Timeout:       time.Minute,
	})

	events, err := client.StreamMessage(context.Background(), []message.Message{{Role: message.RoleUser, Content: "read"}}, nil)
	if err != nil {
		t.Fatalf("StreamMessage() error = %v", err)
	}

	var calls []message.ToolCall
	for ev := range events {
		if ev.Err != nil {
			t.Fatalf("stream error = %v", ev.Err)
		}
		calls = append(calls, ev.ToolCalls...)
	}
	if len(calls) != 1 || string(calls[0].Input) != `{"file_path":"fresh.go"}` {
		t.Fatalf("tool calls = %#v, want delta input to override start-block input", calls)
	}
}

func TestStreamMessageFallsBackToOpenAIAndParsesDeepSeekUsage(t *testing.T) {
	anthropicChecked := false
	openAIChecked := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() { _ = r.Body.Close() }()
		if r.Header.Get("Anthropic-Version") != "" {
			anthropicChecked = true
			http.Error(w, "anthropic unavailable", http.StatusNotFound)
			return
		}
		if r.URL.Path != "/v1/messages" {
			t.Fatalf("path = %q, want /v1/messages", r.URL.Path)
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
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	client := NewClient(Config{
		Provider:      "anthropic-gateway",
		Transport:     "anthropic-compatible",
		APIBaseURL:    server.URL,
		APIPath:       "/v1/messages",
		APIKey:        "sk-test",
		APIKeyEnvName: "TEST_ANTHROPIC_API_KEY",
		Model:         "anthropic-model",
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
		Provider:      "openai-gateway",
		APIBaseURL:    server.URL,
		APIPath:       "/chat/completions",
		APIKey:        "sk-test",
		APIKeyEnvName: "TEST_OPENAI_API_KEY",
		Model:         "openai-model",
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

func TestStreamMessageSendsOpenAIToolsWithParametersSchema(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() { _ = r.Body.Close() }()
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("path = %q, want /chat/completions", r.URL.Path)
		}

		var raw map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		var tools []map[string]json.RawMessage
		if err := json.Unmarshal(raw["tools"], &tools); err != nil {
			t.Fatalf("decode tools: %v", err)
		}
		if len(tools) != 1 {
			t.Fatalf("tools = %#v, want one tool", tools)
		}
		var fn map[string]json.RawMessage
		if err := json.Unmarshal(tools[0]["function"], &fn); err != nil {
			t.Fatalf("decode function: %v", err)
		}
		if _, exists := fn["input_schema"]; exists {
			t.Fatalf("function contains input_schema, want OpenAI parameters: %s", tools[0]["function"])
		}
		if _, exists := fn["parameters"]; !exists {
			t.Fatalf("function = %s, want parameters", tools[0]["function"])
		}
		var params map[string]json.RawMessage
		if err := json.Unmarshal(fn["parameters"], &params); err != nil {
			t.Fatalf("decode parameters: %v", err)
		}
		if string(params["required"]) != `["file_path"]` {
			t.Fatalf("required = %s, want file_path", params["required"])
		}

		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\n")
	}))
	defer server.Close()

	client := NewClient(Config{
		Provider:      "openai-gateway",
		APIBaseURL:    server.URL,
		APIPath:       "/chat/completions",
		APIKey:        "sk-test",
		APIKeyEnvName: "TEST_OPENAI_API_KEY",
		Model:         "openai-model",
		Timeout:       time.Minute,
	})

	events, err := client.StreamMessage(context.Background(), []message.Message{{Role: message.RoleUser, Content: "inspect"}}, []ToolDefinition{{
		Name:        "Read",
		Description: "read a file",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"file_path":{"type":"string"}},"required":["file_path"]}`),
	}})
	if err != nil {
		t.Fatalf("StreamMessage() error = %v", err)
	}
	for ev := range events {
		if ev.Err != nil {
			t.Fatalf("stream error = %v", ev.Err)
		}
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
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	client := NewClient(Config{
		Provider:      "openai-gateway",
		APIBaseURL:    server.URL,
		APIPath:       "/chat/completions",
		APIKey:        "sk-test",
		APIKeyEnvName: "TEST_OPENAI_API_KEY",
		Model:         "openai-model",
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
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	client := NewClient(Config{
		Provider:      "openai-gateway",
		APIBaseURL:    server.URL,
		APIPath:       "/chat/completions",
		APIKey:        "sk-test",
		APIKeyEnvName: "TEST_OPENAI_API_KEY",
		Model:         "openai-model",
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

func TestStreamMessageRetriesTransientHTTPFailures(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if requests == 1 {
			http.Error(w, "provider unavailable", http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"recovered\"}}]}\n\n")
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	client := NewClient(Config{
		Provider:   "openai-gateway",
		APIBaseURL: server.URL,
		APIPath:    "/chat/completions",
		Model:      "openai-model",
		RetryCount: 1,
		Timeout:    time.Second,
	})
	events, err := client.StreamMessage(context.Background(), []message.Message{{Role: message.RoleUser, Content: "hello"}}, nil)
	if err != nil {
		t.Fatalf("StreamMessage() error = %v", err)
	}
	var got strings.Builder
	for event := range events {
		if event.Err != nil {
			t.Fatalf("stream event error = %v", event.Err)
		}
		got.WriteString(event.Delta)
	}
	if got.String() != "recovered" || requests != 2 {
		t.Fatalf("response=%q requests=%d, want recovered/2", got.String(), requests)
	}
}

func TestStreamMessageUsesNonStreamingResponseWhenExplicitlyDisabled(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request ChatCompletionsRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if request.Stream {
			t.Fatal("request.Stream = true, want false")
		}
		if request.StreamOptions != nil {
			t.Fatal("non-stream request unexpectedly included stream_options")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"non-stream"}}]}`)
	}))
	defer server.Close()

	client := NewClient(Config{
		Provider:   "openai-gateway",
		APIBaseURL: server.URL,
		APIPath:    "/chat/completions",
		Model:      "openai-model",
		Stream:     false,
		streamSet:  true,
		Timeout:    time.Second,
	})
	events, err := client.StreamMessage(context.Background(), []message.Message{{Role: message.RoleUser, Content: "hello"}}, nil)
	if err != nil {
		t.Fatalf("StreamMessage() error = %v", err)
	}
	var got string
	var done bool
	for event := range events {
		got += event.Delta
		done = done || event.Done
		if event.Err != nil {
			t.Fatalf("stream event error = %v", event.Err)
		}
	}
	if got != "non-stream" || !done {
		t.Fatalf("events = content %q done=%v, want non-stream/true", got, done)
	}
}

func TestStreamMessageMergesProfileAndModelExtraBodyIntoOpenAIStreamRequest(t *testing.T) {
	var captured map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() { _ = r.Body.Close() }()
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\n")
	}))
	defer server.Close()

	client := NewClient(Config{
		Provider:   "openai-gateway",
		Transport:  "openai-compatible",
		APIBaseURL: server.URL,
		APIPath:    "/chat/completions",
		Model:      "model-a",
		Models:     []string{"model-a", "model-b"},
		Stream:     true,
		ExtraBody: RequestBody{
			"metadata": RequestBody{
				"profile": "profile-value",
				"nested":  RequestBody{"profile": true},
			},
			"array":    []any{"profile"},
			"scalar":   "profile-value",
			"nullable": "profile-value",
		},
		ModelExtraBody: map[string]RequestBody{
			"model-a": {
				"metadata": RequestBody{
					"model":  "model-value",
					"nested": RequestBody{"model": true},
				},
				"array":       []any{"model", 2},
				"scalar":      float64(42),
				"nullable":    nil,
				"temperature": float64(0.2),
			},
		},
		Timeout: time.Second,
	})

	events, err := client.StreamMessage(context.Background(), []message.Message{{Role: message.RoleUser, Content: "hello"}}, nil)
	if err != nil {
		t.Fatalf("StreamMessage() error = %v", err)
	}
	for event := range events {
		if event.Err != nil {
			t.Fatalf("stream event error = %v", event.Err)
		}
	}

	if captured["model"] != "model-a" || captured["stream"] != true {
		t.Fatalf("protected fields = %#v, want model-a/true", captured)
	}
	if !reflect.DeepEqual(captured["metadata"], map[string]any{
		"profile": "profile-value",
		"model":   "model-value",
		"nested":  map[string]any{"profile": true, "model": true},
	}) {
		t.Fatalf("metadata = %#v, want recursively merged object", captured["metadata"])
	}
	if !reflect.DeepEqual(captured["array"], []any{"model", float64(2)}) {
		t.Fatalf("array = %#v, want model-level replacement", captured["array"])
	}
	if captured["scalar"] != float64(42) {
		t.Fatalf("scalar = %#v, want model-level replacement", captured["scalar"])
	}
	if value, ok := captured["nullable"]; !ok || value != nil {
		t.Fatalf("nullable = %#v (present=%v), want explicit null", value, ok)
	}
	if captured["temperature"] != float64(0.2) {
		t.Fatalf("temperature = %#v, want legal extension", captured["temperature"])
	}
	streamOptions, ok := captured["stream_options"].(map[string]any)
	if !ok || streamOptions["include_usage"] != true {
		t.Fatalf("stream_options = %#v, want include_usage=true", captured["stream_options"])
	}
}

func TestStreamMessageMergesExtraBodyIntoOpenAINonStreamingRequest(t *testing.T) {
	var captured map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() { _ = r.Body.Close() }()
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"choices":[{"message":{"content":"ok"}}]}`)
	}))
	defer server.Close()

	client := NewClient(Config{
		Provider:   "openai-gateway",
		Transport:  "openai-compatible",
		APIBaseURL: server.URL,
		APIPath:    "/chat/completions",
		Model:      "model-a",
		Models:     []string{"model-a"},
		Stream:     false,
		streamSet:  true,
		ExtraBody:  RequestBody{"temperature": float64(0.7)},
		ModelExtraBody: map[string]RequestBody{
			"model-a": {"max_tokens": float64(123)},
		},
		Timeout: time.Second,
	})

	events, err := client.StreamMessage(context.Background(), []message.Message{{Role: message.RoleUser, Content: "hello"}}, nil)
	if err != nil {
		t.Fatalf("StreamMessage() error = %v", err)
	}
	for event := range events {
		if event.Err != nil {
			t.Fatalf("stream event error = %v", event.Err)
		}
	}
	if captured["model"] != "model-a" || captured["stream"] != false {
		t.Fatalf("protected fields = %#v, want model-a/false", captured)
	}
	if captured["temperature"] != float64(0.7) || captured["max_tokens"] != float64(123) {
		t.Fatalf("extensions = %#v, want temperature/max_tokens", captured)
	}
	if _, ok := captured["stream_options"]; ok {
		t.Fatalf("stream_options = %#v, want omitted for non-streaming request", captured["stream_options"])
	}
}

func TestOpenAIExtraBodyRejectsProtectedFields(t *testing.T) {
	protected := []string{"model", "messages", "stream", "stream_options"}
	for _, field := range protected {
		t.Run(field, func(t *testing.T) {
			client := NewClient(Config{Provider: "openai-gateway", Transport: "openai-compatible", Model: "model-a", Models: []string{"model-a"}})
			err := client.ApplyModelConfig(Config{
				Provider:  "openai-gateway",
				Transport: "openai-compatible",
				Model:     "model-a",
				Models:    []string{"model-a"},
				ExtraBody: RequestBody{
					field: "must-not-override",
				},
			})
			if err == nil || !strings.Contains(err.Error(), `contains protected field "`+field+`"`) {
				t.Fatalf("ApplyModelConfig() error = %v, want protected-field error for %s", err, field)
			}
		})
	}
}

func TestStreamMessageMergesAnthropicExtraBodyAndAllowsMaxTokens(t *testing.T) {
	var captured map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() { _ = r.Body.Close() }()
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		if r.Header.Get("Anthropic-Version") != anthropicVersion {
			t.Fatalf("Anthropic-Version = %q, want %s", r.Header.Get("Anthropic-Version"), anthropicVersion)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"message_stop\"}\n\n")
	}))
	defer server.Close()

	client := NewClient(Config{
		Provider:   "anthropic-gateway",
		Transport:  "anthropic-compatible",
		APIBaseURL: server.URL,
		APIPath:    "/v1/messages",
		Model:      "claude-model",
		Models:     []string{"claude-model"},
		ExtraBody: RequestBody{
			"temperature": float64(0.4),
			"metadata":    RequestBody{"profile": "profile-value"},
		},
		ModelExtraBody: map[string]RequestBody{
			"claude-model": {
				"max_tokens": float64(1234),
				"metadata":   RequestBody{"model": "model-value"},
			},
		},
		Timeout: time.Second,
	})

	events, err := client.StreamMessage(context.Background(), []message.Message{
		{Role: message.RoleSystem, Content: "system prompt"},
		{Role: message.RoleUser, Content: "hello"},
	}, []ToolDefinition{{
		Name:        "Read",
		Description: "read a file",
		InputSchema: json.RawMessage(`{"type":"object"}`),
	}})
	if err != nil {
		t.Fatalf("StreamMessage() error = %v", err)
	}
	for event := range events {
		if event.Err != nil {
			t.Fatalf("stream event error = %v", event.Err)
		}
	}

	if captured["model"] != "claude-model" || captured["stream"] != true {
		t.Fatalf("protected fields = %#v, want claude-model/true", captured)
	}
	if _, ok := captured["system"]; !ok {
		t.Fatalf("system missing from request: %#v", captured)
	}
	if _, ok := captured["messages"]; !ok {
		t.Fatalf("messages missing from request: %#v", captured)
	}
	if _, ok := captured["tools"]; !ok {
		t.Fatalf("tools missing from request: %#v", captured)
	}
	if captured["max_tokens"] != float64(1234) || captured["temperature"] != float64(0.4) {
		t.Fatalf("extensions = %#v, want max_tokens/temperature", captured)
	}
	if !reflect.DeepEqual(captured["metadata"], map[string]any{
		"profile": "profile-value",
		"model":   "model-value",
	}) {
		t.Fatalf("metadata = %#v, want recursively merged object", captured["metadata"])
	}
}

func TestAnthropicExtraBodyRejectsProtectedFields(t *testing.T) {
	protected := []string{"model", "system", "messages", "tools", "stream", "stream_options"}
	for _, field := range protected {
		t.Run(field, func(t *testing.T) {
			client := NewClient(Config{Provider: "anthropic-gateway", Transport: "anthropic-compatible", Model: "claude-model", Models: []string{"claude-model"}})
			err := client.ApplyModelConfig(Config{
				Provider:  "anthropic-gateway",
				Transport: "anthropic-compatible",
				Model:     "claude-model",
				Models:    []string{"claude-model"},
				ExtraBody: RequestBody{field: "must-not-override"},
			})
			if err == nil || !strings.Contains(err.Error(), `contains protected field "`+field+`"`) {
				t.Fatalf("ApplyModelConfig() error = %v, want protected-field error for %s", err, field)
			}
		})
	}
}

func TestStreamMessageRejectsInvalidChatCompletionsArguments(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_bad","type":"function","function":{"name":"Read","arguments":"{\"file_path\":"}}]},"finish_reason":null}]}`+"\n\n")
		_, _ = fmt.Fprint(w, `data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`+"\n\n")
	}))
	defer server.Close()

	client := NewClient(Config{
		Provider: "openai-gateway", Transport: "openai-compatible", APIBaseURL: server.URL,
		APIPath: "/chat/completions", Model: "openai-model", Timeout: time.Second,
	})
	events, err := client.StreamMessage(context.Background(), []message.Message{{Role: message.RoleUser, Content: "inspect"}}, nil)
	if err != nil {
		t.Fatalf("StreamMessage() error = %v", err)
	}
	var sawCalls, sawDone bool
	var gotErr error
	for event := range events {
		sawCalls = sawCalls || len(event.ToolCalls) != 0
		sawDone = sawDone || event.Done
		if event.Err != nil {
			gotErr = event.Err
		}
	}
	if sawCalls {
		t.Fatal("invalid Chat Completions arguments emitted ToolCalls")
	}
	if sawDone {
		t.Fatal("invalid Chat Completions arguments emitted Done")
	}
	if gotErr == nil || !strings.Contains(gotErr.Error(), "invalid JSON object arguments") {
		t.Fatalf("error = %v, want invalid arguments error", gotErr)
	}
}

func TestNonStreamingMessageRejectsInvalidChatCompletionsArguments(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"choices":[{"message":{"tool_calls":[{"id":"call_bad","function":{"name":"Read","arguments":"[]"}}]}}]}`)
	}))
	defer server.Close()

	client := NewClient(Config{
		Provider: "openai-gateway", Transport: "openai-compatible", APIBaseURL: server.URL,
		APIPath: "/chat/completions", Model: "openai-model", Stream: false, streamSet: true, Timeout: time.Second,
	})
	events, err := client.StreamMessage(context.Background(), []message.Message{{Role: message.RoleUser, Content: "inspect"}}, nil)
	if err == nil {
		for range events {
		}
		t.Fatal("StreamMessage() accepted invalid non-streaming Chat Completions arguments")
	}
	if !strings.Contains(err.Error(), "invalid JSON object arguments") {
		t.Fatalf("error = %v, want invalid arguments error", err)
	}
}
