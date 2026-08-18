package loop

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"paw/internal/message"
	"paw/internal/session"
)

// StateBlockProvider 提供模式 B（状态压缩）恢复用的结构化状态块。
// 实现方（cmd/agent）负责读取 plan/todo/memory/ariadne 并组装为
// 字节稳定、带时间戳标注的文本（设计文档 D9/D12）。
// 返回空字符串表示无可用状态（不注入）。
type StateBlockProvider interface {
	BuildStateContext(ctx context.Context) (string, error)
}

// stateBlockHeader 是状态块消息的固定前缀（字节稳定，D9）。
const stateBlockHeader = "[工作状态（State Context）——由系统在恢复时注入，反映 plan/todo/memory/ariadne 的最新状态；对话细节可用 search_transcript 按需取回]\n\n"

// recentTurnsLoader 是 session.JSONLStore 的清洗最近轮次读取能力。
type recentTurnsLoader interface {
	LoadRecentTurns(ctx context.Context, sessionID string, n int) ([]message.Message, error)
}

// stateConfig 收敛模式 B 恢复配置：压缩模式、近邻轮数、压缩比与状态块
// 提供者。P2 从 Runner 字段提取，自带锁。
type stateConfig struct {
	mu            sync.RWMutex
	mode          string
	recentTurns   int
	ratio         float64
	blockProvider StateBlockProvider
}

func (c *stateConfig) setMode(mode string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if mode == "state" || mode == "summary" {
		c.mode = mode
	}
}

func (c *stateConfig) isStateMode() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.mode == "state"
}

func (c *stateConfig) setRecentTurns(n int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.recentTurns = n
}

func (c *stateConfig) recentTurnsValue() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.recentTurns
}

func (c *stateConfig) setRatio(ratio float64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ratio = ratio
}

func (c *stateConfig) ratioValue() float64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.ratio
}

func (c *stateConfig) setBlockProvider(provider StateBlockProvider) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.blockProvider = provider
}

func (c *stateConfig) buildBlock(ctx context.Context) (string, error) {
	c.mu.RLock()
	provider := c.blockProvider
	c.mu.RUnlock()
	if provider == nil {
		return "", nil
	}
	return provider.BuildStateContext(ctx)
}

// loadStateModeHistory 实现模式 B 恢复：状态块 + 最近 N 轮清洗对话
// （验证实验 v2 结论），替代全量历史。返回 (messages, recovery, error)；
// 不支持清洗读取或读取失败时返回错误，由调用方降级全量恢复。
func (runner *Runner) loadStateModeHistory(ctx context.Context) ([]message.Message, *session.RecoveryState, error) {
	journal := runner.turnJournal()
	if journal == nil {
		return nil, nil, nil
	}
	loader, ok := journal.(recentTurnsLoader)
	if !ok {
		return nil, nil, nil
	}

	stateBlock, sbErr := runner.buildStateContext(ctx)
	if sbErr != nil {
		runner.notifySystem("state-context", "state block unavailable: "+sbErr.Error())
	}
	var messages []message.Message
	if strings.TrimSpace(stateBlock) != "" {
		messages = append(messages, message.Message{Role: message.RoleUser, Content: stateBlockHeader + stateBlock})
	}
	recent, rErr := loader.LoadRecentTurns(ctx, runner.sessionID, runner.resumeRecentTurns())
	if rErr != nil {
		return nil, nil, rErr
	}
	messages = append(messages, recent...)

	var recovery *session.RecoveryState
	if snapshot, snapErr := journal.LoadSnapshot(ctx, runner.sessionID); snapErr == nil {
		recovery = snapshot.Recovery
	}
	return messages, recovery, nil
}

// resumeRecentTurns 返回恢复时保留的完整轮数（settings resumeRecentTurns）。
func (runner *Runner) resumeRecentTurns() int {
	if runner == nil {
		return 3
	}
	if turns := runner.stateCfg.recentTurnsValue(); turns > 0 {
		return turns
	}
	return 3
}

// SetStateCompactionRatio 设置模式 B 压缩触发阈值（0~1，默认 0.9）。
func (runner *Runner) SetStateCompactionRatio(ratio float64) {
	if runner == nil || ratio <= 0 || ratio >= 1 {
		return
	}
	runner.stateCfg.setRatio(ratio)
}

// SetResumeRecentTurns 设置恢复时保留的完整轮数（T5 由 settings 接入）。
func (runner *Runner) SetResumeRecentTurns(n int) {
	if runner == nil || n <= 0 {
		return
	}
	runner.stateCfg.setRecentTurns(n)
}

// stateRefreshInstruction 是状态压缩触发时注入的刷新指令（随下一轮请求
// 发送，不单独调用模型）。
const stateRefreshInstruction = "[系统] 上下文接近上限，已执行状态压缩：较早的对话已归档（可用 search_transcript 取回细节）。工作状态块中的 todo 是权威快照：先按其中稳定 ID、状态和内容继续；只有任务确有变化时才调用 update_todo，并提交完整列表，保留未变化条目的 ID、内容和 Explanation，不得因压缩删项或改写状态。必要时再更新 memory/ariadne，然后继续当前工作。"

// maintainStateProjection 实现模式 B 的运行时上下文维护（设计文档 §6）：
// 与模式 A 共享同样的 4 级压力响应（soft → snip → compact → force），
// 工具结果处理策略跨链路一致：
//   - soft 档以下：对话全量保留（缓存稳定）
//   - snip 档：工具结果头尾裁剪（保留 head+tail），历史结构不动
//   - compact 档（stateCompactionRatio，默认 0.9）：工具结果归档
//     （prune 前置保真）后执行状态压缩——状态块 + 最近 N 轮（清洗）+
//     刷新指令替换整个历史，不调用摘要模型
//   - force 档：状态压缩后仍超窗口（极端）降级模式 A 摘要
func (runner *Runner) maintainStateProjection(ctx context.Context, history []message.Message) (contextMaintenanceResult, error) {
	result := contextMaintenanceResult{history: history}
	if runner == nil || len(history) == 0 {
		return result, nil
	}
	limit := runner.compact.limit()
	ratio := runner.stateCfg.ratioValue()
	turns := runner.stateCfg.recentTurnsValue()
	cfg := runner.compact.currentMaintenance()
	if limit <= 0 {
		return result, nil
	}
	if ratio <= 0 {
		ratio = 0.9
	}
	if turns <= 0 {
		turns = 3
	}
	if cfg.compactRatio == 0 {
		cfg = defaultContextMaintenanceConfig()
	}
	compactThreshold := pressureThreshold(limit, ratio)

	outcome, err := runner.maintainToolResultsByPressure(ctx, history, limit, compactThreshold, cfg)
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
	history = outcome.rewritten

	// compact 档：状态压缩（prune 已前置归档原始工具结果）。
	// 状态压缩不调模型（本地组装状态块），prune 后无需像模式 A 一样
	// 做经济性检查——达到阈值即压缩。
	journal := runner.turnJournal()
	loader, _ := journal.(recentTurnsLoader)
	if journal == nil || loader == nil {
		// 无可靠 turn 边界：降级模式 A 摘要。
		return runner.compactStateFallback(ctx, history)
	}
	stateBlock, sbErr := runner.buildStateContext(ctx)
	if sbErr != nil {
		runner.notifySystem("state-context", "state block unavailable: "+sbErr.Error())
	}
	var compacted []message.Message
	if strings.TrimSpace(stateBlock) != "" {
		compacted = append(compacted, message.Message{Role: message.RoleUser, Content: stateBlockHeader + stateBlock})
	}
	recent, rErr := loader.LoadRecentTurns(ctx, runner.sessionID, turns)
	if rErr != nil {
		return runner.compactStateFallback(ctx, history)
	}
	compacted = append(compacted, recent...)
	compacted = append(compacted, message.Message{Role: message.RoleUser, Content: stateRefreshInstruction})

	// force 档兜底：状态块 + N 轮 + 指令仍超阈值（几乎不可能）→ 模式 A 摘要。
	if estimateMessageTokens(compacted) >= compactThreshold {
		return runner.compactStateFallback(ctx, history)
	}

	before := len(history)
	runner.recordStateCompaction(ctx, before, len(compacted), ratio)
	result.history = compacted
	result.estimatedTokensSaved += maxInt(0, estimateMessageTokens(history)-estimateMessageTokens(compacted))
	return result, nil
}

// compactStateFallback 降级模式 A（摘要压缩，现状路径）。
func (runner *Runner) compactStateFallback(ctx context.Context, history []message.Message) (contextMaintenanceResult, error) {
	compacted, compaction, err := runner.compactHistory(ctx, history, "", true)
	if err != nil {
		return contextMaintenanceResult{history: history}, err
	}
	result := contextMaintenanceResult{history: compacted}
	if compaction != nil {
		result.compaction = compaction
		result.estimatedTokensSaved = compaction.EstimatedTokensSaved
	}
	return result, nil
}

// recordStateCompaction 记录状态压缩审计事件（best-effort）。
func (runner *Runner) recordStateCompaction(ctx context.Context, before, after int, ratio float64) {
	journal := runner.turnJournal()
	recorder, ok := journal.(interface {
		AppendStateEvent(ctx context.Context, sessionID string, kind session.StateEventKind, summary string) (int64, error)
	})
	if !ok {
		return
	}
	summary := fmt.Sprintf("kept_turns=%d dropped_messages=%d ratio=%.2f", runner.resumeRecentTurns(), before-after, ratio)
	if _, err := recorder.AppendStateEvent(ctx, runner.sessionID, session.StateEventCompacted, summary); err != nil {
		runner.notifySystem("state-context", "state compaction event failed: "+err.Error())
	}
}
