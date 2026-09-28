package goal

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"paw/internal/message"
	"paw/internal/runtime/loop"
	"paw/internal/todo"
	"strings"
	"time"
)

type DecisionAction string

const (
	ActionContinue DecisionAction = "continue"
	ActionCompact  DecisionAction = "compact"
	ActionComplete DecisionAction = "complete"
	ActionPause    DecisionAction = "pause"
	ActionBlocked  DecisionAction = "blocked"
	ActionFailed   DecisionAction = "failed"
)

type Decision struct {
	Action      DecisionAction
	Reason      string
	PauseReason PauseReason
	NextPrompt  message.Message
	NoProgress  int
}

type Observation struct {
	GoalID                  GoalID
	Assistant               message.Message
	Todo                    todo.Snapshot
	HasTodo                 bool
	GateDecision            loop.CompletionDecision
	ContextNeedsMaintenance bool
	ContinuationUsed        int
	NoProgressCount         int
	TurnsUsed               int
	ToolCallsUsed           int
	MaxTurns                int
	MaxToolCalls            int
	AcceptancePassed        bool
	HasAcceptanceCriteria   bool
	Verification            []VerificationSpec
	Evidence                []Evidence
	ToolError               error
	Metric                  *MetricSpec
	Measurement             *MetricResult
	MeasurementValid        bool
	ProgressToken           string
	HadToolActivity         bool
}

type EvaluatorConfig struct {
	Policy Policy
	Now    func() time.Time
}

type Evaluator struct {
	policy          Policy
	now             func() time.Time
	lastFingerprint string
	repeated        int
}

func NewEvaluator(policy Policy) *Evaluator {
	return NewEvaluatorWithConfig(EvaluatorConfig{Policy: policy})
}

func NewEvaluatorWithConfig(config EvaluatorConfig) *Evaluator {
	policy := config.Policy.Normalize()
	nowFn := config.Now
	if nowFn == nil {
		nowFn = time.Now
	}
	return &Evaluator{policy: policy, now: nowFn}
}

func (e *Evaluator) Evaluate(o Observation) Decision {
	if e == nil {
		return Decision{Action: ActionFailed, Reason: "evaluator is nil"}
	}
	if o.ToolError != nil {
		return Decision{Action: ActionPause, Reason: o.ToolError.Error(), PauseReason: ClassifyError(o.ToolError)}
	}
	if o.Metric != nil && o.Measurement != nil && o.MeasurementValid && o.Metric.Meets(o.Measurement.Value) {
		return Decision{Action: ActionComplete, Reason: fmt.Sprintf("verified metric %s reached %g %s (target %s %g)", o.Metric.Name, o.Measurement.Value, o.Metric.Unit, o.Metric.Comparison, o.Metric.Target)}
	}
	if e.policy.DeadlineExceeded(e.now()) {
		return Decision{Action: ActionPause, Reason: "goal deadline exceeded", PauseReason: PauseBudgetExhausted}
	}
	if e.policy.Budget.MaxTurns > 0 && o.TurnsUsed >= e.policy.Budget.MaxTurns {
		return Decision{Action: ActionPause, Reason: "goal turn budget exhausted", PauseReason: PauseBudgetExhausted}
	}
	if e.policy.Budget.MaxToolCalls > 0 && o.ToolCallsUsed >= e.policy.Budget.MaxToolCalls {
		return Decision{Action: ActionPause, Reason: "goal tool-call budget exhausted", PauseReason: PauseBudgetExhausted}
	}
	if o.GateDecision.Action == loop.CompletionBlocked {
		return Decision{Action: ActionBlocked, Reason: o.GateDecision.Reason, PauseReason: PauseBlocked}
	}
	if o.GateDecision.Action == loop.CompletionPause {
		return Decision{Action: ActionPause, Reason: o.GateDecision.Reason, PauseReason: PauseNoProgress}
	}
	if o.GateDecision.Action == loop.CompletionFailed {
		return Decision{Action: ActionFailed, Reason: o.GateDecision.Reason, PauseReason: PauseBlocked}
	}
	if o.ContextNeedsMaintenance || o.GateDecision.Action == loop.CompletionCompact {
		return Decision{Action: ActionCompact, Reason: "context maintenance is required"}
	}
	if o.Metric == nil && !o.HadToolActivity {
		return Decision{Action: ActionPause, Reason: "measurable acceptance is not defined; clarify the user's metric, target and measurement protocol", PauseReason: PauseUserInputRequired}
	}
	fingerprint := fingerprint(o)
	if fingerprint == e.lastFingerprint {
		e.repeated++
	} else {
		e.lastFingerprint, e.repeated = fingerprint, 0
	}
	if e.repeated >= e.policy.Budget.MaxNoProgress {
		return Decision{Action: ActionPause, Reason: fmt.Sprintf("no new experiment or measurement for %d turns", e.repeated), PauseReason: PauseNoProgress, NoProgress: e.repeated}
	}
	if o.ContinuationUsed >= e.policy.Budget.MaxContinuations {
		return Decision{Action: ActionPause, Reason: "goal continuation budget exhausted", PauseReason: PauseBudgetExhausted}
	}
	return Decision{Action: ActionContinue, Reason: "quantitative acceptance is not yet verified; continue investigation, experiments and measurement", NextPrompt: continuationPrompt(o), NoProgress: e.repeated}
}

func pendingTodos(s todo.Snapshot) int {
	n := 0
	for _, item := range s.Items {
		if item.Status == todo.StatusPending || item.Status == todo.StatusInProgress {
			n++
		}
	}
	return n
}
func fingerprint(o Observation) string {
	var b strings.Builder
	b.WriteString(string(o.GoalID))
	b.WriteString(o.ProgressToken)
	if o.Measurement != nil {
		b.WriteString(o.Measurement.ToolCallID)
		b.WriteString(o.Measurement.Digest)
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}
func continuationPrompt(o Observation) message.Message {
	var b strings.Builder
	b.WriteString("量化目标尚未达标，请自主推进：调查 → 提出方法 → 实验 → 测量 → 根据结果改进。失败或低分实验是证据，请尝试不同方法。TODO 全部完成不代表目标完成。\n")
	if o.Metric == nil {
		b.WriteString("先明确用户要求的指标、阈值和单位；缺少用户意图时提问，不得猜测或降低目标。可以先调查并构造评测方法，然后调用 goal_set_metric 冻结准确的测量命令和 JSON 数值路径。\n")
	} else {
		fmt.Fprintf(&b, "冻结指标：%s %s %g %s；测量命令：%s；cwd：%q；JSON Pointer：%s。\n", o.Metric.Name, o.Metric.Comparison, o.Metric.Target, o.Metric.Unit, o.Metric.Measurement.Command, o.Metric.Measurement.CWD, o.Metric.Measurement.JSONPointer)
		if o.Measurement != nil {
			fmt.Fprintf(&b, "最近记录：%g %s（工具调用 %s）；继续改进并重新测量。\n", o.Measurement.Value, o.Metric.Unit, o.Measurement.ToolCallID)
		}
		b.WriteString("通过普通 Bash 工具执行冻结的测量命令，再在下一次工具批次调用 goal_record_metric 引用该 tool_call_id。不得直接自报数值或 passed，不得修改冻结指标。\n")
	}
	b.WriteString("\n当前未完成事项：\n")
	b.WriteString("当前可执行事项：\n")
	var waiting strings.Builder
	for _, item := range o.Todo.Items {
		if item.Status == todo.StatusPending || item.Status == todo.StatusInProgress {
			if blockers := o.Todo.UnresolvedBlockers(item.ID); len(blockers) > 0 {
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
	b.WriteString("\n请先检查当前状态，直接执行下一项最有价值的工作；完成后执行验证。")
	return message.Message{Role: message.RoleUser, Content: b.String(), Synthetic: message.SyntheticAutoContinue}
}
func now() time.Time { return time.Now() }

func ClassifyError(err error) PauseReason {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return PauseUserInputRequired
	}
	text := strings.ToLower(err.Error())
	if strings.Contains(text, "permission") || strings.Contains(text, "approval") {
		return PausePermissionRequired
	}
	if strings.Contains(text, "dangerous") || strings.Contains(text, "unsafe") {
		return PauseDangerousCommand
	}
	if strings.Contains(text, "blocked") {
		return PauseBlocked
	}
	return PauseBlocked
}
