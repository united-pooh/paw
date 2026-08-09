package config

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestJSONCInsertIgnoresCommasInsideTrailingComments(t *testing.T) {
	raw := []byte("{\n  \"items\": {\n    \"one\": 1 // keep, comment\n  }\n}\n")
	updated, err := patchJSONCMember(raw, []string{"items"}, "two", json.RawMessage(`2`), false)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := decodeJSONC(updated, &document); err != nil {
		t.Fatalf("updated JSONC invalid: %v\n%s", err, updated)
	}
	if !strings.Contains(string(updated), "// keep, comment") {
		t.Fatal("comment was lost")
	}
}

func TestJSONCDeleteOnlyTrailingMemberRemovesItsComma(t *testing.T) {
	raw := []byte(`{"items":{"one":1,}}`)
	updated, err := patchJSONCMember(raw, []string{"items"}, "one", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := decodeJSONC(updated, &document); err != nil {
		t.Fatalf("updated JSONC invalid: %v: %s", err, updated)
	}
}

func TestDecodeJSONCRejectsTrailingGarbage(t *testing.T) {
	var value map[string]any
	if err := decodeJSONC([]byte(`{} garbage`), &value); err == nil {
		t.Fatal("trailing garbage was accepted")
	}
}
