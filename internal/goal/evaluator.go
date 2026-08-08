package goal

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"paw/internal/loop"
	"paw/internal/message"
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
	pending := o.HasTodo && pendingTodos(o.Todo) > 0
	if !pending {
		if o.HasAcceptanceCriteria && !o.AcceptancePassed {
			return Decision{Action: ActionPause, Reason: "acceptance criteria are not satisfied", PauseReason: PauseVerificationFailed}
		}
		if len(o.Verification) > 0 && !RequiredEvidenceComplete(o.Verification, o.Evidence) {
			return Decision{Action: ActionPause, Reason: "required verification evidence is missing or stale", PauseReason: PauseVerificationFailed}
		}
		return Decision{Action: ActionComplete, Reason: "no unfinished todo items"}
	}
	fingerprint := fingerprint(o)
	if fingerprint == e.lastFingerprint {
		e.repeated++
	} else {
		e.lastFingerprint, e.repeated = fingerprint, 0
	}
	if e.repeated >= e.policy.Budget.MaxNoProgress {
		return Decision{Action: ActionPause, Reason: fmt.Sprintf("no verifiable progress for %d turns", e.repeated), PauseReason: PauseNoProgress}
	}
	if o.ContinuationUsed >= e.policy.Budget.MaxContinuations {
		return Decision{Action: ActionPause, Reason: "goal continuation budget exhausted", PauseReason: PauseBudgetExhausted}
	}
	return Decision{Action: ActionContinue, Reason: fmt.Sprintf("%d unfinished todo item(s) remain", pendingTodos(o.Todo)), NextPrompt: continuationPrompt(o)}
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
	for _, item := range o.Todo.Items {
		fmt.Fprintf(&b, "%s\x00%s\x00%s\x00", item.ID, item.Status, item.Content)
	}
	b.WriteString(strings.TrimSpace(o.Assistant.Content))
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}
func continuationPrompt(o Observation) message.Message {
	var b strings.Builder
	b.WriteString("任务尚未完成，请继续执行，不要只做总结。\n\n当前未完成事项：\n")
	for _, item := range o.Todo.Items {
		if item.Status == todo.StatusPending || item.Status == todo.StatusInProgress {
			fmt.Fprintf(&b, "- [%s] %s\n", item.Status, item.Content)
		}
	}
	b.WriteString("\n请先检查当前状态，直接执行下一项最有价值的工作；完成后执行验证。")
	return message.Message{Role: message.RoleUser, Content: b.String()}
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
