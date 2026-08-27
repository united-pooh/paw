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

func TestDecodeToolArgumentsRepairsCommonLLMJsonMistakes(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{name: "raw newline inside string", raw: "{\"path\":\"a\nb\"}", want: `{"path":"a\nb"}`},
		{name: "raw tab inside string", raw: "{\"path\":\"a\tb\"}", want: `{"path":"a\tb"}`},
		{name: "invalid escape", raw: `{"path":"C:\Program Files\x"}`, want: `{"path":"C:\\Program Files\\x"}`},
		{name: "trailing backslash before closing quote", raw: "{\"path\":\"logs\\2024\\\"}", want: `{"path":"logs\\2024\\"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := decodeToolArguments("anthropic", "call_1", "Read", []byte(tt.raw))
			if err != nil {
				t.Fatalf("decodeToolArguments() error = %v", err)
			}
			if string(got) != tt.want {
				t.Fatalf("decodeToolArguments() = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestDecodeToolArgumentsStillRejectsStructuralDamage(t *testing.T) {
	tests := []struct {
		name string
		raw  string
	}{
		{name: "truncated", raw: `{"file_path":`},
		{name: "trailing garbage", raw: `{"path":"."}garbage`},
		{name: "unterminated brace", raw: `{"path":"."`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := decodeToolArguments("responses", "call_1", "Read", []byte(tt.raw)); err == nil {
				t.Fatalf("decodeToolArguments(%q) accepted damaged JSON", tt.raw)
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
