package app

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestCanonicalWorkspaceRejectsRelativeAndBrokenSymlink(t *testing.T) {
	_, err := CanonicalWorkspace("relative/path")
	if !errors.Is(err, ErrWorkspacePathNotAbsolute) {
		t.Fatalf("relative path error = %v", err)
	}

	root := t.TempDir()
	broken := filepath.Join(root, "broken")
	if err := os.Symlink(filepath.Join(root, "missing"), broken); err != nil {
		skipWindowsSymlinkPrivilege(t, err)
		t.Fatal(err)
	}
	_, err = CanonicalWorkspace(broken)
	if !errors.Is(err, ErrWorkspacePathUnresolvable) {
		t.Fatalf("broken symlink error = %v", err)
	}
}

func TestCanonicalWorkspaceRejectsNonDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file.txt")
	if err := os.WriteFile(path, []byte("not a workspace"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := CanonicalWorkspace(path)
	if !errors.Is(err, ErrWorkspacePathNotDirectory) {
		t.Fatalf("non-directory error = %v", err)
	}
}

func TestCanonicalWorkspacePreservesUnderlyingPathError(t *testing.T) {
	_, err := CanonicalWorkspace(filepath.Join(t.TempDir(), "missing"))
	if !errors.Is(err, ErrWorkspacePathUnresolvable) || !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing path error = %v", err)
	}
}

func TestCanonicalWorkspaceResolvesSymlinkAndStableID(t *testing.T) {
	root := t.TempDir()
	link := filepath.Join(t.TempDir(), "workspace")
	if err := os.Symlink(root, link); err != nil {
		skipWindowsSymlinkPrivilege(t, err)
		t.Fatal(err)
	}

	direct, err := CanonicalWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	viaLink, err := CanonicalWorkspace(link)
	if err != nil {
		t.Fatal(err)
	}

	if direct.Path != viaLink.Path || direct.ID != viaLink.ID {
		t.Fatalf("direct=%+v link=%+v", direct, viaLink)
	}
	if direct.Name != filepath.Base(direct.Path) {
		t.Fatalf("name = %q, path = %q", direct.Name, direct.Path)
	}
	if len(direct.ID) != 32 {
		t.Fatalf("workspace ID length = %d, want 32", len(direct.ID))
	}
}

func skipWindowsSymlinkPrivilege(t *testing.T, err error) {
	t.Helper()
	if runtime.GOOS == "windows" && errors.Is(err, os.ErrPermission) {
		t.Skipf("symlink privilege unavailable: %v", err)
	}
}
