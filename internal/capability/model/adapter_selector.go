package model

import "strings"

func SelectModelAdapter(cfg Config) ModelAdapter {
	switch strings.ToLower(strings.TrimSpace(cfg.Adapter)) {
	case "deepseek":
		return DeepSeekAdapter{}
	case "gpt":
		return GPTAdapter{}
	case "openai-compatible":
		return OpenAICompatibleAdapter{}
	}
	provider := strings.ToLower(strings.TrimSpace(cfg.Provider))
	model := strings.ToLower(strings.TrimSpace(cfg.Model))
	if isDeepSeekConfig(cfg) {
		return DeepSeekAdapter{}
	}
	if provider == "gpt" || provider == "openai" || strings.HasPrefix(model, "gpt-") {
		return GPTAdapter{}
	}
	return OpenAICompatibleAdapter{}
}

func isDeepSeekConfig(cfg Config) bool {
	modelName := strings.ToLower(strings.TrimSpace(cfg.Model))
	modelName = modelName[strings.LastIndexByte(modelName, '/')+1:]
	return strings.EqualFold(strings.TrimSpace(cfg.Provider), "deepseek") || strings.HasPrefix(modelName, "deepseek-")
}

// UsesDeepSeekChatHistory keeps request serialization and local context estimates
// aligned without changing the explicitly selected tool schema adapter.
func UsesDeepSeekChatHistory(cfg Config) bool {
	switch strings.ToLower(strings.TrimSpace(cfg.Transport)) {
	case "", "openai-compatible":
	default:
		return false
	}
	switch strings.ToLower(strings.TrimSpace(cfg.Adapter)) {
	case "deepseek":
		return true
	case "", "openai-compatible":
		return isDeepSeekConfig(cfg)
	default:
		return false
	}
}
