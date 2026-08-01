package loop

import (
	"context"

	"paw/internal/message"
	"paw/internal/settings"
)

const minimumEconomicFoldTokens = 400

type contextMaintenanceResult struct {
	history              []message.Message
	compaction           *ContextCompactionResult
	snippedResults       int
	prunedResults        int
	estimatedTokensSaved int
	archivePaths         []string
	summaryPerformed     bool
}

func (runner *Runner) maintainContextProjection(ctx context.Context, history []message.Message, allowSummary bool) (contextMaintenanceResult, error) {
	result := contextMaintenanceResult{history: history}
	if runner == nil || len(history) == 0 {
		return result, nil
	}

	runner.mu.RLock()
	limit := runner.contextLimitTokens
	usage := runner.usage
	usageKnown := runner.usageKnown
	cfg := runner.contextMaintenance
	archive := runner.compactionArchive
	registry := runner.registry
	runner.mu.RUnlock()
	if limit <= 0 {
		return result, nil
	}
	if cfg.compactRatio == 0 {
		cfg = defaultContextMaintenanceConfig()
	}

	estimated := estimateMessageTokens(history)
	promptTokens := estimated
	if usageKnown && usage.PromptTokenCount() > promptTokens {
		promptTokens = usage.PromptTokenCount()
	}
	soft := pressureThreshold(limit, cfg.softCompactRatio)
	snip := pressureThreshold(limit, cfg.toolResultSnipRatio)
	compact := pressureThreshold(limit, cfg.compactRatio)
	force := pressureThreshold(limit, cfg.compactForceRatio)

	if promptTokens < soft {
		runner.recordAutomaticCompaction(false, true)
		return result, nil
	}
	if promptTokens < snip {
		runner.markSoftContextPressure()
		return result, nil
	}

	head, tail := planHistoryCompactionWithConfig(history, limit, cfg)
	_ = head
	mode := maintenanceSnip
	if promptTokens >= compact {
		mode = maintenancePrune
	}
	if archive == nil {
		var err error
		archive, err = newCompactionArchive(runner.workRoot, runner.sessionID, cfg.archiveEnabled)
		if err != nil {
			return result, err
		}
	}
	rewritten, stats, err := maintainToolResults(history, maintenanceRequest{
		mode:      mode,
		tailStart: tail,
		minBytes:  cfg.minToolResultBytes,
		policy: keepPolicy{
			errors:     cfg.keepErrors,
			userMarked: cfg.keepUserMarked,
		},
		archive:  archive,
		registry: registry,
	})
	if err != nil {
		return result, err
	}
	result.history = rewritten
	result.archivePaths = append(result.archivePaths, stats.archives...)
	result.estimatedTokensSaved = estimatedTokenSavings(stats.savedChars)
	if mode == maintenanceSnip {
		result.snippedResults = stats.results
		return result, nil
	}
	result.prunedResults = stats.results

	postTokens := estimateMessageTokens(rewritten)
	forced := promptTokens >= force
	if !allowSummary || (!forced && postTokens < compact) {
		runner.recordAutomaticCompaction(false, postTokens < compact)
		return result, nil
	}
	if !runner.automaticSummaryAllowed() {
		return result, nil
	}
	if !forced && !foldEconomicsForHistory(rewritten, limit, cfg) {
		return result, nil
	}

	compacted, compaction, err := runner.compactHistory(ctx, rewritten, "", forced)
	if err != nil {
		return result, err
	}
	if compaction == nil {
		return result, nil
	}
	result.history = compacted
	result.compaction = compaction
	result.summaryPerformed = true
	result.estimatedTokensSaved += maxInt(0, postTokens-estimateMessageTokens(compacted))
	runner.recordAutomaticCompaction(true, false)
	return result, nil
}

func pressureThreshold(limit int, ratio float64) int {
	return int(float64(limit) * ratio)
}

func estimatedTokenSavings(savedChars int) int {
	if savedChars <= 0 {
		return 0
	}
	return (savedChars + 3) / 4
}

func foldEconomics(messages []message.Message) bool {
	return estimateMessageTokens(messages) >= minimumEconomicFoldTokens
}

func foldEconomicsForHistory(history []message.Message, limit int, cfg contextMaintenanceConfig) bool {
	head, tail := planHistoryCompactionWithConfig(history, limit, cfg)
	if tail <= head {
		return false
	}
	_, fold := partitionCompactionRegionWithPolicy(history[head:tail], limit, keepPolicy{
		errors:     cfg.keepErrors,
		userMarked: cfg.keepUserMarked,
	})
	return foldEconomics(fold)
}

func (runner *Runner) markSoftContextPressure() bool {
	if runner == nil {
		return false
	}
	runner.mu.Lock()
	defer runner.mu.Unlock()
	if runner.softCompactNoticed {
		return false
	}
	runner.softCompactNoticed = true
	return true
}

func (runner *Runner) automaticSummaryAllowed() bool {
	if runner == nil {
		return false
	}
	runner.mu.RLock()
	defer runner.mu.RUnlock()
	return !runner.compactStuck
}

func (runner *Runner) recordAutomaticCompaction(performed bool, belowThreshold bool) {
	if runner == nil {
		return
	}
	runner.mu.Lock()
	defer runner.mu.Unlock()
	if belowThreshold {
		runner.softCompactNoticed = false
		runner.consecutiveCompacts = 0
		runner.compactStuck = false
		return
	}
	if !performed {
		return
	}
	runner.consecutiveCompacts++
	if runner.consecutiveCompacts >= 2 {
		runner.compactStuck = true
	}
}

func defaultContextMaintenanceConfig() contextMaintenanceConfig {
	cfg, err := contextMaintenanceConfigFromSettings(settings.DefaultContextMaintenanceConfig())
	if err != nil {
		panic(err)
	}
	return cfg
}
