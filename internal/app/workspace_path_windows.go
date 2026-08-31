//go:build windows

package app

import (
	"path/filepath"
	"strings"
)

func normalizeWorkspacePath(path string) string {
	return strings.ToLower(filepath.Clean(path))
}
