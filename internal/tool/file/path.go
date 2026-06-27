package file

import (
	"fmt"
	"path/filepath"
	"strings"
)

func resolvePathWithinRoot(root, target string) (string, error) {
	resolved, _, err := resolvePathWithinRoots(root, nil, target)
	return resolved, err
}

func resolvePathWithinRoots(root string, readRoots []string, target string) (string, string, error) {
	if strings.TrimSpace(root) == "" {
		return "", "", fmt.Errorf("tool root is empty")
	}

	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", "", err
	}

	var roots []string
	roots = append(roots, absRoot)
	for _, readRoot := range readRoots {
		readRoot = strings.TrimSpace(readRoot)
		if readRoot == "" {
			continue
		}
		absReadRoot, err := filepath.Abs(readRoot)
		if err != nil {
			return "", "", err
		}
		roots = append(roots, absReadRoot)
	}

	target = strings.TrimSpace(target)
	resolvedTarget := absRoot
	if target != "" {
		if filepath.IsAbs(target) {
			resolvedTarget = target
		} else {
			resolvedTarget = filepath.Join(absRoot, target)
		}
	}

	absTarget, err := filepath.Abs(resolvedTarget)
	if err != nil {
		return "", "", err
	}

	for _, allowedRoot := range roots {
		if isPathWithinRoot(allowedRoot, absTarget) {
			return absTarget, allowedRoot, nil
		}
	}

	return "", "", fmt.Errorf("path escapes allowed roots: %s", target)
}

func isPathWithinRoot(root, target string) bool {
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func relativePath(root, target string) string {
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return target
	}
	return filepath.ToSlash(rel)
}

func displayPath(root, target string) string {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return filepath.ToSlash(target)
	}
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return filepath.ToSlash(target)
	}
	if isPathWithinRoot(absRoot, absTarget) {
		return relativePath(absRoot, absTarget)
	}
	return filepath.ToSlash(absTarget)
}
