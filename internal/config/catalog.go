package config

import "strings"

type Preset struct {
	ID             string
	Name           string
	Provider       Provider
	DefaultModelID string
	DefaultModel   Model
	DetectionEnv   []string
	RequiresAuth   bool
}

var builtinPresets = map[string]Preset{
	"openai": {
		ID: "openai", Name: "OpenAI", RequiresAuth: true,
		Provider:       Provider{Transport: TransportOpenAIResponses, Endpoint: "https://api.openai.com/v1", Auth: Auth{Credential: "provider/openai", Env: []string{"OPENAI_API_KEY"}}, TimeoutSeconds: 60, Retries: intPointer(3)},
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
		Provider:       Provider{Transport: TransportOpenAICompatible, Endpoint: "https://api.deepseek.com", Auth: Auth{Credential: "provider/deepseek", Env: []string{"DEEPSEEK_API_KEY"}}, TimeoutSeconds: 60, Retries: intPointer(3)},
		DefaultModelID: "deepseek/chat", DefaultModel: Model{Provider: "deepseek", Name: "deepseek-chat", Adapter: AdapterDeepSeek, ContextWindow: 128000},
		DetectionEnv: []string{"DEEPSEEK_API_KEY"},
	},
	"openrouter": {
		ID: "openrouter", Name: "OpenRouter", RequiresAuth: true,
		Provider:       Provider{Transport: TransportOpenAICompatible, Endpoint: "https://openrouter.ai/api/v1", Auth: Auth{Credential: "provider/openrouter", Env: []string{"OPENROUTER_API_KEY"}}, TimeoutSeconds: 60, Retries: intPointer(3)},
		DefaultModelID: "openrouter/default", DefaultModel: Model{Provider: "openrouter", Name: "openai/gpt-5", Adapter: AdapterOpenAICompatible},
		DetectionEnv: []string{"OPENROUTER_API_KEY"},
	},
	"ollama": {
		ID: "ollama", Name: "Ollama", RequiresAuth: false,
		Provider:       Provider{Transport: TransportOpenAICompatible, Endpoint: "http://127.0.0.1:11434/v1", TimeoutSeconds: 120, Retries: intPointer(1)},
		DefaultModelID: "ollama/default", DefaultModel: Model{Provider: "ollama", Name: "llama3.2", Adapter: AdapterOpenAICompatible},
		DetectionEnv: []string{"OLLAMA_HOST", "OLLAMA_MODEL"},
	},
	"custom": {
		ID: "custom", Name: "Custom", RequiresAuth: false,
		Provider:       Provider{Transport: TransportOpenAICompatible, Endpoint: "http://127.0.0.1:8000/v1", TimeoutSeconds: 60, Retries: intPointer(3)},
		DefaultModelID: "custom/model", DefaultModel: Model{Provider: "custom", Name: "model", Adapter: AdapterOpenAICompatible},
	},
}

func BuiltinPresets() map[string]Preset {
	out := make(map[string]Preset, len(builtinPresets))
	for id, preset := range builtinPresets {
		preset.Provider.Auth.Env = append([]string(nil), preset.Provider.Auth.Env...)
		preset.DetectionEnv = append([]string(nil), preset.DetectionEnv...)
		if preset.Provider.Retries != nil {
			retries := *preset.Provider.Retries
			preset.Provider.Retries = &retries
		}
		preset.Provider.Stream = cloneBoolPointer(preset.Provider.Stream)
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
		base = preset.Provider
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
	if configured.Auth.Credential != "" {
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
	base.Preset = configured.Preset
	return base
}

func intPointer(value int) *int { return &value }
