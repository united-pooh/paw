package bubble

import (
	"encoding/json"
	"fmt"
	"strings"

	selecttool "paw/internal/tool/select"
)

type selectToolPresentation struct {
	target string
	detail string
}

type selectToolQuestion struct {
	Prompt  string              `json:"prompt"`
	Mode    selecttool.Mode     `json:"mode"`
	Options []selecttool.Option `json:"options"`
}

type selectToolRequest struct {
	Questions []selectToolQuestion `json:"questions"`
}

type selectToolResult struct {
	Cancelled       bool                        `json:"cancelled"`
	SelectedOptions []selecttool.SelectedOption `json:"selected_options"`
}

type selectToolBatchResult struct {
	Results []selectToolResult `json:"results"`
}

func parseSelectToolPresentation(input json.RawMessage, content string) (selectToolPresentation, bool) {
	var request selectToolRequest
	if !decodeJSON(input, &request) || len(request.Questions) == 0 {
		return selectToolPresentation{}, false
	}
	for i := range request.Questions {
		if !normalizeSelectToolQuestion(&request.Questions[i]) {
			return selectToolPresentation{}, false
		}
	}
	var batch selectToolBatchResult
	if !decodeJSON([]byte(content), &batch) || len(batch.Results) != len(request.Questions) {
		return selectToolPresentation{}, false
	}
	for i := range batch.Results {
		if batch.Results[i].SelectedOptions == nil {
			return selectToolPresentation{}, false
		}
		for j := range batch.Results[i].SelectedOptions {
			batch.Results[i].SelectedOptions[j].ID = strings.TrimSpace(batch.Results[i].SelectedOptions[j].ID)
			batch.Results[i].SelectedOptions[j].Label = strings.TrimSpace(batch.Results[i].SelectedOptions[j].Label)
		}
		if !validSelectedOptions(request.Questions[i], batch.Results[i]) {
			return selectToolPresentation{}, false
		}
	}

	if batch.Results[0].Cancelled {
		return selectToolPresentation{
			target: "cancelled",
			detail: questionHeaders(request) + "\n\nSelection cancelled.",
		}, true
	}

	parts := make([]string, 0, len(request.Questions))
	for i, question := range request.Questions {
		block := []string{fmt.Sprintf("Q%d  %s", i+1, strings.TrimSpace(question.Prompt))}
		descriptions := make(map[string]string, len(question.Options))
		for _, option := range question.Options {
			descriptions[option.ID] = strings.TrimSpace(option.Description)
		}
		for _, selected := range batch.Results[i].SelectedOptions {
			line := strings.TrimSpace(selected.Label)
			description := descriptions[selected.ID]
			if selected.ID == selecttool.CustomOptionID {
				description = "Custom option"
			}
			if description != "" {
				line += "\n  " + description
			}
			block = append(block, line)
		}
		parts = append(parts, strings.Join(block, "\n\n"))
	}
	return selectToolPresentation{
		target: fmt.Sprintf("answered %d %s", len(request.Questions), questionNoun(len(request.Questions))),
		detail: strings.Join(parts, "\n\n"),
	}, true
}

func questionNoun(count int) string {
	if count == 1 {
		return "question"
	}
	return "questions"
}

func questionHeaders(request selectToolRequest) string {
	headers := make([]string, 0, len(request.Questions))
	for i, question := range request.Questions {
		headers = append(headers, fmt.Sprintf("Q%d  %s", i+1, strings.TrimSpace(question.Prompt)))
	}
	return strings.Join(headers, "\n")
}

func decodeJSON(data []byte, target any) bool {
	return json.Unmarshal(data, target) == nil
}

func normalizeSelectToolQuestion(question *selectToolQuestion) bool {
	question.Prompt = strings.TrimSpace(question.Prompt)
	if question.Prompt == "" {
		return false
	}
	if question.Mode != selecttool.ModeSingle && question.Mode != selecttool.ModeMultiple {
		return false
	}
	if len(question.Options) == 0 {
		return false
	}
	seen := make(map[string]struct{}, len(question.Options))
	for i := range question.Options {
		question.Options[i].ID = strings.TrimSpace(question.Options[i].ID)
		question.Options[i].Label = strings.TrimSpace(question.Options[i].Label)
		question.Options[i].Description = strings.TrimSpace(question.Options[i].Description)
		if question.Options[i].ID == "" || question.Options[i].Label == "" || question.Options[i].ID == selecttool.CustomOptionID {
			return false
		}
		if _, exists := seen[question.Options[i].ID]; exists {
			return false
		}
		seen[question.Options[i].ID] = struct{}{}
	}
	return true
}

func validSelectedOptions(question selectToolQuestion, result selectToolResult) bool {
	if result.Cancelled && len(result.SelectedOptions) != 0 {
		return false
	}
	requestIDs := make(map[string]struct{}, len(question.Options))
	for _, option := range question.Options {
		requestIDs[option.ID] = struct{}{}
	}
	seen := make(map[string]struct{}, len(result.SelectedOptions))
	for _, option := range result.SelectedOptions {
		if strings.TrimSpace(option.ID) == "" || strings.TrimSpace(option.Label) == "" {
			return false
		}
		if option.ID != selecttool.CustomOptionID {
			if _, exists := requestIDs[option.ID]; !exists {
				return false
			}
		}
		if _, exists := seen[option.ID]; exists {
			return false
		}
		seen[option.ID] = struct{}{}
	}
	return true
}
