package loop

import "paw/internal/settings"

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
func (runner *Runner) SetContextMaintenanceConfig(in settings.ContextMaintenanceConfig) error {
	cfg, err := contextMaintenanceConfigFromSettings(in)
	if err != nil {
		return err
	}
	archive, err := newCompactionArchive(runner.workRoot, runner.sessionID, cfg.archiveEnabled)
	if err != nil {
		return err
	}
	runner.mu.Lock()
	runner.contextMaintenance = cfg
	runner.compactionArchive = archive
	runner.mu.Unlock()
	return nil
}
