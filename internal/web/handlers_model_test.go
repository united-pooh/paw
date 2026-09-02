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
			"local/c": {Model: configv2.Model{
				Provider:     "local",
				Name:         "c",
				Capabilities: configv2.Capabilities{Reasoning: boolPtr(false)},
			}},
			"local/a": {
				Model: configv2.Model{
					Provider:     "local",
					Name:         "a",
					Capabilities: configv2.Capabilities{Reasoning: boolPtr(true)},
					Parameters:   map[string]any{"reasoning_effort": "high"},
				},
				Source: configv2.ModelSourceConfigured,
			},
		},
	}
	response := modelOptionsFromSnapshot(snapshot)
	if response.ActiveModelID != "local/a" {
		t.Fatalf("active model = %q, want local/a", response.ActiveModelID)
	}
	if len(response.Models) != 3 {
		t.Fatalf("models = %d, want 3", len(response.Models))
	}
	// 按 ID 排序，local/a 在前。
	first := response.Models[0]
	if first.ID != "local/a" || !first.ReasoningCapable || first.Effort != "high" {
		t.Fatalf("first option = %+v", first)
	}
	// 能力标记未知（nil）视为可调：未显式声明不支持推理的模型不禁用强度选择。
	if !response.Models[1].ReasoningCapable || response.Models[1].Effort != "" {
		t.Fatalf("second option = %+v", response.Models[1])
	}
	// 显式声明不支持推理的模型禁用强度选择。
	if response.Models[2].ReasoningCapable {
		t.Fatalf("third option = %+v", response.Models[2])
	}
	if len(response.EffortOptions) == 0 || response.EffortOptions[0] != "default" {
		t.Fatalf("effort options = %v", response.EffortOptions)
	}
}

func TestModelEffortParsing(t *testing.T) {
	if got := modelEffort(configv2.Model{}); got != "" {
		t.Fatalf("empty parameters effort = %q, want empty", got)
	}
	// 规范存储：扁平 reasoning_effort（与 TUI、运行时一致）。
	if got := modelEffort(configv2.Model{Parameters: map[string]any{"reasoning_effort": " Max "}}); got != "max" {
		t.Fatalf("flat effort = %q, want max", got)
	}
	// 旧版 Web UI 写入的嵌套 reasoning.effort 仍可回读。
	if got := modelEffort(configv2.Model{Parameters: map[string]any{"reasoning": map[string]any{"effort": "low"}}}); got != "low" {
		t.Fatalf("legacy nested effort = %q, want low", got)
	}
	// 两种键并存时以扁平键为准。
	if got := modelEffort(configv2.Model{Parameters: map[string]any{
		"reasoning_effort": "medium",
		"reasoning":        map[string]any{"effort": "low"},
	}}); got != "medium" {
		t.Fatalf("conflicting effort = %q, want medium", got)
	}
	if !isValidEffort("default") || !isValidEffort("low") || !isValidEffort("xhigh") || isValidEffort("extreme") {
		t.Fatal("isValidEffort mismatch")
	}
}

func TestApplyEffortToParameters(t *testing.T) {
	// 设置档位：写入扁平键，保留其余参数。
	got := applyEffortToParameters(map[string]any{"temperature": 0.5}, "high")
	if got["reasoning_effort"] != "high" || got["temperature"] != 0.5 {
		t.Fatalf("set effort = %#v", got)
	}
	// default：移除扁平键。
	got = applyEffortToParameters(map[string]any{"reasoning_effort": "high"}, "default")
	if _, ok := got["reasoning_effort"]; ok {
		t.Fatalf("default should remove flat key: %#v", got)
	}
	// 迁移：嵌套 reasoning.effort 被清理，reasoning 其他键保留。
	got = applyEffortToParameters(map[string]any{
		"reasoning": map[string]any{"effort": "low", "summary": "auto"},
	}, "high")
	if got["reasoning_effort"] != "high" {
		t.Fatalf("migrated flat effort = %#v", got)
	}
	reasoning, ok := got["reasoning"].(map[string]any)
	if !ok || len(reasoning) != 1 || reasoning["summary"] != "auto" {
		t.Fatalf("reasoning after migration = %#v", got["reasoning"])
	}
	// 迁移：嵌套仅剩 effort 时整个 reasoning 对象移除。
	got = applyEffortToParameters(map[string]any{
		"reasoning": map[string]any{"effort": "low"},
	}, "default")
	if _, ok := got["reasoning"]; ok {
		t.Fatalf("empty reasoning should be removed: %#v", got)
	}
	if _, ok := got["reasoning_effort"]; ok {
		t.Fatalf("default should remove flat key: %#v", got)
	}
}
