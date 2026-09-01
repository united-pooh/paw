package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	"paw/internal/loop"
	selecttool "paw/internal/tool/select"
)

var (
	ErrInteractionMissing = errors.New("interaction_not_found")
	ErrInteractionClosed  = errors.New("interaction_closed")
)

const (
	InteractionKindQuestion   = "question"
	InteractionKindPermission = "permission"
)

type SelectedAnswerOption = selecttool.SelectedOption

type InteractionAnswer struct {
	Cancelled       bool                   `json:"cancelled,omitempty"`
	SelectedOptions []SelectedAnswerOption `json:"selected_options,omitempty"`
}

type InteractionDecision string

const (
	DecisionAllowOnce InteractionDecision = "allow_once"
	DecisionDeny      InteractionDecision = "deny"
)

type InteractionHub struct {
	workspaceID WorkspaceID
	coordinator *WorkspaceCoordinator
	events      *EventHub
	now         func() time.Time

	mu       sync.Mutex
	answers  map[string]chan InteractionAnswer
	decided  map[string]loop.PermissionDecision
	resolved map[string]InteractionAnswer
}

func NewInteractionHub(workspaceID WorkspaceID, coordinator *WorkspaceCoordinator, events *EventHub) *InteractionHub {
	return &InteractionHub{
		workspaceID: workspaceID, coordinator: coordinator, events: events, now: time.Now,
		answers: make(map[string]chan InteractionAnswer), decided: make(map[string]loop.PermissionDecision), resolved: make(map[string]InteractionAnswer),
	}
}

func newInteractionRequestID() (string, error) {
	buffer := make([]byte, 16)
	if _, err := rand.Read(buffer); err != nil {
		return "", fmt.Errorf("generate interaction request ID: %w", err)
	}
	return "req_" + hex.EncodeToString(buffer), nil
}

func (h *InteractionHub) Close() error {
	if h == nil {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for requestID, answer := range h.answers {
		close(answer)
		delete(h.answers, requestID)
	}
	return nil
}

func questionList(request selecttool.Request) []selecttool.Question {
	if len(request.Questions) != 0 {
		return append([]selecttool.Question(nil), request.Questions...)
	}
	return []selecttool.Question{{Prompt: request.Prompt, Mode: request.Mode, Options: append([]selecttool.Option(nil), request.Options...), InitialSelectedIDs: append([]string(nil), request.InitialSelectedIDs...), MinSelect: request.MinSelect, MaxSelect: request.MaxSelect}}
}

func (h *InteractionHub) expired(requestID, sessionID, turnID, kind, reason string) {
	h.mu.Lock()
	if _, waitingOK := h.answers[requestID]; waitingOK {
		close(h.answers[requestID])
		delete(h.answers, requestID)
	}
	h.mu.Unlock()
	_, _, _ = h.coordinator.ResolveInteraction(requestID)
	h.publish(sessionID, turnID, EventInteractionExpired, map[string]any{
		"request_id": requestID, "kind": kind, "reason": reason, "expired_at": h.now().UTC(),
	})
}

func (h *InteractionHub) CloseInteraction(requestID, sessionID, turnID, kind, reason string) {
	if h == nil {
		return
	}
	h.expired(requestID, sessionID, turnID, kind, reason)
}

func (h *InteractionHub) RequestQuestion(ctx context.Context, sessionID, turnID string, request selecttool.Request) (selecttool.BatchResult, error) {
	if h == nil {
		return selecttool.BatchResult{}, ErrInteractionClosed
	}
	requestID, err := newInteractionRequestID()
	if err != nil {
		return selecttool.BatchResult{}, err
	}
	if _, err := h.coordinator.AddInteraction(InteractionState{RequestID: requestID, SessionID: sessionID, TurnID: turnID, Kind: InteractionKindQuestion}); err != nil {
		return selecttool.BatchResult{}, err
	}
	if err := h.publishQuestionRequested(sessionID, turnID, requestID, request); err != nil {
		_, _, _ = h.coordinator.ResolveInteraction(requestID)
		return selecttool.BatchResult{}, err
	}
	answer, err := h.waitAnswer(ctx, requestID, sessionID, turnID, InteractionKindQuestion)
	if err != nil {
		return selecttool.BatchResult{}, err
	}
	if answer.Cancelled {
		return selecttool.BatchResult{Results: []selecttool.Result{{Cancelled: true}}}, nil
	}
	return answer.toBatchResult(request), nil
}

func (h *InteractionHub) RequestPermission(ctx context.Context, sessionID, turnID string, request loop.PermissionRequest) (loop.PermissionDecision, error) {
	if h == nil {
		return "", ErrInteractionClosed
	}
	requestID, err := newInteractionRequestID()
	if err != nil {
		return "", err
	}
	if _, err := h.coordinator.AddInteraction(InteractionState{RequestID: requestID, SessionID: sessionID, TurnID: turnID, Kind: InteractionKindPermission}); err != nil {
		return "", err
	}
	if err := h.publish(sessionID, turnID, EventPermissionRequested, PermissionRequestedPayload{
		RequestID: requestID, Operation: "workspace_read", CanonicalTarget: request.CanonicalPath, CreatedAt: h.now().UTC(),
	}); err != nil {
		_, _, _ = h.coordinator.ResolveInteraction(requestID)
		return "", err
	}
	answer, err := h.waitAnswer(ctx, requestID, sessionID, turnID, InteractionKindPermission)
	if err != nil {
		return "", err
	}
	decision := loop.PermissionDeny
	if len(answer.SelectedOptions) == 1 && answer.SelectedOptions[0].ID == string(DecisionAllowOnce) {
		decision = loop.PermissionAllowOnce
	}
	h.mu.Lock()
	h.decided[requestID] = decision
	h.mu.Unlock()
	return decision, nil
}

func (h *InteractionHub) waitAnswer(ctx context.Context, requestID, sessionID, turnID, kind string) (InteractionAnswer, error) {
	answerChannel := make(chan InteractionAnswer, 1)
	h.mu.Lock()
	h.answers[requestID] = answerChannel
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		if _, waiting := h.answers[requestID]; waiting {
			delete(h.answers, requestID)
		}
		h.mu.Unlock()
	}()
	select {
	case answer, ok := <-answerChannel:
		if !ok {
			return InteractionAnswer{}, ErrInteractionClosed
		}
		return answer, nil
	case <-ctx.Done():
		cause := context.Cause(ctx)
		h.expired(requestID, sessionID, turnID, kind, cause.Error())
		return InteractionAnswer{}, cause
	}
}

func (h *InteractionHub) AnswerQuestion(requestID string, answer InteractionAnswer) error {
	if h == nil {
		return ErrInteractionClosed
	}
	interaction, state, err := h.coordinator.ResolveInteraction(requestID)
	if err != nil {
		if errors.Is(err, ErrInteractionNotFound) {
			h.mu.Lock()
			_, found := h.resolved[requestID]
			h.mu.Unlock()
			if found {
				return nil
			}
		}
		return err
	}
	h.mu.Lock()
	h.resolved[requestID] = answer
	waiting := h.answers[requestID]
	delete(h.answers, requestID)
	h.mu.Unlock()
	if waiting != nil {
		waiting <- answer
	}
	h.publish(interaction.SessionID, interaction.TurnID, EventQuestionResolved, map[string]any{
		"request_id": requestID, "answer": answer, "resolved_at": h.now().UTC(), "session_version": state.SessionVersion[interaction.SessionID],
	})
	return nil
}

func (h *InteractionHub) DecidePermission(requestID string, decision InteractionDecision) error {
	if h == nil {
		return ErrInteractionClosed
	}
	if decision != DecisionAllowOnce && decision != DecisionDeny {
		return fmt.Errorf("invalid interaction decision %q", decision)
	}
	interaction, state, err := h.coordinator.ResolveInteraction(requestID)
	if err != nil {
		h.mu.Lock()
		_, found := h.decided[requestID]
		h.mu.Unlock()
		if found {
			return nil
		}
		return err
	}
	var answer InteractionAnswer
	if decision == DecisionAllowOnce {
		answer.SelectedOptions = []selecttool.SelectedOption{{ID: string(DecisionAllowOnce), Label: "Allow once"}}
	}
	h.mu.Lock()
	h.decided[requestID] = loop.PermissionDecision(decision)
	h.resolved[requestID] = answer
	waiting := h.answers[requestID]
	delete(h.answers, requestID)
	h.mu.Unlock()
	if waiting != nil {
		waiting <- answer
	}
	h.publish(interaction.SessionID, interaction.TurnID, EventPermissionResolved, map[string]any{
		"request_id": requestID, "decision": string(decision), "resolved_at": h.now().UTC(), "session_version": state.SessionVersion[interaction.SessionID],
	})
	return nil
}

func (h *InteractionHub) publishQuestionRequested(sessionID, turnID, requestID string, request selecttool.Request) error {
	questions := questionList(request)
	payload := QuestionRequestedPayload{RequestID: requestID, Mode: string(request.Mode), CreatedAt: h.now().UTC()}
	if len(questions) > 0 {
		payload.Prompt = questions[0].Prompt
		payload.Mode = string(questions[0].Mode)
		for _, option := range questions[0].Options {
			payload.Options = append(payload.Options, QuestionOptionPayload{ID: option.ID, Label: option.Label, Description: option.Description})
		}
	}
	return h.publish(sessionID, turnID, EventQuestionRequested, payload)
}

func (h *InteractionHub) publish(sessionID, turnID string, eventType EventType, payload any) error {
	if h == nil || h.events == nil {
		return nil
	}
	version := uint64(0)
	if h.coordinator != nil {
		version = h.coordinator.SessionSnapshot(sessionID).SessionVersion
	}
	event, err := NewAppEvent(h.workspaceID, sessionID, turnID, eventType, h.now(), version, payload)
	if err != nil {
		return err
	}
	_, err = h.events.Publish(event)
	return err
}

func (a InteractionAnswer) toBatchResult(request selecttool.Request) selecttool.BatchResult {
	results := make([]selecttool.Result, 0, len(questionList(request)))
	for range questionList(request) {
		selected := make([]selecttool.SelectedOption, 0, len(a.SelectedOptions))
		for _, chosen := range a.SelectedOptions {
			selected = append(selected, selecttool.SelectedOption{ID: chosen.ID, Label: chosen.Label})
		}
		results = append(results, selecttool.Result{SelectedOptions: selected})
	}
	return selecttool.BatchResult{Results: results}
}
