package selecttool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

const selectInputSchema = `{"type":"object","properties":{"prompt":{"type":"string"},"mode":{"type":"string","enum":["single","multiple"]},"options":{"type":"array","minItems":1,"items":{"type":"object","properties":{"id":{"type":"string"},"label":{"type":"string"},"description":{"type":"string"}},"required":["id","label"]}},"initial_selected_id":{"type":"string"},"initial_selected_ids":{"type":"array","items":{"type":"string"}},"min_select":{"type":"integer","minimum":0},"max_select":{"type":"integer","minimum":0}},"required":["prompt","mode","options"]}`

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
	if result.SelectedOptions == nil {
		result.SelectedOptions = []SelectedOption{}
	}
	data, err := json.Marshal(result)
	if err != nil {
		return "", fmt.Errorf("encode Select result: %w", err)
	}
	return string(data), nil
}
