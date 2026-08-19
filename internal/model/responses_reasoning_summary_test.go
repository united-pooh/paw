package model

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"paw/internal/message"
	"strings"
	"testing"
	"time"
)

func TestGPTResponsesRequestDefaultsReasoningSummary(t *testing.T) {
	var request map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, `data: {"type":"response.completed","response":{"output":[{"type":"message","content":[{"type":"output_text","text":"ok"}]}]}}`+"\n\n")
	}))
	defer server.Close()

	client := NewClient(Config{
		Provider: "openai", Adapter: "gpt", Transport: "openai-responses",
		APIBaseURL: server.URL, APIPath: "/responses", Model: "gpt-test", Timeout: time.Second,
	})
	events, err := client.StreamMessage(context.Background(), []message.Message{{Role: message.RoleUser, Content: "hello"}}, nil)
	if err != nil {
		t.Fatalf("StreamMessage() error = %v", err)
	}
	for range events {
	}

	reasoning, ok := request["reasoning"].(map[string]any)
	if !ok {
		t.Fatalf("reasoning = %#v, want object", request["reasoning"])
	}
	if reasoning["summary"] != "auto" {
		t.Fatalf("reasoning.summary = %#v, want auto", reasoning["summary"])
	}
}

func TestGPTResponsesRequestNormalizesLegacyReasoningParameters(t *testing.T) {
	var request map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, `data: {"type":"response.completed","response":{"output":[{"type":"message","content":[{"type":"output_text","text":"ok"}]}]}}`+"\n\n")
	}))
	defer server.Close()

	client := NewClient(Config{
		Provider: "openai", Adapter: "gpt", Transport: "openai-responses",
		APIBaseURL: server.URL, APIPath: "/responses", Model: "gpt-test", Timeout: time.Second,
		ExtraBody: RequestBody{
			"reasoning_effort": "high",
			"thinking":         RequestBody{"type": "enabled"},
		},
	})
	events, err := client.StreamMessage(context.Background(), []message.Message{{Role: message.RoleUser, Content: "hello"}}, nil)
	if err != nil {
		t.Fatalf("StreamMessage() error = %v", err)
	}
	for range events {
	}

	reasoning, ok := request["reasoning"].(map[string]any)
	if !ok {
		t.Fatalf("reasoning = %#v, want object", request["reasoning"])
	}
	if reasoning["effort"] != "high" || reasoning["summary"] != "auto" {
		t.Fatalf("reasoning = %#v, want effort=high summary=auto", reasoning)
	}
	if _, ok := request["reasoning_effort"]; ok {
		t.Fatalf("request retained legacy reasoning_effort: %#v", request)
	}
	if _, ok := request["thinking"]; ok {
		t.Fatalf("request retained Anthropic thinking field: %#v", request)
	}
}

func TestResponsesReasoningSummaryFromCompletedOutputBecomesThinking(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, `data: {"type":"response.completed","response":{"output":[{"type":"reasoning","id":"rs_1","summary":[{"type":"summary_text","text":"I checked the repository."}],"encrypted_content":"cipher"},{"type":"message","content":[{"type":"output_text","text":"answer"}]}]}}`+"\n\n")
	}))
	defer server.Close()

	client := NewClient(Config{
		Provider: "openai", Adapter: "gpt", Transport: "openai-responses",
		APIBaseURL: server.URL, APIPath: "/responses", Model: "gpt-test", Timeout: time.Second,
	})
	events, err := client.StreamMessage(context.Background(), []message.Message{{Role: message.RoleUser, Content: "hello"}}, nil)
	if err != nil {
		t.Fatalf("StreamMessage() error = %v", err)
	}
	var thinking, text string
	var providerData json.RawMessage
	for event := range events {
		if event.Err != nil {
			t.Fatalf("stream error = %v", event.Err)
		}
		thinking += event.Thinking
		text += event.Delta
		if len(event.ProviderData) != 0 {
			providerData = append(json.RawMessage(nil), event.ProviderData...)
		}
	}
	if thinking != "I checked the repository." {
		t.Fatalf("thinking = %q, want summary text", thinking)
	}
	if text != "answer" {
		t.Fatalf("text = %q, want answer", text)
	}
	items, ok := decodeResponsesProviderData(providerData)
	if !ok || len(items) != 2 || !strings.Contains(string(items[0]), "cipher") {
		t.Fatalf("provider data = %s, items=%d ok=%v", providerData, len(items), ok)
	}
}

// TestResponsesDeepSeekReasoningTextDeltaStreamsIncrementally 验证 DeepSeek
// Responses API 的逐 token CoT 事件（response.reasoning_text.delta）被增量
// 转发，且随后的 done 全量与 completed 兜底不会重复回放。回归：旧实现只识别
// OpenAI 的 reasoning_summary_text.delta，DeepSeek 的全部思考 delta 被静默
// 丢弃，只能从 response.completed 一次性提取——UI 表现为思考瞬间全量输出。
func TestResponsesDeepSeekReasoningTextDeltaStreamsIncrementally(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, `data: {"type":"response.reasoning_text.delta","delta":"先分析"}`+"\n\n")
		_, _ = fmt.Fprint(w, `data: {"type":"response.reasoning_text.delta","delta":"再推理"}`+"\n\n")
		_, _ = fmt.Fprint(w, `data: {"type":"response.reasoning_text.done","text":"先分析再推理"}`+"\n\n")
		_, _ = fmt.Fprint(w, `data: {"type":"response.output_text.delta","delta":"answer"}`+"\n\n")
		_, _ = fmt.Fprint(w, `data: {"type":"response.completed","response":{"output":[{"type":"reasoning","id":"rs_1","content":[{"type":"reasoning_text","text":"先分析再推理"}]},{"type":"message","content":[{"type":"output_text","text":"answer"}]}]}}`+"\n\n")
	}))
	defer server.Close()

	client := NewClient(Config{
		Provider: "deepseek", Adapter: "openai-compatible", Transport: "openai-responses",
		APIBaseURL: server.URL, APIPath: "/responses", Model: "deepseek-v4-pro", Timeout: time.Second,
	})
	events, err := client.StreamMessage(context.Background(), []message.Message{{Role: message.RoleUser, Content: "hello"}}, nil)
	if err != nil {
		t.Fatalf("StreamMessage() error = %v", err)
	}
	var thinkingEvents []string
	var thinking, text string
	for event := range events {
		if event.Err != nil {
			t.Fatalf("stream error = %v", event.Err)
		}
		if event.Thinking != "" {
			thinkingEvents = append(thinkingEvents, event.Thinking)
		}
		thinking += event.Thinking
		text += event.Delta
	}
	if len(thinkingEvents) != 2 {
		t.Fatalf("thinking delta events = %v, want exactly 2 incremental deltas (done/completed must not replay)", thinkingEvents)
	}
	if thinking != "先分析再推理" {
		t.Fatalf("thinking = %q, want streamed CoT", thinking)
	}
	if text != "answer" {
		t.Fatalf("text = %q, want answer", text)
	}
}

// TestResponsesDeepSeekReasoningTextDoneFallbackWithoutDeltas 验证网关只发
// response.reasoning_text.done（全量 CoT）而不发增量 delta 时，done 作为兜底
// 回放一次全文，completed 不再重复。
func TestResponsesDeepSeekReasoningTextDoneFallbackWithoutDeltas(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, `data: {"type":"response.reasoning_text.done","text":"完整思考"}`+"\n\n")
		_, _ = fmt.Fprint(w, `data: {"type":"response.completed","response":{"output":[{"type":"reasoning","id":"rs_1","content":[{"type":"reasoning_text","text":"完整思考"}]},{"type":"message","content":[{"type":"output_text","text":"answer"}]}]}}`+"\n\n")
	}))
	defer server.Close()

	client := NewClient(Config{
		Provider: "deepseek", Adapter: "openai-compatible", Transport: "openai-responses",
		APIBaseURL: server.URL, APIPath: "/responses", Model: "deepseek-v4-pro", Timeout: time.Second,
	})
	events, err := client.StreamMessage(context.Background(), []message.Message{{Role: message.RoleUser, Content: "hello"}}, nil)
	if err != nil {
		t.Fatalf("StreamMessage() error = %v", err)
	}
	var thinking string
	thinkingCount := 0
	for event := range events {
		if event.Err != nil {
			t.Fatalf("stream error = %v", event.Err)
		}
		if event.Thinking != "" {
			thinkingCount++
		}
		thinking += event.Thinking
	}
	if thinking != "完整思考" || thinkingCount != 1 {
		t.Fatalf("thinking = %q (events=%d), want full CoT replayed exactly once", thinking, thinkingCount)
	}
}

func TestResponsesReasoningTextFromCompletedOutputBecomesThinking(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, `data: {"type":"response.completed","response":{"output":[{"type":"reasoning","id":"rs_1","summary":[],"content":[{"type":"reasoning_text","text":"I inspected the OpenRouter response."}]},{"type":"message","content":[{"type":"output_text","text":"answer"}]}]}}`+"\n\n")
	}))
	defer server.Close()

	client := NewClient(Config{
		Provider: "openrouter", Adapter: "openai-compatible", Transport: "openai-responses",
		APIBaseURL: server.URL, APIPath: "/responses", Model: "deepseek/deepseek-v4-flash", Timeout: time.Second,
	})
	events, err := client.StreamMessage(context.Background(), []message.Message{{Role: message.RoleUser, Content: "hello"}}, nil)
	if err != nil {
		t.Fatalf("StreamMessage() error = %v", err)
	}
	var thinking, text string
	for event := range events {
		if event.Err != nil {
			t.Fatalf("stream error = %v", event.Err)
		}
		thinking += event.Thinking
		text += event.Delta
	}
	if thinking != "I inspected the OpenRouter response." || text != "answer" {
		t.Fatalf("thinking=%q text=%q", thinking, text)
	}
}

func TestResponsesStringReasoningSummaryFromCompletedOutputBecomesThinking(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, `data: {"type":"response.completed","response":{"output":[{"type":"reasoning","id":"rs_1","summary":["First step","Second step"]},{"type":"message","content":[{"type":"output_text","text":"answer"}]}]}}`+"\n\n")
	}))
	defer server.Close()

	client := NewClient(Config{
		Provider: "openrouter", Adapter: "openai-compatible", Transport: "openai-responses",
		APIBaseURL: server.URL, APIPath: "/responses", Model: "deepseek/deepseek-v4-flash", Timeout: time.Second,
	})
	events, err := client.StreamMessage(context.Background(), []message.Message{{Role: message.RoleUser, Content: "hello"}}, nil)
	if err != nil {
		t.Fatalf("StreamMessage() error = %v", err)
	}
	var thinking string
	for event := range events {
		if event.Err != nil {
			t.Fatalf("stream error = %v", event.Err)
		}
		thinking += event.Thinking
	}
	if thinking != "First step\n\nSecond step" {
		t.Fatalf("thinking = %q, want string reasoning summaries", thinking)
	}
}

func TestResponsesReasoningSummaryStreamDeltasAreNotDuplicatedAtCompletion(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, `data: {"type":"response.reasoning_summary_text.delta","delta":"first "}`+"\n\n")
		_, _ = fmt.Fprint(w, `data: {"type":"response.reasoning_summary_text.delta","delta":"second"}`+"\n\n")
		_, _ = fmt.Fprint(w, `data: {"type":"response.completed","response":{"output":[{"type":"reasoning","id":"rs_1","summary":[{"type":"summary_text","text":"first second"}]},{"type":"message","content":[{"type":"output_text","text":"answer"}]}]}}`+"\n\n")
	}))
	defer server.Close()

	client := NewClient(Config{
		Provider: "openai", Adapter: "gpt", Transport: "openai-responses",
		APIBaseURL: server.URL, APIPath: "/responses", Model: "gpt-test", Timeout: time.Second,
	})
	events, err := client.StreamMessage(context.Background(), []message.Message{{Role: message.RoleUser, Content: "hello"}}, nil)
	if err != nil {
		t.Fatalf("StreamMessage() error = %v", err)
	}
	var thinking string
	for event := range events {
		if event.Err != nil {
			t.Fatalf("stream error = %v", event.Err)
		}
		thinking += event.Thinking
	}
	if thinking != "first second" {
		t.Fatalf("thinking = %q, want one streamed summary", thinking)
	}
}

func TestResponsesReasoningStreamDeltasAreLiveAndNotDuplicatedAtCompletion(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, `data: {"type":"response.reasoning.delta","delta":"first "}`+"\n\n")
		_, _ = fmt.Fprint(w, `data: {"type":"response.reasoning.delta","delta":"second"}`+"\n\n")
		_, _ = fmt.Fprint(w, `data: {"type":"response.completed","response":{"output":[{"type":"reasoning","id":"rs_1","summary":[],"content":[{"type":"reasoning_text","text":"first second"}]},{"type":"message","content":[{"type":"output_text","text":"answer"}]}]}}`+"\n\n")
	}))
	defer server.Close()

	client := NewClient(Config{
		Provider: "openrouter", Adapter: "openai-compatible", Transport: "openai-responses",
		APIBaseURL: server.URL, APIPath: "/responses", Model: "deepseek/deepseek-v4-flash", Timeout: time.Second,
	})
	events, err := client.StreamMessage(context.Background(), []message.Message{{Role: message.RoleUser, Content: "hello"}}, nil)
	if err != nil {
		t.Fatalf("StreamMessage() error = %v", err)
	}
	var thinkingEvents []string
	for event := range events {
		if event.Err != nil {
			t.Fatalf("stream error = %v", event.Err)
		}
		if event.Thinking != "" {
			thinkingEvents = append(thinkingEvents, event.Thinking)
		}
	}
	if got := strings.Join(thinkingEvents, "|"); got != "first |second" {
		t.Fatalf("thinking events = %q, want two live deltas without completion duplicate", got)
	}
}

func TestNonStreamingResponsesReasoningSummaryBecomesThinking(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"status":"completed","output":[{"type":"reasoning","id":"rs_1","summary":[{"type":"summary_text","text":"non-stream summary"}]},{"type":"message","content":[{"type":"output_text","text":"non-stream answer"}]}]}`)
	}))
	defer server.Close()

	client := NewClient(Config{
		Provider: "openai", Adapter: "gpt", Transport: "openai-responses",
		APIBaseURL: server.URL, APIPath: "/responses", Model: "gpt-test",
		Stream: false, streamSet: true, Timeout: time.Second,
	})
	events, err := client.StreamMessage(context.Background(), []message.Message{{Role: message.RoleUser, Content: "hello"}}, nil)
	if err != nil {
		t.Fatalf("StreamMessage() error = %v", err)
	}
	var thinking, text string
	for event := range events {
		if event.Err != nil {
			t.Fatalf("stream error = %v", event.Err)
		}
		thinking += event.Thinking
		text += event.Delta
	}
	if thinking != "non-stream summary" || text != "non-stream answer" {
		t.Fatalf("thinking=%q text=%q", thinking, text)
	}
}
