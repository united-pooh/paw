package model

import (
	"encoding/json"
	"strings"
	"testing"

	"paw/internal/message"
)

func TestSelectModelAdapter(t *testing.T) {
	tests := []struct {
		name     string
		cfg      Config
		wantName string
	}{
		{"deepseek provider", Config{Provider: " DeepSeek "}, "deepseek"},
		{"deepseek model wins", Config{Provider: "openrouter", Model: "deepseek-chat"}, "deepseek"},
		{"openai provider", Config{Provider: " OpenAI "}, "gpt"},
		{"gpt provider", Config{Provider: "gpt"}, "gpt"},
		{"gpt model", Config{Provider: "gateway", Model: "gpt-4.1"}, "gpt"},
		{"o model is compatible", Config{Model: "o3"}, "openai-compatible"},
		{"fallback", Config{Provider: "gateway", Model: "claude-3"}, "openai-compatible"},
		{"explicit adapter wins", Config{Provider: "openai", Model: "gpt-5", Adapter: "deepseek"}, "deepseek"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SelectModelAdapter(tt.cfg).Name(); got != tt.wantName {
				t.Fatalf("adapter = %q, want %q", got, tt.wantName)
			}
		})
	}
}

func TestDeepSeekAdapterBuildsStrictToolsWithoutMutatingInput(t *testing.T) {
	raw := json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"}}}`)
	before := append(json.RawMessage(nil), raw...)
	adapter := DeepSeekAdapter{}
	prepared, err := adapter.PrepareTools([]ToolDefinition{{Name: "Search", Description: "search", InputSchema: raw}})
	if err != nil {
		t.Fatalf("PrepareTools() error = %v", err)
	}
	req, err := adapter.BuildChatCompletionsRequest(
		Config{Model: "deepseek-chat"},
		[]message.Message{{Role: message.RoleUser, Content: "hi"}},
		prepared,
		true,
	)
	if err != nil {
		t.Fatalf("BuildChatCompletionsRequest() error = %v", err)
	}
	if len(req.Tools) != 1 || !req.Tools[0].Function.Strict {
		t.Fatalf("tools = %#v, want one strict tool", req.Tools)
	}
	if string(raw) != string(before) {
		t.Fatalf("input schema mutated: %s", raw)
	}
	var schema map[string]any
	if err := json.Unmarshal(req.Tools[0].Function.Parameters, &schema); err != nil {
		t.Fatal(err)
	}
	if schema["additionalProperties"] != false {
		t.Fatalf("additionalProperties = %#v, want false", schema["additionalProperties"])
	}
	if got := schema["required"].([]any); len(got) != 1 || got[0] != "query" {
		t.Fatalf("required = %#v, want query", got)
	}
}

func TestFilterChatCompletionsExtraRequestBody(t *testing.T) {
	body := RequestBody{
		"tools":       []any{"bad"},
		"tool_choice": "bad",
		"function":    RequestBody{"strict": false},
		"strict":      false,
		"parameters":  RequestBody{"type": "string"},
		"temperature": 0.2,
	}
	filtered := FilterChatCompletionsExtraRequestBody(body)
	for _, key := range []string{"tools", "tool_choice", "function", "strict", "parameters"} {
		if _, ok := filtered[key]; ok {
			t.Errorf("filtered body contains protected tool field %q", key)
		}
	}
	if filtered["temperature"] != 0.2 {
		t.Fatalf("temperature = %#v, want 0.2", filtered["temperature"])
	}
	if _, ok := body["tools"]; !ok {
		t.Fatal("filter mutated input")
	}
}

func TestDeepSeekAdapterIncludesToolNameInSchemaErrors(t *testing.T) {
	_, err := (DeepSeekAdapter{}).PrepareTools(
		[]ToolDefinition{{Name: "BadTool", InputSchema: json.RawMessage("[]")}},
	)
	if err == nil || !strings.Contains(err.Error(), `工具 "BadTool"`) {
		t.Fatalf("error = %v, want tool name", err)
	}
}

func TestOpenAICompatibleAdapterBuildsResponsesRequest(t *testing.T) {
	adapter := OpenAICompatibleAdapter{}
	req, err := adapter.BuildResponsesRequest(
		Config{Provider: "openai", Adapter: "gpt", Model: "gpt-test"},
		[]message.Message{{Role: message.RoleUser, Content: "hello"}},
		nil,
		true,
	)
	if err != nil {
		t.Fatalf("BuildResponsesRequest() error = %v", err)
	}
	if req.Model != "gpt-test" || !req.Stream {
		t.Fatalf("request = %#v", req)
	}
	if req.Reasoning["summary"] != "auto" {
		t.Fatalf("reasoning = %#v, want summary=auto", req.Reasoning)
	}
}
