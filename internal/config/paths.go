package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Paths struct {
	Home            string
	GlobalConfig    string
	Settings        string
	MCP             string
	Skills          string
	Schemas         string
	Schema          string
	MigrationMarker string
	LegacyHome      string
	LegacyConfig    string
	WorkspaceRoot   string
	WorkspaceConfig string
}

// PathOptions makes path behavior deterministic in tests and keeps callers
// from mutating HOME/USERPROFILE process-wide.
type PathOptions struct {
	ConfigHome    string
	UserConfigDir string
	UserHomeDir   string
	WorkspaceRoot string
}

func ResolvePaths(options PathOptions) (Paths, error) {
	root := strings.TrimSpace(options.ConfigHome)
	if root == "" {
		root = strings.TrimSpace(os.Getenv("PAW_CONFIG_HOME"))
	}
	if root == "" {
		base := strings.TrimSpace(options.UserConfigDir)
		if base == "" {
			var err error
			base, err = os.UserConfigDir()
			if err != nil {
				return Paths{}, fmt.Errorf("resolve user config directory: %w", err)
			}
		}
		root = filepath.Join(base, "Paw")
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return Paths{}, fmt.Errorf("resolve Paw config directory: %w", err)
	}

	userHome := strings.TrimSpace(options.UserHomeDir)
	if userHome == "" {
		userHome, err = os.UserHomeDir()
		if err != nil {
			return Paths{}, fmt.Errorf("resolve user home directory: %w", err)
		}
	}
	legacyHome := filepath.Join(userHome, ".paw")
	paths := Paths{
		Home:            root,
		GlobalConfig:    filepath.Join(root, "config.jsonc"),
		Settings:        filepath.Join(root, "settings.json"),
		MCP:             filepath.Join(root, "mcp.toml"),
		Skills:          filepath.Join(root, "skills"),
		Schemas:         filepath.Join(root, "schemas"),
		Schema:          filepath.Join(root, "schemas", "config-v2.schema.json"),
		MigrationMarker: filepath.Join(root, ".migration-v2.json"),
		LegacyHome:      legacyHome,
		LegacyConfig:    filepath.Join(legacyHome, "config.json"),
	}
	if work := strings.TrimSpace(options.WorkspaceRoot); work != "" {
		work, err = filepath.Abs(work)
		if err != nil {
			return Paths{}, fmt.Errorf("resolve workspace directory: %w", err)
		}
		paths.WorkspaceRoot = work
		paths.WorkspaceConfig = filepath.Join(work, ".paw", "config.jsonc")
	}
	return paths, nil
}
