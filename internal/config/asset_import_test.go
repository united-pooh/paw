package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCopyLegacyV2AssetsPreservesLegacyFilesAndAddsMissingEntries(t *testing.T) {
	root := t.TempDir()
	userHome := filepath.Join(root, "home")
	legacyHome := filepath.Join(userHome, ".paw")
	v2Home := filepath.Join(root, "Library", "Application Support", "Paw")
	if err := os.MkdirAll(legacyHome, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(v2Home, 0o700); err != nil {
		t.Fatal(err)
	}

	legacySettings := `{"ui":{"theme":"dracula"}}
`
	v2Settings := `{"context_compression":{"enabled":true},"ui":{"theme":"tokyo-night"}}
`
	if err := os.WriteFile(filepath.Join(legacyHome, "settings.json"), []byte(legacySettings), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(v2Home, "settings.json"), []byte(v2Settings), 0o600); err != nil {
		t.Fatal(err)
	}

	legacyMCP := `[mcp_servers.jina-mcp-server]
url = "https://mcp.jina.ai/v1"
`
	v2MCP := `[mcp_servers.colab-mcp]
command = "colab-mcp"
args = ["serve"]
`
	if err := os.WriteFile(filepath.Join(legacyHome, "mcp.toml"), []byte(legacyMCP), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(v2Home, "mcp.toml"), []byte(v2MCP), 0o600); err != nil {
		t.Fatal(err)
	}

	v2Config := `{"schemaVersion":2,"activeModel":"newapi/model"}
`
	if err := os.WriteFile(filepath.Join(v2Home, "config.jsonc"), []byte(v2Config), 0o600); err != nil {
		t.Fatal(err)
	}

	paths := Paths{
		Home:                legacyHome,
		GlobalConfig:        filepath.Join(legacyHome, "config.jsonc"),
		Settings:            filepath.Join(legacyHome, "settings.json"),
		MCP:                 filepath.Join(legacyHome, "mcp.toml"),
		ModelDiscoveryCache: filepath.Join(legacyHome, "model-discovery-cache.json"),
		LegacyV2Home:        v2Home,
	}
	if diagnostics := copyLegacyAssets(paths); len(diagnostics) != 0 {
		t.Fatalf("copyLegacyAssets diagnostics=%v", diagnostics)
	}

	if got, err := os.ReadFile(paths.GlobalConfig); err != nil || string(got) != v2Config {
		t.Fatalf("config.jsonc got=%q err=%v", got, err)
	}
	if got, err := os.ReadFile(paths.Settings); err != nil {
		t.Fatal(err)
	} else {
		if !strings.Contains(string(got), `"theme": "dracula"`) {
			t.Fatalf("legacy settings were not preserved: %s", got)
		}
		if !strings.Contains(string(got), `"context_compression":`) {
			t.Fatalf("v2-only settings were not added: %s", got)
		}
	}
	if got, err := os.ReadFile(paths.MCP); err != nil {
		t.Fatal(err)
	} else {
		if !strings.Contains(string(got), "jina-mcp-server") || !strings.Contains(string(got), "colab-mcp") {
			t.Fatalf("MCP entries were not merged: %s", got)
		}
	}
}

func TestCopyLegacyV2AssetsOnlyRunsOnce(t *testing.T) {
	root := t.TempDir()
	userHome := filepath.Join(root, "home")
	legacyHome := filepath.Join(userHome, ".paw")
	v2Home := filepath.Join(root, "Library", "Application Support", "Paw")
	if err := os.MkdirAll(legacyHome, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(v2Home, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacyHome, "settings.json"), []byte(`{"ui":{"theme":"legacy"}}
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(v2Home, "settings.json"), []byte(`{"ui":{"theme":"v2"}}
`), 0o600); err != nil {
		t.Fatal(err)
	}

	paths := Paths{
		Home:         legacyHome,
		GlobalConfig: filepath.Join(legacyHome, "config.jsonc"),
		Settings:     filepath.Join(legacyHome, "settings.json"),
		MCP:          filepath.Join(legacyHome, "mcp.toml"),
		LegacyV2Home: v2Home,
	}
	if diagnostics := copyLegacyAssets(paths); len(diagnostics) != 0 {
		t.Fatalf("first migration diagnostics=%v", diagnostics)
	}
	if _, err := os.Stat(filepath.Join(paths.Home, migrationMarkerName)); err != nil {
		t.Fatalf("migration marker: %v", err)
	}
	if err := os.WriteFile(filepath.Join(v2Home, "settings.json"), []byte(`{"ui":{"theme":"changed-v2"}}
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if diagnostics := copyLegacyAssets(paths); len(diagnostics) != 0 {
		t.Fatalf("second migration diagnostics=%v", diagnostics)
	}
	got, err := os.ReadFile(paths.Settings)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(got), "changed-v2") {
		t.Fatalf("completed migration was repeated: %s", got)
	}
}
