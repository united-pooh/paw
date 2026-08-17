package main

import (
	configv2 "paw/internal/config"
	"testing"
)

func TestEffectiveYoloModeUsesConfigOrCommandLine(t *testing.T) {
	if effectiveYoloMode(false, configv2.Document{}) {
		t.Fatal("YOLO enabled without config or command-line flag")
	}
	if !effectiveYoloMode(false, configv2.Document{Yolo: true}) {
		t.Fatal("config.jsonc YOLO setting was ignored")
	}
	if !effectiveYoloMode(true, configv2.Document{}) {
		t.Fatal("command-line YOLO flag was ignored")
	}
}

func TestConfigOpenOptionsUsesExplicitWorkerMode(t *testing.T) {
	paths := configv2.Paths{
		Home:                "/tmp/paw",
		GlobalConfig:        "/tmp/paw/config.jsonc",
		Settings:            "/tmp/paw/settings.json",
		MCP:                 "/tmp/paw/mcp.toml",
		Skills:              "/tmp/paw/skills",
		Schemas:             "/tmp/paw/schemas",
		Schema:              "/tmp/paw/schemas/config-v2.schema.json",
		ModelDiscoveryCache: "/tmp/paw/model-discovery-cache.json",
		WorkspaceRoot:       "/tmp/workspace",
		WorkspaceConfig:     "/tmp/workspace/.paw/config.jsonc",
	}
	tests := []struct {
		name     string
		context  taskRuntimeContext
		disabled bool
	}{
		{name: "root", context: taskRuntimeContext{}},
		{name: "root ignores depth", context: taskRuntimeContext{depth: 2}},
		{name: "first-level worker", context: taskRuntimeContext{workerMode: true, depth: 1, maxDepth: 4}, disabled: true},
		{name: "delegated worker", context: taskRuntimeContext{workerMode: true, depth: 2, maxDepth: 4}, disabled: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := configOpenOptions(paths, tt.context)
			if got.Paths != paths {
				t.Fatalf("paths changed: got %#v want %#v", got.Paths, paths)
			}
			if got.DisableModelDiscovery != tt.disabled {
				t.Fatalf("DisableModelDiscovery = %v, want %v", got.DisableModelDiscovery, tt.disabled)
			}
		})
	}
}
