package selecttool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

const questionInputSchema = `{"type":"object","properties":{"questions":{"type":"array","minItems":1,"items":{"type":"object","properties":{"prompt":{"type":"string"},"mode":{"type":"string","enum":["single","multiple"]},"options":{"type":"array","minItems":1,"items":{"type":"object","properties":{"id":{"type":"string","not":{"const":"custom_option"}},"label":{"type":"string"},"description":{"type":"string"}},"required":["id","label"]}},"initial_selected_id":{"type":"string"},"initial_selected_ids":{"type":"array","items":{"type":"string"}},"min_select":{"type":"integer","minimum":0},"max_select":{"type":"integer","minimum":0}},"required":["prompt","mode","options"]}}},"required":["questions"]}`

type Tool struct{ broker *Broker }

func New(broker *Broker) *Tool { return &Tool{broker: broker} }
func (*Tool) Name() string     { return "question" }
func (*Tool) Description() string {
	return "Ask the user one or more structured single- or multiple-choice questions in the main TUI, one at a time, and wait for the answers. When you need the user to choose among two or more concrete options, prefer this tool instead of writing an A/B/C list or asking the question in normal assistant text. Pass every question that must be asked in a single call via the questions array (each with its own prompt/mode/options); the UI shows them in order and the result is an array aligned with the input. Use single mode when exactly one choice is needed and multiple mode when several choices may be selected. Provide short, distinct option labels and use descriptions for consequences or trade-offs. Do not use this tool for open-ended questions, rhetorical questions, confirmations with no meaningful alternatives, or information you can determine yourself."
}
func (*Tool) InputSchema() json.RawMessage { return json.RawMessage(questionInputSchema) }
func (t *Tool) Run(ctx context.Context, raw json.RawMessage) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if t == nil || t.broker == nil {
		return "", errors.New("selection broker is unavailable")
	}
	questions, err := decodeInput(raw)
	if err != nil {
		return "", err
	}
	request := Request{Questions: make([]Question, len(questions))}
	for i, question := range questions {
		request.Questions[i] = Question{
			Prompt: question.Prompt, Mode: question.Mode, Options: question.Options,
			InitialSelectedIDs: question.InitialSelectedIDs,
			MinSelect:          question.MinSelect, MaxSelect: question.MaxSelect,
		}
	}
	result, err := t.broker.Ask(ctx, request)
	if err != nil {
		return "", err
	}
	result, err = normalizeBatchResult(request, result)
	if err != nil {
		return "", fmt.Errorf("invalid question broker result: %w", err)
	}
	data, err := json.Marshal(BatchResult{Results: result.questionResults()})
	if err != nil {
		return "", fmt.Errorf("encode question result: %w", err)
	}
	return string(data), nil
}

func normalizeBatchResult(request Request, result Result) (Result, error) {
	questions := request.questionList()
	results := result.questionResults()
	if len(results) != len(questions) {
		return Result{}, fmt.Errorf("result count %d does not match question count %d", len(results), len(questions))
	}
	cancelled := false
	for i := range results {
		normalized, err := normalizeQuestionResult(questions[i], results[i])
		if err != nil {
			return Result{}, fmt.Errorf("results[%d]: %w", i, err)
		}
		results[i] = normalized
		cancelled = cancelled || normalized.Cancelled
	}
	if cancelled {
		for i := range results {
			results[i] = Result{Cancelled: true, SelectedOptions: []SelectedOption{}}
		}
	}
	result.Results = results
	result.Cancelled = cancelled
	result.SelectedOptions = nil
	return result, nil
}

func normalizeResult(request Request, result Result) (Result, error) {
	if len(request.questionList()) != 1 {
		return normalizeBatchResult(request, result)
	}
	questions := request.questionList()
	normalized, err := normalizeQuestionResult(questions[0], QuestionResult{Cancelled: result.Cancelled, SelectedOptions: result.SelectedOptions})
	if err != nil {
		return Result{}, err
	}
	result.Cancelled = normalized.Cancelled
	result.SelectedOptions = normalized.SelectedOptions
	return result, nil
}

func normalizeQuestionResult(request Question, result QuestionResult) (QuestionResult, error) {
	if result.Cancelled {
		return QuestionResult{Cancelled: true, SelectedOptions: []SelectedOption{}}, nil
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
			return QuestionResult{}, fmt.Errorf("selected_options[%d].id is required", i)
		}
		if _, duplicate := seen[selected.ID]; duplicate {
			return QuestionResult{}, fmt.Errorf("duplicate selected option id: %s", selected.ID)
		}
		seen[selected.ID] = struct{}{}
		if selected.ID == CustomOptionID {
			customCount++
			if customCount > 1 {
				return QuestionResult{}, fmt.Errorf("at most one custom option is allowed")
			}
			if selected.Label == "" {
				return QuestionResult{}, fmt.Errorf("custom option label is required")
			}
			continue
		}
		label, ok := canonical[selected.ID]
		if !ok {
			return QuestionResult{}, fmt.Errorf("selected option id %q is not in the request", selected.ID)
		}
		selected.Label = label
	}
	count := len(result.SelectedOptions)
	if request.Mode == ModeSingle && count != 1 {
		return QuestionResult{}, fmt.Errorf("single mode requires exactly one selected option, got %d", count)
	}
	if request.Mode == ModeMultiple && (count < request.MinSelect || count > request.MaxSelect) {
		return QuestionResult{}, fmt.Errorf("multiple mode selection count %d is outside allowed range %d-%d", count, request.MinSelect, request.MaxSelect)
	}
	if result.SelectedOptions == nil {
		result.SelectedOptions = []SelectedOption{}
	}
	return result, nil
}
