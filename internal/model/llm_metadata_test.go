package model

import "testing"

func TestMetadataContextLimitKnownModels(t *testing.T) {
	cases := []struct {
		provider string
		model    string
		want     int
	}{
		{"NewAPI", "deepseek-v4-flash", 1_000_000},
		{"deepseek", "deepseek-chat", 1_000_000},
		{"deepseek", "deepseek-reasoner", 1_000_000},
		{"openai", "gpt-4o", 128000},
		{"openai", "gpt-5", 400000},
		{"anthropic", "claude-sonnet-4-5", 1_000_000},
	}
	for _, tc := range cases {
		if got := MetadataContextLimit(tc.provider, tc.model); got != tc.want {
			t.Errorf("MetadataContextLimit(%q, %q) = %d, want %d", tc.provider, tc.model, got, tc.want)
		}
	}
}

func TestMetadataContextLimitMatchingNormalization(t *testing.T) {
	if got := MetadataContextLimit("", "DeepSeek-V4-FLASH"); got != 1_000_000 {
		t.Errorf("case-insensitive match = %d, want 1000000", got)
	}
	if got := MetadataContextLimit("", "openai/gpt-5"); got != 400000 {
		t.Errorf("provider-prefixed match = %d, want 400000", got)
	}
	if got := MetadataContextLimit("anything", "  deepseek-v4-flash  "); got != 1_000_000 {
		t.Errorf("whitespace-padded match = %d, want 1000000", got)
	}
}

func TestMetadataContextLimitUnknownModel(t *testing.T) {
	if got := MetadataContextLimit("NewAPI", "deepseek-v4-flash-pro-max-ultra"); got != 0 {
		t.Errorf("unknown model = %d, want 0", got)
	}
	if got := MetadataContextLimit("", ""); got != 0 {
		t.Errorf("empty model = %d, want 0", got)
	}
}

func TestMetadataContextWindowsEmbeddedData(t *testing.T) {
	windows := loadMetadataContextWindows()
	if len(windows) < 100 {
		t.Fatalf("embedded metadata has %d entries, want at least 100", len(windows))
	}
	if got := windows["deepseek-v4-flash"]; got != 1_000_000 {
		t.Fatalf("embedded deepseek-v4-flash window = %d, want 1000000", got)
	}
}
