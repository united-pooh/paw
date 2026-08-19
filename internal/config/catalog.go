package config

import (
	"strings"

	"paw/internal/model"
)

type Preset struct {
	ID             string
	Name           string
	Provider       Provider
	DefaultModelID string
	DefaultModel   Model
	DetectionEnv   []string
	RequiresAuth   bool
}

// defaultPresetID 是 first-run 时未检测到任何凭据的内置默认 provider：
// starter 配置直接落到该 preset，用户设置对应 env 凭据（DEEPSEEK_API_KEY）
// 即可开箱可用，无需先进 /config 选 provider。
const defaultPresetID = "deepseek"

var builtinPresets = map[string]Preset{
	"openai": {
		ID: "openai", Name: "OpenAI", RequiresAuth: true,
		Provider:       Provider{Transport: TransportOpenAIResponses, Endpoint: "https://api.openai.com/v1", Auth: Auth{Credential: "provider/openai", Env: []string{"OPENAI_API_KEY"}}, TimeoutSeconds: 60, Retries: intPointer(3), Discovery: &DiscoveryConfig{Enabled: boolPointer(true), Path: "models", PathSet: true, Format: DiscoveryFormatOpenAIList}},
		DefaultModelID: "openai/gpt-5", DefaultModel: Model{Provider: "openai", Name: "gpt-5", Adapter: AdapterGPT, ContextWindow: 400000},
		DetectionEnv: []string{"OPENAI_API_KEY"},
	},
	"anthropic": {
		ID: "anthropic", Name: "Anthropic", RequiresAuth: true,
		Provider:       Provider{Transport: TransportAnthropicCompatible, Endpoint: "https://api.anthropic.com/v1", Auth: Auth{Credential: "provider/anthropic", Env: []string{"ANTHROPIC_API_KEY"}}, TimeoutSeconds: 60, Retries: intPointer(3)},
		DefaultModelID: "anthropic/claude-sonnet", DefaultModel: Model{Provider: "anthropic", Name: "claude-sonnet-4-5", Adapter: AdapterOpenAICompatible, ContextWindow: 200000},
		DetectionEnv: []string{"ANTHROPIC_API_KEY"},
	},
	"deepseek": {
		ID: "deepseek", Name: "DeepSeek", RequiresAuth: true,
		Provider:       Provider{Transport: TransportOpenAICompatible, Endpoint: "https://api.deepseek.com", Auth: Auth{Credential: "provider/deepseek", Env: []string{"DEEPSEEK_API_KEY"}}, TimeoutSeconds: 60, Retries: intPointer(3), Discovery: &DiscoveryConfig{Enabled: boolPointer(true), Path: "models", PathSet: true, Format: DiscoveryFormatOpenAIList}},
		DefaultModelID: "deepseek/chat", DefaultModel: Model{Provider: "deepseek", Name: "deepseek-chat", Adapter: AdapterDeepSeek, ContextWindow: 128000},
		DetectionEnv: []string{"DEEPSEEK_API_KEY"},
	},
	"openrouter": {
		ID: "openrouter", Name: "OpenRouter", RequiresAuth: true,
		Provider:       Provider{Transport: TransportOpenAICompatible, Endpoint: "https://openrouter.ai/api/v1", Auth: Auth{Credential: "provider/openrouter", Env: []string{"OPENROUTER_API_KEY"}}, TimeoutSeconds: 60, Retries: intPointer(3), Discovery: &DiscoveryConfig{Enabled: boolPointer(true), Path: "models", PathSet: true, Format: DiscoveryFormatOpenAIList}},
		DefaultModelID: "openrouter/default", DefaultModel: Model{Provider: "openrouter", Name: "openai/gpt-5", Adapter: AdapterOpenAICompatible},
		DetectionEnv: []string{"OPENROUTER_API_KEY"},
	},
	"ollama": {
		ID: "ollama", Name: "Ollama", RequiresAuth: false,
		Provider:       Provider{Transport: TransportOpenAICompatible, Endpoint: "http://127.0.0.1:11434/v1", TimeoutSeconds: 120, Retries: intPointer(1), Discovery: &DiscoveryConfig{Enabled: boolPointer(true), Path: "/api/tags", PathSet: true, Format: DiscoveryFormatOllamaTags}},
		DefaultModelID: "ollama/default", DefaultModel: Model{Provider: "ollama", Name: "llama3.2", Adapter: AdapterOpenAICompatible},
		DetectionEnv: []string{"OLLAMA_HOST", "OLLAMA_MODEL"},
	},
	"custom": {
		ID: "custom", Name: "Custom", RequiresAuth: false,
		Provider:       Provider{Transport: TransportOpenAICompatible, Endpoint: "http://127.0.0.1:8000/v1", TimeoutSeconds: 60, Retries: intPointer(3), Discovery: &DiscoveryConfig{Enabled: boolPointer(true), Path: "models", PathSet: true, Format: DiscoveryFormatOpenAIList}},
		DefaultModelID: "custom/model", DefaultModel: Model{Provider: "custom", Name: "model", Adapter: AdapterOpenAICompatible},
	},
}

func BuiltinPresets() map[string]Preset {
	out := make(map[string]Preset, len(builtinPresets))
	for id, preset := range builtinPresets {
		preset.Provider = cloneProvider(preset.Provider)
		preset.DefaultModel = cloneModel(preset.DefaultModel)
		preset.DetectionEnv = cloneStringSlice(preset.DetectionEnv)
		out[id] = preset
	}
	return out
}

func mergePreset(id string, configured Provider) Provider {
	base := Provider{}
	presetID := strings.TrimSpace(configured.Preset)
	if presetID == "" {
		presetID = strings.TrimSpace(id)
	}
	if preset, ok := builtinPresets[presetID]; ok {
		base = cloneProvider(preset.Provider)
	}
	if configured.Transport != "" {
		base.Transport = configured.Transport
	}
	if configured.Endpoint != "" {
		base.Endpoint = configured.Endpoint
	}
	if configured.APIPath != "" {
		base.APIPath = configured.APIPath
	}
	// An explicitly configured credential — including an explicit empty
	// string that clears the preset's keyring reference — always wins.
	// An unset field inherits the preset's credential.
	if configured.Auth.CredentialSet || configured.Auth.Credential != "" {
		base.Auth.Credential = configured.Auth.Credential
	}
	if len(configured.Auth.Env) > 0 {
		base.Auth.Env = append([]string(nil), configured.Auth.Env...)
	}
	if configured.Headers != nil {
		base.Headers = cloneStringMap(configured.Headers)
	}
	if configured.Body != nil {
		base.Body = cloneAnyMap(configured.Body)
	}
	if configured.Proxy != nil {
		base.Proxy = model.CloneProxyConfig(configured.Proxy)
	}
	if configured.TimeoutSeconds > 0 {
		base.TimeoutSeconds = configured.TimeoutSeconds
	}
	if configured.Retries != nil {
		retries := *configured.Retries
		base.Retries = &retries
	}
	if configured.Stream != nil {
		value := *configured.Stream
		base.Stream = &value
	}
	if configured.Discovery != nil {
		merged := DiscoveryConfig{}
		if base.Discovery != nil {
			merged = *cloneDiscoveryConfig(base.Discovery)
		}
		if configured.Discovery.Enabled != nil {
			merged.Enabled = cloneBoolPointer(configured.Discovery.Enabled)
		}
		if configured.Discovery.PathSet || configured.Discovery.Path != "" {
			merged.Path = configured.Discovery.Path
			merged.PathSet = true
		}
		if configured.Discovery.Format != "" {
			merged.Format = configured.Discovery.Format
		}
		if configured.Discovery.TimeoutSeconds != 0 {
			merged.TimeoutSeconds = configured.Discovery.TimeoutSeconds
		}
		if configured.Discovery.Include != nil {
			merged.Include = cloneStringSlice(configured.Discovery.Include)
		}
		if configured.Discovery.Exclude != nil {
			merged.Exclude = cloneStringSlice(configured.Discovery.Exclude)
		}
		if configured.Discovery.Mode != "" {
			merged.Mode = configured.Discovery.Mode
		}
		base.Discovery = &merged
	}
	base.Preset = configured.Preset
	return base
}

func intPointer(value int) *int    { return &value }
func boolPointer(value bool) *bool { return &value }
