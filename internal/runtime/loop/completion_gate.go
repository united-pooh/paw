package loop

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"paw/internal/message"
	"paw/internal/todo"
	"sort"
	"strings"
	"sync"
)

// CompletionAction is the action the task-level completion gate recommends
// after a model turn has finished.
type CompletionAction string

const (
	CompletionComplete CompletionAction = "complete"
	CompletionContinue CompletionAction = "continue"
	CompletionCompact  CompletionAction = "compact"
	CompletionPause    CompletionAction = "pause"
	CompletionBlocked  CompletionAction = "blocked"
	CompletionFailed   CompletionAction = "failed"
)

type CompletionDecision struct {
	Action            CompletionAction
	Reason            string
	BudgetUsed        int
	BudgetLimit       int
	NoProgress        int
	StaleTodoTurns    int
	StaleTodoReminder bool
	// PauseKind 仅在 Action 为 pause/compact 时设置，用于面向用户的
	// 通知文案分类，避免解析 Reason 机器串。
	PauseKind string
}

// 暂停原因分类（面向用户通知用）。
const (
	PauseNoProgress         = "no-progress"
	PauseBudgetExhausted    = "budget-exhausted"
	PauseContextMaintenance = "context-maintenance"
	PauseExternalBlocker    = "external-blocker"
)

type CompletionObservation struct {
	Assistant               message.Message
	Todo                    todo.Snapshot
	HasTodo                 bool
	TurnHadToolCalls        bool
	ContextNeedsMaintenance bool
	ContinuationUsed        int
	NoProgressCount         int
	ProgressHash            string
	StaleTodoTurns          int
}

type AutoContinueConfig struct {
	Enabled       bool
	BaseBudget    int
	AbsoluteMax   int
	MaxNoProgress int
	// StaleTodoThreshold 是 todo 快照连续未变化多少轮后在续行提示中追加
	// "立即更新 todo" 提醒；<=0 禁用该提醒。
	StaleTodoThreshold int
}

func DefaultAutoContinueConfig() AutoContinueConfig {
	return AutoContinueConfig{Enabled: true, BaseBudget: 2, AbsoluteMax: 12, MaxNoProgress: 2, StaleTodoThreshold: 3}
}

type CompletionGate struct {
	Config AutoContinueConfig
}

func (g CompletionGate) Evaluate(observation CompletionObservation) CompletionDecision {
	config := g.Config
	if config.BaseBudget < 0 {
		config.BaseBudget = 0
	}
	if config.AbsoluteMax <= 0 {
		config.AbsoluteMax = 12
	}
	if config.MaxNoProgress <= 0 {
		config.MaxNoProgress = 2
	}
	limit := config.BaseBudget
	if observation.HasTodo {
		limit += pendingTodoCount(observation.Todo)
	}
	if limit > config.AbsoluteMax {
		limit = config.AbsoluteMax
	}
	decision := CompletionDecision{BudgetUsed: observation.ContinuationUsed, BudgetLimit: limit, NoProgress: observation.NoProgressCount}
	if !config.Enabled {
		decision.Action = CompletionComplete
		decision.Reason = "automatic continuation disabled"
		return decision
	}
	if observation.ContextNeedsMaintenance {
		decision.Action = CompletionCompact
		decision.Reason = "context maintenance is required"
		decision.PauseKind = PauseContextMaintenance
		return decision
	}
	pending := observation.HasTodo && pendingTodoCount(observation.Todo) > 0
	if !pending {
		decision.Action = CompletionComplete
		decision.Reason = "no unfinished todo items"
		return decision
	}
	ready := false
	for _, item := range observation.Todo.Items {
		if item.Status != todo.StatusCompleted && item.BlockedReason == "" && len(observation.Todo.UnresolvedBlockers(item.ID)) == 0 {
			ready = true
			break
		}
	}
	if !ready {
		decision.Action = CompletionBlocked
		decision.Reason = "all unfinished todo items are blocked"
		decision.PauseKind = PauseExternalBlocker
		return decision
	}
	if observation.NoProgressCount >= config.MaxNoProgress {
		decision.Action = CompletionPause
		decision.Reason = fmt.Sprintf("no verifiable progress for %d turns", observation.NoProgressCount)
		decision.PauseKind = PauseNoProgress
		return decision
	}
	if observation.ContinuationUsed >= limit {
		decision.Action = CompletionPause
		decision.Reason = fmt.Sprintf("automatic continuation budget exhausted (%d/%d)", observation.ContinuationUsed, limit)
		decision.PauseKind = PauseBudgetExhausted
		return decision
	}
	decision.Action = CompletionContinue
	decision.Reason = fmt.Sprintf("%d unfinished todo item(s) remain", pendingTodoCount(observation.Todo))
	decision.StaleTodoTurns = observation.StaleTodoTurns
	if config.StaleTodoThreshold > 0 && observation.StaleTodoTurns >= config.StaleTodoThreshold {
		decision.StaleTodoReminder = true
	}
	return decision
}

func pendingTodoCount(snapshot todo.Snapshot) int {
	count := 0
	for _, item := range snapshot.Items {
		if item.Status == todo.StatusPending || item.Status == todo.StatusInProgress {
			count++
		}
	}
	return count
}

// Narrative changes alone are not verifiable progress toward unfinished todos.
func progressFingerprint(snapshot todo.Snapshot) string {
	var b strings.Builder
	items := append([]todo.Item(nil), snapshot.Items...)
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	for _, item := range items {
		blockers := append([]string(nil), item.BlockedBy...)
		sort.Strings(blockers)
		fmt.Fprintf(&b, "%s\x00%s\x00%q\x00%t\x00", item.ID, item.Status, blockers, item.BlockedReason != "")
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}

// todoFingerprint 只反映 todo 快照本身（explanation + 各项状态/内容），
// 用于检测快照连续未更新（stale）的轮数。
func todoFingerprint(snapshot todo.Snapshot) string {
	var b strings.Builder
	b.WriteString(snapshot.Explanation)
	b.WriteByte('\x00')
	for _, item := range snapshot.Items {
		blockers := append([]string(nil), item.BlockedBy...)
		sort.Strings(blockers)
		fmt.Fprintf(&b, "%s\x00%s\x00%s\x00%q\x00%s\x00", item.ID, item.Status, item.Content, blockers, item.BlockedReason)
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}

func buildContinuationPrompt(decision CompletionDecision, snapshot todo.Snapshot, noProgress int) string {
	var b strings.Builder
	b.WriteString("任务尚未完成，请继续执行，不要只做总结。\n")
	b.WriteString("这是同一个任务的续行；沿用已有计划、调查证据和已读取的技能，不要重新输出调查提纲或重复已完成的步骤。\n\n当前未完成事项：\n")
	b.WriteString("当前可执行事项：\n")
	var waiting strings.Builder
	for _, item := range snapshot.Items {
		if item.Status == todo.StatusPending || item.Status == todo.StatusInProgress {
			if item.BlockedReason != "" {
				fmt.Fprintf(&waiting, "- [blocked] #%s %s（外部阻塞：%s）\n", item.ID, item.Content, item.BlockedReason)
			} else if blockers := snapshot.UnresolvedBlockers(item.ID); len(blockers) > 0 {
				fmt.Fprintf(&waiting, "- [blocked] #%s %s（等待：#%s）\n", item.ID, item.Content, strings.Join(blockers, ", #"))
			} else {
				fmt.Fprintf(&b, "- [%s] #%s %s\n", item.Status, item.ID, item.Content)
			}
		}
	}
	if waiting.Len() > 0 {
		b.WriteString("\n等待前置事项：\n")
		b.WriteString(waiting.String())
		b.WriteString("先执行可执行的前置事项；等待项保持 pending，前置全部完成后继续原事项，不要跳过或提前标记完成。\n")
	}
	fmt.Fprintf(&b, "\n续行原因：%s\n", decision.Reason)
	if noProgress > 0 {
		fmt.Fprintf(&b, "已连续无进展：%d\n", noProgress)
	}
	if decision.StaleTodoReminder {
		fmt.Fprintf(&b, "提醒：todo 快照已连续 %d 轮未更新。如任务状态有变化（含已完成项），请立即调用 update_todo 标记，不要攒到最后一次性更新。\n", decision.StaleTodoTurns)
	}
	b.WriteString("\n请先检查当前状态，然后直接执行下一项最有价值的工作；必要时更新 todo。")
	return b.String()
}

func (runner *Engine) SetAutoContinueConfig(config AutoContinueConfig) {
	if runner == nil {
		return
	}
	runner.gate.setConfig(config)
}

func (runner *Engine) SetTodoBroker(broker *todo.Broker) {
	if runner == nil {
		return
	}
	runner.gate.setBroker(broker)
}

func (runner *Engine) autoContinueState() (AutoContinueConfig, *todo.Broker) {
	return runner.gate.state()
}

// progressGate 收敛完成门状态：自动续跑配置、todo broker、进展指纹与
// 陈旧 todo 计数。P2 从 Engine 字段提取，自带锁。
type progressGate struct {
	mu               sync.Mutex
	autoContinue     AutoContinueConfig
	broker           *todo.Broker
	lastProgressHash string
	lastTodoHash     string
	staleTodoTurns   int
	toolEvidence     map[[sha256.Size]byte]struct{}
}

func (g *progressGate) setConfig(config AutoContinueConfig) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.autoContinue = config
}

func (g *progressGate) setBroker(broker *todo.Broker) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.broker = broker
}

func (g *progressGate) state() (AutoContinueConfig, *todo.Broker) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.autoContinue, g.broker
}

// resetProgress 在新外层 turn 开始时清零进展指纹（runTask 路径）。
func (g *progressGate) resetProgress() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.lastProgressHash = ""
	g.lastTodoHash = ""
	g.staleTodoTurns = 0
	g.toolEvidence = nil
}

func (g *progressGate) recordToolEvidence(history []message.Message) bool {
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].Role == message.RoleUser && len(toolResultsFromMessage(history[i])) == 0 {
			history = history[i+1:]
			break
		}
	}
	calls := make(map[string]message.ToolCall)
	var evidence [][sha256.Size]byte
	for _, msg := range history {
		for _, call := range toolCallsFromMessage(msg) {
			calls[call.ID] = call
		}
		for _, result := range toolResultsFromMessage(msg) {
			call, ok := calls[result.ToolUseID]
			if !ok || result.IsError {
				continue
			}
			switch call.Name {
			case "update_todo", "update_memory", "update_ariadne":
				continue
			}
			input := call.Input
			var value any
			if json.Unmarshal(input, &value) == nil {
				input, _ = json.Marshal(value)
			}
			evidence = append(evidence, sha256.Sum256([]byte(call.Name+"\x00"+string(input)+"\x00"+result.Content)))
		}
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.toolEvidence == nil {
		g.toolEvidence = make(map[[sha256.Size]byte]struct{})
	}
	changed := false
	for _, key := range evidence {
		if _, seen := g.toolEvidence[key]; !seen {
			g.toolEvidence[key] = struct{}{}
			changed = true
		}
	}
	return changed
}

// recordProgress 记录本轮进展/todo 指纹，返回上一轮进展指纹与陈旧 todo
// 轮数，供完成门评估无进展与提醒。
func (g *progressGate) recordProgress(progressHash, todoHash string) (previousHash string, staleTodoTurns int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	previousHash = g.lastProgressHash
	g.lastProgressHash = progressHash
	if g.lastTodoHash != "" && todoHash == g.lastTodoHash {
		g.staleTodoTurns++
	} else {
		g.staleTodoTurns = 0
	}
	g.lastTodoHash = todoHash
	return previousHash, g.staleTodoTurns
}

func (runner *Engine) evaluateCompletion(assistant message.Message, hadToolCalls bool, used, noProgress int) (CompletionDecision, todo.Snapshot, bool, int) {
	config, broker := runner.autoContinueState()
	if broker == nil {
		return CompletionGate{Config: config}.Evaluate(CompletionObservation{Assistant: assistant, TurnHadToolCalls: hadToolCalls, ContinuationUsed: used, NoProgressCount: noProgress}), todo.Snapshot{}, false, noProgress
	}
	snapshot, ok := broker.Latest()
	if !ok {
		return CompletionGate{Config: config}.Evaluate(CompletionObservation{Assistant: assistant, TurnHadToolCalls: hadToolCalls, ContinuationUsed: used, NoProgressCount: noProgress}), todo.Snapshot{}, false, noProgress
	}
	hash := progressFingerprint(snapshot)
	todoHash := todoFingerprint(snapshot)
	previousHash, staleTodoTurns := runner.gate.recordProgress(hash, todoHash)
	newEvidence := runner.gate.recordToolEvidence(runner.currentHistory()) && hadToolCalls
	if used > 0 && hash == previousHash && !newEvidence {
		noProgress++
	} else {
		noProgress = 0
	}
	decision := CompletionGate{Config: config}.Evaluate(CompletionObservation{Assistant: assistant, Todo: snapshot, HasTodo: true, TurnHadToolCalls: hadToolCalls, ContinuationUsed: used, NoProgressCount: noProgress, ProgressHash: hash, StaleTodoTurns: staleTodoTurns})
	return decision, snapshot, true, noProgress
}

// notifyAutoContinuePaused 是 auto-continue 唯一对用户可见的通知：续行本身
// 是无感的（模型持续输出即表现为继续工作），只有停下来时才需要告知用户，
// 并给出可操作的下一步。
func (runner *Engine) notifyAutoContinuePaused(decision CompletionDecision) {
	var reason string
	switch decision.PauseKind {
	case PauseNoProgress:
		reason = fmt.Sprintf("连续 %d 轮没有可验证的进展", decision.NoProgress)
	case PauseBudgetExhausted:
		reason = fmt.Sprintf("续行次数已达上限（%d/%d）", decision.BudgetUsed, decision.BudgetLimit)
	case PauseExternalBlocker:
		reason = "剩余事项都在等待外部条件或其前置事项"
	default:
		reason = decision.Reason
	}
	var b strings.Builder
	b.WriteString("自动续行已暂停：")
	b.WriteString(reason)
	if _, broker := runner.autoContinueState(); broker != nil {
		if snapshot, ok := broker.Latest(); ok {
			if pending := pendingTodoCount(snapshot); pending > 0 {
				fmt.Fprintf(&b, "，还有 %d 项 todo 未完成", pending)
			}
		}
	}
	if decision.PauseKind == PauseExternalBlocker {
		b.WriteString("。解除阻塞后回复「继续」即可接着执行。")
	} else {
		b.WriteString("。回复「继续」即可接着执行。")
	}
	runner.notifySystem("auto-continue", b.String())
}
