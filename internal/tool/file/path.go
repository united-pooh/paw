package file

import (
	"fmt"
	"path/filepath"
	"strings"
)

func resolvePathWithinRoot(root, target string) (string, error) {
	return resolvePathWithinRoots(root, target, nil)
}

func resolvePathWithinRoots(root, target string, extraRoots []string) (string, error) {
	if strings.TrimSpace(root) == "" {
		return "", fmt.Errorf("tool root is empty")
	}

	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}

	allowedRoots := append([]string{filepath.Clean(absRoot)}, cleanExtraRoots(extraRoots)...)
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
		return "", err
	}
	absTarget = filepath.Clean(absTarget)

	for _, allowedRoot := range allowedRoots {
		if isWithinRoot(allowedRoot, absTarget) {
			return absTarget, nil
		}
	}

	return "", fmt.Errorf("path escapes allowed roots: %s", target)
}

func relativePath(root, target string) string {
	if strings.TrimSpace(root) == "" {
		return filepath.ToSlash(target)
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return filepath.ToSlash(target)
	}
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return filepath.ToSlash(target)
	}
	rel, err := filepath.Rel(absRoot, absTarget)
	if err != nil {
		return filepath.ToSlash(absTarget)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return filepath.ToSlash(absTarget)
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
	if isWithinRoot(filepath.Clean(absRoot), filepath.Clean(absTarget)) {
		return relativePath(absRoot, absTarget)
	}
	return filepath.ToSlash(absTarget)
}

// DisplayPath formats a resolved file path for user-facing output.
// Paths inside root are slash-normalized and relative to root; paths outside
// root remain absolute.
func DisplayPath(root, target string) string {
	root = strings.TrimSpace(root)
	target = strings.TrimSpace(target)
	if root != "" && target != "" && !filepath.IsAbs(target) {
		target = filepath.Join(root, target)
	}
	return displayPath(root, target)
}

func cleanExtraRoots(roots []string) []string {
	cleaned := make([]string, 0, len(roots))
	seen := make(map[string]bool, len(roots))
	for _, root := range roots {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		abs, err := filepath.Abs(root)
		if err != nil {
			continue
		}
		abs = filepath.Clean(abs)
		if seen[abs] {
			continue
		}
		seen[abs] = true
		cleaned = append(cleaned, abs)
	}
	return cleaned
}

func isWithinRoot(root, target string) bool {
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}
