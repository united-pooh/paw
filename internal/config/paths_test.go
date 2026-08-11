package config

import (
	"path/filepath"
	"testing"
)

func TestResolvePathsUsesExplicitConfigAndWorkspaceRoots(t *testing.T) {
	root := t.TempDir()
	configHome := filepath.Join(root, "portable-paw")
	workspace := filepath.Join(root, "work")
	paths, err := ResolvePaths(PathOptions{ConfigHome: configHome, UserHomeDir: filepath.Join(root, "home"), WorkspaceRoot: workspace})
	if err != nil {
		t.Fatal(err)
	}
	checks := map[string]string{"home": paths.Home, "config": paths.GlobalConfig, "settings": paths.Settings, "mcp": paths.MCP, "skills": paths.Skills, "schema": paths.Schema, "workspace": paths.WorkspaceConfig}
	wants := map[string]string{"home": configHome, "config": filepath.Join(configHome, "config.jsonc"), "settings": filepath.Join(configHome, "settings.json"), "mcp": filepath.Join(configHome, "mcp.toml"), "skills": filepath.Join(configHome, "skills"), "schema": filepath.Join(configHome, "schemas", "config-v2.schema.json"), "workspace": filepath.Join(workspace, ".paw", "config.jsonc")}
	for name, got := range checks {
		wantAbs, _ := filepath.Abs(wants[name])
		if got != wantAbs {
			t.Errorf("%s=%q want=%q", name, got, wantAbs)
		}
	}
}

func TestResolvePathsUsesInjectedUserConfigDirectory(t *testing.T) {
	t.Setenv("PAW_CONFIG_HOME", "")
	root := t.TempDir()
	paths, err := ResolvePaths(PathOptions{UserConfigDir: root, UserHomeDir: filepath.Join(root, "home")})
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, "Paw", "config.jsonc")
	if paths.GlobalConfig != want {
		t.Fatalf("config=%q want=%q", paths.GlobalConfig, want)
	}
}

func TestResolvePathsIncludesModelDiscoveryCache(t *testing.T) {
	root := t.TempDir()
	paths, err := ResolvePaths(PathOptions{ConfigHome: root, UserHomeDir: filepath.Join(root, "home")})
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, "model-discovery-cache.json")
	if paths.ModelDiscoveryCache != want {
		t.Fatalf("model discovery cache=%q want=%q", paths.ModelDiscoveryCache, want)
	}
}
