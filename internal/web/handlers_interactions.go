package web

import (
	"errors"
	"net/http"
	"strings"

	"paw/internal/app"
)

type answerQuestionRequest struct {
	Cancelled      bool   `json:"cancelled,omitempty"`
	SelectedOption string `json:"selected_option,omitempty"`
}

type decidePermissionRequest struct {
	Decision string `json:"decision"`
}

func (s *Server) handleAnswerQuestion(writer http.ResponseWriter, request *http.Request) {
	s.handleInteraction(writer, request, func(runtime *app.WorkspaceRuntime, requestID string) error {
		var input answerQuestionRequest
		if err := DecodeJSON(writer, request, &input); err != nil {
			return err
		}
		answer := app.InteractionAnswer{Cancelled: input.Cancelled}
		if strings.TrimSpace(input.SelectedOption) != "" {
			answer.SelectedOptions = []app.SelectedAnswerOption{{ID: strings.TrimSpace(input.SelectedOption)}}
		}
		return runtime.Interactions.AnswerQuestion(requestID, answer)
	})
}

func (s *Server) handleDecidePermission(writer http.ResponseWriter, request *http.Request) {
	s.handleInteraction(writer, request, func(runtime *app.WorkspaceRuntime, requestID string) error {
		var input decidePermissionRequest
		if err := DecodeJSON(writer, request, &input); err != nil {
			return err
		}
		return runtime.Interactions.DecidePermission(requestID, app.InteractionDecision(strings.TrimSpace(input.Decision)))
	})
}

func (s *Server) handleInteraction(writer http.ResponseWriter, request *http.Request, run func(*app.WorkspaceRuntime, string) error) {
	runtime, ok := s.runtime(request)
	if !ok || runtime.Interactions == nil {
		writeJSONError(writer, http.StatusNotFound, "workspace_not_loaded", "workspace is not loaded", RequestID(request.Context()))
		return
	}
	requestID := request.PathValue("request_id")
	if err := run(runtime, requestID); err != nil {
		requestIDValue := RequestID(request.Context())
		switch {
		case errors.Is(err, app.ErrInteractionNotFound):
			writeJSONError(writer, http.StatusNotFound, "interaction_not_found", "interaction request is unknown or already resolved", requestIDValue)
		case errors.Is(err, app.ErrInteractionClosed):
			writeJSONError(writer, http.StatusConflict, "interaction_closed", "interaction is no longer pending", requestIDValue)
		case strings.Contains(err.Error(), "invalid interaction decision"):
			writeJSONError(writer, http.StatusBadRequest, "invalid_decision", "decision is invalid", requestIDValue)
		default:
			writeJSONError(writer, http.StatusInternalServerError, "interaction_failed", "interaction could not be applied", requestIDValue)
		}
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}
