package model

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"paw/internal/message"
)

func TestDeepSeekResponsesSerializesEveryPreparedToolAsStrict(t *testing.T) {
	originals := []ToolDefinition{
		{Name: "empty", InputSchema: json.RawMessage(`{"type":"object","properties":{}}`)},
		{Name: "optional", InputSchema: json.RawMessage(`{"type":"object","properties":{"uri":{"type":"string","format":"uri","default":"https://example.com"},"enabled":{"type":"boolean","default":true},"items":{"type":"array","items":{"type":"string"},"maxItems":3}}}`)},
		{Name: "complex", InputSchema: json.RawMessage(`{"type":"object","properties":{"choice":{"oneOf":[{"type":"string"},{"type":"object"}]}}}`)},
	}
	before := make([]string, len(originals))
	for i := range originals {
		before[i] = string(originals[i].InputSchema)
	}

	var captured responsesRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Errorf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}]}`)
	}))
	defer server.Close()

	client := NewClient(Config{
		Provider: "gateway", Model: "  DeepSeek-v4-flash  ", Transport: "openai-responses",
		APIBaseURL: server.URL, APIPath: "/responses", Stream: false, streamSet: true, Timeout: time.Second,
	})
	events, err := client.StreamMessage(context.Background(), []message.Message{{Role: message.RoleUser, Content: "hi"}}, originals)
	if err != nil {
		t.Fatal(err)
	}
	for range events {
	}
	if len(captured.Tools) != len(originals) {
		t.Fatalf("tools=%d, want %d", len(captured.Tools), len(originals))
	}
	for i, tool := range captured.Tools {
		if !tool.Strict || !json.Valid(tool.Parameters) {
			t.Fatalf("tool %s strict=%v parameters=%s", tool.Name, tool.Strict, tool.Parameters)
		}
		if string(originals[i].InputSchema) != before[i] {
			t.Fatalf("original schema %s mutated", originals[i].Name)
		}
	}
}

func TestDeepSeekResponsesRestoresCodecArgumentsAndPreservesRawProviderData(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%v", stream), func(t *testing.T) {
			wireArguments := `{"config":"{\"x\":1}"}`
			item, err := json.Marshal(map[string]any{
				"type": "function_call", "id": "fc-1", "call_id": "call-1", "name": "Configure", "arguments": wireArguments,
			})
			if err != nil {
				t.Fatal(err)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				defer r.Body.Close()
				w.Header().Set("Content-Type", "application/json")
				if stream {
					w.Header().Set("Content-Type", "text/event-stream")
					completed, _ := json.Marshal(map[string]any{
						"type":     "response.completed",
						"response": map[string]any{"status": "completed", "output": []json.RawMessage{item}},
					})
					_, _ = fmt.Fprintf(w, "data: %s\n\n", completed)
					return
				}
				response, _ := json.Marshal(map[string]any{"status": "completed", "output": []json.RawMessage{item}})
				_, _ = w.Write(response)
			}))
			defer server.Close()
			client := NewClient(Config{
				Provider: "DeepSeek", Model: "deepseek-test", Transport: "openai-responses",
				APIBaseURL: server.URL, APIPath: "/responses", Stream: stream, streamSet: true, Timeout: time.Second,
			})
			definitions := []ToolDefinition{{
				Name: "Configure", InputSchema: json.RawMessage(`{"type":"object","properties":{"config":{"type":"object"}},"required":["config"]}`),
			}}
			events, err := client.StreamMessage(context.Background(), []message.Message{{Role: message.RoleUser, Content: "configure"}}, definitions)
			if err != nil {
				t.Fatal(err)
			}
			var calls []message.ToolCall
			var providerData json.RawMessage
			for event := range events {
				calls = append(calls, event.ToolCalls...)
				if len(event.ProviderData) != 0 {
					providerData = append(json.RawMessage(nil), event.ProviderData...)
				}
			}
			if len(calls) != 1 || calls[0].InputError != "" {
				t.Fatalf("calls=%#v", calls)
			}
			assertJSONEqual(t, calls[0].Input, json.RawMessage(`{"config":{"x":1}}`))
			items, ok := decodeResponsesProviderData(providerData)
			if !ok || len(items) != 1 {
				t.Fatalf("ProviderData=%s", providerData)
			}
			var rawView responsesOutputItemView
			if err := json.Unmarshal(items[0], &rawView); err != nil {
				t.Fatal(err)
			}
			if rawView.Arguments != wireArguments {
				t.Fatalf("raw arguments=%q, want %q", rawView.Arguments, wireArguments)
			}
		})
	}
}

func TestDeepSeekCodecRoundTripsWireArguments(t *testing.T) {
	tests := []struct {
		name   string
		schema string
		wire   string
		want   string
	}{
		{"closed empty placeholder", `{"type":"object","properties":{},"additionalProperties":false}`, `{"__paw_empty":true}`, `{}`},
		{"optional null omitted", `{"type":"object","properties":{"name":{"type":"string"}}}`, `{"name":null}`, `{}`},
		{"nested free map", `{"type":"object","properties":{"config":{"type":"object"}},"required":["config"]}`, `{"config":"{\"x\":1}"}`, `{"config":{"x":1}}`},
		{"array item codec", `{"type":"object","properties":{"items":{"type":"array","items":{"type":"object"}}},"required":["items"]}`, `{"items":["{\"x\":1}","{\"y\":true}"]}`, `{"items":[{"x":1},{"y":true}]}`},
		{"oneOf codec", `{"type":"object","properties":{"value":{"oneOf":[{"type":"string"},{"type":"number"}]}},"required":["value"]}`, `{"value":"\"hello\""}`, `{"value":"hello"}`},
		{"root free map envelope", `{"type":"object"}`, `{"__paw_arguments_json":"{\"x\":1}"}`, `{"x":1}`},
		{"additionalProperties schema", `{"type":"object","properties":{"labels":{"type":"object","additionalProperties":{"type":"string"}}},"required":["labels"]}`, `{"labels":"{\"a\":\"b\"}"}`, `{"labels":{"a":"b"}}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prepared, err := prepareDeepSeekTools([]ToolDefinition{{Name: "Tool", InputSchema: json.RawMessage(tt.schema)}})
			if err != nil {
				t.Fatal(err)
			}
			got, err := restoreToolArguments(prepared[0], json.RawMessage(tt.wire))
			if err != nil {
				t.Fatal(err)
			}
			assertJSONEqual(t, got, json.RawMessage(tt.want))
		})
	}
}

func TestDeepSeekCodecAcceptsNativeResponsesFallback(t *testing.T) {
	prepared, err := prepareDeepSeekTools([]ToolDefinition{{
		Name: "Tool", InputSchema: json.RawMessage(`{"type":"object","properties":{"config":{"type":"object"}},"required":["config"]}`),
	}})
	if err != nil {
		t.Fatal(err)
	}
	got, err := restoreToolArguments(prepared[0], json.RawMessage(`{"config":{"native":true}}`))
	if err != nil {
		t.Fatal(err)
	}
	assertJSONEqual(t, got, json.RawMessage(`{"config":{"native":true}}`))
}

func TestDeepSeekWireDropsValidationKeywordsButOriginalValidatorEnforcesThem(t *testing.T) {
	prepared, err := prepareDeepSeekTools([]ToolDefinition{{
		Name: "Select", InputSchema: json.RawMessage(`{"type":"object","properties":{"items":{"type":"array","items":{"type":"string"},"minItems":1,"maxItems":1},"uri":{"type":"string","format":"uri","default":"https://example.com"},"mode":{"type":"string","not":{"const":"blocked"}}},"required":["items"]}`),
	}})
	if err != nil {
		t.Fatal(err)
	}
	wire := string(prepared[0].Parameters)
	for _, keyword := range []string{"minItems", "maxItems", "format", "default", "not"} {
		if strings.Contains(wire, `"`+keyword+`"`) {
			t.Fatalf("wire schema retained %s: %s", keyword, wire)
		}
	}
	for _, input := range []string{
		`{"items":[]}`,
		`{"items":["a","b"]}`,
		`{"items":["a"],"mode":"blocked"}`,
	} {
		if _, err := restoreToolArguments(prepared[0], json.RawMessage(input)); err == nil {
			t.Fatalf("input %s passed original validation", input)
		}
	}
}

func TestDeepSeekCodecKeepsRemoteAndRecursiveSchemasCallable(t *testing.T) {
	schemas := []string{
		`{"type":"object","properties":{"value":{"$ref":"https://example.invalid/schema.json"}}}`,
		`{"$defs":{"Node":{"type":"object","properties":{"next":{"$ref":"#/$defs/Node"}}}},"$ref":"#/$defs/Node"}`,
		`{"type":"object","patternProperties":{"^x-":{"type":"string"}}}`,
		`{"type":"object","properties":{"value":{"type":"string","x-service-rule":true}}}`,
	}
	for i, schema := range schemas {
		prepared, err := prepareDeepSeekTools([]ToolDefinition{{Name: fmt.Sprintf("Tool%d", i), InputSchema: json.RawMessage(schema)}})
		if err != nil {
			t.Fatalf("schema %d was hidden: %v", i, err)
		}
		if len(prepared) != 1 || !prepared[0].Strict || !json.Valid(prepared[0].Parameters) {
			t.Fatalf("prepared schema %d = %#v", i, prepared)
		}
		if (i == 0 || i == 3) && prepared[0].validator != nil {
			t.Fatalf("schema %d should delegate final validation", i)
		}
	}
}

func TestOriginalSchemaValidatorHonorsDeclaredDialect(t *testing.T) {
	prepared, err := prepareDeepSeekTools([]ToolDefinition{{
		Name: "Draft4", InputSchema: json.RawMessage(`{"$schema":"http://json-schema.org/draft-04/schema#","type":"object","properties":{"value":{"type":"number","minimum":0,"exclusiveMinimum":true}},"required":["value"]}`),
	}})
	if err != nil {
		t.Fatal(err)
	}
	if prepared[0].validator == nil {
		t.Fatal("draft-04 schema was not compiled")
	}
	if _, err := restoreToolArguments(prepared[0], json.RawMessage(`{"value":0}`)); err == nil {
		t.Fatal("draft-04 exclusiveMinimum was not enforced")
	}
	if _, err := restoreToolArguments(prepared[0], json.RawMessage(`{"value":1}`)); err != nil {
		t.Fatalf("valid draft-04 input rejected: %v", err)
	}
}

func TestRootCodecEnvelopeAvoidsOriginalPropertyCollision(t *testing.T) {
	prepared, err := prepareDeepSeekTools([]ToolDefinition{{
		Name: "Collision", InputSchema: json.RawMessage(`{"type":"object","properties":{"__paw_arguments_json":{"type":"string"}},"allOf":[{"type":"object"}]}`),
	}})
	if err != nil {
		t.Fatal(err)
	}
	wire := decodeSchema(t, prepared[0].Parameters)
	properties := wire["properties"].(map[string]any)
	if _, exists := properties[deepSeekEnvelopeProperty+"_2"]; !exists {
		t.Fatalf("collision-safe envelope missing: %#v", properties)
	}
}

func TestCurrentCodeGraphJinaAndVirtualMCPSchemaShapesPrepare(t *testing.T) {
	definitions := []ToolDefinition{
		{Name: "codegraph__status", InputSchema: json.RawMessage(`{"type":"object","properties":{}}`)},
		{Name: "codegraph__explore", InputSchema: json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"},"depth":{"type":"number","default":2},"include_tests":{"type":"boolean","default":false}}}`)},
		{Name: "jina__search", InputSchema: json.RawMessage(`{"type":"object","properties":{"query":{"anyOf":[{"type":"string"},{"type":"array","items":{"type":"string"}}]},"site":{"type":"string","format":"uri"},"max_results":{"type":"array","items":{"type":"string"},"maxItems":10}},"required":["query"]}`)},
		{Name: "jina__reader", InputSchema: json.RawMessage(`{"type":"object","properties":{"url":{"type":"string","format":"uri"},"with_links":{"type":"boolean","default":true}},"required":["url"]}`)},
		{Name: "codegraph__get_prompt", InputSchema: json.RawMessage(`{"type":"object","properties":{"name":{"type":"string"},"arguments":{"type":"object"}},"required":["name"]}`)},
	}
	prepared, err := (DeepSeekAdapter{}).PrepareTools(definitions)
	if err != nil {
		t.Fatal(err)
	}
	if len(prepared) != len(definitions) {
		t.Fatalf("prepared=%d definitions=%d", len(prepared), len(definitions))
	}
	for _, tool := range prepared {
		if !tool.Strict || !json.Valid(tool.Parameters) {
			t.Fatalf("tool %s was not prepared: %s", tool.Name, tool.Parameters)
		}
	}
}

func TestDeepSeekCodecSchemaSummaryIsBounded(t *testing.T) {
	longDescription := strings.Repeat("长", deepSeekSchemaSummaryLimit)
	prepared, err := prepareDeepSeekTools([]ToolDefinition{{
		Name: "Bounded", InputSchema: json.RawMessage(`{"type":"object","properties":{"map":{"type":"object","description":` + mustJSONString(t, longDescription) + `}}}`),
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(prepared[0].Parameters) > deepSeekSchemaSummaryLimit+1024 {
		t.Fatalf("wire schema grew without bound: %d bytes", len(prepared[0].Parameters))
	}
}

func TestPreparedToolCallFailureIsIsolated(t *testing.T) {
	prepared, err := prepareDeepSeekTools([]ToolDefinition{{
		Name: "Map", InputSchema: json.RawMessage(`{"type":"object","properties":{"value":{"type":"object"}},"required":["value"]}`),
	}})
	if err != nil {
		t.Fatal(err)
	}
	calls := prepared.adaptCalls([]message.ToolCall{
		{ID: "bad", Name: "Map", Input: json.RawMessage(`{"value":"{"}`)},
		{ID: "good", Name: "Map", Input: json.RawMessage(`{"value":"{\"ok\":true}"}`)},
	})
	if calls[0].InputError == "" {
		t.Fatal("bad call has no retryable input error")
	}
	if calls[1].InputError != "" {
		t.Fatalf("good call error = %q", calls[1].InputError)
	}
	assertJSONEqual(t, calls[1].Input, json.RawMessage(`{"value":{"ok":true}}`))
}

type countingToolAdapter struct{ prepares *atomic.Int32 }

func (adapter countingToolAdapter) Name() string { return "counting" }
func (adapter countingToolAdapter) PrepareTools(tools []ToolDefinition) (PreparedToolSet, error) {
	adapter.prepares.Add(1)
	return preparePassthroughTools(tools), nil
}
func (countingToolAdapter) BuildChatCompletionsRequest(Config, []message.Message, PreparedToolSet, bool) (ChatCompletionsRequest, error) {
	return ChatCompletionsRequest{}, nil
}

func TestPreparedToolCatalogCacheUsesAdapterAndSchemaHash(t *testing.T) {
	client := NewClient(Config{})
	var prepares atomic.Int32
	adapter := countingToolAdapter{prepares: &prepares}
	definitions := []ToolDefinition{{Name: "Tool", InputSchema: json.RawMessage(`{"type":"object","properties":{}}`)}}
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := client.prepareTools(adapter, definitions); err != nil {
				t.Errorf("prepareTools: %v", err)
			}
		}()
	}
	wg.Wait()
	if prepares.Load() != 1 {
		t.Fatalf("prepares=%d, want 1", prepares.Load())
	}
	definitions[0].InputSchema = json.RawMessage(`{"type":"object","properties":{"changed":{"type":"boolean"}}}`)
	if _, err := client.prepareTools(adapter, definitions); err != nil {
		t.Fatal(err)
	}
	if prepares.Load() != 2 {
		t.Fatalf("prepares=%d after schema change, want 2", prepares.Load())
	}
}

func assertJSONEqual(t *testing.T, got, want json.RawMessage) {
	t.Helper()
	var gotValue, wantValue any
	if err := json.Unmarshal(got, &gotValue); err != nil {
		t.Fatalf("decode got %s: %v", got, err)
	}
	if err := json.Unmarshal(want, &wantValue); err != nil {
		t.Fatalf("decode want %s: %v", want, err)
	}
	gotData, _ := json.Marshal(gotValue)
	wantData, _ := json.Marshal(wantValue)
	if string(gotData) != string(wantData) {
		t.Fatalf("got %s, want %s", gotData, wantData)
	}
}

func mustJSONString(t *testing.T, value string) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
