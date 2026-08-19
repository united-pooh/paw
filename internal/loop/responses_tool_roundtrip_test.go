package loop

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"paw/internal/message"
	"paw/internal/model"
	"paw/internal/tool"
)

func TestRenderMessageForModelPreservesNativeToolStructures(t *testing.T) {
	call := message.ToolCall{ID: "call-1", Name: "LS", Input: json.RawMessage(`{"path":"."}`)}
	renderedCall := renderMessageForModel(buildAssistantToolCallMessage([]message.ToolCall{call}))
	if calls := toolCallsFromMessage(renderedCall); len(calls) != 1 || calls[0].ID != call.ID {
		t.Fatalf("rendered tool calls = %#v", calls)
	}

	result := message.ToolResult{ToolUseID: call.ID, Content: "README.md"}
	renderedResult := renderMessageForModel(buildToolResultsMessage([]message.ToolResult{result}))
	if results := toolResultsFromMessage(renderedResult); len(results) != 1 || results[0].ToolUseID != call.ID {
		t.Fatalf("rendered tool results = %#v", results)
	}
	if renderedResult.Content == "" {
		t.Fatal("Chat-compatible textual tool-result fallback was removed")
	}
}

func TestDeepSeekResponsesRunnerSendsFunctionCallOutputOnSecondRound(t *testing.T) {
	var requestMu sync.Mutex
	requestCount := 0
	var secondInput []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		defer request.Body.Close()
		var body struct {
			Input []map[string]any `json:"input"`
			Tools []map[string]any `json:"tools"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Errorf("decode Responses request: %v", err)
			return
		}
		requestMu.Lock()
		requestCount++
		current := requestCount
		if current == 2 {
			secondInput = append([]map[string]any(nil), body.Input...)
		}
		requestMu.Unlock()

		w.Header().Set("Content-Type", "text/event-stream")
		var output []map[string]any
		if current == 1 {
			output = []map[string]any{{
				"type": "function_call", "id": "fc-1", "call_id": "call-1",
				"name": "LS", "arguments": `{"path":"."}`,
			}}
		} else {
			output = []map[string]any{{
				"type": "message", "role": "assistant",
				"content": []map[string]any{{"type": "output_text", "text": "done"}},
			}}
		}
		payload, err := json.Marshal(map[string]any{
			"type":     "response.completed",
			"response": map[string]any{"status": "completed", "output": output},
		})
		if err != nil {
			t.Errorf("encode Responses event: %v", err)
			return
		}
		_, _ = fmt.Fprintf(w, "data: %s\n\n", payload)
	}))
	defer server.Close()

	client := model.NewClient(model.Config{
		Provider: "DeepSeek", Transport: "openai-responses", Model: "deepseek-v4-flash",
		APIBaseURL: server.URL, APIPath: "/responses", Stream: true, Timeout: time.Second,
	})
	registry := tool.NewRegistry()
	registry.Register(&fakeTool{name: "LS", output: "README.md", schema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}}}`)})
	runner := NewEngine(client, &fakeUI{}, registry, nil, "")

	response, err := runner.RunTurn(context.Background(), "list files")
	if err != nil {
		t.Fatalf("RunTurn() error = %v", err)
	}
	if response.Content != "done" {
		t.Fatalf("response = %#v", response)
	}

	requestMu.Lock()
	defer requestMu.Unlock()
	if requestCount != 2 {
		t.Fatalf("request count = %d, want 2", requestCount)
	}
	callIndex, outputIndex := -1, -1
	for index, item := range secondInput {
		switch item["type"] {
		case "function_call":
			if item["call_id"] == "call-1" {
				callIndex = index
			}
		case "function_call_output":
			if item["call_id"] == "call-1" {
				outputIndex = index
			}
		}
	}
	if callIndex < 0 || outputIndex != callIndex+1 {
		data, _ := json.Marshal(secondInput)
		t.Fatalf("second Responses input has no adjacent function_call_output: %s", data)
	}
}
