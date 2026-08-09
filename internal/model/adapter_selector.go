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
	if provider == "deepseek" || strings.HasPrefix(model, "deepseek-") {
		return DeepSeekAdapter{}
	}
	if provider == "gpt" || provider == "openai" || strings.HasPrefix(model, "gpt-") {
		return GPTAdapter{}
	}
	return OpenAICompatibleAdapter{}
}
