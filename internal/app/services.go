package app

import (
	"context"
	"fmt"
	"strings"

	"paw/internal/session"
)

type SessionService struct {
	store       *session.JSONLStore
	coordinator *WorkspaceCoordinator
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
	SessionID string
}

type ForkSessionCommand struct {
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
	sessionID := strings.TrimSpace(command.SessionID)
	if sessionID == "" {
		var err error
		sessionID, err = session.GenerateSessionID()
		if err != nil {
			return SessionMutationResult{}, err
		}
	}
	if _, err := s.store.CreateRoot(ctx, session.CreateRootRequest{SessionID: sessionID}); err != nil {
		return SessionMutationResult{}, err
	}
	return SessionMutationResult{SessionID: sessionID, SessionVersion: s.sessionVersion(sessionID)}, nil
}

func (s *SessionService) Fork(ctx context.Context, command ForkSessionCommand) (SessionMutationResult, error) {
	if s == nil || s.store == nil {
		return SessionMutationResult{}, fmt.Errorf("session service is unavailable")
	}
	parentID := strings.TrimSpace(command.ParentSessionID)
	if parentID == "" {
		return SessionMutationResult{}, fmt.Errorf("parent session ID is required")
	}
	sessionID := strings.TrimSpace(command.SessionID)
	if sessionID == "" {
		var err error
		sessionID, err = session.GenerateSessionID()
		if err != nil {
			return SessionMutationResult{}, err
		}
	}
	if _, err := s.store.Fork(ctx, session.ForkRequest{
		SessionID: sessionID, ParentSessionID: parentID, ForkFromSeq: -1,
	}); err != nil {
		return SessionMutationResult{}, err
	}
	return SessionMutationResult{SessionID: sessionID, SessionVersion: s.sessionVersion(sessionID)}, nil
}

func (s *SessionService) sessionVersion(sessionID string) uint64 {
	if s == nil || s.coordinator == nil {
		return 0
	}
	return s.coordinator.SessionSnapshot(sessionID).SessionVersion
}
