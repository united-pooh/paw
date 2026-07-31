package selecttool

import (
	"encoding/json"
	"fmt"
	"strings"
)

type toolInput struct {
	Prompt             string    `json:"prompt"`
	Mode               Mode      `json:"mode"`
	Options            []Option  `json:"options"`
	InitialSelectedID  *string   `json:"initial_selected_id"`
	InitialSelectedIDs *[]string `json:"initial_selected_ids"`
	MinSelect          *int      `json:"min_select"`
	MaxSelect          *int      `json:"max_select"`
}

func decodeInput(raw json.RawMessage) (Request, error) {
	var in toolInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return Request{}, fmt.Errorf("decode Select input: %w", err)
	}
	in.Prompt = strings.TrimSpace(in.Prompt)
	if in.Prompt == "" {
		return Request{}, fmt.Errorf("prompt is required")
	}
	if in.Mode != ModeSingle && in.Mode != ModeMultiple {
		return Request{}, fmt.Errorf("mode must be \"single\" or \"multiple\"")
	}
	if len(in.Options) == 0 {
		return Request{}, fmt.Errorf("options must contain at least one option")
	}
	ids := make(map[string]struct{}, len(in.Options))
	options := append([]Option(nil), in.Options...)
	for i := range options {
		options[i].ID = strings.TrimSpace(options[i].ID)
		options[i].Label = strings.TrimSpace(options[i].Label)
		options[i].Description = strings.TrimSpace(options[i].Description)
		if options[i].ID == "" {
			return Request{}, fmt.Errorf("options[%d].id is required", i)
		}
		if options[i].ID == CustomOptionID {
			return Request{}, fmt.Errorf("option id %q is reserved", CustomOptionID)
		}
		if options[i].Label == "" {
			return Request{}, fmt.Errorf("options[%d].label is required", i)
		}
		if _, exists := ids[options[i].ID]; exists {
			return Request{}, fmt.Errorf("duplicate option id: %s", options[i].ID)
		}
		ids[options[i].ID] = struct{}{}
	}

	request := Request{Prompt: in.Prompt, Mode: in.Mode, Options: options}
	if in.Mode == ModeSingle {
		if in.InitialSelectedIDs != nil {
			return Request{}, fmt.Errorf("initial_selected_ids is only valid in multiple mode")
		}
		if in.MinSelect != nil {
			return Request{}, fmt.Errorf("min_select is only valid in multiple mode")
		}
		if in.MaxSelect != nil {
			return Request{}, fmt.Errorf("max_select is only valid in multiple mode")
		}
		request.MinSelect, request.MaxSelect = 1, 1
		if in.InitialSelectedID != nil {
			id := strings.TrimSpace(*in.InitialSelectedID)
			if _, ok := ids[id]; !ok {
				return Request{}, fmt.Errorf("initial_selected_id references unknown option id: %s", id)
			}
			request.InitialSelectedIDs = []string{id}
		}
		return request, nil
	}

	if in.InitialSelectedID != nil {
		return Request{}, fmt.Errorf("initial_selected_id is only valid in single mode")
	}
	request.MinSelect = 0
	request.MaxSelect = len(options)
	if in.MinSelect != nil {
		request.MinSelect = *in.MinSelect
	}
	if in.MaxSelect != nil {
		request.MaxSelect = *in.MaxSelect
	}
	if request.MinSelect < 0 || request.MinSelect > len(options) {
		return Request{}, fmt.Errorf("min_select must be between 0 and %d", len(options))
	}
	if request.MaxSelect < 0 || request.MaxSelect > len(options) {
		return Request{}, fmt.Errorf("max_select must be between 0 and %d", len(options))
	}
	if request.MinSelect > request.MaxSelect {
		return Request{}, fmt.Errorf("min_select must not exceed max_select")
	}
	if in.InitialSelectedIDs != nil {
		seen := make(map[string]struct{}, len(*in.InitialSelectedIDs))
		for _, rawID := range *in.InitialSelectedIDs {
			id := strings.TrimSpace(rawID)
			if _, duplicate := seen[id]; duplicate {
				return Request{}, fmt.Errorf("duplicate initial selected id: %s", id)
			}
			if _, ok := ids[id]; !ok {
				return Request{}, fmt.Errorf("initial_selected_ids references unknown option id: %s", id)
			}
			seen[id] = struct{}{}
			request.InitialSelectedIDs = append(request.InitialSelectedIDs, id)
		}
	}
	if len(request.InitialSelectedIDs) > request.MaxSelect {
		return Request{}, fmt.Errorf("initial selection count %d exceeds max_select %d", len(request.InitialSelectedIDs), request.MaxSelect)
	}
	return request, nil
}
