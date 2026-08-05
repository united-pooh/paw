package model

import (
	"bytes"
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

func TestDefaultTransportUsesResponsesAPI(t *testing.T) {
	cfg := fillConfigDefaults(Config{Model: "gpt-test"})
	if cfg.Transport != "openai-responses" {
		t.Fatalf("Transport = %q, want openai-responses", cfg.Transport)
	}
	if cfg.APIPath != "/responses" {
		t.Fatalf("APIPath = %q, want /responses", cfg.APIPath)
	}

	legacy := fillConfigDefaults(Config{APIPath: "/chat/completions", Model: "gpt-test"})
	if legacy.Transport != "openai-compatible" || legacy.APIPath != "/chat/completions" {
		t.Fatalf("legacy config = transport %q path %q", legacy.Transport, legacy.APIPath)
	}
}

func TestStreamMessageUsesResponsesAPIAndParsesTextToolCallsAndUsage(t *testing.T) {
	requestChecked := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		if r.URL.Path != "/v1/responses" {
			t.Fatalf("path = %q, want /v1/responses", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer sk-test" {
			t.Fatalf("Authorization = %q", got)
		}
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if request["model"] != "gpt-test" || request["stream"] != true {
			t.Fatalf("request = %#v", request)
		}
		input, ok := request["input"].([]any)
		if !ok || len(input) != 4 {
			t.Fatalf("input = %#v, want user/function call/function output/user", request["input"])
		}
		call := input[1].(map[string]any)
		if call["type"] != "function_call" || call["call_id"] != "call_old" || call["name"] != "Read" {
			t.Fatalf("function call input = %#v", call)
		}
		output := input[2].(map[string]any)
		if output["type"] != "function_call_output" || output["call_id"] != "call_old" || output["output"] != "old result" {
			t.Fatalf("function output input = %#v", output)
		}
		tools := request["tools"].([]any)
		tool := tools[0].(map[string]any)
		if tool["type"] != "function" || tool["name"] != "LS" {
			t.Fatalf("tool = %#v", tool)
		}
		if _, nested := tool["function"]; nested {
			t.Fatalf("Responses tool unexpectedly uses Chat Completions wrapper: %#v", tool)
		}
		requestChecked = true

		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello \"}\n\n")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"world\"}\n\n")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"response.output_item.added\",\"output_index\":1,\"item\":{\"type\":\"function_call\",\"id\":\"fc_1\",\"call_id\":\"call_ls\",\"name\":\"LS\",\"arguments\":\"\"}}\n\n")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"response.function_call_arguments.delta\",\"output_index\":1,\"item_id\":\"fc_1\",\"delta\":\"{\\\"path\\\":\"}\n\n")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"response.function_call_arguments.delta\",\"output_index\":1,\"item_id\":\"fc_1\",\"delta\":\"\\\".\\\"}\"}\n\n")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"response.output_item.done\",\"output_index\":1,\"item\":{\"type\":\"function_call\",\"id\":\"fc_1\",\"call_id\":\"call_ls\",\"name\":\"LS\",\"arguments\":\"{\\\"path\\\":\\\".\\\"}\"}}\n\n")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"response\":{\"output\":[{\"type\":\"message\",\"id\":\"msg_1\",\"content\":[{\"type\":\"output_text\",\"text\":\"hello world\"}]},{\"type\":\"function_call\",\"id\":\"fc_1\",\"call_id\":\"call_ls\",\"name\":\"LS\",\"arguments\":\"{\\\"path\\\":\\\".\\\"}\"}],\"usage\":{\"input_tokens\":120,\"output_tokens\":8,\"total_tokens\":128,\"input_tokens_details\":{\"cached_tokens\":50}}}}\n\n")
	}))
	defer server.Close()

	client := NewClient(Config{
		Transport: "openai-responses", APIBaseURL: server.URL + "/v1", APIPath: "/responses",
		APIKey: "sk-test", Model: "gpt-test", Timeout: time.Second,
	})
	events, err := client.StreamMessage(context.Background(), []message.Message{
		{Role: message.RoleUser, Content: "first"},
		{Role: message.RoleAssistant, ToolUse: &message.ToolCall{ID: "call_old", Name: "Read", Input: json.RawMessage(`{"file_path":"a.go"}`)}},
		{Role: message.RoleUser, Content: "old result", ToolResult: &message.ToolResult{ToolUseID: "call_old", Content: "old result"}},
		{Role: message.RoleUser, Content: "continue"},
	}, []ToolDefinition{{Name: "LS", Description: "list files", InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}}}`)}})
	if err != nil {
		t.Fatalf("StreamMessage() error = %v", err)
	}

	var text strings.Builder
	var calls []message.ToolCall
	var usage *Usage
	var providerData json.RawMessage
	var done bool
	for event := range events {
		if event.Err != nil {
			t.Fatalf("event error = %v", event.Err)
		}
		text.WriteString(event.Delta)
		calls = append(calls, event.ToolCalls...)
		if event.Usage != nil {
			copy := *event.Usage
			usage = &copy
		}
		if len(event.ProviderData) != 0 {
			providerData = append(json.RawMessage(nil), event.ProviderData...)
		}
		done = done || event.Done
	}
	if !requestChecked {
		t.Fatal("request was not checked")
	}
	if text.String() != "hello world" || !done {
		t.Fatalf("text=%q done=%v", text.String(), done)
	}
	if len(calls) != 1 || calls[0].ID != "call_ls" || calls[0].Name != "LS" || string(calls[0].Input) != `{"path":"."}` {
		t.Fatalf("calls = %#v", calls)
	}
	if usage == nil || usage.ContextTokenCount() != 128 || usage.CacheHitTokens() != 50 {
		t.Fatalf("usage = %#v", usage)
	}
	items, ok := decodeResponsesProviderData(providerData)
	if !ok || len(items) != 2 {
		t.Fatalf("provider data = %s, items=%d ok=%v", providerData, len(items), ok)
	}
}

func TestStreamingResponsesFallsBackToCompletedTextWithoutDeltas(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, `data: {"type":"response.completed","response":{"output":[{"type":"message","id":"msg_snapshot","content":[{"type":"output_text","text":"snapshot only"}]}]}}`+"\n\n")
	}))
	defer server.Close()

	client := NewClient(Config{
		Transport: "openai-responses", APIBaseURL: server.URL, APIPath: "/responses",
		Model: "gpt-test", Timeout: time.Second,
	})
	events, err := client.StreamMessage(context.Background(), []message.Message{{Role: message.RoleUser, Content: "hello"}}, nil)
	if err != nil {
		t.Fatalf("StreamMessage() error = %v", err)
	}

	var text strings.Builder
	var providerData json.RawMessage
	var done bool
	for event := range events {
		if event.Err != nil {
			t.Fatalf("event error = %v", event.Err)
		}
		text.WriteString(event.Delta)
		if len(event.ProviderData) != 0 {
			providerData = append(json.RawMessage(nil), event.ProviderData...)
		}
		done = done || event.Done
	}
	if text.String() != "snapshot only" || !done {
		t.Fatalf("text=%q done=%v", text.String(), done)
	}
	items, ok := decodeResponsesProviderData(providerData)
	if !ok || len(items) != 1 {
		t.Fatalf("provider data = %s, items=%d ok=%v", providerData, len(items), ok)
	}
}

func TestStreamMessageUsesNonStreamingResponsesAPI(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request responsesRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if request.Stream {
			t.Fatal("stream = true, want false")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"output":[{"type":"message","content":[{"type":"output_text","text":"non-stream"}]},{"type":"function_call","call_id":"call_read","name":"Read","arguments":"{\"file_path\":\"go.mod\"}"}],"usage":{"input_tokens":10,"output_tokens":2,"total_tokens":12}}`)
	}))
	defer server.Close()

	client := NewClient(Config{
		Transport: "openai-responses", APIBaseURL: server.URL, APIPath: "/responses", Model: "gpt-test",
		Stream: false, streamSet: true, Timeout: time.Second,
	})
	events, err := client.StreamMessage(context.Background(), []message.Message{{Role: message.RoleUser, Content: "hello"}}, nil)
	if err != nil {
		t.Fatalf("StreamMessage() error = %v", err)
	}
	var text string
	var calls []message.ToolCall
	var done bool
	for event := range events {
		text += event.Delta
		calls = append(calls, event.ToolCalls...)
		done = done || event.Done
	}
	if text != "non-stream" || !done || len(calls) != 1 || calls[0].ID != "call_read" {
		t.Fatalf("text=%q done=%v calls=%#v", text, done, calls)
	}
}

func TestResponsesExtraBodyProtectsInput(t *testing.T) {
	client := NewClient(Config{Transport: "openai-responses", Model: "gpt-test", Models: []string{"gpt-test"}})
	err := client.ApplyModelConfig(Config{
		Transport: "openai-responses", Model: "gpt-test", Models: []string{"gpt-test"},
		ExtraBody: RequestBody{"input": "must-not-override"},
	})
	if err == nil || !strings.Contains(err.Error(), `contains protected field "input"`) {
		t.Fatalf("error = %v", err)
	}
}

func TestNonStreamingResponsesSurfacesInvalidArgumentsAsRetryableToolCall(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"status":"completed","output":[{"type":"function_call","id":"fc_bad","call_id":"call_bad","name":"Read","arguments":"{\"file_path\":"}]}`)
	}))
	defer server.Close()

	client := NewClient(Config{
		Transport: "openai-responses", APIBaseURL: server.URL, APIPath: "/responses", Model: "gpt-test",
		Stream: false, streamSet: true, Timeout: time.Second,
	})
	events, err := client.StreamMessage(context.Background(), []message.Message{{Role: message.RoleUser, Content: "read"}}, nil)
	if err != nil {
		t.Fatalf("StreamMessage() error = %v", err)
	}
	var calls []message.ToolCall
	var done bool
	for event := range events {
		calls = append(calls, event.ToolCalls...)
		done = done || event.Done
	}
	if len(calls) != 1 || !strings.Contains(calls[0].InputError, "invalid JSON object arguments") || !done {
		t.Fatalf("calls=%#v done=%v", calls, done)
	}
}

func TestResponsesProviderDataPreservesUnknownOutputItems(t *testing.T) {
	items := []json.RawMessage{
		json.RawMessage(`{"type":"reasoning","id":"rs_1","encrypted_content":"cipher"}`),
		json.RawMessage(`{"type":"future_item","id":"future_1","payload":{"x":1}}`),
	}
	encoded, err := encodeResponsesProviderData(items)
	if err != nil {
		t.Fatalf("encodeResponsesProviderData() error = %v", err)
	}
	decoded, ok := decodeResponsesProviderData(encoded)
	if !ok {
		t.Fatal("decodeResponsesProviderData() rejected valid envelope")
	}
	if len(decoded) != 2 || !bytes.Contains(decoded[1], []byte(`"future_item"`)) {
		t.Fatalf("decoded items = %s", decoded)
	}
}

func TestDecodeResponsesProviderDataRejectsWrongTransportAndVersion(t *testing.T) {
	tests := []json.RawMessage{
		json.RawMessage(`{"transport":"openai-compatible","version":1,"output_items":[]}`),
		json.RawMessage(`{"transport":"openai-responses","version":2,"output_items":[]}`),
		json.RawMessage(`{"transport":"openai-responses","version":1,"output_items":[[]]}`),
		json.RawMessage(`{"transport":"openai-responses","version":1,"output_items":[{}]}`),
	}
	for _, raw := range tests {
		if _, ok := decodeResponsesProviderData(raw); ok {
			t.Fatalf("accepted invalid provider data: %s", raw)
		}
	}
}

func TestBuildResponsesInputReplaysProviderOutputItems(t *testing.T) {
	assistant := message.Message{
		Role:    message.RoleAssistant,
		Content: "visible fallback",
		ToolUse: &message.ToolCall{ID: "call_1", Name: "Read", Input: json.RawMessage(`{"file_path":"README.md"}`)},
		ProviderData: json.RawMessage(`{
			"transport":"openai-responses",
			"version":1,
			"output_items":[
				{"type":"reasoning","id":"rs_1","encrypted_content":"cipher"},
				{"type":"function_call","id":"fc_1","call_id":"call_1","name":"Read","arguments":"{\"file_path\":\"README.md\"}"}
			]
		}`),
	}
	result := message.Message{
		Role:       message.RoleUser,
		ToolResult: &message.ToolResult{ToolUseID: "call_1", Content: "contents"},
	}

	items, err := buildResponsesInput([]message.Message{assistant, result})
	if err != nil {
		t.Fatalf("buildResponsesInput() error = %v", err)
	}
	if len(items) != 3 {
		t.Fatalf("items = %#v, want reasoning/function_call/function_call_output", items)
	}
	if got := string(items[0]); !strings.Contains(got, `"reasoning"`) {
		t.Fatalf("items[0] = %s, want reasoning item", got)
	}
	if got := string(items[1]); !strings.Contains(got, `"function_call"`) || !strings.Contains(got, `"call_1"`) {
		t.Fatalf("items[1] = %s, want function_call item", got)
	}
	if got := string(items[2]); !strings.Contains(got, `"function_call_output"`) || !strings.Contains(got, `"call_1"`) {
		t.Fatalf("items[2] = %s, want function_call_output item", got)
	}
}

func TestBuildResponsesInputFallsBackForLegacyAssistant(t *testing.T) {
	assistant := message.Message{
		Role:    message.RoleAssistant,
		Content: "visible fallback",
		ToolUse: &message.ToolCall{ID: "call_1", Name: "Read", Input: json.RawMessage(`{"file_path":"README.md"}`)},
	}
	result := message.Message{
		Role:       message.RoleUser,
		ToolResult: &message.ToolResult{ToolUseID: "call_1", Content: "contents"},
	}

	items, err := buildResponsesInput([]message.Message{assistant, result})
	if err != nil {
		t.Fatalf("buildResponsesInput() error = %v", err)
	}
	if len(items) != 3 {
		t.Fatalf("items = %#v, want assistant message/function_call/function_call_output", items)
	}
	if got := string(items[0]); !strings.Contains(got, `"role":"assistant"`) {
		t.Fatalf("items[0] = %s, want assistant message fallback", got)
	}
	if got := string(items[1]); !strings.Contains(got, `"function_call"`) || !strings.Contains(got, `"call_1"`) {
		t.Fatalf("items[1] = %s, want function_call fallback", got)
	}
	if got := string(items[2]); !strings.Contains(got, `"function_call_output"`) || !strings.Contains(got, `"call_1"`) {
		t.Fatalf("items[2] = %s, want function_call_output fallback", got)
	}
}

func TestBuildResponsesInputFallsBackForDamagedProviderData(t *testing.T) {
	assistant := message.Message{
		Role:         message.RoleAssistant,
		Content:      "visible fallback",
		ToolUse:      &message.ToolCall{ID: "call_1", Name: "Read", Input: json.RawMessage(`{"file_path":"README.md"}`)},
		ProviderData: json.RawMessage(`{"transport":"openai-responses","version":99,"output_items":[{"type":"function_call","id":"fc_1","call_id":"call_1","name":"Read","arguments":"{\"file_path\":\"README.md\"}"}]}`),
	}

	items, err := buildResponsesInput([]message.Message{assistant})
	if err != nil {
		t.Fatalf("buildResponsesInput() error = %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("items = %#v, want fallback assistant message + function_call", items)
	}
	if got := string(items[0]); !strings.Contains(got, `"role":"assistant"`) {
		t.Fatalf("items[0] = %s, want assistant message fallback", got)
	}
	if got := string(items[1]); !strings.Contains(got, `"function_call"`) {
		t.Fatalf("items[1] = %s, want function_call fallback", got)
	}
}

func TestResponsesRequestToolsStrictFalse(t *testing.T) {
	var captured map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, `data: {"type":"response.completed","response":{"output":[]}}`+"\n\n")
	}))
	defer server.Close()

	client := NewClient(Config{
		Transport: "openai-responses", APIBaseURL: server.URL, APIPath: "/responses", Model: "gpt-test", Timeout: time.Second,
	})
	events, err := client.StreamMessage(context.Background(), []message.Message{{Role: message.RoleUser, Content: "hello"}}, []ToolDefinition{{
		Name: "LS", Description: "list files", InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}}}`),
	}})
	if err != nil {
		t.Fatalf("StreamMessage() error = %v", err)
	}
	for range events {
	}
	tools, ok := captured["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("tools = %#v", captured["tools"])
	}
	tool := tools[0].(map[string]any)
	strict, ok := tool["strict"]
	if !ok || strict != false {
		t.Fatalf("tool strict = %#v, want explicit false", strict)
	}
}

func TestStreamingResponsesFinalEventCarriesReasoningProviderData(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, `data: {"type":"response.output_item.done","output_index":0,"item":{"type":"reasoning","id":"rs_1","encrypted_content":"cipher"}}`+"\n\n")
		_, _ = fmt.Fprint(w, `data: {"type":"response.output_item.done","output_index":1,"item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"Read","arguments":"{\"file_path\":\"README.md\"}"}}`+"\n\n")
		_, _ = fmt.Fprint(w, `data: {"type":"response.completed","response":{"output":[{"type":"reasoning","id":"rs_1","encrypted_content":"cipher"},{"type":"function_call","id":"fc_1","call_id":"call_1","name":"Read","arguments":"{\"file_path\":\"README.md\"}"}],"usage":{"input_tokens":10,"output_tokens":2,"total_tokens":12}}}`+"\n\n")
	}))
	defer server.Close()

	client := NewClient(Config{
		Transport: "openai-responses", APIBaseURL: server.URL, APIPath: "/responses", Model: "gpt-test", Timeout: time.Second,
	})
	events, err := client.StreamMessage(context.Background(), []message.Message{{Role: message.RoleUser, Content: "read"}}, nil)
	if err != nil {
		t.Fatalf("StreamMessage() error = %v", err)
	}
	var last *StreamEvent
	for event := range events {
		if event.Err != nil {
			t.Fatalf("stream error = %v", event.Err)
		}
		last = &event
	}
	if last == nil || !last.Done || len(last.ToolCalls) != 1 || len(last.ProviderData) == 0 || last.Usage == nil {
		t.Fatalf("final event = %#v, want Done with tool call, provider data and usage", last)
	}
	if last.ToolCalls[0].ID != "call_1" || last.ToolCalls[0].Name != "Read" {
		t.Fatalf("tool calls = %#v", last.ToolCalls)
	}
	items, ok := decodeResponsesProviderData(last.ProviderData)
	if !ok || len(items) != 2 {
		t.Fatalf("provider items = %s, ok=%v", last.ProviderData, ok)
	}
	if !bytes.Contains(items[0], []byte(`"reasoning"`)) || !bytes.Contains(items[0], []byte("cipher")) {
		t.Fatalf("items[0] = %s, want reasoning with encrypted_content preserved", items[0])
	}
	if !bytes.Contains(items[1], []byte(`"function_call"`)) {
		t.Fatalf("items[1] = %s, want function_call", items[1])
	}
}

func TestStreamingResponsesRejectsTruncatedStream(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, `data: {"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"Read","arguments":""}}`+"\n\n")
		_, _ = fmt.Fprint(w, `data: {"type":"response.function_call_arguments.delta","output_index":0,"item_id":"fc_1","delta":"{\"file_path\":"}`+"\n\n")
		// EOF without response.completed
	}))
	defer server.Close()

	client := NewClient(Config{
		Transport: "openai-responses", APIBaseURL: server.URL, APIPath: "/responses", Model: "gpt-test", Timeout: time.Second,
	})
	events, err := client.StreamMessage(context.Background(), []message.Message{{Role: message.RoleUser, Content: "read"}}, nil)
	if err != nil {
		t.Fatalf("StreamMessage() error = %v", err)
	}
	var sawDone, sawToolCalls bool
	var gotErr error
	for event := range events {
		sawDone = sawDone || event.Done
		sawToolCalls = sawToolCalls || len(event.ToolCalls) != 0
		if event.Err != nil {
			gotErr = event.Err
		}
	}
	if sawDone || sawToolCalls {
		t.Fatal("truncated Responses stream completed or emitted tools")
	}
	if gotErr == nil || !strings.Contains(gotErr.Error(), "before response.completed") {
		t.Fatalf("error = %v, want truncation error", gotErr)
	}
}

func TestNonStreamingResponsesRejectsIncompleteStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"status":"incomplete","output":[{"type":"message","content":[{"type":"output_text","text":"partial"}]}]}`)
	}))
	defer server.Close()

	client := NewClient(Config{
		Transport: "openai-responses", APIBaseURL: server.URL, APIPath: "/responses", Model: "gpt-test",
		Stream: false, streamSet: true, Timeout: time.Second,
	})
	events, err := client.StreamMessage(context.Background(), []message.Message{{Role: message.RoleUser, Content: "hello"}}, nil)
	if err == nil {
		for range events {
		}
		t.Fatal("incomplete non-streaming Responses response was accepted")
	}
	if !strings.Contains(err.Error(), "未完成") && !strings.Contains(err.Error(), "incomplete") {
		t.Fatalf("error = %v, want incomplete status error", err)
	}
}

func TestStreamingResponsesSurfacesInvalidArgumentsAsRetryableToolCall(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, `data: {"type":"response.output_item.done","output_index":0,"item":{"type":"function_call","id":"fc_bad","call_id":"call_bad","name":"Read","arguments":"[]"}}`+"\n\n")
		_, _ = fmt.Fprint(w, `data: {"type":"response.completed","response":{}}`+"\n\n")
	}))
	defer server.Close()

	client := NewClient(Config{
		Transport: "openai-responses", APIBaseURL: server.URL, APIPath: "/responses", Model: "gpt-test", Timeout: time.Second,
	})
	events, err := client.StreamMessage(context.Background(), []message.Message{{Role: message.RoleUser, Content: "read"}}, nil)
	if err != nil {
		t.Fatalf("StreamMessage() error = %v", err)
	}
	var calls []message.ToolCall
	var sawDone bool
	for event := range events {
		calls = append(calls, event.ToolCalls...)
		sawDone = sawDone || event.Done
	}
	if len(calls) != 1 || !strings.Contains(calls[0].InputError, "invalid JSON object arguments") || !sawDone {
		t.Fatalf("calls=%#v done=%v", calls, sawDone)
	}
}

func TestStreamingResponsesRecoversToolCallFromArgumentsDelta(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, `data: {"type":"response.function_call_arguments.delta","output_index":0,"item_id":"fc_delta","call_id":"call_delta","name":"Read","delta":"{\"file_path\":\"go.mod\"}"}`+"\n\n")
		_, _ = fmt.Fprint(w, `data: {"type":"response.completed","response":{}}`+"\n\n")
	}))
	defer server.Close()

	client := NewClient(Config{
		Transport: "openai-responses", APIBaseURL: server.URL, APIPath: "/responses", Model: "gpt-test", Timeout: time.Second,
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
	if !sawDone || len(calls) != 1 {
		t.Fatalf("done=%v calls=%#v, want one recovered call", sawDone, calls)
	}
	if calls[0].ID != "call_delta" || calls[0].Name != "Read" || string(calls[0].Input) != `{"file_path":"go.mod"}` {
		t.Fatalf("recovered call = %#v", calls[0])
	}
}

func TestStreamingResponsesDeltaOverridesEmptyCompletedArguments(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, `data: {"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","id":"fc_empty","call_id":"call_empty","name":"Read","arguments":""}}`+"\n\n")
		_, _ = fmt.Fprint(w, `data: {"type":"response.function_call_arguments.delta","output_index":0,"item_id":"fc_empty","delta":"{\"file_path\":\"fresh.go\"}"}`+"\n\n")
		_, _ = fmt.Fprint(w, `data: {"type":"response.output_item.done","output_index":0,"item":{"type":"function_call","id":"fc_empty","call_id":"call_empty","name":"Read","arguments":""}}`+"\n\n")
		_, _ = fmt.Fprint(w, `data: {"type":"response.completed","response":{"output":[{"type":"function_call","id":"fc_empty","call_id":"call_empty","name":"Read","arguments":""}]}}`+"\n\n")
	}))
	defer server.Close()

	client := NewClient(Config{
		Transport: "openai-responses", APIBaseURL: server.URL, APIPath: "/responses", Model: "gpt-test", Timeout: time.Second,
	})
	events, err := client.StreamMessage(context.Background(), []message.Message{{Role: message.RoleUser, Content: "read"}}, nil)
	if err != nil {
		t.Fatalf("StreamMessage() error = %v", err)
	}
	var calls []message.ToolCall
	for event := range events {
		if event.Err != nil {
			t.Fatalf("stream error = %v", event.Err)
		}
		calls = append(calls, event.ToolCalls...)
	}
	if len(calls) != 1 || string(calls[0].Input) != `{"file_path":"fresh.go"}` {
		t.Fatalf("calls = %#v, want delta arguments", calls)
	}
}
