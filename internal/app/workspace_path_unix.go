//go:build !windows

package app

import "path/filepath"

func normalizeWorkspacePath(path string) string {
	return filepath.Clean(path)
}
