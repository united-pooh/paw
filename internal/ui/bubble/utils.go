package bubble

import (
	"encoding/json"
	"strings"
)

func summarizeToolContent(content string) string {
	trimmed := strings.Join(strings.Fields(content), " ")
	if trimmed == "" {
		return ""
	}
	const maxPreviewRunes = 140
	runes := []rune(trimmed)
	if len(runes) <= maxPreviewRunes {
		return trimmed
	}
	return string(runes[:maxPreviewRunes]) + "..."
}

func prettyJSON(raw json.RawMessage) string {
	input := strings.TrimSpace(string(raw))
	if input == "" {
		return "{}"
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return input
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return input
	}
	return string(encoded)
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
