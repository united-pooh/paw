//go:build windows

package app

import "testing"

func TestNormalizeWorkspacePathIgnoresWindowsCase(t *testing.T) {
	upper := normalizeWorkspacePath(`C:\Users\Example\Workspace`)
	lower := normalizeWorkspacePath(`c:\users\example\workspace`)
	if upper != lower {
		t.Fatalf("upper = %q, lower = %q", upper, lower)
	}
}
