package file

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// mutationPathError reports a filesystem operation using a model-facing path
// while preserving the underlying reason for errors.Is/errors.As.
func mutationPathError(op, display string, err error) error {
	var pathErr *os.PathError
	if errors.As(err, &pathErr) {
		return &os.PathError{Op: op, Path: display, Err: pathErr.Err}
	}
	var linkErr *os.LinkError
	if errors.As(err, &linkErr) {
		return &os.PathError{Op: op, Path: display, Err: linkErr.Err}
	}
	return fmt.Errorf("%s %s: %w", op, display, err)
}

// resolveMutationPath resolves a lexical workspace path and verifies that the
// target's real path cannot escape root through a final or intermediate
// symlink. When allowMissing is true, the nearest existing ancestor is
// resolved and the missing suffix is checked beneath that real ancestor.
func resolveMutationPath(root, target string, allowMissing bool) (string, bool, error) {
	resolved, err := resolvePathWithinRoot(root, target)
	if err != nil {
		return "", false, err
	}

	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", false, err
	}
	display := relativePath(absRoot, resolved)
	realRoot, err := filepath.EvalSymlinks(filepath.Clean(absRoot))
	if err != nil {
		return "", false, mutationPathError("resolve workspace root", ".", err)
	}
	realRoot, err = filepath.Abs(realRoot)
	if err != nil {
		return "", false, mutationPathError("resolve workspace root", ".", err)
	}
	realRoot = filepath.Clean(realRoot)

	info, lstatErr := os.Lstat(resolved)
	if lstatErr == nil && info.Mode()&os.ModeSymlink != 0 {
		return "", false, fmt.Errorf("symbolic link target is not supported: %s", display)
	}
	if lstatErr != nil && !os.IsNotExist(lstatErr) {
		return "", false, mutationPathError("lstat mutation target", display, lstatErr)
	}

	candidate := resolved
	for {
		_, statErr := os.Lstat(candidate)
		if statErr == nil {
			break
		}
		if !os.IsNotExist(statErr) {
			return "", false, mutationPathError("lstat mutation path", display, statErr)
		}
		parent := filepath.Dir(candidate)
		if parent == candidate {
			return "", false, mutationPathError("lstat mutation path", display, statErr)
		}
		candidate = parent
	}

	realAncestor, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return "", false, mutationPathError("resolve mutation path", display, err)
	}
	realAncestor, err = filepath.Abs(realAncestor)
	if err != nil {
		return "", false, mutationPathError("resolve mutation path", display, err)
	}

	remainder, err := filepath.Rel(candidate, resolved)
	if err != nil {
		return "", false, mutationPathError("resolve mutation path", display, err)
	}
	realTarget := filepath.Clean(filepath.Join(realAncestor, remainder))
	if !isWithinRoot(realRoot, realTarget) {
		return "", false, fmt.Errorf("path escapes allowed roots through symlink: %s", display)
	}

	_, statErr := os.Stat(resolved)
	if statErr == nil {
		return resolved, true, nil
	}
	if !os.IsNotExist(statErr) {
		return "", false, mutationPathError("stat mutation target", display, statErr)
	}
	if !allowMissing {
		return "", false, mutationPathError("stat mutation target", display, statErr)
	}
	return resolved, false, nil
}
