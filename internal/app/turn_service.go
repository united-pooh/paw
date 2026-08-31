package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"paw/internal/loop"
	"paw/internal/session"
)

type TurnRunner interface {
	CurrentSessionID() string
	LoadSession(context.Context, string) (loop.SessionLoadResult, error)
	RunTurnWithTiming(context.Context, string, string, time.Time) (loop.TurnExecution, error)
}

var ErrSessionVersionChanged = errors.New("session_version_changed")

type SubmitCommand struct {
	CommandID      string `json:"command_id"`
	SessionID      string `json:"session_id"`
	SessionVersion uint64 `json:"session_version"`
	Text           string `json:"text"`
}

type CommandReceiptResult struct {
	CommandID      string `json:"command_id"`
	Status         string `json:"status"`
	ResourceID     string `json:"resource_id"`
	SessionVersion uint64 `json:"session_version"`
}

type TurnService struct {
	runner      TurnRunner
	store       *session.JSONLStore
	coordinator *WorkspaceCoordinator
	events      *EventHub
	ui          *UIAdapter

	mu      sync.Mutex
	cancels map[string]context.CancelCauseFunc
}

func NewTurnService(runner TurnRunner, store *session.JSONLStore, coordinator *WorkspaceCoordinator, events *EventHub, ui *UIAdapter) *TurnService {
	return newTurnService(runner, store, coordinator, events, ui)
}

func newTurnService(runner TurnRunner, store *session.JSONLStore, coordinator *WorkspaceCoordinator, events *EventHub, ui *UIAdapter) *TurnService {
	return &TurnService{runner: runner, store: store, coordinator: coordinator, events: events, ui: ui, cancels: make(map[string]context.CancelCauseFunc)}
}

func (s *TurnService) Submit(ctx context.Context, command SubmitCommand) (CommandReceiptResult, error) {
	if s == nil || s.runner == nil || s.store == nil || s.coordinator == nil {
		return CommandReceiptResult{}, fmt.Errorf("turn service is unavailable")
	}
	command.CommandID = strings.TrimSpace(command.CommandID)
	command.SessionID = strings.TrimSpace(command.SessionID)
	command.Text = strings.TrimSpace(command.Text)
	if command.CommandID == "" || command.SessionID == "" || command.Text == "" {
		return CommandReceiptResult{}, fmt.Errorf("command_id, session_id, and text are required")
	}
	turnID, err := deterministicCommandResourceID(CommandKindSubmitTurn, command.CommandID, "")
	if err != nil {
		return CommandReceiptResult{}, err
	}
	if receipt, found, findErr := s.store.FindCommandReceipt(ctx, command.SessionID, command.CommandID, CommandKindSubmitTurn); findErr != nil {
		return CommandReceiptResult{}, findErr
	} else if found {
		return CommandReceiptResult{CommandID: receipt.CommandID, Status: receipt.Status, ResourceID: receipt.ResourceID, SessionVersion: receipt.SessionVersion}, nil
	}
	currentVersion := s.coordinator.SessionSnapshot(command.SessionID).SessionVersion
	if command.SessionVersion != currentVersion {
		return CommandReceiptResult{}, fmt.Errorf("%w: got %d want %d", ErrSessionVersionChanged, command.SessionVersion, currentVersion)
	}
	state, err := s.coordinator.BeginTurn(command.SessionID, turnID)
	if err != nil {
		return CommandReceiptResult{}, err
	}
	if s.runner.CurrentSessionID() != command.SessionID {
		if _, err := s.runner.LoadSession(ctx, command.SessionID); err != nil {
			_, _ = s.coordinator.FailTurn(command.SessionID, turnID)
			return CommandReceiptResult{}, err
		}
	}
	if s.ui != nil {
		if err := s.ui.BindTurn(command.SessionID, turnID); err != nil {
			_, _ = s.coordinator.FailTurn(command.SessionID, turnID)
			return CommandReceiptResult{}, err
		}
	}
	receipt := session.CommandReceipt{
		CommandID: command.CommandID, Kind: CommandKindSubmitTurn, ResourceID: turnID,
		Status: CommandStatusAccepted, SessionVersion: state.SessionVersion[command.SessionID],
	}
	if _, err := s.store.AppendCommandReceipt(ctx, command.SessionID, receipt); err != nil {
		_, _ = s.coordinator.FailTurn(command.SessionID, turnID)
		return CommandReceiptResult{}, err
	}
	s.publish(command.SessionID, turnID, EventTurnStarted, map[string]any{"turn_id": turnID, "session_id": command.SessionID, "started_at": time.Now().UTC()})

	turnCtx, cancel := context.WithCancelCause(context.Background())
	s.mu.Lock()
	s.cancels[turnID] = cancel
	s.mu.Unlock()
	go s.runTurn(turnCtx, command, turnID)
	return CommandReceiptResult{CommandID: command.CommandID, Status: CommandStatusAccepted, ResourceID: turnID, SessionVersion: receipt.SessionVersion}, nil
}

func (s *TurnService) runTurn(ctx context.Context, command SubmitCommand, turnID string) {
	execution, runErr := s.runner.RunTurnWithTiming(ctx, command.Text, turnID, time.Now())
	s.mu.Lock()
	delete(s.cancels, turnID)
	s.mu.Unlock()
	if runErr != nil {
		_, _ = s.coordinator.FailTurn(command.SessionID, turnID)
		s.publish(command.SessionID, turnID, EventTurnFailed, map[string]any{"turn_id": turnID, "finished_at": time.Now().UTC(), "error_code": "turn_failed", "message": runErr.Error()})
		return
	}
	_, _ = s.coordinator.CompleteTurn(command.SessionID, turnID)
	s.publish(command.SessionID, turnID, EventTurnCompleted, map[string]any{"turn_id": turnID, "finished_at": time.Now().UTC(), "input_tokens": 0, "output_tokens": 0, "content": execution.Message.Content})
}

func (s *TurnService) ActiveTurn() (sessionID, turnID string) {
	if s == nil || s.coordinator == nil {
		return "", ""
	}
	state := s.coordinator.WorkspaceSnapshot()
	return state.ActiveSessionID, state.ActiveTurnID
}

func (s *TurnService) publish(sessionID, turnID string, eventType EventType, payload any) {
	if s == nil || s.events == nil {
		return
	}
	version := s.coordinator.SessionSnapshot(sessionID).SessionVersion
	event, err := NewAppEvent(s.events.workspaceID, sessionID, turnID, eventType, time.Now(), version, payload)
	if err == nil {
		_, _ = s.events.Publish(event)
	}
}
