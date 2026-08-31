package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

type RecentWorkspace struct {
	ID           WorkspaceID `json:"id"`
	Path         string      `json:"path"`
	Name         string      `json:"name"`
	LastOpenedAt time.Time   `json:"last_opened_at"`
}

type recentWorkspaceFile struct {
	Workspaces []RecentWorkspace `json:"workspaces"`
}

type RecentWorkspaceStore struct {
	mu   sync.Mutex
	path string
	now  func() time.Time
}

func NewRecentWorkspaceStore(path string) (*RecentWorkspaceStore, error) {
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("resolve recent workspace home: %w", err)
		}
		path = filepath.Join(home, ".paw", "recent-workspaces.json")
	}
	absolute, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return nil, fmt.Errorf("resolve recent workspace path: %w", err)
	}
	return &RecentWorkspaceStore{path: absolute, now: time.Now}, nil
}

func (s *RecentWorkspaceStore) Path() string {
	if s == nil {
		return ""
	}
	return s.path
}

func (s *RecentWorkspaceStore) Remember(ctx context.Context, workspace WorkspacePath) error {
	if s == nil {
		return nil
	}
	if err := contextError(ctx); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	items, err := s.loadLocked()
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	if s.now != nil {
		now = s.now().UTC()
	}
	updated := false
	for index := range items {
		if items[index].ID == workspace.ID {
			items[index].Path = workspace.Path
			items[index].Name = workspace.Name
			items[index].LastOpenedAt = now
			updated = true
			break
		}
	}
	if !updated {
		items = append(items, RecentWorkspace{
			ID: workspace.ID, Path: workspace.Path, Name: workspace.Name, LastOpenedAt: now,
		})
	}
	return s.saveLocked(items)
}

func (s *RecentWorkspaceStore) List(ctx context.Context) ([]RecentWorkspace, error) {
	if s == nil {
		return nil, nil
	}
	if err := contextError(ctx); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	items, err := s.loadLocked()
	if err != nil {
		return nil, err
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].LastOpenedAt.After(items[j].LastOpenedAt)
	})
	return items, nil
}

func (s *RecentWorkspaceStore) Forget(ctx context.Context, id WorkspaceID) error {
	if s == nil {
		return nil
	}
	if err := contextError(ctx); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	items, err := s.loadLocked()
	if err != nil {
		return err
	}
	filtered := items[:0]
	for _, item := range items {
		if item.ID != id {
			filtered = append(filtered, item)
		}
	}
	return s.saveLocked(filtered)
}

func (s *RecentWorkspaceStore) loadLocked() ([]RecentWorkspace, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read recent workspaces: %w", err)
	}
	var file recentWorkspaceFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("parse recent workspaces: %w", err)
	}
	return append([]RecentWorkspace(nil), file.Workspaces...), nil
}

func (s *RecentWorkspaceStore) saveLocked(items []RecentWorkspace) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return fmt.Errorf("create recent workspace directory: %w", err)
	}
	data, err := json.MarshalIndent(recentWorkspaceFile{Workspaces: items}, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal recent workspaces: %w", err)
	}
	data = append(data, '\n')
	temp, err := os.CreateTemp(filepath.Dir(s.path), ".recent-workspaces-*.tmp")
	if err != nil {
		return fmt.Errorf("create recent workspace temp file: %w", err)
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(0o600); err != nil {
		_ = temp.Close()
		return err
	}
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return fmt.Errorf("write recent workspaces: %w", err)
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return fmt.Errorf("sync recent workspaces: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close recent workspaces: %w", err)
	}
	if err := os.Rename(tempPath, s.path); err != nil {
		return fmt.Errorf("replace recent workspaces: %w", err)
	}
	return nil
}

func contextError(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return context.Cause(ctx)
	}
	return nil
}
