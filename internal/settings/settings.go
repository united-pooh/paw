package settings

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"paw/internal/theme"
)

const (
	defaultSettingsRelativePath = ".paw/settings.json"
	DefaultContextLimitTokens   = 1024 * 1024

	ContextModeEmpty ContextMode = "empty"
	ContextModeFork  ContextMode = "fork"

	RunModeSync       RunMode = "sync"
	RunModeBackground RunMode = "background"

	MeterLocationInputTitle MeterLocation = "input-title"
	MeterLocationHeader     MeterLocation = "header"
	MeterLocationInputAbove MeterLocation = "input-above"
)

type ContextMode string
type RunMode string
type MeterLocation string
type HomeDirFunc func() (string, error)

type Config struct {
	Subagent SubagentConfig `json:"subagent"`
	UI       UIConfig       `json:"ui"`
}

type SubagentConfig struct {
	DefaultContextMode ContextMode `json:"default_context_mode"`
	DefaultRunMode     RunMode     `json:"default_run_mode"`
}

type UIConfig struct {
	Theme                theme.ThemeID `json:"theme"`
	ContextLimitTokens   int           `json:"context_limit_tokens"`
	ContextMeterLocation MeterLocation `json:"context_meter_location"`
}

type Controller struct {
	mu   sync.RWMutex
	path string
	cfg  Config
}

func DefaultConfig() Config {
	return Config{
		Subagent: SubagentConfig{DefaultContextMode: ContextModeEmpty, DefaultRunMode: RunModeBackground},
		UI:       UIConfig{Theme: theme.Default, ContextLimitTokens: DefaultContextLimitTokens, ContextMeterLocation: MeterLocationInputAbove},
	}
}

func DefaultPath(homeDir HomeDirFunc) (string, error) {
	if homeDir == nil {
		homeDir = os.UserHomeDir
	}
	home, err := homeDir()
	if err != nil {
		return "", fmt.Errorf("resolve settings home directory: %w", err)
	}
	if strings.TrimSpace(home) == "" {
		return "", fmt.Errorf("resolve settings home directory: empty path")
	}
	return filepath.Join(home, defaultSettingsRelativePath), nil
}

func NewDefaultController(homeDir HomeDirFunc) (*Controller, error) {
	path, err := DefaultPath(homeDir)
	if err != nil {
		return nil, err
	}
	return NewController(path)
}

func NewController(path string) (*Controller, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("settings path is empty")
	}
	cfg, err := Load(path)
	if err != nil {
		return nil, err
	}
	return &Controller{path: path, cfg: cfg}, nil
}

func Load(path string) (Config, error) {
	cfg := DefaultConfig()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return Config{}, fmt.Errorf("read settings %s: %w", path, err)
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse settings %s: %w", path, err)
	}
	return Normalize(cfg), nil
}

func Save(path string, cfg Config) error {
	cfg = Normalize(cfg)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create settings directory %s: %w", filepath.Dir(path), err)
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal settings %s: %w", path, err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write settings %s: %w", path, err)
	}
	return nil
}

func Normalize(cfg Config) Config {
	cfg.Subagent.DefaultContextMode = NormalizeContextMode(cfg.Subagent.DefaultContextMode)
	cfg.Subagent.DefaultRunMode = NormalizeRunMode(cfg.Subagent.DefaultRunMode)
	cfg.UI.Theme = theme.NormalizeID(string(cfg.UI.Theme))
	cfg.UI.ContextMeterLocation = NormalizeMeterLocation(cfg.UI.ContextMeterLocation)
	if cfg.UI.ContextLimitTokens <= 0 {
		cfg.UI.ContextLimitTokens = DefaultContextLimitTokens
	}
	return cfg
}

func NormalizeContextMode(mode ContextMode) ContextMode {
	switch ContextMode(strings.ToLower(strings.TrimSpace(string(mode)))) {
	case ContextModeFork:
		return ContextModeFork
	default:
		return ContextModeEmpty
	}
}

func NormalizeRunMode(mode RunMode) RunMode {
	switch RunMode(strings.ToLower(strings.TrimSpace(string(mode)))) {
	case RunModeSync:
		return RunModeSync
	case RunModeBackground:
		return RunModeBackground
	default:
		return RunModeBackground
	}
}

func NormalizeMeterLocation(_ MeterLocation) MeterLocation { return MeterLocationInputAbove }

func (c *Controller) CurrentSettings() Config {
	if c == nil {
		return DefaultConfig()
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.cfg
}

func (c *Controller) SaveSettings(cfg Config) error {
	if c == nil {
		return fmt.Errorf("settings controller is nil")
	}
	cfg = Normalize(cfg)
	if err := Save(c.path, cfg); err != nil {
		return err
	}
	c.mu.Lock()
	c.cfg = cfg
	c.mu.Unlock()
	return nil
}
