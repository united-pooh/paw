package model

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestProfileConfigNormalizesModelsAndDeepCopiesMutableFields(t *testing.T) {
	profile := Profile{
		ID:        "gateway",
		Provider:  "gateway",
		Transport: "openai-compatible",
		Model:     " model-a ",
		Models:    []string{"model-a", "model-b", "model-a", ""},
		Headers:   map[string]string{"X-Test": "original"},
		Proxy:     &ProxyConfig{Mode: ProxyModeCustom, URL: "http://127.0.0.1:7890"},
		ExtraBody: RequestBody{"metadata": map[string]any{"team": "platform"}},
		ModelExtraBody: map[string]RequestBody{
			"model-a": {"service_tier": "fast"},
		},
		ModelContextLimitTokens: map[string]int{"model-a": 131072},
		Stream:                  false,
		StreamSet:               true,
	}

	cfg := profile.Config()
	if got, want := cfg.Models, []string{"model-a", "model-b"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("models = %#v, want %#v", got, want)
	}
	if cfg.Model != "model-a" || cfg.Stream || !cfg.StreamSet {
		t.Fatalf("resolved config model/stream = %q/%v set=%v", cfg.Model, cfg.Stream, cfg.StreamSet)
	}

	cfg.Headers["X-Test"] = "changed"
	cfg.Proxy.URL = "http://changed.invalid"
	cfg.ExtraBody["metadata"].(map[string]any)["team"] = "changed"
	cfg.ModelExtraBody["model-a"]["service_tier"] = "slow"
	cfg.ModelContextLimitTokens["model-a"] = 1
	if profile.Headers["X-Test"] != "original" || profile.Proxy.URL != "http://127.0.0.1:7890" {
		t.Fatalf("Profile.Config shared headers/proxy with source: %#v %#v", profile.Headers, profile.Proxy)
	}
	if profile.ExtraBody["metadata"].(map[string]any)["team"] != "platform" || profile.ModelExtraBody["model-a"]["service_tier"] != "fast" {
		t.Fatalf("Profile.Config shared request bodies with source: %#v %#v", profile.ExtraBody, profile.ModelExtraBody)
	}
	if profile.ModelContextLimitTokens["model-a"] != 131072 {
		t.Fatalf("Profile.Config shared context limits with source: %#v", profile.ModelContextLimitTokens)
	}
}

func TestConfiguredProfilesReturnsDeepCopy(t *testing.T) {
	profiles := []Profile{{
		ID:        "gateway",
		Models:    []string{"model-a"},
		Headers:   map[string]string{"X-Test": "original"},
		ExtraBody: RequestBody{"metadata": map[string]any{"team": "platform"}},
	}}
	selected := ConfiguredProfiles(Config{Profiles: profiles})
	selected[0].Models[0] = "changed"
	selected[0].Headers["X-Test"] = "changed"
	selected[0].ExtraBody["metadata"].(map[string]any)["team"] = "changed"
	if profiles[0].Models[0] != "model-a" || profiles[0].Headers["X-Test"] != "original" || profiles[0].ExtraBody["metadata"].(map[string]any)["team"] != "platform" {
		t.Fatalf("ConfiguredProfiles shared mutable state: %#v", profiles[0])
	}
}

func TestAvailableModelsAndSupportsModel(t *testing.T) {
	cfg := Config{Model: "model-b", Models: []string{"model-a", "model-b", "model-a", ""}}
	if got, want := AvailableModels(cfg), []string{"model-a", "model-b"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("AvailableModels = %#v, want %#v", got, want)
	}
	if !SupportsModel(cfg, "model-a") || SupportsModel(cfg, "missing") || SupportsModel(cfg, " ") {
		t.Fatalf("SupportsModel returned unexpected values")
	}
}

func TestFillConfigDefaultsPreservesExplicitZeroAndFalse(t *testing.T) {
	cfg := fillConfigDefaults(Config{
		Transport:     "openai-compatible",
		Model:         "model-a",
		RetryCount:    0,
		RetryCountSet: true,
		Stream:        false,
		StreamSet:     true,
	})
	if cfg.RetryCount != 0 || cfg.Stream {
		t.Fatalf("explicit retry/stream were overwritten: %#v", cfg)
	}
	if cfg.Timeout != 60*time.Second {
		t.Fatalf("default timeout = %s, want 60s", cfg.Timeout)
	}
}

func TestEffectiveContextLimitTokens(t *testing.T) {
	cfg := Config{
		Model:                   "model-a",
		ContextLimitTokens:      200000,
		ModelContextLimitTokens: map[string]int{"model-b": 131072},
	}
	if got := EffectiveContextLimitTokens(cfg); got != 200000 {
		t.Fatalf("model-a limit = %d, want 200000", got)
	}
	cfg.Model = "model-b"
	if got := EffectiveContextLimitTokens(cfg); got != 131072 {
		t.Fatalf("model-b limit = %d, want 131072", got)
	}
	if got := EffectiveContextLimitTokens(Config{Model: "unconfigured"}); got != 128*1024 {
		t.Fatalf("default limit = %d, want 128 Ki tokens", got)
	}
	if got := EffectiveContextLimitTokens(Config{Model: "deepseek-v4-flash"}); got != 1_000_000 {
		t.Fatalf("metadata fallback limit = %d, want 1000000", got)
	}
	if got := EffectiveContextLimitTokens(Config{ContextLimitTokens: 200000, Model: "deepseek-v4-flash"}); got != 200000 {
		t.Fatalf("explicit limit must beat metadata, got %d", got)
	}
}

func TestLoadOptionalEnvFilesUsesLocalOverride(t *testing.T) {
	t.Setenv("PAW_CONFIG_TEST", "")
	t.Setenv("PAW_QUOTED", "")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("PAW_CONFIG_TEST=base\nPAW_QUOTED=\"quoted value\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".env.local"), []byte("export PAW_CONFIG_TEST=local\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })

	loaded, err := LoadOptionalEnvFiles()
	if err != nil {
		t.Fatal(err)
	}
	if loaded["PAW_CONFIG_TEST"] != "local" || os.Getenv("PAW_CONFIG_TEST") != "local" {
		t.Fatalf("local override was not applied: %#v", loaded)
	}
	if loaded["PAW_QUOTED"] != "quoted value" {
		t.Fatalf("quoted value = %q", loaded["PAW_QUOTED"])
	}
}

func TestLoadOptionalEnvFilesAllowsMissingFiles(t *testing.T) {
	dir := t.TempDir()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
	loaded, err := LoadOptionalEnvFiles()
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 0 {
		t.Fatalf("loaded missing files = %#v", loaded)
	}
}
