package model

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadConfigFromEnvUsesFirstConfiguredProfileAndModel(t *testing.T) {
	restoreCWD := chdirForTest(t, t.TempDir())
	defer restoreCWD()

	writePawConfig(t, map[string]any{
		"schemaVersion": 1,
		"modelProfiles": []any{
			map[string]any{
				"id":             "gateway",
				"name":           "Gateway",
				"provider":       "gateway",
				"transport":      "openai-compatible",
				"baseUrl":        "http://gateway.test/v1",
				"apiPath":        "/chat/completions",
				"apiKey":         "profile-secret",
				"models":         []string{"first-model", "second-model"},
				"timeoutSeconds": 42,
			},
			map[string]any{
				"id":       "other",
				"provider": "other",
				"model":    "other-model",
			},
		},
	})

	cfg, err := LoadConfigFromEnv()
	if err != nil {
		t.Fatalf("LoadConfigFromEnv() error = %v", err)
	}
	if cfg.ProfileID != "gateway" || cfg.Provider != "gateway" {
		t.Fatalf("profile/provider = %q/%q, want gateway/gateway", cfg.ProfileID, cfg.Provider)
	}
	if cfg.Model != "first-model" {
		t.Fatalf("cfg.Model = %q, want first-model", cfg.Model)
	}
	if cfg.APIBaseURL != "http://gateway.test/v1" || cfg.APIPath != "/chat/completions" {
		t.Fatalf("endpoint = %q%s, want configured endpoint", cfg.APIBaseURL, cfg.APIPath)
	}
	if cfg.APIKey != "profile-secret" {
		t.Fatalf("cfg.APIKey = %q, want profile-secret", cfg.APIKey)
	}
	if cfg.Timeout != 42*time.Second {
		t.Fatalf("cfg.Timeout = %v, want 42s", cfg.Timeout)
	}
	want := []string{"first-model", "second-model"}
	if got := AvailableModels(cfg); len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("AvailableModels(cfg) = %#v, want %#v", got, want)
	}
}

func TestLoadConfigFromEnvUsesActiveProfileWhenSet(t *testing.T) {
	restoreCWD := chdirForTest(t, t.TempDir())
	defer restoreCWD()

	writePawConfig(t, map[string]any{
		"modelProfiles": []any{
			map[string]any{"id": "first", "model": "first-model"},
			map[string]any{
				"id":       "second",
				"provider": "second-provider",
				"model":    "selected-model",
				"models":   []string{"selected-model", "other-model"},
			},
		},
		"activeModelProfileId": "second",
	})

	cfg, err := LoadConfigFromEnv()
	if err != nil {
		t.Fatalf("LoadConfigFromEnv() error = %v", err)
	}
	if cfg.ProfileID != "second" || cfg.Model != "selected-model" {
		t.Fatalf("active profile/model = %q/%q, want second/selected-model", cfg.ProfileID, cfg.Model)
	}
}

func TestLoadConfigFromEnvUsesEnvironmentKeyConfiguredByProfile(t *testing.T) {
	restoreCWD := chdirForTest(t, t.TempDir())
	defer restoreCWD()

	t.Setenv("GATEWAY_API_KEY", "environment-secret")
	writePawConfig(t, map[string]any{
		"modelProfiles": []any{map[string]any{
			"id":            "gateway",
			"apiKey":        "profile-secret",
			"apiKeyEnvName": "GATEWAY_API_KEY",
			"model":         "model",
		}},
	})

	cfg, err := LoadConfigFromEnv()
	if err != nil {
		t.Fatalf("LoadConfigFromEnv() error = %v", err)
	}
	if cfg.APIKey != "environment-secret" {
		t.Fatalf("cfg.APIKey = %q, want environment-secret", cfg.APIKey)
	}
}

func TestLoadConfigFromEnvHonorsExplicitNonStreamingConfig(t *testing.T) {
	restoreCWD := chdirForTest(t, t.TempDir())
	defer restoreCWD()

	writePawConfig(t, map[string]any{
		"modelProfiles": []any{map[string]any{
			"id":     "gateway",
			"model":  "model",
			"stream": false,
		}},
	})

	cfg, err := LoadConfigFromEnv()
	if err != nil {
		t.Fatalf("LoadConfigFromEnv() error = %v", err)
	}
	if cfg.Stream {
		t.Fatal("cfg.Stream = true, want explicit false")
	}
	if !cfg.streamSet {
		t.Fatal("explicit stream=false was not retained as configured")
	}
}

func TestLoadConfigFromEnvMissingConfigCreatesEmptyDocument(t *testing.T) {
	restoreCWD := chdirForTest(t, t.TempDir())
	defer restoreCWD()

	if _, err := LoadConfigFromEnv(); err == nil {
		t.Fatal("LoadConfigFromEnv() error = nil, want missing profile error")
	}
	configPath, err := modelConfigPath()
	if err != nil {
		t.Fatalf("modelConfigPath() error = %v", err)
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read auto-created config: %v", err)
	}
	var document map[string]any
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatalf("unmarshal auto-created config: %v", err)
	}
	if _, ok := document["modelProfiles"]; !ok {
		t.Fatalf("auto-created config has no modelProfiles: %#v", document)
	}
}

func TestSaveModelConfigPersistsSelectedProfileWithoutAPIKey(t *testing.T) {
	restoreCWD := chdirForTest(t, t.TempDir())
	defer restoreCWD()

	writePawConfig(t, map[string]any{
		"modelProfiles": []any{map[string]any{
			"id":    "gateway",
			"name":  "Gateway",
			"model": "old-model",
		}},
		"activeModelProfileId": "gateway",
	})

	if err := SaveModelConfig(Config{
		ProfileID:     "gateway",
		ProfileName:   "Gateway",
		Provider:      "gateway",
		Transport:     "openai-compatible",
		APIBaseURL:    "http://gateway.test/v1",
		APIPath:       "/chat/completions",
		APIKey:        "do-not-persist",
		APIKeyEnvName: "GATEWAY_API_KEY",
		Model:         "new-model",
		Models:        []string{"new-model", "other-model"},
		Timeout:       75 * time.Second,
		RetryCount:    5,
	}); err != nil {
		t.Fatalf("SaveModelConfig() error = %v", err)
	}

	document := readPawConfig(t)
	profiles, ok := document["modelProfiles"].([]any)
	if !ok || len(profiles) != 1 {
		t.Fatalf("modelProfiles = %#v, want one profile", document["modelProfiles"])
	}
	profile := profiles[0].(map[string]any)
	if profile["model"] != "new-model" || profile["provider"] != "gateway" {
		t.Fatalf("saved profile = %#v, want selected values", profile)
	}
	if _, ok := profile["apiKey"]; ok {
		t.Fatalf("saved profile unexpectedly contains apiKey")
	}
	if profile["timeoutSeconds"] != float64(75) || profile["retryCount"] != float64(5) {
		t.Fatalf("saved retry/timeout = %#v/%#v", profile["retryCount"], profile["timeoutSeconds"])
	}
}

func TestSaveModelConfigPreservesOtherGlobalConfigFields(t *testing.T) {
	restoreCWD := chdirForTest(t, t.TempDir())
	defer restoreCWD()

	writePawConfig(t, map[string]any{
		"appearance": "system",
		"ui":         map[string]any{"contextLimitTokens": float64(1048576)},
		"modelProfiles": []any{map[string]any{
			"id":           "gateway",
			"credentialId": "default",
			"model":        "old-model",
		}},
		"activeModelProfileId": "gateway",
	})

	if err := SaveModelConfig(Config{ProfileID: "gateway", Model: "new-model"}); err != nil {
		t.Fatalf("SaveModelConfig() error = %v", err)
	}
	document := readPawConfig(t)
	if document["appearance"] != "system" {
		t.Fatalf("appearance = %#v, want preserved value", document["appearance"])
	}
	ui := document["ui"].(map[string]any)
	if ui["contextLimitTokens"] != float64(1048576) {
		t.Fatalf("ui = %#v, want preserved value", ui)
	}
	profile := document["modelProfiles"].([]any)[0].(map[string]any)
	if profile["credentialId"] != "default" || profile["model"] != "new-model" {
		t.Fatalf("profile = %#v, want credential preserved and model changed", profile)
	}
}

func writePawConfig(t *testing.T, document map[string]any) {
	t.Helper()
	configPath, err := modelConfigPath()
	if err != nil {
		t.Fatalf("modelConfigPath() error = %v", err)
	}
	data, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		t.Fatalf("mkdir config directory: %v", err)
	}
	if err := os.WriteFile(configPath, append(data, '\n'), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

func readPawConfig(t *testing.T) map[string]any {
	t.Helper()
	configPath, err := modelConfigPath()
	if err != nil {
		t.Fatalf("modelConfigPath() error = %v", err)
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var document map[string]any
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatalf("unmarshal config: %v", err)
	}
	return document
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
	t.Setenv("HOME", filepath.Join(dir, "home"))
	return func() {
		if err := os.Chdir(wd); err != nil {
			t.Fatalf("restore cwd: %v", err)
		}
	}
}
