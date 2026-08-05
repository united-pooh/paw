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
	options := nullableSchema(t, props["options"].(map[string]any))
	if options["additionalProperties"] != false {
		t.Fatalf("nested additionalProperties = %#v", options["additionalProperties"])
	}
	limit := options["properties"].(map[string]any)["limit"].(map[string]any)
	if !containsNullType(limit) {
		t.Fatalf("optional nested property is not nullable: %#v", limit)
	}
	itemsProperty := props["items"].(map[string]any)
	if !containsNullType(itemsProperty) {
		t.Fatalf("optional array property is not nullable: %#v", itemsProperty)
	}
	items := nullableSchema(t, itemsProperty)["items"].(map[string]any)
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
	user := nullableSchema(t, root["properties"].(map[string]any)["user"].(map[string]any))
	if user["additionalProperties"] != false {
		t.Fatalf("resolved ref additionalProperties = %#v", user["additionalProperties"])
	}
	if string(mustJSON(t, user["required"])) != `["name"]` {
		t.Fatalf("resolved ref required = %s", mustJSON(t, user["required"]))
	}
}

func TestNormalizeDeepSeekSchemaDistinguishesClosedEmptyObjectAndFreeMap(t *testing.T) {
	closed, err := normalizeDeepSeekToolSchema("EmptyTool", nil)
	if err != nil {
		t.Fatal(err)
	}
	closedSchema := decodeSchema(t, closed)
	properties := closedSchema["properties"].(map[string]any)
	if properties[deepSeekPlaceholderProperty].(map[string]any)["type"] != "boolean" {
		t.Fatalf("closed empty schema placeholder = %#v", properties)
	}

	freeMap, err := normalizeDeepSeekToolSchema("MapTool", json.RawMessage(`{"type":"object"}`))
	if err != nil {
		t.Fatal(err)
	}
	freeMapSchema := decodeSchema(t, freeMap)
	envelope := freeMapSchema["properties"].(map[string]any)[deepSeekEnvelopeProperty].(map[string]any)
	if envelope["type"] != "string" {
		t.Fatalf("free-map envelope = %#v", envelope)
	}

	nested, err := normalizeDeepSeekToolSchema("NestedMap", json.RawMessage(`{"type":"object","properties":{"options":{"type":"object"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	nestedProperty := nullableSchema(t, decodeSchema(t, nested)["properties"].(map[string]any)["options"].(map[string]any))
	if nestedProperty["type"] != "string" {
		t.Fatalf("nested free-map codec = %#v", nestedProperty)
	}
}

func TestNormalizeDeepSeekSchemaRejectsInvalidDocuments(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{"invalid json", `{`, "非法 JSON"},
		{"top-level array", `[]`, "必须是 JSON object"},
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

func TestNormalizeDeepSeekSchemaCodecsUnsupportedSubtrees(t *testing.T) {
	got, err := normalizeDeepSeekToolSchema("Flexible", json.RawMessage(`{"type":"object","properties":{"map":{"type":"object","additionalProperties":{"type":"string"}},"choice":{"oneOf":[{"type":"string"},{"type":"number"}]},"scalar":1}}`))
	if err != nil {
		t.Fatal(err)
	}
	properties := decodeSchema(t, got)["properties"].(map[string]any)
	for _, name := range []string{"map", "choice", "scalar"} {
		wire := nullableSchema(t, properties[name].(map[string]any))
		if wire["type"] != "string" || !strings.Contains(wire["description"].(string), "Original schema") {
			t.Fatalf("property %s = %#v, want JSON codec", name, wire)
		}
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

func nullableSchema(t *testing.T, schema map[string]any) map[string]any {
	t.Helper()
	values, ok := schema["anyOf"].([]any)
	if !ok {
		t.Fatalf("schema has no anyOf nullable form: %#v", schema)
	}
	for _, value := range values {
		candidate, ok := value.(map[string]any)
		if !ok || candidate["type"] == "null" {
			continue
		}
		return candidate
	}
	t.Fatalf("schema has no non-null anyOf branch: %#v", schema)
	return nil
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal JSON: %v", err)
	}
	return data
}
