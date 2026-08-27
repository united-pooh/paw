package model

import (
	"encoding/json"
	"testing"

	"paw/internal/message"
)

func TestCoerceValueToSchemaScalars(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"count":   map[string]any{"type": "integer"},
			"ratio":   map[string]any{"type": "number"},
			"enabled": map[string]any{"type": "boolean"},
			"name":    map[string]any{"type": "string"},
		},
	}
	value := map[string]any{
		"count":   "5",
		"ratio":   "0.5",
		"enabled": "true",
		"name":    "keep",
	}
	coerced, changed, err := coerceValueToSchema(value, schema)
	if err != nil {
		t.Fatalf("coerceValueToSchema() error = %v", err)
	}
	if !changed {
		t.Fatal("changed = false, want true")
	}
	object := coerced.(map[string]any)
	if object["count"] != float64(5) {
		t.Fatalf("count = %#v, want float64(5)", object["count"])
	}
	if object["ratio"] != 0.5 {
		t.Fatalf("ratio = %#v, want 0.5", object["ratio"])
	}
	if object["enabled"] != true {
		t.Fatalf("enabled = %#v, want true", object["enabled"])
	}
	if object["name"] != "keep" {
		t.Fatalf("name = %#v, want unchanged", object["name"])
	}
}

func TestCoerceValueToSchemaNestedAndArrays(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"filters": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"limit": map[string]any{"type": "integer"},
					},
				},
			},
		},
	}
	value := map[string]any{
		"filters": []any{
			map[string]any{"limit": "10"},
			map[string]any{"limit": "20"},
		},
	}
	coerced, changed, err := coerceValueToSchema(value, schema)
	if err != nil {
		t.Fatalf("coerceValueToSchema() error = %v", err)
	}
	if !changed {
		t.Fatal("changed = false, want true")
	}
	array := coerced.(map[string]any)["filters"].([]any)
	if array[0].(map[string]any)["limit"] != float64(10) || array[1].(map[string]any)["limit"] != float64(20) {
		t.Fatalf("coerced = %#v", array)
	}
}

func TestCoerceValueToSchemaAnyOfBranch(t *testing.T) {
	schema := map[string]any{
		"anyOf": []any{
			map[string]any{"type": "object", "properties": map[string]any{"n": map[string]any{"type": "integer"}}},
			map[string]any{"type": "string"},
		},
	}
	coerced, changed, err := coerceValueToSchema(map[string]any{"n": "7"}, schema)
	if err != nil {
		t.Fatalf("coerceValueToSchema() error = %v", err)
	}
	if !changed {
		t.Fatal("changed = false, want true")
	}
	if coerced.(map[string]any)["n"] != float64(7) {
		t.Fatalf("coerced = %#v", coerced)
	}
}

func TestCoerceValueToSchemaLeavesIncompatibleTypesAlone(t *testing.T) {
	schema := map[string]any{
		"type":       "object",
		"properties": map[string]any{"count": map[string]any{"type": "integer"}},
	}
	value := map[string]any{"count": "not-a-number"}
	coerced, changed, err := coerceValueToSchema(value, schema)
	if err != nil {
		t.Fatalf("coerceValueToSchema() error = %v", err)
	}
	if changed {
		t.Fatalf("changed = true, want false for incompatible value: %#v", coerced)
	}
}

func TestRestoreToolArgumentsCoercesStringNumbersBeforeValidation(t *testing.T) {
	prepared, err := prepareDeepSeekTools([]ToolDefinition{{
		Name: "Search",
		InputSchema: json.RawMessage(
			`{"type":"object","properties":{"limit":{"type":"integer"}},"required":["limit"],"additionalProperties":false}`,
		),
	}})
	if err != nil {
		t.Fatal(err)
	}
	calls := prepared.adaptCalls([]message.ToolCall{
		{ID: "call_1", Name: "Search", Input: json.RawMessage(`{"limit":"30"}`)},
	})
	if calls[0].InputError != "" {
		t.Fatalf("InputError = %q, want empty after coercion", calls[0].InputError)
	}
	assertJSONEqual(t, calls[0].Input, json.RawMessage(`{"limit":30}`))
}

func TestRestoreToolArgumentsStillFailsOnImpossibleCoercion(t *testing.T) {
	prepared, err := prepareDeepSeekTools([]ToolDefinition{{
		Name: "Search",
		InputSchema: json.RawMessage(
			`{"type":"object","properties":{"limit":{"type":"integer"}},"required":["limit"],"additionalProperties":false}`,
		),
	}})
	if err != nil {
		t.Fatal(err)
	}
	calls := prepared.adaptCalls([]message.ToolCall{
		{ID: "call_1", Name: "Search", Input: json.RawMessage(`{"limit":"many"}`)},
	})
	if calls[0].InputError == "" {
		t.Fatal("InputError empty, want retryable validation error")
	}
}
