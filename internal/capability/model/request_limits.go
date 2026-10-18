package model

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

type maxOutputTokensKey struct{}

// WithMaxOutputTokens bounds only requests made with this context. Existing
// smaller provider or model limits remain authoritative.
func WithMaxOutputTokens(ctx context.Context, limit int) context.Context {
	if previous, ok := ctx.Value(maxOutputTokensKey{}).(int); ok && previous > 0 && limit > previous {
		limit = previous
	}
	return context.WithValue(ctx, maxOutputTokensKey{}, limit)
}

func configWithOutputLimit(ctx context.Context, cfg Config) (Config, error) {
	limit, ok := ctx.Value(maxOutputTokensKey{}).(int)
	if !ok {
		return cfg, nil
	}
	if limit <= 0 {
		return Config{}, fmt.Errorf("max output tokens must be positive")
	}
	body := EffectiveExtraRequestBody(cfg)
	field := "max_tokens"
	fields := []string{"max_tokens", "max_completion_tokens"}
	if shouldUseResponsesAPI(cfg) {
		field, fields = "max_output_tokens", []string{"max_output_tokens"}
	} else if isAnthropicTransport(cfg.Transport) {
		fields = []string{"max_tokens"}
		if _, configured := body[field]; !configured {
			limit = min(limit, anthropicDefaultMaxTokens)
		}
	} else if SelectModelAdapter(cfg).Name() == "deepseek" {
		field = "max_tokens"
	} else if _, configured := body["max_completion_tokens"]; configured {
		field = "max_completion_tokens"
	}
	for _, configured := range []RequestBody{cfg.ExtraBody, cfg.ModelExtraBody[strings.TrimSpace(cfg.Model)]} {
		for _, name := range fields {
			if value, exists := configured[name]; exists {
				current, err := positiveTokenLimit(value)
				if err != nil {
					return Config{}, fmt.Errorf("%s: %w", name, err)
				}
				if current < int64(limit) {
					limit = int(current)
				}
			}
		}
	}
	body[field] = limit
	for _, name := range fields {
		if _, exists := body[name]; exists {
			body[name] = limit
		}
	}
	cfg.ExtraBody, cfg.ModelExtraBody = body, nil
	return cfg, nil
}

func positiveTokenLimit(value any) (int64, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return 0, fmt.Errorf("output token limit must be a positive integer")
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	var decoded any
	if err := decoder.Decode(&decoded); err != nil {
		return 0, fmt.Errorf("output token limit must be a positive integer")
	}
	number, ok := decoded.(json.Number)
	if !ok {
		return 0, fmt.Errorf("output token limit must be a positive integer")
	}
	limit, err := number.Int64()
	if err != nil || limit <= 0 {
		return 0, fmt.Errorf("output token limit must be a positive integer")
	}
	return limit, nil
}
