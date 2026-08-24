package model

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"paw/internal/message"
	"strings"
	"testing"
	"time"
)

func TestChatCompletionsStreamStopFinishReasonSucceeds(t *testing.T) {
	client := newChatCompletionsSSETestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, `data: {"choices":[{"delta":{"content":"ok"}}]}`+"\n\n")
		_, _ = fmt.Fprint(w, `data: {"choices":[{"delta":{},"finish_reason":"stop"}]}`+"\n\n")
	})

	result := collectChatCompletionEvents(t, client)
	if result.err != nil || result.delta != "ok" || result.doneReason != FinishReasonStop {
		t.Fatalf("result=%#v, want ok stop without error", result)
	}
}

func TestChatCompletionsStreamTrailingUsageAfterFinishReason(t *testing.T) {
	client := newChatCompletionsSSETestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, `data: {"choices":[{"delta":{"content":"ok"}}]}`+"\n\n")
		_, _ = fmt.Fprint(w, `data: {"choices":[{"delta":{},"finish_reason":"stop"}]}`+"\n\n")
		_, _ = fmt.Fprint(w, `data: {"choices":[],"usage":{"prompt_tokens":10,"completion_tokens":2,"total_tokens":12}}`+"\n\n")
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	})

	result := collectChatCompletionEvents(t, client)
	if result.err != nil {
		t.Fatalf("result.err = %v, want nil", result.err)
	}
	if result.delta != "ok" {
		t.Fatalf("delta = %q, want ok", result.delta)
	}
	if result.doneCount != 1 || result.doneReason != FinishReasonStop {
		t.Fatalf("doneCount=%d doneReason=%q, want one stop Done", result.doneCount, result.doneReason)
	}
	if result.usage == nil || result.usage.TotalTokens != 12 || result.usage.PromptTokens != 10 || result.usage.CompletionTokens != 2 {
		t.Fatalf("usage=%#v, want trailing usage preserved", result.usage)
	}
}

func TestChatCompletionsStreamDuplicateFinishReasonIsIdempotent(t *testing.T) {
	client := newChatCompletionsSSETestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, `data: {"choices":[{"delta":{"content":"ok"}}]}`+"\n\n")
		_, _ = fmt.Fprint(w, `data: {"choices":[{"delta":{},"finish_reason":"stop"}]}`+"\n\n")
		_, _ = fmt.Fprint(w, `data: {"choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":2,"total_tokens":12}}`+"\n\n")
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	})

	result := collectChatCompletionEvents(t, client)
	if result.err != nil || result.delta != "ok" {
		t.Fatalf("result=%#v, want duplicate stop accepted", result)
	}
	if result.doneCount != 1 || result.doneReason != FinishReasonStop {
		t.Fatalf("doneCount=%d doneReason=%q, want one stop Done", result.doneCount, result.doneReason)
	}
	if result.usage == nil || result.usage.TotalTokens != 12 {
		t.Fatalf("usage=%#v, want usage from duplicate finish tail", result.usage)
	}
}

func TestChatCompletionsStreamConflictingDuplicateFinishReasonErrors(t *testing.T) {
	client := newChatCompletionsSSETestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, `data: {"choices":[{"delta":{},"finish_reason":"stop"}]}`+"\n\n")
		_, _ = fmt.Fprint(w, `data: {"choices":[{"delta":{},"finish_reason":"length"}]}`+"\n\n")
	})

	result := collectChatCompletionEvents(t, client)
	assertChatCompletionStreamError(t, result, "conflicting finish_reason")
}

func TestChatCompletionsStreamCleanEOFAfterFinishReasonEmitsDoneOnce(t *testing.T) {
	client := newChatCompletionsSSETestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, `data: {"choices":[{"delta":{"content":"ok"}}]}`+"\n\n")
		_, _ = fmt.Fprint(w, `data: {"choices":[{"delta":{},"finish_reason":"stop"}]}`+"\n\n")
	})

	result := collectChatCompletionEvents(t, client)
	if result.err != nil || result.delta != "ok" || result.doneCount != 1 || result.doneReason != FinishReasonStop {
		t.Fatalf("result=%#v, want one stop Done on clean EOF after finish_reason", result)
	}
}

func TestChatCompletionsStreamToolCallsFinishReasonSucceeds(t *testing.T) {
	client := newChatCompletionsSSETestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_read","function":{"name":"Read","arguments":"{\"file_path\":\"go.mod\"}"}}]},"finish_reason":"tool_calls"}]}`+"\n\n")
	})

	result := collectChatCompletionEvents(t, client)
	if result.err != nil || result.doneReason != FinishReasonToolCalls {
		t.Fatalf("result=%#v, want tool_calls done without error", result)
	}
	if len(result.calls) != 1 || result.calls[0].ID != "call_read" || string(result.calls[0].Input) != `{"file_path":"go.mod"}` {
		t.Fatalf("calls=%#v, want parsed Read call", result.calls)
	}
}

func TestChatCompletionsStreamToolCallsFinishReasonWithoutCallsErrors(t *testing.T) {
	client := newChatCompletionsSSETestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, `data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`+"\n\n")
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	})

	result := collectChatCompletionEvents(t, client)
	assertChatCompletionStreamError(t, result, "tool_calls without tool calls")
}

func TestChatCompletionsStreamLengthFinishReasonSignalsContinuation(t *testing.T) {
	client := newChatCompletionsSSETestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, `data: {"choices":[{"delta":{"content":"partial"},"finish_reason":"length"}]}`+"\n\n")
	})

	result := collectChatCompletionEvents(t, client)
	if result.err != nil || result.delta != "partial" || result.doneReason != FinishReasonLength {
		t.Fatalf("result=%#v, want partial length continuation signal", result)
	}
}

func TestChatCompletionsStreamBareEOFBeforeFinishReasonErrors(t *testing.T) {
	client := newChatCompletionsSSETestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, `data: {"choices":[{"delta":{"content":"partial"}}]}`+"\n\n")
	})

	result := collectChatCompletionEvents(t, client)
	assertChatCompletionStreamError(t, result, "ended before finish_reason")
}

func TestChatCompletionsStreamDoneBeforeFinishReasonErrors(t *testing.T) {
	client := newChatCompletionsSSETestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, `data: {"choices":[{"delta":{"content":"partial"}}]}`+"\n\n")
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	})

	result := collectChatCompletionEvents(t, client)
	assertChatCompletionStreamError(t, result, "[DONE] before finish_reason")
}

func TestChatCompletionsStreamContentFilterFinishReasonErrors(t *testing.T) {
	client := newChatCompletionsSSETestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, `data: {"choices":[{"delta":{},"finish_reason":"content_filter"}]}`+"\n\n")
	})

	result := collectChatCompletionEvents(t, client)
	assertChatCompletionStreamError(t, result, "content_filter")
}

func TestChatCompletionsStreamUnknownFinishReasonErrors(t *testing.T) {
	client := newChatCompletionsSSETestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, `data: {"choices":[{"delta":{},"finish_reason":"mystery"}]}`+"\n\n")
	})

	result := collectChatCompletionEvents(t, client)
	assertChatCompletionStreamError(t, result, `unknown finish_reason "mystery"`)
}

func TestChatCompletionsNonStreamingMissingFinishReasonDefaultsToStop(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"compat"}}]}`)
	}))
	defer server.Close()

	client := NewClient(Config{
		Provider:   "openai-gateway",
		Transport:  "openai-compatible",
		APIBaseURL: server.URL,
		APIPath:    "/chat/completions",
		Model:      "openai-model",
		Stream:     false,
		streamSet:  true,
		Timeout:    time.Second,
	})

	result := collectChatCompletionEvents(t, client)
	if result.err != nil || result.delta != "compat" || result.doneReason != FinishReasonStop {
		t.Fatalf("result=%#v, want compat stop without error", result)
	}
}

func TestChatCompletionsNonStreamingToolCallsFinishReasonWithoutCallsErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":""},"finish_reason":"tool_calls"}]}`)
	}))
	defer server.Close()

	client := NewClient(Config{
		Provider:   "openai-gateway",
		Transport:  "openai-compatible",
		APIBaseURL: server.URL,
		APIPath:    "/chat/completions",
		Model:      "openai-model",
		Stream:     false,
		streamSet:  true,
		Timeout:    time.Second,
	})

	result := collectChatCompletionEvents(t, client)
	assertChatCompletionStreamError(t, result, "tool_calls without tool calls")
}

type chatCompletionEventResult struct {
	delta      string
	calls      []message.ToolCall
	done       bool
	doneCount  int
	doneReason FinishReason
	err        error
	usage      *Usage
}

func newChatCompletionsSSETestClient(t *testing.T, handler func(http.ResponseWriter, *http.Request)) *Client {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(handler))
	t.Cleanup(server.Close)
	return NewClient(Config{
		Provider:   "openai-gateway",
		Transport:  "openai-compatible",
		APIBaseURL: server.URL,
		APIPath:    "/chat/completions",
		Model:      "openai-model",
		Timeout:    time.Second,
	})
}

func collectChatCompletionEvents(t *testing.T, client *Client) chatCompletionEventResult {
	t.Helper()
	events, err := client.StreamMessage(context.Background(), []message.Message{{Role: message.RoleUser, Content: "hello"}}, nil)
	if err != nil {
		return chatCompletionEventResult{err: err}
	}
	var result chatCompletionEventResult
	for ev := range events {
		if ev.Err != nil {
			result.err = ev.Err
			continue
		}
		result.delta += ev.Delta
		result.calls = append(result.calls, ev.ToolCalls...)
		if ev.Usage != nil {
			usage := *ev.Usage
			result.usage = &usage
		}
		if ev.Done {
			result.done = true
			result.doneCount++
			result.doneReason = ev.FinishReason
		}
	}
	return result
}

func assertChatCompletionStreamError(t *testing.T, result chatCompletionEventResult, want string) {
	t.Helper()
	if result.done {
		t.Fatalf("result=%#v emitted Done, want stream error", result)
	}
	if result.err == nil || !strings.Contains(result.err.Error(), want) {
		t.Fatalf("result.err=%v, want substring %q", result.err, want)
	}
}
