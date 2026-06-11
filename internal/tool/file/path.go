package file

import (
	"fmt"
	"path/filepath"
	"strings"
)

func resolvePathWithinRoot(root, target string) (string, error) {
	if strings.TrimSpace(root) == "" {
		return "", fmt.Errorf("tool root is empty")
	}

	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}

	resolvedTarget := absRoot
	if strings.TrimSpace(target) != "" {
		resolvedTarget = filepath.Join(absRoot, target)
	}

	absTarget, err := filepath.Abs(resolvedTarget)
	if err != nil {
		return "", err
	}

	rel, err := filepath.Rel(absRoot, absTarget)
	if err != nil {
		return "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes workspace root: %s", target)
	}

	// TODO: 如果后续要把文件访问做成严格沙箱，这里还需要补 symlink-aware 校验。
	return absTarget, nil
}

func relativePath(root, target string) string {
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return target
	}
	return filepath.ToSlash(rel)
}
