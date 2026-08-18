package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Paths struct {
	Home                string
	GlobalConfig        string
	Settings            string
	MCP                 string
	Skills              string
	Schemas             string
	Schema              string
	ModelDiscoveryCache string
	LegacyAssetsHome    string
	LegacyV2Home        string
	WorkspaceRoot       string
	WorkspaceConfig     string
}

// PathOptions makes path behavior deterministic in tests without mutating
// process-wide environment variables.
type PathOptions struct {
	ConfigHome    string
	UserConfigDir string
	UserHomeDir   string
	LegacyV2Home  string
	WorkspaceRoot string
}

func ResolvePaths(options PathOptions) (Paths, error) {
	userHome := strings.TrimSpace(options.UserHomeDir)
	if userHome == "" {
		var err error
		userHome, err = os.UserHomeDir()
		if err != nil {
			return Paths{}, fmt.Errorf("resolve user home directory: %w", err)
		}
	}
	userHome, err := filepath.Abs(userHome)
	if err != nil {
		return Paths{}, fmt.Errorf("resolve user home directory: %w", err)
	}

	configuredRoot := strings.TrimSpace(options.ConfigHome)
	if configuredRoot == "" {
		configuredRoot = strings.TrimSpace(os.Getenv("PAW_CONFIG_HOME"))
	}
	explicitRoot := configuredRoot != ""
	root := configuredRoot
	userConfigDir := strings.TrimSpace(options.UserConfigDir)
	if userConfigDir == "" && !explicitRoot {
		userConfigDir, err = os.UserConfigDir()
		if err != nil {
			return Paths{}, fmt.Errorf("resolve user config directory: %w", err)
		}
	}
	if root == "" {
		root = filepath.Join(userHome, ".paw")
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return Paths{}, fmt.Errorf("resolve Paw config directory: %w", err)
	}

	legacyV2Home := strings.TrimSpace(options.LegacyV2Home)
	if legacyV2Home == "" && !explicitRoot && userConfigDir != "" {
		legacyV2Home = filepath.Join(userConfigDir, "Paw")
	}
	if legacyV2Home != "" {
		legacyV2Home, err = filepath.Abs(legacyV2Home)
		if err != nil {
			return Paths{}, fmt.Errorf("resolve legacy Paw v2 directory: %w", err)
		}
	}

	paths := Paths{
		Home:                root,
		GlobalConfig:        filepath.Join(root, "config.jsonc"),
		Settings:            filepath.Join(root, "settings.json"),
		MCP:                 filepath.Join(root, "mcp.toml"),
		Skills:              filepath.Join(root, "skills"),
		Schemas:             filepath.Join(root, "schemas"),
		Schema:              filepath.Join(root, "schemas", "config-v2.schema.json"),
		ModelDiscoveryCache: filepath.Join(root, "model-discovery-cache.json"),
		LegacyAssetsHome:    filepath.Join(userHome, ".paw"),
		LegacyV2Home:        legacyV2Home,
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
