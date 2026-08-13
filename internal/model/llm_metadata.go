package model

import (
	_ "embed"
	"encoding/json"
	"strings"
	"sync"
)

// contextWindowsJSON is generated from basellm/llm-metadata's built API
// (dist/api/all.json) by scripts/gen-llm-metadata.sh. It maps lowercased
// model ids to their context window in tokens; when the same model name
// exists under multiple providers the largest window wins.
//
//go:embed metadata/context_windows.json
var contextWindowsJSON []byte

var (
	metadataOnce           sync.Once
	metadataContextWindows map[string]int
)

func loadMetadataContextWindows() map[string]int {
	metadataOnce.Do(func() {
		var payload struct {
			ContextWindows map[string]int `json:"contextWindows"`
		}
		if err := json.Unmarshal(contextWindowsJSON, &payload); err != nil {
			return
		}
		metadataContextWindows = payload.ContextWindows
	})
	return metadataContextWindows
}

// MetadataContextLimit returns the embedded llm-metadata context window for
// the model, or 0 when no metadata is available. Matching is case-insensitive
// and ignores any "provider/" prefix on the model name.
func MetadataContextLimit(provider, model string) int {
	model = strings.TrimSpace(model)
	if slash := strings.LastIndexByte(model, '/'); slash >= 0 {
		model = model[slash+1:]
	}
	if model == "" {
		return 0
	}
	return loadMetadataContextWindows()[strings.ToLower(model)]
}
