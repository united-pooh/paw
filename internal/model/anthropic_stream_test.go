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

func TestStreamMessageRejectsInvalidAnthropicArguments(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, `data: {"type":"content_block_start","content_block":{"type":"tool_use","id":"toolu_bad","name":"Read","input":{}}}`+"\n\n")
		_, _ = fmt.Fprint(w, `data: {"type":"content_block_delta","delta":{"type":"input_json_delta","partial_json":"{\"file_path\":"}}`+"\n\n")
		_, _ = fmt.Fprint(w, `data: {"type":"content_block_stop","index":0}`+"\n\n")
	}))
	defer server.Close()

	client := NewClient(Config{
		Provider: "anthropic-gateway", Transport: "anthropic-compatible", APIBaseURL: server.URL,
		APIPath: "/v1/messages", Model: "anthropic-model", Timeout: time.Second,
	})
	events, err := client.StreamMessage(context.Background(), []message.Message{{Role: message.RoleUser, Content: "read"}}, nil)
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
		t.Fatal("invalid Anthropic arguments emitted ToolCalls")
	}
	if sawDone {
		t.Fatal("invalid Anthropic arguments emitted Done")
	}
	if gotErr == nil || !strings.Contains(gotErr.Error(), "invalid JSON object arguments") {
		t.Fatalf("error = %v, want invalid arguments error", gotErr)
	}
}

func TestStreamMessageAnthropicRejectsWholeBatchWhenAnyCallInvalid(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		w.Header().Set("Content-Type", "text/event-stream")
		// 第一个工具调用合法
		_, _ = fmt.Fprint(w, `data: {"type":"content_block_start","content_block":{"type":"tool_use","id":"toolu_ok","name":"Read","input":{}}}`+"\n\n")
		_, _ = fmt.Fprint(w, `data: {"type":"content_block_delta","delta":{"type":"input_json_delta","partial_json":"{\"file_path\":\"go.mod\"}"}}`+"\n\n")
		_, _ = fmt.Fprint(w, `data: {"type":"content_block_stop","index":0}`+"\n\n")
		// 第二个工具调用非法（截断的 JSON）
		_, _ = fmt.Fprint(w, `data: {"type":"content_block_start","content_block":{"type":"tool_use","id":"toolu_bad","name":"LS","input":{}}}`+"\n\n")
		_, _ = fmt.Fprint(w, `data: {"type":"content_block_delta","delta":{"type":"input_json_delta","partial_json":"{\"path\":"}}`+"\n\n")
		_, _ = fmt.Fprint(w, `data: {"type":"content_block_stop","index":1}`+"\n\n")
		_, _ = fmt.Fprint(w, `data: {"type":"message_stop"}`+"\n\n")
	}))
	defer server.Close()

	client := NewClient(Config{
		Provider: "anthropic-gateway", Transport: "anthropic-compatible", APIBaseURL: server.URL,
		APIPath: "/v1/messages", Model: "anthropic-model", Timeout: time.Second,
	})
	events, err := client.StreamMessage(context.Background(), []message.Message{{Role: message.RoleUser, Content: "read"}}, nil)
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
		t.Fatal("mixed batch emitted ToolCalls for earlier valid call; want whole batch rejected")
	}
	if sawDone {
		t.Fatal("mixed batch emitted Done; want error only")
	}
	if gotErr == nil || !strings.Contains(gotErr.Error(), "invalid JSON object arguments") {
		t.Fatalf("error = %v, want invalid arguments error", gotErr)
	}
}

func TestStreamMessageAnthropicEmitsWholeBatchAtMessageStop(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, `data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_1","name":"Read","input":{}}}`+"\n\n")
		_, _ = fmt.Fprint(w, `data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"file_path\":\"a.go\"}"}}`+"\n\n")
		_, _ = fmt.Fprint(w, `data: {"type":"content_block_stop","index":0}`+"\n\n")
		_, _ = fmt.Fprint(w, `data: {"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"toolu_2","name":"LS","input":{}}}`+"\n\n")
		_, _ = fmt.Fprint(w, `data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"path\":\"src\"}"}}`+"\n\n")
		_, _ = fmt.Fprint(w, `data: {"type":"content_block_stop","index":1}`+"\n\n")
		_, _ = fmt.Fprint(w, `data: {"type":"message_stop"}`+"\n\n")
	}))
	defer server.Close()

	client := NewClient(Config{
		Provider: "anthropic-gateway", Transport: "anthropic-compatible", APIBaseURL: server.URL,
		APIPath: "/v1/messages", Model: "anthropic-model", Timeout: time.Second,
	})
	events, err := client.StreamMessage(context.Background(), []message.Message{{Role: message.RoleUser, Content: "read"}}, nil)
	if err != nil {
		t.Fatalf("StreamMessage() error = %v", err)
	}
	var calls []message.ToolCall
	var sawDone bool
	for event := range events {
		if event.Err != nil {
			t.Fatalf("stream error = %v", event.Err)
		}
		calls = append(calls, event.ToolCalls...)
		sawDone = sawDone || event.Done
	}
	if len(calls) != 2 {
		t.Fatalf("tool calls = %#v, want both in one batch", calls)
	}
	if calls[0].ID != "toolu_1" || calls[0].Name != "Read" || string(calls[0].Input) != `{"file_path":"a.go"}` {
		t.Fatalf("first call = %#v", calls[0])
	}
	if calls[1].ID != "toolu_2" || calls[1].Name != "LS" || string(calls[1].Input) != `{"path":"src"}` {
		t.Fatalf("second call = %#v", calls[1])
	}
	if !sawDone {
		t.Fatal("no Done emitted")
	}
}

func TestStreamMessageAnthropicMaxTokensEmitsFinishReasonLength(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, `data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_trunc","name":"Read","input":{}}}`+"\n\n")
		_, _ = fmt.Fprint(w, `data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"file_path\":\"go.mod\"}"}}`+"\n\n")
		_, _ = fmt.Fprint(w, `data: {"type":"content_block_stop","index":0}`+"\n\n")
		_, _ = fmt.Fprint(w, `data: {"type":"message_delta","delta":{"stop_reason":"max_tokens","stop_sequence":null}}`+"\n\n")
		_, _ = fmt.Fprint(w, `data: {"type":"message_stop"}`+"\n\n")
	}))
	defer server.Close()

	client := NewClient(Config{
		Provider: "anthropic-gateway", Transport: "anthropic-compatible", APIBaseURL: server.URL,
		APIPath: "/v1/messages", Model: "anthropic-model", Timeout: time.Second,
	})
	events, err := client.StreamMessage(context.Background(), []message.Message{{Role: message.RoleUser, Content: "read"}}, nil)
	if err != nil {
		t.Fatalf("StreamMessage() error = %v", err)
	}
	var sawCalls bool
	var doneReason FinishReason
	for event := range events {
		if event.Err != nil {
			t.Fatalf("stream error = %v", event.Err)
		}
		sawCalls = sawCalls || len(event.ToolCalls) != 0
		if event.Done {
			doneReason = event.FinishReason
		}
	}
	if !sawCalls {
		t.Fatal("tool calls not emitted")
	}
	if doneReason != FinishReasonLength {
		t.Fatalf("done finish reason = %q, want %q so the runner can reject truncated tool calls", doneReason, FinishReasonLength)
	}
}

func TestStreamMessageAnthropicEndTurnEmitsFinishReasonStop(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, `data: {"type":"content_block_start","index":0,"content_block":{"type":"text"}}`+"\n\n")
		_, _ = fmt.Fprint(w, `data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi"}}`+"\n\n")
		_, _ = fmt.Fprint(w, `data: {"type":"content_block_stop","index":0}`+"\n\n")
		_, _ = fmt.Fprint(w, `data: {"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null}}`+"\n\n")
		_, _ = fmt.Fprint(w, `data: {"type":"message_stop"}`+"\n\n")
	}))
	defer server.Close()

	client := NewClient(Config{
		Provider: "anthropic-gateway", Transport: "anthropic-compatible", APIBaseURL: server.URL,
		APIPath: "/v1/messages", Model: "anthropic-model", Timeout: time.Second,
	})
	events, err := client.StreamMessage(context.Background(), []message.Message{{Role: message.RoleUser, Content: "hi"}}, nil)
	if err != nil {
		t.Fatalf("StreamMessage() error = %v", err)
	}
	var doneReason FinishReason
	for event := range events {
		if event.Err != nil {
			t.Fatalf("stream error = %v", event.Err)
		}
		if event.Done {
			doneReason = event.FinishReason
		}
	}
	if doneReason != FinishReasonStop {
		t.Fatalf("done finish reason = %q, want %q", doneReason, FinishReasonStop)
	}
}

func TestStreamMessageRejectsNestedStringAnthropicArguments(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, `data: {"type":"content_block_start","content_block":{"type":"tool_use","id":"toolu_bad","name":"Read","input":"{\"file_path\":\"go.mod\"}"}}`+"\n\n")
		_, _ = fmt.Fprint(w, `data: {"type":"content_block_stop","index":0}`+"\n\n")
	}))
	defer server.Close()

	client := NewClient(Config{
		Provider: "anthropic-gateway", Transport: "anthropic-compatible", APIBaseURL: server.URL,
		APIPath: "/v1/messages", Model: "anthropic-model", Timeout: time.Second,
	})
	events, err := client.StreamMessage(context.Background(), []message.Message{{Role: message.RoleUser, Content: "read"}}, nil)
	if err != nil {
		t.Fatalf("StreamMessage() error = %v", err)
	}
	var gotErr error
	for event := range events {
		if len(event.ToolCalls) != 0 {
			t.Fatal("nested-string Anthropic arguments emitted ToolCalls")
		}
		if event.Err != nil {
			gotErr = event.Err
		}
	}
	if gotErr == nil {
		t.Fatal("nested-string Anthropic arguments did not produce an error")
	}
}
