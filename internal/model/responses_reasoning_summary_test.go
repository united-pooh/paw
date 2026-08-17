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
