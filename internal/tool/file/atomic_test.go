package file

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestAtomicWriteFileCreatesAndOverwrites(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "sub", "out.txt")

	if err := atomicWriteFile(target, []byte("hello"), 0o644); err != nil {
		t.Fatalf("first write: %v", err)
	}
	got, err := os.ReadFile(target)
	if err != nil || string(got) != "hello" {
		t.Fatalf("after first write got %q, %v", string(got), err)
	}

	if err := atomicWriteFile(target, []byte("world"), 0o644); err != nil {
		t.Fatalf("second write: %v", err)
	}
	got, err = os.ReadFile(target)
	if err != nil || string(got) != "world" {
		t.Fatalf("after overwrite got %q, %v", string(got), err)
	}

	// No leftover temp files in the directory.
	entries, err := os.ReadDir(filepath.Join(dir, "sub"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 file in dir, got %d", len(entries))
	}
}

func TestAtomicWriteFileCleansUpTempFileWhenRenameFails(t *testing.T) {
	parent := t.TempDir()
	target := filepath.Join(parent, "target")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(target, "sentinel.txt")
	if err := os.WriteFile(sentinel, []byte("keep me"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := atomicWriteFile(target, []byte("replacement"), 0o600); err == nil {
		t.Fatal("atomicWriteFile unexpectedly replaced a non-empty directory")
	}

	matches, err := filepath.Glob(filepath.Join(parent, ".edit-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("temporary files remain after rename failure: %v", matches)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatalf("target directory was damaged: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("target type changed after rename failure: mode %v", info.Mode())
	}
	got, err := os.ReadFile(sentinel)
	if err != nil {
		t.Fatalf("sentinel was damaged: %v", err)
	}
	if string(got) != "keep me" {
		t.Fatalf("sentinel content = %q, want %q", got, "keep me")
	}
}

func TestAtomicWriteFileAppliesRequestedMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not provide Unix permission semantics")
	}

	target := filepath.Join(t.TempDir(), "script.sh")
	if err := atomicWriteFile(target, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o755 {
		t.Fatalf("mode = %04o, want 0755", got)
	}
}
