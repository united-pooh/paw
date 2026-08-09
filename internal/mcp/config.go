package mcp

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// expandEnvList expands ${VAR} / $VAR references in a list of strings using the
// current process environment. Each element is expanded independently so a
// missing variable yields an empty argument rather than a malformed one.
func expandEnvList(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	expanded := make([]string, len(values))
	for i, value := range values {
		expanded[i] = os.ExpandEnv(value)
	}
	return expanded
}

// expandEnvMap expands ${VAR} / $VAR references in env map values using the
// current process environment. Keys are left untouched so callers can rely on
// the configured variable names, and so undefined variables resolve to empty
// values instead of a literal reference reaching the child process.
func expandEnvMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	expanded := make(map[string]string, len(values))
	for key, value := range values {
		expanded[key] = os.ExpandEnv(value)
	}
	return expanded
}

const (
	configDirectoryName = ".paw"
	configFileName      = "mcp.toml"
)

type rawConfig struct {
	Servers map[string]rawServerConfig `toml:"mcp_servers"`
}

type rawServerConfig struct {
	Command string            `toml:"command"`
	Args    []string          `toml:"args"`
	CWD     string            `toml:"cwd"`
	Enabled *bool             `toml:"enabled"`
	Env     map[string]string `toml:"env"`
}

// LoadConfig loads the global Paw MCP configuration. homeDir is injectable
// for tests; an empty value falls back to the current user's home directory.
func LoadConfig(homeDir, workspaceRoot string) (Config, error) {
	if strings.TrimSpace(homeDir) == "" {
		var err error
		homeDir, err = os.UserHomeDir()
		if err != nil {
			return Config{}, fmt.Errorf("resolve home directory: %w", err)
		}
	}
	homeDir, err := filepath.Abs(homeDir)
	if err != nil {
		return Config{}, fmt.Errorf("resolve home directory: %w", err)
	}
	workspaceRoot, err = filepath.Abs(strings.TrimSpace(workspaceRoot))
	if err != nil {
		return Config{}, fmt.Errorf("resolve workspace root: %w", err)
	}
	if strings.TrimSpace(workspaceRoot) == "" {
		return Config{}, fmt.Errorf("workspace root is empty")
	}

	path := filepath.Join(homeDir, configDirectoryName, configFileName)
	return LoadConfigFile(path, workspaceRoot)
}

// LoadConfigFile loads an explicitly resolved global MCP configuration path.
// Config-v2 callers use this to avoid re-reading HOME or assuming ~/.paw.
func LoadConfigFile(path, workspaceRoot string) (Config, error) {
	path, err := filepath.Abs(strings.TrimSpace(path))
	if err != nil {
		return Config{}, fmt.Errorf("resolve MCP config path: %w", err)
	}
	if strings.TrimSpace(path) == "" {
		return Config{}, fmt.Errorf("MCP config path is empty")
	}
	workspaceRoot, err = filepath.Abs(strings.TrimSpace(workspaceRoot))
	if err != nil {
		return Config{}, fmt.Errorf("resolve workspace root: %w", err)
	}
	if strings.TrimSpace(workspaceRoot) == "" {
		return Config{}, fmt.Errorf("workspace root is empty")
	}
	if err := ensureConfigFile(path); err != nil {
		return Config{}, err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read MCP config %s: %w", path, err)
	}
	config := rawConfig{}
	if strings.TrimSpace(string(data)) != "" {
		if _, err := toml.Decode(string(data), &config); err != nil {
			return Config{}, fmt.Errorf("parse MCP config %s: %w", path, err)
		}
	}

	servers := make(map[string]ServerConfig, len(config.Servers))
	for name, raw := range config.Servers {
		if err := validateServerName(name); err != nil {
			return Config{}, err
		}
		enabled := true
		if raw.Enabled != nil {
			enabled = *raw.Enabled
		}
		command := strings.TrimSpace(raw.Command)
		if enabled && command == "" {
			return Config{}, fmt.Errorf("MCP server %q has an empty command", name)
		}

		workDir := workspaceRoot
		if strings.TrimSpace(raw.CWD) != "" {
			workDir = raw.CWD
			if !filepath.IsAbs(workDir) {
				workDir = filepath.Join(workspaceRoot, workDir)
			}
			workDir, err = filepath.Abs(workDir)
			if err != nil {
				return Config{}, fmt.Errorf("resolve cwd for MCP server %q: %w", name, err)
			}
		}

		servers[name] = ServerConfig{
			Name:    name,
			Command: command,
			Args:    expandEnvList(raw.Args),
			WorkDir: workDir,
			Enabled: enabled,
			Env:     expandEnvMap(raw.Env),
		}
	}

	return Config{Path: path, Servers: servers}, nil
}

func ensureConfigFile(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create MCP config directory: %w", err)
	}
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("inspect MCP config %s: %w", path, err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		if os.IsExist(err) {
			return nil
		}
		return fmt.Errorf("create MCP config %s: %w", path, err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close MCP config %s: %w", path, err)
	}
	return nil
}

func validateServerName(name string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("MCP server name is empty")
	}
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			continue
		}
		return fmt.Errorf("invalid MCP server name %q: use only letters, digits, underscore, or hyphen", name)
	}
	return nil
}
