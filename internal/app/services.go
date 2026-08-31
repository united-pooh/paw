package app

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"paw/internal/session"
)

type SessionService struct {
	store       *session.JSONLStore
	coordinator *WorkspaceCoordinator
	commandMu   sync.Mutex
}

func NewSessionService(store *session.JSONLStore, coordinator *WorkspaceCoordinator) *SessionService {
	return &SessionService{store: store, coordinator: coordinator}
}

type SessionPageRequest struct {
	Cursor string
	Limit  int
}

type SessionPage struct {
	Items      []SessionSummary `json:"items"`
	NextCursor string           `json:"next_cursor,omitempty"`
}

type SnapshotRequest struct {
	Before string
	Limit  int
}

type CreateSessionCommand struct {
	CommandID string
	SessionID string
}

type ForkSessionCommand struct {
	CommandID       string
	SessionID       string
	ParentSessionID string
}

type SessionMutationResult struct {
	SessionID      string `json:"session_id"`
	SessionVersion uint64 `json:"session_version"`
}

func (s *SessionService) List(ctx context.Context, request SessionPageRequest) (SessionPage, error) {
	if s == nil || s.store == nil {
		return SessionPage{}, fmt.Errorf("session service is unavailable")
	}
	page, err := s.store.ListSessionPage(ctx, session.SessionPageRequest{Cursor: request.Cursor, Limit: request.Limit})
	if err != nil {
		return SessionPage{}, err
	}
	items := make([]SessionSummary, 0, len(page.Items))
	for _, summary := range page.Items {
		items = append(items, SessionSummary{
			SessionID: summary.SessionID, CreatedAt: summary.CreatedAt, LastUsedAt: summary.LastUsedAt,
			Title: summary.FirstMessage, TranscriptSize: summary.TranscriptSize,
		})
	}
	return SessionPage{Items: items, NextCursor: page.NextCursor}, nil
}

func (s *SessionService) Snapshot(ctx context.Context, sessionID string, request SnapshotRequest) (SessionProjection, error) {
	if s == nil || s.store == nil {
		return SessionProjection{}, fmt.Errorf("session service is unavailable")
	}
	return projectSession(ctx, s.store, s.coordinator, strings.TrimSpace(sessionID), request)
}

func (s *SessionService) Create(ctx context.Context, command CreateSessionCommand) (SessionMutationResult, error) {
	if s == nil || s.store == nil {
		return SessionMutationResult{}, fmt.Errorf("session service is unavailable")
	}
	s.commandMu.Lock()
	defer s.commandMu.Unlock()
	commandID := strings.TrimSpace(command.CommandID)
	var sessionID string
	var err error
	if commandID != "" {
		sessionID, err = deterministicCommandResourceID(CommandKindCreateSession, commandID, command.SessionID)
	} else {
		sessionID = strings.TrimSpace(command.SessionID)
		if sessionID == "" {
			sessionID, err = session.GenerateSessionID()
		}
	}
	if err != nil {
		return SessionMutationResult{}, err
	}
	if commandID != "" {
		if receipt, found, findErr := s.findReceipt(ctx, sessionID, commandID, CommandKindCreateSession); findErr != nil {
			return SessionMutationResult{}, findErr
		} else if found {
			return mutationResultFromReceipt(receipt), nil
		}
	}
	if _, err := s.store.CreateRoot(ctx, session.CreateRootRequest{SessionID: sessionID}); err != nil {
		return SessionMutationResult{}, err
	}
	result := SessionMutationResult{SessionID: sessionID, SessionVersion: s.sessionVersion(sessionID)}
	if commandID != "" {
		receipt := CommandReceipt{
			CommandID: commandID, Kind: CommandKindCreateSession, ResourceID: sessionID,
			Status: CommandStatusAccepted, SessionVersion: result.SessionVersion,
		}
		if _, err := s.store.AppendCommandReceipt(ctx, sessionID, receipt); err != nil {
			return SessionMutationResult{}, err
		}
	}
	return result, nil
}

func (s *SessionService) Fork(ctx context.Context, command ForkSessionCommand) (SessionMutationResult, error) {
	if s == nil || s.store == nil {
		return SessionMutationResult{}, fmt.Errorf("session service is unavailable")
	}
	s.commandMu.Lock()
	defer s.commandMu.Unlock()
	parentID := strings.TrimSpace(command.ParentSessionID)
	if parentID == "" {
		return SessionMutationResult{}, fmt.Errorf("parent session ID is required")
	}
	commandID := strings.TrimSpace(command.CommandID)
	var sessionID string
	var err error
	if commandID != "" {
		sessionID, err = deterministicCommandResourceID(CommandKindForkSession, commandID, command.SessionID)
	} else {
		sessionID = strings.TrimSpace(command.SessionID)
		if sessionID == "" {
			sessionID, err = session.GenerateSessionID()
		}
	}
	if err != nil {
		return SessionMutationResult{}, err
	}
	if commandID != "" {
		if receipt, found, findErr := s.findReceipt(ctx, sessionID, commandID, CommandKindForkSession); findErr != nil {
			return SessionMutationResult{}, findErr
		} else if found {
			return mutationResultFromReceipt(receipt), nil
		}
	}
	if _, err := s.store.Fork(ctx, session.ForkRequest{
		SessionID: sessionID, ParentSessionID: parentID, ForkFromSeq: -1,
	}); err != nil {
		return SessionMutationResult{}, err
	}
	result := SessionMutationResult{SessionID: sessionID, SessionVersion: s.sessionVersion(sessionID)}
	if commandID != "" {
		receipt := CommandReceipt{
			CommandID: commandID, Kind: CommandKindForkSession, ResourceID: sessionID,
			Status: CommandStatusAccepted, SessionVersion: result.SessionVersion,
		}
		if _, err := s.store.AppendCommandReceipt(ctx, sessionID, receipt); err != nil {
			return SessionMutationResult{}, err
		}
	}
	return result, nil
}

func (s *SessionService) findReceipt(ctx context.Context, sessionID, commandID, kind string) (CommandReceipt, bool, error) {
	exists, err := s.store.Exists(ctx, sessionID)
	if err != nil || !exists {
		return CommandReceipt{}, false, err
	}
	return s.store.FindCommandReceipt(ctx, sessionID, commandID, kind)
}

func (s *SessionService) sessionVersion(sessionID string) uint64 {
	if s == nil || s.coordinator == nil {
		return 0
	}
	return s.coordinator.SessionSnapshot(sessionID).SessionVersion
}
