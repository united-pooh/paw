package loop

import (
	"context"
	"fmt"

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
	if runner.contextModeState() {
		return runner.maintainStateProjection(ctx, history)
	}

	runner.mu.RLock()
	limit := runner.contextLimitTokens
	cfg := runner.contextMaintenance
	runner.mu.RUnlock()
	if limit <= 0 {
		return result, nil
	}
	if cfg.compactRatio == 0 {
		cfg = defaultContextMaintenanceConfig()
	}

	outcome, err := runner.maintainToolResultsByPressure(ctx, history, limit, pressureThreshold(limit, cfg.compactRatio), cfg)
	if err != nil {
		return result, err
	}
	if outcome.rewritten == nil {
		return result, nil
	}
	result.history = outcome.rewritten
	result.archivePaths = append(result.archivePaths, outcome.archivePaths...)
	result.estimatedTokensSaved = outcome.estimatedTokensSaved
	if outcome.mode == maintenanceSnip {
		result.snippedResults = outcome.snippedResults
		return result, nil
	}
	result.prunedResults = outcome.prunedResults
	postTokens := outcome.postTokens
	compact := pressureThreshold(limit, cfg.compactRatio)
	forced := outcome.promptTokens >= pressureThreshold(limit, cfg.compactForceRatio)
	if !allowSummary || (!forced && postTokens < compact) {
		runner.recordAutomaticCompaction(false, postTokens < compact)
		return result, nil
	}
	if !runner.automaticSummaryAllowed() {
		return result, nil
	}
	if !forced && !foldEconomicsForHistory(outcome.rewritten, limit, cfg) {
		return result, nil
	}

	compacted, compaction, err := runner.compactHistory(ctx, outcome.rewritten, "", forced)
	if err != nil {
		return result, err
	}
	if compaction == nil {
		return result, nil
	}
	result.history = compacted
	compaction.SnippedResults += result.snippedResults
	compaction.PrunedResults += result.prunedResults
	compaction.EstimatedTokensSaved += result.estimatedTokensSaved
	compaction.ArchivePaths = appendUniqueStrings(compaction.ArchivePaths, result.archivePaths...)
	result.compaction = compaction
	result.summaryPerformed = true
	result.estimatedTokensSaved += maxInt(0, postTokens-estimateMessageTokens(compacted))
	compaction.EstimatedTokensSaved = result.estimatedTokensSaved
	if runner.recordAutomaticCompaction(true, false) {
		runner.notifySystem("context-compaction", "automatic summary compaction paused after two consecutive folds; tool-result pruning remains active")
	}
	return result, nil
}

// toolMaintenanceOutcome 是两条压缩链路共享的工具结果维护结果。
// rewritten == nil 表示未达 snip 档（压力过低，历史全量保留）。
type toolMaintenanceOutcome struct {
	rewritten            []message.Message
	mode                 maintenanceMode
	snippedResults       int
	prunedResults        int
	estimatedTokensSaved int
	archivePaths         []string
	promptTokens         int
	postTokens           int
}

// maintainToolResultsByPressure 按压力档位维护工具结果（模式 A/B 共用，
// 工具结果处理策略跨链路一致）：
//
//   - < soft：仅复位自动压缩状态，历史不动（缓存稳定）
//   - < snip：标记软压力，历史不动
//   - < compactThreshold：头尾裁剪（snip，保留 head+tail）
//   - ≥ compactThreshold：归档并替换为 elided marker（prune）
//
// compactThreshold 由调用方传入：模式 A 用 cfg.compactRatio，模式 B 用
// 状态压缩阈值（stateCompactionRatio）。soft/snip 档被钳制到不超过
// compactThreshold，保证档位单调。
func (runner *Runner) maintainToolResultsByPressure(ctx context.Context, history []message.Message, limit, compactThreshold int, cfg contextMaintenanceConfig) (toolMaintenanceOutcome, error) {
	var outcome toolMaintenanceOutcome
	if limit <= 0 {
		return outcome, nil
	}
	if cfg.compactRatio == 0 {
		cfg = defaultContextMaintenanceConfig()
	}

	runner.mu.RLock()
	usage := runner.usage
	usageKnown := runner.usageKnown
	archive := runner.compactionArchive
	registry := runner.registry
	runner.mu.RUnlock()

	estimated := estimateMessageTokens(history)
	promptTokens := estimated
	if usageKnown && usage.PromptTokenCount() > promptTokens {
		promptTokens = usage.PromptTokenCount()
	}
	outcome.promptTokens = promptTokens

	soft := pressureThreshold(limit, cfg.softCompactRatio)
	snip := pressureThreshold(limit, cfg.toolResultSnipRatio)
	if soft > compactThreshold {
		soft = compactThreshold
	}
	if snip > compactThreshold {
		snip = compactThreshold
	}

	if promptTokens < soft {
		runner.recordAutomaticCompaction(false, true)
		return outcome, nil
	}
	if promptTokens < snip {
		if runner.markSoftContextPressure() {
			runner.notifySystem("context-compaction", fmt.Sprintf("context pressure reached %.0f%%; preserving the cache-stable prefix until cleanup is needed", cfg.softCompactRatio*100))
		}
		return outcome, nil
	}

	head, tail := planHistoryCompactionWithConfig(history, limit, cfg)
	_ = head
	mode := maintenanceSnip
	if promptTokens >= compactThreshold {
		mode = maintenancePrune
	}
	if archive == nil {
		var err error
		archive, err = newCompactionArchive(runner.workRoot, runner.sessionID, cfg.archiveEnabled)
		if err != nil {
			return outcome, err
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
		return outcome, err
	}
	outcome.rewritten = rewritten
	outcome.mode = mode
	if mode == maintenanceSnip {
		outcome.snippedResults = stats.results
	} else {
		outcome.prunedResults = stats.results
	}
	outcome.estimatedTokensSaved = estimatedTokenSavings(stats.savedChars)
	outcome.archivePaths = stats.archives
	outcome.postTokens = estimateMessageTokens(rewritten)
	return outcome, nil
}

func (runner *Runner) notifyContextMaintenance(result contextMaintenanceResult) {
	if result.snippedResults > 0 {
		runner.notifySystem("context-compaction", fmt.Sprintf("snipped %d stale tool result(s); estimated %d tokens saved", result.snippedResults, result.estimatedTokensSaved))
	}
	if result.prunedResults > 0 {
		runner.notifySystem("context-compaction", fmt.Sprintf("pruned %d stale tool result(s); estimated %d tokens saved", result.prunedResults, result.estimatedTokensSaved))
	}
	if result.compaction != nil {
		runner.notifySystem("context-compaction", fmt.Sprintf("compacted %d messages: %d → %d; recent messages and user constraints were kept verbatim", result.compaction.FoldedMessages, result.compaction.BeforeMessages, result.compaction.AfterMessages))
	}
}

func appendUniqueStrings(base []string, values ...string) []string {
	seen := make(map[string]bool, len(base)+len(values))
	result := make([]string, 0, len(base)+len(values))
	for _, value := range append(append([]string(nil), base...), values...) {
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	return result
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

func (runner *Runner) recordAutomaticCompaction(performed bool, belowThreshold bool) bool {
	if runner == nil {
		return false
	}
	runner.mu.Lock()
	defer runner.mu.Unlock()
	if belowThreshold {
		runner.softCompactNoticed = false
		runner.consecutiveCompacts = 0
		runner.compactStuck = false
		return false
	}
	if !performed {
		return false
	}
	wasStuck := runner.compactStuck
	runner.consecutiveCompacts++
	if runner.consecutiveCompacts >= 2 {
		runner.compactStuck = true
	}
	return !wasStuck && runner.compactStuck
}

func defaultContextMaintenanceConfig() contextMaintenanceConfig {
	cfg, err := contextMaintenanceConfigFromSettings(settings.DefaultContextMaintenanceConfig())
	if err != nil {
		panic(err)
	}
	return cfg
}
