package model

import (
	"encoding/json"
	"strings"
	"testing"
)

func decodeSchema(t *testing.T, raw json.RawMessage) map[string]any {
	t.Helper()
	var schema map[string]any
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatalf("decode schema: %v", err)
	}
	return schema
}

func TestNormalizeDeepSeekSchemaNestedOptionalAndArray(t *testing.T) {
	raw := json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"},"options":{"type":"object","properties":{"limit":{"type":"integer"}}},"items":{"type":"array","items":{"type":"object","properties":{"id":{"type":"string"}}}}},"required":["query"]}`)
	got, err := normalizeDeepSeekToolSchema("Search", raw)
	if err != nil {
		t.Fatalf("normalizeDeepSeekToolSchema() error = %v", err)
	}
	root := decodeSchema(t, got)
	if root["additionalProperties"] != false {
		t.Fatalf("root additionalProperties = %#v", root["additionalProperties"])
	}
	if want := `["items","options","query"]`; string(mustJSON(t, root["required"])) != want {
		t.Fatalf("root required = %s, want %s", mustJSON(t, root["required"]), want)
	}
	props := root["properties"].(map[string]any)
	options := props["options"].(map[string]any)
	if options["additionalProperties"] != false {
		t.Fatalf("nested additionalProperties = %#v", options["additionalProperties"])
	}
	limit := options["properties"].(map[string]any)["limit"].(map[string]any)
	if !containsNullType(limit) {
		t.Fatalf("optional nested property is not nullable: %#v", limit)
	}
	items := props["items"].(map[string]any)["items"].(map[string]any)
	if items["additionalProperties"] != false {
		t.Fatalf("array item additionalProperties = %#v", items["additionalProperties"])
	}
}

func TestNormalizeDeepSeekSchemaDefinitionsAndRefs(t *testing.T) {
	raw := json.RawMessage(`{"type":"object","properties":{"user":{"$ref":"#/$defs/User"}},"$defs":{"User":{"type":"object","properties":{"name":{"type":"string"}}}}}`)
	got, err := normalizeDeepSeekToolSchema("UserTool", raw)
	if err != nil {
		t.Fatalf("normalizeDeepSeekToolSchema() error = %v", err)
	}
	root := decodeSchema(t, got)
	user := root["properties"].(map[string]any)["user"].(map[string]any)
	if user["$ref"] != "#/$defs/User" {
		t.Fatalf("ref = %#v, want unchanged", user["$ref"])
	}
	defs := root["$defs"].(map[string]any)["User"].(map[string]any)
	if defs["additionalProperties"] != false {
		t.Fatalf("definition additionalProperties = %#v", defs["additionalProperties"])
	}
	if string(mustJSON(t, defs["required"])) != `["name"]` {
		t.Fatalf("definition required = %s", mustJSON(t, defs["required"]))
	}
}

func TestNormalizeDeepSeekSchemaRejectsUnsafeForms(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{"invalid json", `{`, "非法 JSON"},
		{"top-level array", `[]`, "必须是 JSON object"},
		{"dynamic additional properties", `{"type":"object","additionalProperties":true}`, "additionalProperties"},
		{"property is scalar", `{"type":"object","properties":{"x":1}}`, "properties"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := normalizeDeepSeekToolSchema("Bad", json.RawMessage(tt.raw))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want substring %q", err, tt.want)
			}
		})
	}
}

func TestNormalizeDeepSeekSchemaDoesNotMutateInput(t *testing.T) {
	raw := json.RawMessage(`{"type":"object","properties":{"optional":{"type":"string"}}}`)
	before := string(raw)
	if _, err := normalizeDeepSeekToolSchema("Immutable", raw); err != nil {
		t.Fatal(err)
	}
	if string(raw) != before {
		t.Fatalf("input schema mutated: %s", raw)
	}
}

func containsNullType(schema map[string]any) bool {
	if types, ok := schema["type"].([]any); ok {
		for _, typ := range types {
			if typ == "null" {
				return true
			}
		}
	}
	if values, ok := schema["anyOf"].([]any); ok {
		for _, value := range values {
			if item, ok := value.(map[string]any); ok && item["type"] == "null" {
				return true
			}
		}
	}
	return false
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal JSON: %v", err)
	}
	return data
}
