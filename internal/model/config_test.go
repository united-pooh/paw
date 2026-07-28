package model

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadConfigFromEnvDefaultsToCustom(t *testing.T) {
	restoreCWD := chdirForTest(t, t.TempDir())
	defer restoreCWD()

	unsetEnvNamesForTest(t, apiKeyEnvNames...)

	cfg, err := LoadConfigFromEnv()
	if err != nil {
		t.Fatalf("LoadConfigFromEnv() error = %v", err)
	}

	if cfg.Provider != ProviderCustom {
		t.Fatalf("cfg.Provider = %q, want %q", cfg.Provider, ProviderCustom)
	}
	if cfg.APIBaseURL != CustomAPIBaseURL {
		t.Fatalf("cfg.APIBaseURL = %q, want %q", cfg.APIBaseURL, CustomAPIBaseURL)
	}
	if cfg.APIPath != CustomChatPath {
		t.Fatalf("cfg.APIPath = %q, want %q", cfg.APIPath, CustomChatPath)
	}
	if cfg.Model != CustomDefaultModel {
		t.Fatalf("cfg.Model = %q, want %q", cfg.Model, CustomDefaultModel)
	}
	if cfg.APIKey != CustomDefaultAPIKey {
		t.Fatalf("cfg.APIKey = %q, want %q", cfg.APIKey, CustomDefaultAPIKey)
	}
}

func TestLoadConfigFromEnvUsesPersistedProvider(t *testing.T) {
	restoreCWD := chdirForTest(t, t.TempDir())
	defer restoreCWD()

	unsetEnvNamesForTest(t, apiKeyEnvNames...)
	if err := os.WriteFile(".env.local", []byte(CustomAPIKeyEnvName+"=custom-secret\n"), 0o600); err != nil {
		t.Fatalf("write .env.local: %v", err)
	}

	persisted := persistedModelConfig{
		Provider:      ProviderCustom,
		APIBaseURL:    CustomAPIBaseURL,
		APIPath:       CustomChatPath,
		APIKeyEnvName: CustomAPIKeyEnvName,
		Model:         legacyCustomModel,
		Timeout:       42,
	}
	writePersistedModelConfig(t, persisted)

	cfg, err := LoadConfigFromEnv()
	if err != nil {
		t.Fatalf("LoadConfigFromEnv() error = %v", err)
	}

	if cfg.Provider != ProviderCustom {
		t.Fatalf("cfg.Provider = %q, want %q", cfg.Provider, ProviderCustom)
	}
	if cfg.APIKey != "custom-secret" {
		t.Fatalf("cfg.APIKey = %q, want %q", cfg.APIKey, "custom-secret")
	}
	if cfg.Model != CustomDefaultModel {
		t.Fatalf("cfg.Model = %q, want %q", cfg.Model, CustomDefaultModel)
	}
	if cfg.Timeout != 42*time.Second {
		t.Fatalf("cfg.Timeout = %v, want %v", cfg.Timeout, 42*time.Second)
	}
}

func TestLoadConfigFromEnvUsesPersistedModelList(t *testing.T) {
	restoreCWD := chdirForTest(t, t.TempDir())
	defer restoreCWD()

	unsetEnvNamesForTest(t, apiKeyEnvNames...)
	if err := os.WriteFile(".env.local", []byte(CustomAPIKeyEnvName+"=custom-secret\n"), 0o600); err != nil {
		t.Fatalf("write .env.local: %v", err)
	}
	writePersistedModelConfig(t, persistedModelConfig{
		Provider:      ProviderCustom,
		APIBaseURL:    "https://example.test/v1",
		APIPath:       CustomChatPath,
		APIKeyEnvName: CustomAPIKeyEnvName,
		Model:         "gpt-5.6-luna",
		Models:        []string{"gpt-5.6-sol", "gpt-5.6-luna"},
	})

	cfg, err := LoadConfigFromEnv()
	if err != nil {
		t.Fatalf("LoadConfigFromEnv() error = %v", err)
	}
	if cfg.APIBaseURL != "https://example.test/v1" || cfg.Model != "gpt-5.6-luna" {
		t.Fatalf("cfg endpoint/model = %q/%q", cfg.APIBaseURL, cfg.Model)
	}
	want := []string{"gpt-5.6-sol", "gpt-5.6-luna"}
	if got := AvailableModels(cfg); len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("AvailableModels(cfg) = %#v, want %#v", got, want)
	}
}

func TestLoadConfigFromEnvDotEnvLocalOverridesProcessEnv(t *testing.T) {
	restoreCWD := chdirForTest(t, t.TempDir())
	defer restoreCWD()

	unsetEnvNamesForTest(t, apiKeyEnvNames...)
	if err := os.Setenv(DeepSeekAPIKeyEnvName, "process-secret"); err != nil {
		t.Fatalf("set env: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Unsetenv(DeepSeekAPIKeyEnvName)
	})

	if err := os.WriteFile(".env.local", []byte(DeepSeekAPIKeyEnvName+"=file-secret\n"), 0o600); err != nil {
		t.Fatalf("write .env.local: %v", err)
	}
	writePersistedModelConfig(t, persistedModelConfig{
		Provider:      ProviderDeepSeek,
		APIBaseURL:    DeepSeekAPIBaseURL,
		APIPath:       DeepSeekChatPath,
		APIKeyEnvName: DeepSeekAPIKeyEnvName,
		Model:         DeepSeekDefaultModel,
	})

	cfg, err := LoadConfigFromEnv()
	if err != nil {
		t.Fatalf("LoadConfigFromEnv() error = %v", err)
	}

	if cfg.APIKey != "file-secret" {
		t.Fatalf("cfg.APIKey = %q, want %q", cfg.APIKey, "file-secret")
	}
}

func TestLoadConfigFromEnvCustomProviderDoesNotReuseDeepSeekKey(t *testing.T) {
	restoreCWD := chdirForTest(t, t.TempDir())
	defer restoreCWD()

	unsetEnvNamesForTest(t, apiKeyEnvNames...)
	if err := os.WriteFile(".env.local", []byte(DeepSeekAPIKeyEnvName+"=deepseek-secret\n"), 0o600); err != nil {
		t.Fatalf("write .env.local: %v", err)
	}

	persisted := persistedModelConfig{
		Provider:      ProviderCustom,
		APIBaseURL:    CustomAPIBaseURL,
		APIPath:       CustomChatPath,
		APIKeyEnvName: CustomAPIKeyEnvName,
		Model:         CustomDefaultModel,
	}
	writePersistedModelConfig(t, persisted)

	cfg, err := LoadConfigFromEnv()
	if err != nil {
		t.Fatalf("LoadConfigFromEnv() error = %v", err)
	}

	if cfg.Provider != ProviderCustom {
		t.Fatalf("cfg.Provider = %q, want %q", cfg.Provider, ProviderCustom)
	}
	if cfg.APIKey != CustomDefaultAPIKey {
		t.Fatalf("cfg.APIKey = %q, want %q", cfg.APIKey, CustomDefaultAPIKey)
	}
}

func TestLoadConfigFromEnvCustomProviderAllowsPlaceholderKey(t *testing.T) {
	restoreCWD := chdirForTest(t, t.TempDir())
	defer restoreCWD()

	unsetEnvNamesForTest(t, apiKeyEnvNames...)
	if err := os.WriteFile(".env.local", []byte(CustomAPIKeyEnvName+"=sk-dummy\n"), 0o600); err != nil {
		t.Fatalf("write .env.local: %v", err)
	}

	writePersistedModelConfig(t, persistedModelConfig{
		Provider:      ProviderCustom,
		APIBaseURL:    CustomAPIBaseURL,
		APIPath:       CustomChatPath,
		APIKeyEnvName: CustomAPIKeyEnvName,
		Model:         CustomDefaultModel,
	})

	cfg, err := LoadConfigFromEnv()
	if err != nil {
		t.Fatalf("LoadConfigFromEnv() error = %v", err)
	}
	if cfg.Provider != ProviderCustom {
		t.Fatalf("cfg.Provider = %q, want %q", cfg.Provider, ProviderCustom)
	}
	if cfg.APIKey != CustomDefaultAPIKey {
		t.Fatalf("cfg.APIKey = %q, want %q", cfg.APIKey, CustomDefaultAPIKey)
	}
}

func TestSaveModelConfigPersistsProviderSelection(t *testing.T) {
	restoreCWD := chdirForTest(t, t.TempDir())
	defer restoreCWD()

	cfg := Config{
		Provider:      ProviderCustom,
		APIBaseURL:    CustomAPIBaseURL,
		APIPath:       CustomChatPath,
		APIKey:        "should-not-be-persisted",
		APIKeyEnvName: CustomAPIKeyEnvName,
		Model:         CustomDefaultModel,
		Timeout:       75 * time.Second,
	}
	if err := SaveModelConfig(cfg); err != nil {
		t.Fatalf("SaveModelConfig() error = %v", err)
	}

	data, err := os.ReadFile(filepath.Join(".", modelConfigPath))
	if err != nil {
		t.Fatalf("read persisted config: %v", err)
	}

	var persisted map[string]any
	if err := json.Unmarshal(data, &persisted); err != nil {
		t.Fatalf("unmarshal persisted config: %v", err)
	}

	if _, ok := persisted["api_key"]; ok {
		t.Fatalf("persisted config unexpectedly contains api_key")
	}
	if got := persisted["provider"]; got != ProviderCustom {
		t.Fatalf("persisted provider = %#v, want %q", got, ProviderCustom)
	}
	if got := persisted["timeout_seconds"]; got != float64(75) {
		t.Fatalf("persisted timeout_seconds = %#v, want %d", got, 75)
	}
	models, ok := persisted["models"].([]any)
	if !ok || len(models) != 1 || models[0] != CustomDefaultModel {
		t.Fatalf("persisted models = %#v, want [%q]", persisted["models"], CustomDefaultModel)
	}
}

func TestLoadConfigFromEnvErrorsWhenConfiguredKeyMissing(t *testing.T) {
	restoreCWD := chdirForTest(t, t.TempDir())
	defer restoreCWD()

	unsetEnvNamesForTest(t, apiKeyEnvNames...)
	writePersistedModelConfig(t, persistedModelConfig{
		Provider:      ProviderDeepSeek,
		APIBaseURL:    DeepSeekAPIBaseURL,
		APIPath:       DeepSeekChatPath,
		APIKeyEnvName: DeepSeekAPIKeyEnvName,
		Model:         DeepSeekDefaultModel,
	})

	if _, err := LoadConfigFromEnv(); err == nil {
		t.Fatalf("LoadConfigFromEnv() error = nil, want missing key error")
	}
}

func writePersistedModelConfig(t *testing.T, persisted persistedModelConfig) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(modelConfigPath), 0o755); err != nil {
		t.Fatalf("mkdir .ccagent: %v", err)
	}
	data, err := json.Marshal(persisted)
	if err != nil {
		t.Fatalf("marshal persisted config: %v", err)
	}
	if err := os.WriteFile(modelConfigPath, append(data, '\n'), 0o600); err != nil {
		t.Fatalf("write persisted config: %v", err)
	}
}

func chdirForTest(t *testing.T, dir string) func() {
	t.Helper()

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir to temp dir: %v", err)
	}

	return func() {
		if err := os.Chdir(wd); err != nil {
			t.Fatalf("restore cwd: %v", err)
		}
	}
}

func unsetEnvNamesForTest(t *testing.T, keys ...string) {
	t.Helper()

	for _, key := range keys {
		value, exists := os.LookupEnv(key)
		if err := os.Unsetenv(key); err != nil {
			t.Fatalf("unset env %s: %v", key, err)
		}

		t.Cleanup(func() {
			if exists {
				_ = os.Setenv(key, value)
				return
			}
			_ = os.Unsetenv(key)
		})
	}
}
