package plan

import (
	"context"
	"paw/internal/loop"
	"paw/internal/message"
)

// evaluator decides whether a plan session continues, completes, or pauses
// after each turn. Completion is a deterministic signal: the plan document's
// status is approved (only plan_finalize can set it), never inferred from
// model text.
type evaluator struct {
	runtime  *Runtime
	id       PlanID
	session  *Session
	last     string
	repeated int
	err      error
}

func (e *evaluator) EvaluateCompletion(assistant message.Message, used, noProgress int) loop.CompletionEvaluation {
	session, _ := e.runtime.loadSession(e.id)
	e.session = &session

	if doc, ok, err := e.runtime.store.Get(context.Background(), e.id); err == nil && ok {
		if doc.Status == PlanApproved {
			session.Status = SessionApproved
			session.LastDecision = "plan finalized and approved"
			if err := e.runtime.storeSession(context.Background(), session); err != nil {
				e.err = err
				return loop.CompletionEvaluation{Decision: loop.CompletionDecision{Action: loop.CompletionPause, Reason: err.Error()}, HasSignal: true}
			}
			return loop.CompletionEvaluation{
				Decision:  loop.CompletionDecision{Action: loop.CompletionComplete, Reason: "plan finalized and approved"},
				HasSignal: true,
			}
		}
	}
	if session.MaxTurns > 0 && session.TurnsUsed >= session.MaxTurns {
		session.Status = SessionPaused
		session.LastDecision = "plan session turn budget exhausted"
		if err := e.runtime.storeSession(context.Background(), session); err != nil {
			e.err = err
		}
		return loop.CompletionEvaluation{
			Decision:  loop.CompletionDecision{Action: loop.CompletionPause, Reason: session.LastDecision},
			HasSignal: true,
		}
	}
	if session.MaxContinuations > 0 && used >= session.MaxContinuations {
		session.Status = SessionPaused
		session.LastDecision = "plan session continuation budget exhausted"
		if err := e.runtime.storeSession(context.Background(), session); err != nil {
			e.err = err
		}
		return loop.CompletionEvaluation{
			Decision:  loop.CompletionDecision{Action: loop.CompletionPause, Reason: session.LastDecision},
			HasSignal: true,
		}
	}
	fp := assistant.Content
	if fp != "" && fp == e.last {
		e.repeated++
	} else {
		e.last = fp
		e.repeated = 0
	}
	if session.MaxNoProgress > 0 && e.repeated >= session.MaxNoProgress {
		session.Status = SessionPaused
		session.LastDecision = "no verifiable progress in plan session"
		if err := e.runtime.storeSession(context.Background(), session); err != nil {
			e.err = err
		}
		return loop.CompletionEvaluation{
			Decision:  loop.CompletionDecision{Action: loop.CompletionPause, Reason: session.LastDecision},
			HasSignal: true,
		}
	}
	return loop.CompletionEvaluation{
		Decision:  loop.CompletionDecision{Action: loop.CompletionContinue, Reason: "plan session still in progress"},
		HasSignal: true,
		NextInput: message.Message{
			Role: message.RoleUser,
			Content: "继续当前的 PLAN MODE 工作流：澄清未完成则继续提问；文档未写则撰写；" +
				"文档已展示则用 Select 询问【执行/修改】；用户选择执行后调用 plan_finalize。不要开始实现计划。",
		},
	}
}
