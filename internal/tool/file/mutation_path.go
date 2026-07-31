package file

import (
	"fmt"
	"os"
	"path/filepath"
)

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
	realRoot, err := filepath.EvalSymlinks(filepath.Clean(absRoot))
	if err != nil {
		return "", false, fmt.Errorf("resolve workspace root: %w", err)
	}
	realRoot, err = filepath.Abs(realRoot)
	if err != nil {
		return "", false, err
	}
	realRoot = filepath.Clean(realRoot)

	info, lstatErr := os.Lstat(resolved)
	if lstatErr == nil && info.Mode()&os.ModeSymlink != 0 {
		return "", false, fmt.Errorf("symbolic link target is not supported: %s", relativePath(absRoot, resolved))
	}
	if lstatErr != nil && !os.IsNotExist(lstatErr) {
		return "", false, lstatErr
	}

	candidate := resolved
	for {
		_, statErr := os.Lstat(candidate)
		if statErr == nil {
			break
		}
		if !os.IsNotExist(statErr) {
			return "", false, statErr
		}
		parent := filepath.Dir(candidate)
		if parent == candidate {
			return "", false, statErr
		}
		candidate = parent
	}

	realAncestor, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return "", false, fmt.Errorf("resolve mutation path %s: %w", target, err)
	}
	realAncestor, err = filepath.Abs(realAncestor)
	if err != nil {
		return "", false, err
	}

	remainder, err := filepath.Rel(candidate, resolved)
	if err != nil {
		return "", false, err
	}
	realTarget := filepath.Clean(filepath.Join(realAncestor, remainder))
	if !isWithinRoot(realRoot, realTarget) {
		return "", false, fmt.Errorf("path escapes allowed roots through symlink: %s", target)
	}

	_, statErr := os.Stat(resolved)
	if statErr == nil {
		return resolved, true, nil
	}
	if !os.IsNotExist(statErr) {
		return "", false, statErr
	}
	if !allowMissing {
		return "", false, statErr
	}
	return resolved, false, nil
}
