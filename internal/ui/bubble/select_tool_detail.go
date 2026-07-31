package bubble

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	selecttool "paw/internal/tool/select"
)

type selectToolPresentation struct {
	target string
	detail string
}

type selectToolRequest struct {
	Prompt             string              `json:"prompt"`
	Mode               selecttool.Mode     `json:"mode"`
	Options            []selecttool.Option `json:"options"`
	InitialSelectedID  *string             `json:"initial_selected_id,omitempty"`
	InitialSelectedIDs *[]string           `json:"initial_selected_ids,omitempty"`
	MinSelect          *int                `json:"min_select,omitempty"`
	MaxSelect          *int                `json:"max_select,omitempty"`
}

type selectToolResult struct {
	Cancelled       bool                        `json:"cancelled"`
	SelectedOptions []selecttool.SelectedOption `json:"selected_options"`
}

func parseSelectToolPresentation(input json.RawMessage, content string) (selectToolPresentation, bool) {
	var request selectToolRequest
	if !decodeStrictJSON(input, &request) || !validSelectToolRequest(request) {
		return selectToolPresentation{}, false
	}
	var result selectToolResult
	if !decodeStrictJSON([]byte(content), &result) || result.SelectedOptions == nil || !validSelectedOptions(request, result) {
		return selectToolPresentation{}, false
	}

	prompt := strings.TrimSpace(request.Prompt)
	if result.Cancelled {
		return selectToolPresentation{
			target: "cancelled",
			detail: prompt + "\n\nSelection cancelled.",
		}, true
	}

	descriptions := make(map[string]string, len(request.Options))
	for _, option := range request.Options {
		descriptions[option.ID] = strings.TrimSpace(option.Description)
	}
	parts := []string{prompt}
	for _, selected := range result.SelectedOptions {
		block := strings.TrimSpace(selected.Label)
		description := descriptions[selected.ID]
		if selected.ID == selecttool.CustomOptionID {
			description = "Custom option"
		}
		if description != "" {
			block += "\n  " + description
		}
		parts = append(parts, block)
	}
	count := len(result.SelectedOptions)
	return selectToolPresentation{
		target: fmt.Sprintf("selected %d %s", count, optionNoun(count)),
		detail: strings.Join(parts, "\n\n"),
	}, true
}

func decodeStrictJSON(data []byte, target any) bool {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return false
	}
	return decoder.Decode(&struct{}{}) == io.EOF
}

func validSelectToolRequest(request selectToolRequest) bool {
	if strings.TrimSpace(request.Prompt) == "" || len(request.Options) == 0 {
		return false
	}
	if request.Mode != selecttool.ModeSingle && request.Mode != selecttool.ModeMultiple {
		return false
	}
	seen := make(map[string]struct{}, len(request.Options))
	for _, option := range request.Options {
		if strings.TrimSpace(option.ID) == "" || strings.TrimSpace(option.Label) == "" || option.ID == selecttool.CustomOptionID {
			return false
		}
		if _, exists := seen[option.ID]; exists {
			return false
		}
		seen[option.ID] = struct{}{}
	}
	return true
}

func validSelectedOptions(request selectToolRequest, result selectToolResult) bool {
	if result.Cancelled && len(result.SelectedOptions) != 0 {
		return false
	}
	requestIDs := make(map[string]struct{}, len(request.Options))
	for _, option := range request.Options {
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
