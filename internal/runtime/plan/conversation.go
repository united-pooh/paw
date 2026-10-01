package plan

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"

	"paw/internal/runtime/loop"
)

type steerPreparer interface {
	PrepareSteer(string) (loop.SteerAdmission, bool)
}

func (r *Runtime) Submit(ctx context.Context, input string) (Session, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return Session{}, fmt.Errorf("plan input is empty")
	}
	r.mu.Lock()
	if r.closing {
		r.mu.Unlock()
		return Session{}, fmt.Errorf("%w: plan turn is stopping; retry this input after it stops", ErrPlanActive)
	}
	if r.activeID == "" {
		r.mu.Unlock()
		return r.Start(ctx, input)
	}
	id := r.activeID
	session := r.memSessions[id]
	if r.active && (session.Status == SessionPaused || session.Status.Terminal()) {
		r.mu.Unlock()
		return Session{}, fmt.Errorf("%w: plan turn is stopping; retry this input after it stops", ErrPlanActive)
	}
	var admission loop.SteerAdmission
	if r.active {
		if preparer, ok := r.executor.(steerPreparer); ok {
			admission, _ = preparer.PrepareSteer(input)
		}
	}
	if err := r.invalidateReadyLocked(ctx, &session); err != nil {
		if admission != nil {
			admission.Abort()
		}
		r.mu.Unlock()
		return Session{}, err
	}
	session.Status = SessionDrafting
	session.ResumeStatus, session.PauseReason = "", ""
	session.LastDecision = "user updated the plan conversation"
	session.UpdatedAt = r.now()
	if r.active && admission == nil {
		session.PendingInputs = append(session.PendingInputs, input)
	}
	if err := r.persistSessionLocked(ctx, session); err != nil {
		if admission != nil {
			admission.Abort()
		}
		r.mu.Unlock()
		return Session{}, err
	}
	if r.active {
		if admission != nil {
			admission.Commit()
		}
		r.mu.Unlock()
		return session.Snapshot(), nil
	}
	runCtx, cancel := context.WithCancel(context.Background())
	r.active, r.cancel = true, cancel
	r.mu.Unlock()
	r.emit(Event{Type: EventResumed, PlanID: id, Status: session.Status, At: r.now()})
	go r.runAsync(runCtx, id, input)
	return session.Snapshot(), nil
}

func (r *Runtime) invalidateReadyLocked(ctx context.Context, session *Session) error {
	if session.ReadyContentHash == "" && session.Status != SessionReady && session.ResumeStatus != SessionReady {
		return nil
	}
	doc, found, err := r.store.Get(ctx, session.ID)
	if err != nil {
		return err
	}
	if found && doc.Status == PlanReady {
		doc.Status = PlanDraft
		if err := r.store.Update(ctx, doc); err != nil {
			return err
		}
	}
	session.ReadyContentHash = ""
	return nil
}

func (r *Runtime) Execute(ctx context.Context, id PlanID) (PlanDoc, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	session, ok := r.memSessions[id]
	if !ok || r.activeID != id {
		return PlanDoc{}, ErrNoActivePlan
	}
	if r.active || r.closing || session.Status != SessionReady || session.ReadyContentHash == "" || len(session.PendingInputs) > 0 {
		return PlanDoc{}, ErrPlanNotReady
	}
	doc, found, err := r.store.Get(ctx, id)
	if err != nil {
		return PlanDoc{}, err
	}
	if !found || doc.Status != PlanReady || strings.TrimSpace(doc.Content) == "" || readyContentHash(doc) != session.ReadyContentHash {
		return PlanDoc{}, fmt.Errorf("plan changed since it was saved ready; review and save it again")
	}
	doc.Status = PlanApproved
	if err := r.store.Update(ctx, doc); err != nil {
		return PlanDoc{}, err
	}
	if err := session.Transition(SessionApproved, ""); err != nil {
		return PlanDoc{}, err
	}
	session.LastDecision = "user explicitly requested execution"
	if err := r.persistSessionLocked(ctx, session); err != nil {
		return PlanDoc{}, err
	}
	r.activeID = ""
	return doc, nil
}

func (r *Runtime) Pause(ctx context.Context, id PlanID) error {
	r.mu.Lock()
	if r.closing && !r.active {
		r.mu.Unlock()
		return ErrPlanActive
	}
	session, ok := r.memSessions[id]
	if !ok || r.activeID != id || session.Status.Terminal() {
		r.mu.Unlock()
		return ErrNoActivePlan
	}
	if session.Status != SessionPaused {
		session.ResumeStatus = session.Status
		if err := session.Transition(SessionPaused, PauseUserRequested); err != nil {
			r.mu.Unlock()
			return err
		}
	}
	session.LastDecision = "paused by user"
	if err := r.persistSessionLocked(ctx, session); err != nil {
		r.mu.Unlock()
		return err
	}
	active := r.active
	if !active {
		r.closing = true
	}
	if r.cancel != nil {
		r.cancel()
	}
	r.mu.Unlock()
	if !active {
		r.finishRun(id)
	}
	return nil
}

func readyContentHash(doc PlanDoc) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(doc.Title+"\x00"+doc.Content)))
}

func (r *Runtime) finishRun(id PlanID) {
	r.mu.Lock()
	session := r.memSessions[id]
	if r.activeID == id {
		if r.cancel != nil {
			r.cancel()
		}
		r.active, r.closing, r.cancel = false, true, nil
	}
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		if r.activeID == id {
			r.closing = false
			if r.memSessions[id].Status.Terminal() {
				r.activeID = ""
			}
		}
	}()
	event := Event{PlanID: id, Status: session.Status, Decision: session.LastDecision, At: r.now()}
	switch session.Status {
	case SessionReady:
		event.Type = EventFinalized
		r.emit(event)
		if doc, found, err := r.store.Get(context.Background(), id); err == nil && found && r.onFinalized != nil {
			r.onFinalized(doc)
		}
	case SessionFailed:
		event.Type = EventFailed
		r.emit(event)
	case SessionCancelled:
		event.Type = EventCancelled
		r.emit(event)
	default:
		event.Type = EventPaused
		r.emit(event)
	}
}
