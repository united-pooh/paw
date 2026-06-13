package settings

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const (
	DefaultSettingsPath       = ".ccagent/settings.json"
	DefaultContextLimitTokens = 1024 * 1024

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

type Config struct {
	Subagent SubagentConfig `json:"subagent"`
	UI       UIConfig       `json:"ui"`
}

type SubagentConfig struct {
	DefaultContextMode ContextMode `json:"default_context_mode"`
	DefaultRunMode     RunMode     `json:"default_run_mode"`
}

type UIConfig struct {
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
		Subagent: SubagentConfig{
			DefaultContextMode: ContextModeEmpty,
			DefaultRunMode:     RunModeSync,
		},
		UI: UIConfig{
			ContextLimitTokens:   DefaultContextLimitTokens,
			ContextMeterLocation: MeterLocationInputAbove,
		},
	}
}

func NewController(path string) (*Controller, error) {
	if strings.TrimSpace(path) == "" {
		path = DefaultSettingsPath
	}
	cfg, err := Load(path)
	if err != nil {
		return nil, err
	}
	return &Controller{path: path, cfg: cfg}, nil
}

func NewControllerInCwd() (*Controller, error) {
	return NewController(DefaultSettingsPath)
}

func Load(path string) (Config, error) {
	cfg := DefaultConfig()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return Config{}, fmt.Errorf("read settings: %w", err)
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse settings: %w", err)
	}
	return Normalize(cfg), nil
}

func Save(path string, cfg Config) error {
	cfg = Normalize(cfg)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create settings directory: %w", err)
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal settings: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write settings: %w", err)
	}
	return nil
}

func Normalize(cfg Config) Config {
	cfg.Subagent.DefaultContextMode = NormalizeContextMode(cfg.Subagent.DefaultContextMode)
	cfg.Subagent.DefaultRunMode = NormalizeRunMode(cfg.Subagent.DefaultRunMode)
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
	case RunModeBackground:
		return RunModeBackground
	default:
		return RunModeSync
	}
}

func NormalizeMeterLocation(location MeterLocation) MeterLocation {
	switch MeterLocation(strings.ToLower(strings.TrimSpace(string(location)))) {
	case MeterLocationHeader:
		return MeterLocationHeader
	case MeterLocationInputAbove:
		return MeterLocationInputAbove
	case MeterLocationInputTitle:
		return MeterLocationInputTitle
	default:
		return MeterLocationInputAbove
	}
}

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
