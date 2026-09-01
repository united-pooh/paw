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
	PrepareSteer(string) (loop.SteerAdmission, bool)
}

var (
	ErrInvalidTurnCommand    = errors.New("invalid_turn_command")
	ErrSessionVersionChanged = errors.New("session_version_changed")
	ErrSteerNotAccepted      = errors.New("steer_not_accepted")
)

type SubmitCommand struct {
	CommandID      string `json:"command_id"`
	SessionID      string `json:"session_id"`
	SessionVersion uint64 `json:"session_version"`
	Text           string `json:"text"`
}

type ActiveTurnCommand struct {
	CommandID    string `json:"command_id"`
	SessionID    string `json:"session_id"`
	ActiveTurnID string `json:"active_turn_id"`
	Text         string `json:"text,omitempty"`
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

	commandMu sync.Mutex
	mu        sync.Mutex
	cancels   map[string]context.CancelCauseFunc
}

func NewTurnService(runner TurnRunner, store *session.JSONLStore, coordinator *WorkspaceCoordinator, events *EventHub, ui *UIAdapter) *TurnService {
	return newTurnService(runner, store, coordinator, events, ui)
}

func newTurnService(runner TurnRunner, store *session.JSONLStore, coordinator *WorkspaceCoordinator, events *EventHub, ui *UIAdapter) *TurnService {
	return &TurnService{
		runner: runner, store: store, coordinator: coordinator, events: events, ui: ui,
		cancels: make(map[string]context.CancelCauseFunc),
	}
}

func (s *TurnService) Submit(ctx context.Context, command SubmitCommand) (CommandReceiptResult, error) {
	if s == nil || s.runner == nil || s.store == nil || s.coordinator == nil {
		return CommandReceiptResult{}, fmt.Errorf("turn service is unavailable")
	}
	s.commandMu.Lock()
	defer s.commandMu.Unlock()
	command.CommandID = strings.TrimSpace(command.CommandID)
	command.SessionID = strings.TrimSpace(command.SessionID)
	command.Text = strings.TrimSpace(command.Text)
	if command.CommandID == "" || command.SessionID == "" || command.Text == "" {
		return CommandReceiptResult{}, fmt.Errorf("%w: command_id, session_id, and text are required", ErrInvalidTurnCommand)
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
	turnCtx, cancel := context.WithCancelCause(context.WithoutCancel(ctx))
	s.mu.Lock()
	s.cancels[turnID] = cancel
	s.mu.Unlock()
	s.publish(command.SessionID, turnID, EventTurnStarted, map[string]any{"turn_id": turnID, "session_id": command.SessionID, "started_at": time.Now().UTC()})

	go s.runTurn(turnCtx, command, turnID)
	return CommandReceiptResult{CommandID: command.CommandID, Status: CommandStatusAccepted, ResourceID: turnID, SessionVersion: receipt.SessionVersion}, nil
}

func (s *TurnService) runTurn(ctx context.Context, command SubmitCommand, turnID string) {
	execution, runErr := s.runner.RunTurnWithTiming(ctx, command.Text, turnID, time.Now())
	s.mu.Lock()
	delete(s.cancels, turnID)
	s.mu.Unlock()
	if errors.Is(context.Cause(ctx), loop.ErrTurnCanceledByUser) && runErr != nil {
		s.ensureStoppedJournal(command.SessionID, turnID)
		_, _ = s.coordinator.CancelTurn(command.SessionID, turnID)
		s.publish(command.SessionID, turnID, EventTurnCancelled, map[string]any{"turn_id": turnID, "finished_at": time.Now().UTC(), "reason": loop.ErrTurnCanceledByUser.Error()})
		s.startNextQueued(command.SessionID)
		return
	}
	if runErr != nil {
		_, _ = s.coordinator.FailTurn(command.SessionID, turnID)
		s.publish(command.SessionID, turnID, EventTurnFailed, map[string]any{"turn_id": turnID, "finished_at": time.Now().UTC(), "error_code": "turn_failed", "message": runErr.Error()})
		s.startNextQueued(command.SessionID)
		return
	}
	_, _ = s.coordinator.CompleteTurn(command.SessionID, turnID)
	s.publish(command.SessionID, turnID, EventTurnCompleted, map[string]any{"turn_id": turnID, "finished_at": time.Now().UTC(), "input_tokens": 0, "output_tokens": 0, "content": execution.Message.Content})
	s.startNextQueued(command.SessionID)
}

func (s *TurnService) ensureStoppedJournal(sessionID, turnID string) {
	records, err := s.store.LoadResolvedJournalRecords(context.Background(), sessionID)
	if err == nil {
		for _, record := range records {
			if record.TurnID == turnID && record.Kind == session.JournalTurnStopped {
				return
			}
		}
	}
	_ = s.store.StopTurn(context.Background(), sessionID, turnID, loop.ErrTurnCanceledByUser)
}

func (s *TurnService) startNextQueued(sessionID string) {
	draft, state, ok := s.coordinator.PeekQueuedInput(sessionID)
	if !ok {
		return
	}
	command := SubmitCommand{CommandID: draft.CommandID + ":queued", SessionID: sessionID, SessionVersion: state.SessionVersion[sessionID], Text: draft.Content}
	if _, err := s.Submit(context.Background(), command); err != nil {
		s.publish(sessionID, "", EventSystemMessage, SystemMessagePayload{Level: "error", Code: "queued_turn_failed", Title: "排队消息启动失败", Body: "queued input remains pending"})
		return
	}
	_, state, _ = s.coordinator.RemoveQueuedInput(sessionID, draft.CommandID)
	s.publish(sessionID, "", EventQueueUpdated, map[string]any{"items": state.Queue[sessionID], "session_version": state.SessionVersion[sessionID]})
}

func (s *TurnService) Steer(ctx context.Context, command ActiveTurnCommand) (CommandReceiptResult, error) {
	if s == nil || s.runner == nil || s.store == nil || s.coordinator == nil {
		return CommandReceiptResult{}, fmt.Errorf("turn service is unavailable")
	}
	s.commandMu.Lock()
	defer s.commandMu.Unlock()
	command.CommandID = strings.TrimSpace(command.CommandID)
	command.SessionID = strings.TrimSpace(command.SessionID)
	command.ActiveTurnID = strings.TrimSpace(command.ActiveTurnID)
	command.Text = strings.TrimSpace(command.Text)
	if command.CommandID == "" || command.SessionID == "" || command.ActiveTurnID == "" || command.Text == "" {
		return CommandReceiptResult{}, fmt.Errorf("%w: command_id, session_id, active_turn_id, and text are required", ErrInvalidTurnCommand)
	}
	if receipt, found, err := s.store.FindCommandReceipt(ctx, command.SessionID, command.CommandID, CommandKindSteerTurn); err != nil {
		return CommandReceiptResult{}, err
	} else if found {
		return commandReceiptResult(receipt), nil
	}
	if err := s.coordinator.Steer(command.SessionID, command.ActiveTurnID); err != nil {
		return CommandReceiptResult{}, err
	}
	admission, ok := s.runner.PrepareSteer(command.Text)
	if !ok {
		return CommandReceiptResult{}, ErrSteerNotAccepted
	}
	version := s.coordinator.SessionSnapshot(command.SessionID).SessionVersion
	receipt := session.CommandReceipt{CommandID: command.CommandID, Kind: CommandKindSteerTurn, ResourceID: command.ActiveTurnID, Status: CommandStatusAccepted, SessionVersion: version}
	input := session.CommandInput{CommandID: command.CommandID, Kind: CommandKindSteerTurn, TurnID: command.ActiveTurnID, Content: command.Text}
	if _, err := s.store.AppendCommand(ctx, command.SessionID, &input, receipt); err != nil {
		admission.Abort()
		return CommandReceiptResult{}, err
	}
	admission.Commit()
	return commandReceiptResult(receipt), nil
}

func (s *TurnService) Queue(ctx context.Context, command ActiveTurnCommand) (CommandReceiptResult, error) {
	if s == nil || s.store == nil || s.coordinator == nil {
		return CommandReceiptResult{}, fmt.Errorf("turn service is unavailable")
	}
	s.commandMu.Lock()
	defer s.commandMu.Unlock()
	command.CommandID = strings.TrimSpace(command.CommandID)
	command.SessionID = strings.TrimSpace(command.SessionID)
	command.ActiveTurnID = strings.TrimSpace(command.ActiveTurnID)
	command.Text = strings.TrimSpace(command.Text)
	if command.CommandID == "" || command.SessionID == "" || command.ActiveTurnID == "" || command.Text == "" {
		return CommandReceiptResult{}, fmt.Errorf("%w: command_id, session_id, active_turn_id, and text are required", ErrInvalidTurnCommand)
	}
	if receipt, found, err := s.store.FindCommandReceipt(ctx, command.SessionID, command.CommandID, CommandKindQueueTurn); err != nil {
		return CommandReceiptResult{}, err
	} else if found {
		return commandReceiptResult(receipt), nil
	}
	createdAt := time.Now().UTC()
	draft := InputDraft{CommandID: command.CommandID, Content: command.Text, CreatedAt: createdAt}
	var receipt session.CommandReceipt
	state, err := s.coordinator.CommitQueuedInput(command.SessionID, command.ActiveTurnID, draft, func(version uint64) error {
		receipt = session.CommandReceipt{CommandID: command.CommandID, Kind: CommandKindQueueTurn, ResourceID: command.ActiveTurnID, Status: CommandStatusAccepted, SessionVersion: version}
		input := session.CommandInput{CommandID: command.CommandID, Kind: CommandKindQueueTurn, TurnID: command.ActiveTurnID, Content: command.Text, CreatedAt: createdAt}
		_, err := s.store.AppendCommand(ctx, command.SessionID, &input, receipt)
		return err
	})
	if err != nil {
		return CommandReceiptResult{}, err
	}
	version := state.SessionVersion[command.SessionID]
	s.publish(command.SessionID, command.ActiveTurnID, EventQueueUpdated, map[string]any{"items": state.Queue[command.SessionID], "session_version": version})
	return commandReceiptResult(receipt), nil
}

func (s *TurnService) Cancel(ctx context.Context, command ActiveTurnCommand) (CommandReceiptResult, error) {
	if s == nil || s.store == nil || s.coordinator == nil {
		return CommandReceiptResult{}, fmt.Errorf("turn service is unavailable")
	}
	command.CommandID = strings.TrimSpace(command.CommandID)
	command.SessionID = strings.TrimSpace(command.SessionID)
	command.ActiveTurnID = strings.TrimSpace(command.ActiveTurnID)
	if command.CommandID == "" || command.SessionID == "" || command.ActiveTurnID == "" {
		return CommandReceiptResult{}, fmt.Errorf("%w: command_id, session_id, and active_turn_id are required", ErrInvalidTurnCommand)
	}
	state := s.coordinator.WorkspaceSnapshot()
	if state.ActiveTurnID == "" || state.ActiveSessionID != command.SessionID || state.ActiveTurnID != command.ActiveTurnID {
		return s.terminalReceipt(ctx, command, state)
	}
	if err := s.coordinator.Steer(command.SessionID, command.ActiveTurnID); err != nil {
		if errors.Is(err, ErrNoActiveTurn) {
			return s.terminalReceipt(ctx, command, s.coordinator.WorkspaceSnapshot())
		}
		return CommandReceiptResult{}, err
	}
	s.mu.Lock()
	cancel := s.cancels[command.ActiveTurnID]
	s.mu.Unlock()
	if cancel == nil {
		return s.terminalReceipt(ctx, command, s.coordinator.WorkspaceSnapshot())
	}
	cancel(loop.ErrTurnCanceledByUser)
	version := s.coordinator.SessionSnapshot(command.SessionID).SessionVersion
	return CommandReceiptResult{CommandID: command.CommandID, Status: CommandStatusAccepted, ResourceID: command.ActiveTurnID, SessionVersion: version}, nil
}

func (s *TurnService) terminalReceipt(ctx context.Context, command ActiveTurnCommand, state WorkspaceState) (CommandReceiptResult, error) {
	status := ""
	records, err := s.store.LoadResolvedJournalRecords(ctx, command.SessionID)
	if err != nil {
		return CommandReceiptResult{}, err
	}
	for _, record := range records {
		if record.TurnID != command.ActiveTurnID {
			continue
		}
		switch record.Kind {
		case session.JournalTurnCompleted:
			status = "completed"
		case session.JournalTurnFailed:
			status = "failed"
		case session.JournalTurnStopped:
			status = "cancelled"
		}
	}
	if status == "" {
		return CommandReceiptResult{}, fmt.Errorf("%w: turn %s is not active or terminal", ErrActiveTurnChanged, command.ActiveTurnID)
	}
	return CommandReceiptResult{CommandID: command.CommandID, Status: status, ResourceID: command.ActiveTurnID, SessionVersion: state.SessionVersion[command.SessionID]}, nil
}

func commandReceiptResult(receipt session.CommandReceipt) CommandReceiptResult {
	return CommandReceiptResult{CommandID: receipt.CommandID, Status: receipt.Status, ResourceID: receipt.ResourceID, SessionVersion: receipt.SessionVersion}
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
