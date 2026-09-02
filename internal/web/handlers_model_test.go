package web

import (
	"testing"

	configv2 "paw/internal/config"
)

func boolPtr(value bool) *bool { return &value }

func TestModelOptionsFromSnapshot(t *testing.T) {
	snapshot := configv2.Snapshot{
		ActiveModelID: "local/a",
		EffectiveModels: map[string]configv2.CatalogModel{
			"local/b": {Model: configv2.Model{Provider: "local", Name: "b"}},
			"local/a": {
				Model: configv2.Model{
					Provider:     "local",
					Name:         "a",
					Capabilities: configv2.Capabilities{Reasoning: boolPtr(true)},
					Parameters:   map[string]any{"reasoning": map[string]any{"effort": "high"}},
				},
				Source: configv2.ModelSourceConfigured,
			},
		},
	}
	response := modelOptionsFromSnapshot(snapshot)
	if response.ActiveModelID != "local/a" {
		t.Fatalf("active model = %q, want local/a", response.ActiveModelID)
	}
	if len(response.Models) != 2 {
		t.Fatalf("models = %d, want 2", len(response.Models))
	}
	// 按 ID 排序，local/a 在前。
	first := response.Models[0]
	if first.ID != "local/a" || !first.ReasoningCapable || first.Effort != "high" {
		t.Fatalf("first option = %+v", first)
	}
	if response.Models[1].ReasoningCapable || response.Models[1].Effort != "" {
		t.Fatalf("second option = %+v", response.Models[1])
	}
	if len(response.EffortOptions) == 0 || response.EffortOptions[0] != "default" {
		t.Fatalf("effort options = %v", response.EffortOptions)
	}
}

func TestModelEffortParsing(t *testing.T) {
	if got := modelEffort(configv2.Model{}); got != "" {
		t.Fatalf("empty parameters effort = %q, want empty", got)
	}
	if got := modelEffort(configv2.Model{Parameters: map[string]any{"reasoning": map[string]any{"effort": " Max "}}}); got != "max" {
		t.Fatalf("effort = %q, want max", got)
	}
	if !isValidEffort("default") || !isValidEffort("low") || isValidEffort("extreme") {
		t.Fatal("isValidEffort mismatch")
	}
}
