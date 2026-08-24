package bubble

import (
	"strings"
	"testing"

	"paw/internal/model"
)

func TestFormatModelSwitchBlockOmitsEmptyAttrsAndKeepsOrder(t *testing.T) {
	cfg := model.Config{
		Provider:           "openrouter",
		Model:              "stealth/ox-alpha",
		APIBaseURL:         "https://openrouter.ai/api/v1",
		ContextLimitTokens: 131072,
		RetryCount:         3,
		APIKeyEnvName:      "OPENROUTER_API_KEY",
	}
	body := formatModelSwitchBlock(cfg)
	if !isModelCardBlock(body) {
		t.Fatalf("generated body not detected as model card block:\n%s", body)
	}
	wantOrder := []string{
		`provider="openrouter"`,
		`model="stealth/ox-alpha"`,
		`base="https://openrouter.ai/api/v1"`,
		`context="131072"`,
		`retries="3"`,
		`key_env="OPENROUTER_API_KEY"`,
	}
	position := 0
	for _, want := range wantOrder {
		at := strings.Index(body[position:], want)
		if at < 0 {
			t.Fatalf("attr %s missing or out of order in:\n%s", want, body)
		}
		position += at + len(want)
	}
	if strings.Contains(body, `path=`) {
		t.Fatalf("empty path attr should be omitted:\n%s", body)
	}
}

func TestFormatModelSwitchBlockAlwaysWritesRetries(t *testing.T) {
	body := formatModelSwitchBlock(model.Config{Provider: "p1", Model: "m1"})
	if !strings.Contains(body, `retries="0"`) {
		t.Fatalf("retries=0 should still be written:\n%s", body)
	}
}

func TestModelCardBlockEscapeRoundTrip(t *testing.T) {
	cfg := model.Config{Provider: `a&b"c<d>`, Model: "m&1"}
	body := formatModelSwitchBlock(cfg)
	info, ok := parseModelCardBlock(body)
	if !ok {
		t.Fatalf("parse failed:\n%s", body)
	}
	if info.Provider != `a&b"c<d>` || info.Model != "m&1" {
		t.Fatalf("round trip mismatch: %#v", info)
	}
}

func TestIsModelCardBlockBoundaries(t *testing.T) {
	block := "<model provider=\"p\">\n</model>"
	cases := map[string]bool{
		block:                    true,
		"  \n" + block + "  ":    true,
		"<modelx provider=\"p\">": false,
		"plain key=value":        false,
		"<model provider=\"p\">": false,
	}
	for input, want := range cases {
		if got := isModelCardBlock(input); got != want {
			t.Fatalf("isModelCardBlock(%q) = %v, want %v", input, got, want)
		}
	}
}
