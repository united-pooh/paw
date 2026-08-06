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
	Subagent           SubagentConfig           `json:"subagent"`
	UI                 UIConfig                 `json:"ui"`
	ContextMaintenance ContextMaintenanceConfig `json:"context_maintenance"`
}

type ContextMaintenanceConfig struct {
	SoftCompactRatio    float64 `json:"soft_compact_ratio"`
	ToolResultSnipRatio float64 `json:"tool_result_snip_ratio"`
	CompactRatio        float64 `json:"compact_ratio"`
	CompactForceRatio   float64 `json:"compact_force_ratio"`
	CompactTargetRatio  float64 `json:"compact_target_ratio"`
	TailTokens          int     `json:"tail_tokens"`
	MinToolResultBytes  int     `json:"min_tool_result_bytes"`
	KeepErrors          bool    `json:"keep_errors"`
	KeepUserMarked      bool    `json:"keep_user_marked"`
	ArchiveEnabled      bool    `json:"archive_enabled"`
}

type SubagentConfig struct {
	DefaultContextMode ContextMode `json:"default_context_mode"`
	DefaultRunMode     RunMode     `json:"default_run_mode"`
	WaitTimeoutMs      int         `json:"wait_timeout_ms"`
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
		Subagent:           SubagentConfig{DefaultContextMode: ContextModeEmpty, DefaultRunMode: RunModeBackground, WaitTimeoutMs: DefaultSubagentWaitTimeoutMs},
		UI:                 UIConfig{Theme: theme.Default, ContextLimitTokens: DefaultContextLimitTokens, ContextMeterLocation: MeterLocationInputAbove},
		ContextMaintenance: DefaultContextMaintenanceConfig(),
	}
}

// DefaultSubagentWaitTimeoutMs 是 SubagentWait 未显式指定 timeout_ms 时的默认
// 等待上限（10 分钟）。超时返回当前快照 + timed_out 标记，并非错误。
const DefaultSubagentWaitTimeoutMs = 600000

func DefaultContextMaintenanceConfig() ContextMaintenanceConfig {
	return ContextMaintenanceConfig{
		SoftCompactRatio:    0.50,
		ToolResultSnipRatio: 0.60,
		CompactRatio:        0.80,
		CompactForceRatio:   0.90,
		CompactTargetRatio:  0.50,
		TailTokens:          16384,
		MinToolResultBytes:  1024,
		KeepErrors:          true,
		KeepUserMarked:      true,
		ArchiveEnabled:      true,
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
	cfg = Normalize(cfg)
	if err := Validate(cfg); err != nil {
		return Config{}, fmt.Errorf("validate settings %s: %w", path, err)
	}
	return cfg, nil
}

func Save(path string, cfg Config) error {
	cfg = Normalize(cfg)
	if err := Validate(cfg); err != nil {
		return fmt.Errorf("validate settings %s: %w", path, err)
	}
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
	if cfg.Subagent.WaitTimeoutMs <= 0 {
		cfg.Subagent.WaitTimeoutMs = DefaultSubagentWaitTimeoutMs
	}
	cfg.UI.Theme = theme.NormalizeID(string(cfg.UI.Theme))
	cfg.UI.ContextMeterLocation = NormalizeMeterLocation(cfg.UI.ContextMeterLocation)
	if cfg.UI.ContextLimitTokens <= 0 {
		cfg.UI.ContextLimitTokens = DefaultContextLimitTokens
	}
	if cfg.ContextMaintenance == (ContextMaintenanceConfig{}) {
		cfg.ContextMaintenance = DefaultContextMaintenanceConfig()
	}
	return cfg
}

func Validate(cfg Config) error {
	c := cfg.ContextMaintenance
	if !(c.SoftCompactRatio > 0 && c.SoftCompactRatio <= c.ToolResultSnipRatio) {
		return fmt.Errorf("context_maintenance.soft_compact_ratio must be > 0 and <= tool_result_snip_ratio")
	}
	if c.ToolResultSnipRatio > c.CompactRatio {
		return fmt.Errorf("context_maintenance.tool_result_snip_ratio must be <= compact_ratio")
	}
	if c.CompactRatio > c.CompactForceRatio || c.CompactForceRatio >= 1 {
		return fmt.Errorf("context_maintenance ratios must satisfy compact_ratio <= compact_force_ratio < 1")
	}
	if c.CompactTargetRatio <= 0 || c.CompactTargetRatio >= c.CompactRatio {
		return fmt.Errorf("context_maintenance.compact_target_ratio must be > 0 and < compact_ratio")
	}
	if c.TailTokens <= 0 || c.MinToolResultBytes <= 0 {
		return fmt.Errorf("context_maintenance token and byte budgets must be positive")
	}
	return nil
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
