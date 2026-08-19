package loop

import (
	"sync"

	"paw/internal/settings"
)

type contextMaintenanceConfig struct {
	softCompactRatio    float64
	toolResultSnipRatio float64
	compactRatio        float64
	compactForceRatio   float64
	compactTargetRatio  float64
	tailTokens          int
	minToolResultBytes  int
	keepErrors          bool
	keepUserMarked      bool
	archiveEnabled      bool
}

func contextMaintenanceConfigFromSettings(in settings.ContextMaintenanceConfig) (contextMaintenanceConfig, error) {
	cfg := settings.DefaultConfig()
	cfg.ContextMaintenance = in
	if err := settings.Validate(cfg); err != nil {
		return contextMaintenanceConfig{}, err
	}
	return contextMaintenanceConfig{
		softCompactRatio:    in.SoftCompactRatio,
		toolResultSnipRatio: in.ToolResultSnipRatio,
		compactRatio:        in.CompactRatio,
		compactForceRatio:   in.CompactForceRatio,
		compactTargetRatio:  in.CompactTargetRatio,
		tailTokens:          in.TailTokens,
		minToolResultBytes:  in.MinToolResultBytes,
		keepErrors:          in.KeepErrors,
		keepUserMarked:      in.KeepUserMarked,
		archiveEnabled:      in.ArchiveEnabled,
	}, nil
}

// SetContextMaintenanceConfig atomically replaces the runtime maintenance
// policy and its archive destination. It does not rewrite existing history.
func (runner *Engine) SetContextMaintenanceConfig(in settings.ContextMaintenanceConfig) error {
	cfg, err := contextMaintenanceConfigFromSettings(in)
	if err != nil {
		return err
	}
	archive, err := newCompactionArchive(runner.workRoot, runner.sessionID, cfg.archiveEnabled)
	if err != nil {
		return err
	}
	runner.compact.configure(cfg, archive)
	return nil
}

// compactionState 收敛上下文压缩域状态：维护配置、归档器、窗口限值、
// 压力档位标志与工具提示压缩开关。P2 从 Engine 字段提取，自带锁。
type compactionState struct {
	mu                  sync.RWMutex
	maintenance         contextMaintenanceConfig
	archive             *compactionArchive
	limitTokens         int
	softCompactNoticed  bool
	consecutiveCompacts int
	compactStuck        bool
	compactToolPrompt   bool
}

func newCompactionState(maintenance contextMaintenanceConfig, archive *compactionArchive, limitTokens int) compactionState {
	return compactionState{maintenance: maintenance, archive: archive, limitTokens: limitTokens}
}

func (c *compactionState) configure(maintenance contextMaintenanceConfig, archive *compactionArchive) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.maintenance = maintenance
	c.archive = archive
}

func (c *compactionState) currentMaintenance() contextMaintenanceConfig {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.maintenance
}

func (c *compactionState) currentArchive() *compactionArchive {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.archive
}

func (c *compactionState) setLimitTokens(limit int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.limitTokens = maxInt(0, limit)
}

func (c *compactionState) limit() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.limitTokens
}

func (c *compactionState) setToolPrompt(enabled bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.compactToolPrompt = enabled
}

func (c *compactionState) toolPrompt() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.compactToolPrompt
}

// markSoftPressure 首次进入软压力档时返回 true（用于一次性提醒）。
func (c *compactionState) markSoftPressure() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.softCompactNoticed {
		return false
	}
	c.softCompactNoticed = true
	return true
}

// summaryAllowed 连续两次折叠后暂停自动摘要（卡死检测）。
func (c *compactionState) summaryAllowed() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return !c.compactStuck
}

// recordAutomatic 记录一次自动压缩结果；belowThreshold 复位卡死状态，
// performed 连续达到 2 次置位卡死。返回是否“新置位卡死”。
func (c *compactionState) recordAutomatic(performed, belowThreshold bool) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if belowThreshold {
		c.softCompactNoticed = false
		c.consecutiveCompacts = 0
		c.compactStuck = false
		return false
	}
	if !performed {
		return false
	}
	wasStuck := c.compactStuck
	c.consecutiveCompacts++
	if c.consecutiveCompacts >= 2 {
		c.compactStuck = true
	}
	return !wasStuck && c.compactStuck
}
