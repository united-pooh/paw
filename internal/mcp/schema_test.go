package mcp

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestValidateModelToolSchema(t *testing.T) {
	tests := []struct {
		name   string
		schema string
		ok     bool
		want   string
	}{
		{name: "object", schema: `{"type":"object","properties":{"query":{"type":"string"}},"required":["query"]}`, ok: true},
		{name: "empty defaults", schema: ``, ok: true},
		{name: "invalid JSON", schema: `{`, want: "valid JSON"},
		{name: "top level array", schema: `[]`, want: "JSON object"},
		{name: "wrong type", schema: `{"type":"array"}`, want: `type must be "object"`},
		{name: "properties array", schema: `{"type":"object","properties":[]}`, want: "properties must be an object"},
		{name: "required string", schema: `{"type":"object","properties":{},"required":"query"}`, want: "required must be an array"},
		{name: "missing required property", schema: `{"type":"object","properties":{},"required":["query"]}`, want: `required property "query"`},
		{name: "no type accepted", schema: `{"properties":{"query":{"type":"string"}}}`, ok: true},
		{name: "unknown keyword accepted", schema: `{"type":"object","properties":{},"additionalProperties":false}`, ok: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateModelToolSchema("server__tool", json.RawMessage(tt.schema))
			if tt.ok && err != nil {
				t.Fatalf("validateModelToolSchema() error = %v", err)
			}
			if !tt.ok && (err == nil || !strings.Contains(err.Error(), tt.want)) {
				t.Fatalf("error = %v, want substring %q", err, tt.want)
			}
		})
	}
}

func TestValidateToolSpecsRejectsWholeBatchOnAnyInvalid(t *testing.T) {
	err := validateToolSpecs([]ToolSpec{
		{Name: "good__a", InputSchema: json.RawMessage(`{"type":"object","properties":{}}`)},
		{Name: "bad__b", InputSchema: json.RawMessage(`{"type":"array"}`)},
		{Name: "good__c", InputSchema: json.RawMessage(`{"type":"object","properties":{}}`)},
	})
	if err == nil || !strings.Contains(err.Error(), `bad__b`) || !strings.Contains(err.Error(), `type must be "object"`) {
		t.Fatalf("error = %v, want rejection naming the invalid tool", err)
	}
}
