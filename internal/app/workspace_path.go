package app

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

var (
	ErrWorkspacePathNotAbsolute  = errors.New("workspace path is not absolute")
	ErrWorkspacePathUnresolvable = errors.New("workspace path is unresolvable")
	ErrWorkspacePathNotDirectory = errors.New("workspace path is not a directory")
)

type WorkspaceID string

type WorkspacePath struct {
	ID   WorkspaceID
	Path string
	Name string
}

func CanonicalWorkspace(input string) (WorkspacePath, error) {
	if !filepath.IsAbs(input) {
		return WorkspacePath{}, ErrWorkspacePathNotAbsolute
	}

	absolute, err := filepath.Abs(filepath.Clean(input))
	if err != nil {
		return WorkspacePath{}, fmt.Errorf("%w: %w", ErrWorkspacePathUnresolvable, err)
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return WorkspacePath{}, fmt.Errorf("%w: %w", ErrWorkspacePathUnresolvable, err)
	}
	resolved, err = filepath.Abs(filepath.Clean(resolved))
	if err != nil {
		return WorkspacePath{}, fmt.Errorf("%w: %w", ErrWorkspacePathUnresolvable, err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return WorkspacePath{}, fmt.Errorf("%w: %w", ErrWorkspacePathUnresolvable, err)
	}
	if !info.IsDir() {
		return WorkspacePath{}, ErrWorkspacePathNotDirectory
	}

	sum := sha256.Sum256([]byte(normalizeWorkspacePath(resolved)))
	return WorkspacePath{
		ID:   WorkspaceID(hex.EncodeToString(sum[:16])),
		Path: resolved,
		Name: filepath.Base(resolved),
	}, nil
}
