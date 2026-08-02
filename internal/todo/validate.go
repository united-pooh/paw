package todo

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

func DecodeUpdateInput(raw json.RawMessage) (UpdateInput, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()

	var input UpdateInput
	if err := decoder.Decode(&input); err != nil {
		return UpdateInput{}, fmt.Errorf("decode update_todo input: %w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return UpdateInput{}, err
	}
	if input.Items == nil {
		return UpdateInput{}, fmt.Errorf("items is required")
	}
	return NormalizeAndValidate(input)
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err == io.EOF {
		return nil
	} else if err != nil {
		return fmt.Errorf("decode update_todo input: %w", err)
	}
	return fmt.Errorf("decode update_todo input: multiple JSON values")
}

func NormalizeAndValidate(input UpdateInput) (UpdateInput, error) {
	input.Explanation = strings.TrimSpace(input.Explanation)
	input.Items = append([]Item{}, input.Items...)

	seen := make(map[string]struct{}, len(input.Items))
	active := 0
	for i := range input.Items {
		item := &input.Items[i]
		item.ID = strings.TrimSpace(item.ID)
		item.Content = strings.TrimSpace(item.Content)
		if item.ID == "" {
			return UpdateInput{}, fmt.Errorf("items[%d].id must not be empty", i)
		}
		if item.Content == "" {
			return UpdateInput{}, fmt.Errorf("items[%d].content must not be empty", i)
		}
		if _, exists := seen[item.ID]; exists {
			return UpdateInput{}, fmt.Errorf("items contains duplicate id %q", item.ID)
		}
		seen[item.ID] = struct{}{}
		switch item.Status {
		case StatusPending, StatusCompleted:
		case StatusInProgress:
			active++
		default:
			return UpdateInput{}, fmt.Errorf("items[%d].status must be pending, in_progress, or completed", i)
		}
	}
	if active > 1 {
		return UpdateInput{}, fmt.Errorf("items may contain at most one in_progress item")
	}
	return input, nil
}

func ValidateSnapshot(snapshot Snapshot) (Snapshot, error) {
	normalized, err := NormalizeAndValidate(UpdateInput{
		Explanation: snapshot.Explanation,
		Items:       snapshot.Items,
	})
	if err != nil {
		return Snapshot{}, err
	}
	snapshot.Explanation = normalized.Explanation
	snapshot.Items = normalized.Items
	return snapshot, nil
}
