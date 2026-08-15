package loop

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"paw/internal/message"
	"paw/internal/todo"
	"strings"
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
}

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
		return decision
	}
	pending := observation.HasTodo && pendingTodoCount(observation.Todo) > 0
	if !pending {
		decision.Action = CompletionComplete
		decision.Reason = "no unfinished todo items"
		return decision
	}
	if observation.NoProgressCount >= config.MaxNoProgress {
		decision.Action = CompletionPause
		decision.Reason = fmt.Sprintf("no verifiable progress for %d turns", observation.NoProgressCount)
		return decision
	}
	if observation.ContinuationUsed >= limit {
		decision.Action = CompletionPause
		decision.Reason = fmt.Sprintf("automatic continuation budget exhausted (%d/%d)", observation.ContinuationUsed, limit)
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

func progressFingerprint(snapshot todo.Snapshot, assistant message.Message, hadToolCalls bool) string {
	var b strings.Builder
	for _, item := range snapshot.Items {
		fmt.Fprintf(&b, "%s\x00%s\x00%s\x00", item.ID, item.Status, item.Content)
	}
	b.WriteString(strings.TrimSpace(assistant.Content))
	fmt.Fprintf(&b, "\x00%t", hadToolCalls)
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
		fmt.Fprintf(&b, "%s\x00%s\x00%s\x00", item.ID, item.Status, item.Content)
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}

func buildContinuationPrompt(decision CompletionDecision, snapshot todo.Snapshot, noProgress int) string {
	var b strings.Builder
	b.WriteString("任务尚未完成，请继续执行，不要只做总结。\n\n当前未完成事项：\n")
	for _, item := range snapshot.Items {
		if item.Status == todo.StatusPending || item.Status == todo.StatusInProgress {
			fmt.Fprintf(&b, "- [%s] %s\n", item.Status, item.Content)
		}
	}
	fmt.Fprintf(&b, "\n续行原因：%s\n", decision.Reason)
	if noProgress > 0 {
		fmt.Fprintf(&b, "已连续无进展：%d\n", noProgress)
	}
	if decision.StaleTodoReminder {
		fmt.Fprintf(&b, "提醒：todo 快照已连续 %d 轮未更新。如任务状态有变化（含已完成项），请立即调用 update_todo 标记，不要攒到最后一次性更新。\n", decision.StaleTodoTurns)
	}
	b.WriteString("\n请先检查当前状态，直接执行下一项最有价值的工作；必要时更新 todo，修改代码后执行相关验证。只有确认全部目标完成后才输出最终总结。")
	return b.String()
}

func (runner *Runner) SetAutoContinueConfig(config AutoContinueConfig) {
	if runner == nil {
		return
	}
	runner.mu.Lock()
	runner.autoContinueConfig = config
	runner.mu.Unlock()
}

func (runner *Runner) SetTodoBroker(broker *todo.Broker) {
	if runner == nil {
		return
	}
	runner.mu.Lock()
	runner.todoBroker = broker
	runner.mu.Unlock()
}

func (runner *Runner) autoContinueState() (AutoContinueConfig, *todo.Broker) {
	runner.mu.RLock()
	defer runner.mu.RUnlock()
	return runner.autoContinueConfig, runner.todoBroker
}

func (runner *Runner) evaluateCompletion(assistant message.Message, hadToolCalls bool, used, noProgress int) (CompletionDecision, todo.Snapshot, bool, int) {
	config, broker := runner.autoContinueState()
	if broker == nil {
		return CompletionGate{Config: config}.Evaluate(CompletionObservation{Assistant: assistant, TurnHadToolCalls: hadToolCalls, ContinuationUsed: used, NoProgressCount: noProgress}), todo.Snapshot{}, false, noProgress
	}
	snapshot, ok := broker.Latest()
	if !ok {
		return CompletionGate{Config: config}.Evaluate(CompletionObservation{Assistant: assistant, TurnHadToolCalls: hadToolCalls, ContinuationUsed: used, NoProgressCount: noProgress}), todo.Snapshot{}, false, noProgress
	}
	hash := progressFingerprint(snapshot, assistant, hadToolCalls)
	todoHash := todoFingerprint(snapshot)
	runner.mu.Lock()
	previousHash := runner.lastProgressHash
	runner.lastProgressHash = hash
	if runner.lastTodoHash != "" && todoHash == runner.lastTodoHash {
		runner.staleTodoTurns++
	} else {
		runner.staleTodoTurns = 0
	}
	runner.lastTodoHash = todoHash
	staleTodoTurns := runner.staleTodoTurns
	runner.mu.Unlock()
	if used > 0 && hash == previousHash {
		noProgress++
	} else {
		noProgress = 0
	}
	decision := CompletionGate{Config: config}.Evaluate(CompletionObservation{Assistant: assistant, Todo: snapshot, HasTodo: true, TurnHadToolCalls: hadToolCalls, ContinuationUsed: used, NoProgressCount: noProgress, ProgressHash: hash, StaleTodoTurns: staleTodoTurns})
	return decision, snapshot, true, noProgress
}

func (runner *Runner) notifyAutoContinue(decision CompletionDecision) {
	runner.notifySystem("auto-continue", fmt.Sprintf("%s (%d/%d)", decision.Reason, decision.BudgetUsed, decision.BudgetLimit))
}
