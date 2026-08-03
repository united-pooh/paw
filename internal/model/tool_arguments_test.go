package model

import "testing"

func TestDecodeToolArgumentsRequiresJSONObject(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		ok   bool
	}{
		{name: "object", raw: `{"file_path":"README.md"}`, ok: true},
		{name: "empty object", raw: `{}`, ok: true},
		{name: "empty", raw: ``, ok: false},
		{name: "null", raw: `null`, ok: false},
		{name: "array", raw: `[]`, ok: false},
		{name: "truncated", raw: `{"file_path":`, ok: false},
		{name: "nested string", raw: `"{\"file_path\":\"README.md\"}"`, ok: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := decodeToolArguments("responses", "call_1", "Read", []byte(tt.raw))
			if tt.ok && err != nil {
				t.Fatalf("decodeToolArguments() error = %v", err)
			}
			if !tt.ok && err == nil {
				t.Fatalf("decodeToolArguments() = %s, want error", got)
			}
		})
	}
}

func TestDecodeToolArgumentsReturnsIndependentCopy(t *testing.T) {
	raw := []byte(`{"path":"."}`)
	got, err := decodeToolArguments("responses", "call_1", "LS", raw)
	if err != nil {
		t.Fatalf("decodeToolArguments() error = %v", err)
	}
	got[2] = 'X'
	if string(raw) != `{"path":"."}` {
		t.Fatalf("input mutated through returned value: %s", raw)
	}
}
