package selecttool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

const selectInputSchema = `{"type":"object","properties":{"prompt":{"type":"string"},"mode":{"type":"string","enum":["single","multiple"]},"options":{"type":"array","minItems":1,"items":{"type":"object","properties":{"id":{"type":"string","not":{"const":"custom_option"}},"label":{"type":"string"},"description":{"type":"string"}},"required":["id","label"]}},"initial_selected_id":{"type":"string"},"initial_selected_ids":{"type":"array","items":{"type":"string"}},"min_select":{"type":"integer","minimum":0},"max_select":{"type":"integer","minimum":0}},"required":["prompt","mode","options"]}`

type Tool struct{ broker *Broker }

func New(broker *Broker) *Tool { return &Tool{broker: broker} }
func (*Tool) Name() string     { return "Select" }
func (*Tool) Description() string {
	return "Render a blocking single- or multiple-choice prompt in the main TUI and wait for the user to submit or cancel."
}
func (*Tool) InputSchema() json.RawMessage { return json.RawMessage(selectInputSchema) }
func (t *Tool) Run(ctx context.Context, raw json.RawMessage) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if t == nil || t.broker == nil {
		return "", errors.New("selection broker is unavailable")
	}
	r, err := decodeInput(raw)
	if err != nil {
		return "", err
	}
	result, err := t.broker.Ask(ctx, r)
	if err != nil {
		return "", err
	}
	result, err = normalizeResult(r, result)
	if err != nil {
		return "", fmt.Errorf("invalid Select broker result: %w", err)
	}
	data, err := json.Marshal(result)
	if err != nil {
		return "", fmt.Errorf("encode Select result: %w", err)
	}
	return string(data), nil
}

func normalizeResult(request Request, result Result) (Result, error) {
	if result.Cancelled {
		result.SelectedOptions = []SelectedOption{}
		return result, nil
	}

	canonical := make(map[string]string, len(request.Options))
	for _, option := range request.Options {
		canonical[option.ID] = option.Label
	}
	seen := make(map[string]struct{}, len(result.SelectedOptions))
	customCount := 0
	for i := range result.SelectedOptions {
		selected := &result.SelectedOptions[i]
		selected.ID = strings.TrimSpace(selected.ID)
		selected.Label = strings.TrimSpace(selected.Label)
		if selected.ID == "" {
			return Result{}, fmt.Errorf("selected_options[%d].id is required", i)
		}
		if _, duplicate := seen[selected.ID]; duplicate {
			return Result{}, fmt.Errorf("duplicate selected option id: %s", selected.ID)
		}
		seen[selected.ID] = struct{}{}
		if selected.ID == CustomOptionID {
			customCount++
			if customCount > 1 {
				return Result{}, fmt.Errorf("at most one custom option is allowed")
			}
			if selected.Label == "" {
				return Result{}, fmt.Errorf("custom option label is required")
			}
			continue
		}
		label, ok := canonical[selected.ID]
		if !ok {
			return Result{}, fmt.Errorf("selected option id %q is not in the request", selected.ID)
		}
		selected.Label = label
	}

	count := len(result.SelectedOptions)
	if request.Mode == ModeSingle {
		if count != 1 {
			return Result{}, fmt.Errorf("single mode requires exactly one selected option, got %d", count)
		}
	} else if count < request.MinSelect || count > request.MaxSelect {
		return Result{}, fmt.Errorf("multiple mode selection count %d is outside allowed range %d-%d", count, request.MinSelect, request.MaxSelect)
	}
	if result.SelectedOptions == nil {
		result.SelectedOptions = []SelectedOption{}
	}
	return result, nil
}
