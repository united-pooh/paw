package model

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
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
				"extraBody": map[string]any{
					"metadata": map[string]any{"profile": "second"},
				},
				"modelExtraBody": map[string]any{
					"selected-model": map[string]any{"service_tier": "fast"},
				},
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
	if cfg.ExtraBody["metadata"].(map[string]any)["profile"] != "second" {
		t.Fatalf("active profile extraBody = %#v", cfg.ExtraBody)
	}
	if cfg.ModelExtraBody["selected-model"]["service_tier"] != "fast" {
		t.Fatalf("active model extraBody = %#v", cfg.ModelExtraBody)
	}
	cfg.ExtraBody["metadata"].(map[string]any)["profile"] = "changed"
	if cfg.Profiles[1].ExtraBody["metadata"].(map[string]any)["profile"] != "second" {
		t.Fatal("LoadConfigFromEnv shared active extraBody with Profiles")
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

func TestLoadPawConfigDocumentRejectsNonObjectExtraBodies(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "extra null", body: `"extraBody": null`, want: `extraBody must be a JSON object`},
		{name: "extra string", body: `"extraBody": "fast"`, want: `extraBody must be a JSON object`},
		{name: "extra array", body: `"extraBody": []`, want: `extraBody must be a JSON object`},
		{name: "model map null", body: `"modelExtraBody": null`, want: `modelExtraBody must be a JSON object`},
		{name: "model map string", body: `"modelExtraBody": "fast"`, want: `modelExtraBody must be a JSON object`},
		{name: "model map array", body: `"modelExtraBody": []`, want: `modelExtraBody must be a JSON object`},
		{name: "model value null", body: `"modelExtraBody": {"model-a": null}`, want: `modelExtraBody["model-a"] must be a JSON object`},
		{name: "model value string", body: `"modelExtraBody": {"model-a": "fast"}`, want: `modelExtraBody["model-a"] must be a JSON object`},
		{name: "model value array", body: `"modelExtraBody": {"model-a": []}`, want: `modelExtraBody["model-a"] must be a JSON object`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			data := fmt.Sprintf(`{
				"schemaVersion": 1,
				"activeModelProfileId": "gateway",
				"modelProfiles": [{
					"id": "gateway",
					"transport": "openai-compatible",
					"model": "model-a",
					"models": ["model-a"],
					%s
				}]
			}`, tt.body)
			if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
				t.Fatal(err)
			}
			_, _, err := loadPawConfigDocument(path)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want substring %q", err, tt.want)
			}
		})
	}
}

func TestConfiguredProfilesLoadAndDeepCopyExtraBodies(t *testing.T) {
	raw := []persistedModelConfig{{
		ID:        "gateway",
		Transport: "openai-compatible",
		Model:     "model-a",
		Models:    []string{"model-a", "model-b"},
		ExtraBody: RequestBody{
			"metadata": map[string]any{"team": "platform"},
		},
		ModelExtraBody: map[string]RequestBody{
			"model-a": {"service_tier": "fast"},
		},
	}}
	profiles, err := configuredProfiles(raw, nil)
	if err != nil {
		t.Fatalf("configuredProfiles() error = %v", err)
	}

	cfg := profiles[0].Config()
	cfg.ExtraBody["metadata"].(map[string]any)["team"] = "changed"
	cfg.ModelExtraBody["model-a"]["service_tier"] = "slow"
	if profiles[0].ExtraBody["metadata"].(map[string]any)["team"] != "platform" {
		t.Fatalf("Profile.Config shared ExtraBody: %#v", profiles[0].ExtraBody)
	}
	if profiles[0].ModelExtraBody["model-a"]["service_tier"] != "fast" {
		t.Fatalf("Profile.Config shared ModelExtraBody: %#v", profiles[0].ModelExtraBody)
	}

	selected := ConfiguredProfiles(Config{Profiles: profiles})
	selected[0].ExtraBody["metadata"].(map[string]any)["team"] = "selected-change"
	if profiles[0].ExtraBody["metadata"].(map[string]any)["team"] != "platform" {
		t.Fatalf("ConfiguredProfiles shared ExtraBody: %#v", profiles[0].ExtraBody)
	}
}

func TestConfiguredProfilesRejectInvalidModelExtraBody(t *testing.T) {
	_, err := configuredProfiles([]persistedModelConfig{{
		ID:        "gateway",
		Transport: "openai-compatible",
		Model:     "model-a",
		Models:    []string{"model-a"},
		ModelExtraBody: map[string]RequestBody{
			"model-typo": {"service_tier": "fast"},
		},
	}}, nil)
	if err == nil || !strings.Contains(err.Error(), `model profile "gateway": modelExtraBody references unknown model "model-typo"`) {
		t.Fatalf("error = %v", err)
	}
}

func TestSaveModelConfigRoundTripsExtraBodiesAndPreservesUnknownFields(t *testing.T) {
	restoreCWD := chdirForTest(t, t.TempDir())
	defer restoreCWD()

	writePawConfig(t, map[string]any{
		"customGlobal": map[string]any{"keep": true},
		"modelProfiles": []any{map[string]any{
			"id":            "gateway",
			"customProfile": map[string]any{"keep": "profile"},
			"model":         "old-model",
		}},
		"activeModelProfileId": "gateway",
	})

	wantExtra := RequestBody{"metadata": map[string]any{"team": "platform"}}
	wantModelExtra := map[string]RequestBody{"new-model": {"service_tier": "fast"}}
	if err := SaveModelConfig(Config{
		ProfileID:      "gateway",
		ProfileName:    "Gateway",
		Provider:       "openai",
		Transport:      "openai-compatible",
		Model:          "new-model",
		Models:         []string{"new-model", "other-model"},
		ExtraBody:      wantExtra,
		ModelExtraBody: wantModelExtra,
	}); err != nil {
		t.Fatalf("SaveModelConfig() error = %v", err)
	}

	configPath, err := modelConfigPath()
	if err != nil {
		t.Fatalf("modelConfigPath() error = %v", err)
	}
	document, persisted, err := loadPawConfigDocument(configPath)
	if err != nil {
		t.Fatalf("loadPawConfigDocument() error = %v", err)
	}
	if !reflect.DeepEqual(persisted.ModelProfiles[0].ExtraBody, wantExtra) {
		t.Fatalf("ExtraBody = %#v, want %#v", persisted.ModelProfiles[0].ExtraBody, wantExtra)
	}
	if !reflect.DeepEqual(persisted.ModelProfiles[0].ModelExtraBody, wantModelExtra) {
		t.Fatalf("ModelExtraBody = %#v, want %#v", persisted.ModelProfiles[0].ModelExtraBody, wantModelExtra)
	}
	if document["customGlobal"].(map[string]any)["keep"] != true {
		t.Fatalf("customGlobal was not preserved: %#v", document["customGlobal"])
	}
	profile := document["modelProfiles"].([]any)[0].(map[string]any)
	if profile["customProfile"].(map[string]any)["keep"] != "profile" {
		t.Fatalf("customProfile was not preserved: %#v", profile["customProfile"])
	}
}

func TestSaveModelConfigKeepsConfiguredEmptyObjectsAsObjects(t *testing.T) {
	restoreCWD := chdirForTest(t, t.TempDir())
	defer restoreCWD()

	if err := SaveModelConfig(Config{
		ProfileID: "gateway",
		Model:     "model-a",
		Models:    []string{"model-a"},
		ExtraBody: RequestBody{},
		ModelExtraBody: map[string]RequestBody{
			"model-a": {},
		},
	}); err != nil {
		t.Fatalf("SaveModelConfig() error = %v", err)
	}

	profile := readPawConfig(t)["modelProfiles"].([]any)[0].(map[string]any)
	if _, ok := profile["extraBody"].(map[string]any); !ok {
		t.Fatalf("extraBody = %#v, want empty JSON object", profile["extraBody"])
	}
	modelExtraBody, ok := profile["modelExtraBody"].(map[string]any)
	if !ok {
		t.Fatalf("modelExtraBody = %#v, want JSON object", profile["modelExtraBody"])
	}
	if _, ok := modelExtraBody["model-a"].(map[string]any); !ok {
		t.Fatalf("modelExtraBody[model-a] = %#v, want empty JSON object", modelExtraBody["model-a"])
	}
}

func TestPersistedModelConfigMarshalOmitsNilAndKeepsEmptyObjects(t *testing.T) {
	data, err := json.Marshal(persistedModelConfig{
		ID:        "gateway",
		ExtraBody: RequestBody{},
		ModelExtraBody: map[string]RequestBody{
			"model-a": {},
		},
	})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	var document map[string]any
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if _, ok := document["extraBody"].(map[string]any); !ok {
		t.Fatalf("extraBody = %#v, want empty JSON object", document["extraBody"])
	}
	modelExtraBody, ok := document["modelExtraBody"].(map[string]any)
	if !ok {
		t.Fatalf("modelExtraBody = %#v, want JSON object", document["modelExtraBody"])
	}
	if _, ok := modelExtraBody["model-a"].(map[string]any); !ok {
		t.Fatalf("modelExtraBody[model-a] = %#v, want empty JSON object", modelExtraBody["model-a"])
	}

	data, err = json.Marshal(persistedModelConfig{ID: "gateway"})
	if err != nil {
		t.Fatalf("json.Marshal() nil bodies error = %v", err)
	}
	if strings.Contains(string(data), `"extraBody"`) || strings.Contains(string(data), `"modelExtraBody"`) {
		t.Fatalf("nil bodies were serialized: %s", data)
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
	testHome := filepath.Join(dir, "home")
	t.Setenv("HOME", testHome)
	// os.UserHomeDir uses USERPROFILE on Windows. Setting HOME alone caused
	// historical tests to write the real ~/.paw/config.json.
	t.Setenv("USERPROFILE", testHome)
	return func() {
		if err := os.Chdir(wd); err != nil {
			t.Fatalf("restore cwd: %v", err)
		}
	}
}

func TestModelContextLimitConfigurationAndDefault(t *testing.T) {
	restoreCWD := chdirForTest(t, t.TempDir())
	defer restoreCWD()
	writePawConfig(t, map[string]any{
		"modelProfiles": []any{map[string]any{
			"id":                   "gateway",
			"model":                "model-a",
			"models":               []string{"model-a", "model-b", "model-c"},
			"context_limit_tokens": 200000,
			"model_context_limit_tokens": map[string]any{
				"model-b": 131072,
			},
		}},
	})
	cfg, err := LoadConfigFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if got := EffectiveContextLimitTokens(cfg); got != 200000 {
		t.Fatalf("model-a limit = %d, want profile fallback 200000", got)
	}
	cfg.Model = "model-b"
	if got := EffectiveContextLimitTokens(cfg); got != 131072 {
		t.Fatalf("model-b limit = %d, want override 131072", got)
	}
	cfg.Model = "model-c"
	if got := EffectiveContextLimitTokens(cfg); got != 200000 {
		t.Fatalf("model-c limit = %d, want profile fallback 200000", got)
	}
	if got := EffectiveContextLimitTokens(Config{Model: "unconfigured"}); got != 128*1024 {
		t.Fatalf("default limit = %d, want 128 Ki tokens", got)
	}
	if got := EffectiveContextLimitTokens(Config{Model: "deepseek-v4-flash"}); got != 1_000_000 {
		t.Fatalf("metadata fallback limit = %d, want 1000000 from llm-metadata", got)
	}
	if got := EffectiveContextLimitTokens(Config{ContextLimitTokens: 200000, Model: "deepseek-v4-flash"}); got != 200000 {
		t.Fatalf("explicit profile limit must beat metadata, got %d", got)
	}
}

func TestSaveModelConfigPersistsContextLimits(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	cfg := Config{
		ProfileID:               "gateway",
		Model:                   "model-b",
		Models:                  []string{"model-a", "model-b"},
		ContextLimitTokens:      200000,
		ModelContextLimitTokens: map[string]int{"model-b": 131072},
	}
	if err := saveModelConfigAtPath(cfg, path); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	profiles := document["modelProfiles"].([]any)
	profile := profiles[0].(map[string]any)
	if profile["context_limit_tokens"] != float64(200000) {
		t.Fatalf("context_limit_tokens = %#v", profile["context_limit_tokens"])
	}
	overrides := profile["model_context_limit_tokens"].(map[string]any)
	if overrides["model-b"] != float64(131072) {
		t.Fatalf("model_context_limit_tokens = %#v", overrides)
	}
}
