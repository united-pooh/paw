package plan

import (
	"context"
	"paw/internal/runtime/loop"
)

// A plan turn always returns control to the user. Readiness is recorded by
// plan_finalize; assistant text and editable document metadata cannot approve it.
type evaluator struct {
	runtime *Runtime
	id      PlanID
	err     error
}

func (e *evaluator) EvaluateCompletion(_ loop.TurnExecution, _, _ int) loop.CompletionEvaluation {
	e.runtime.mu.Lock()
	defer e.runtime.mu.Unlock()
	session := e.runtime.memSessions[e.id]
	decision := loop.CompletionDecision{Action: loop.CompletionPause}
	if session.Status == SessionReady {
		decision.Action = loop.CompletionComplete
	} else if session.Status != SessionPaused && !session.Status.Terminal() {
		session.Status = SessionAwaitingInput
		session.LastDecision = "awaiting user input"
		if err := e.runtime.persistSessionLocked(context.Background(), session); err != nil {
			e.err = err
		}
	}
	decision.Reason = session.LastDecision
	return loop.CompletionEvaluation{Decision: decision, HasSignal: true}
}
