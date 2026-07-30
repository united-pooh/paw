package file

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAtomicWriteFileCreatesAndOverwrites(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "sub", "out.txt")

	if err := atomicWriteFile(target, []byte("hello")); err != nil {
		t.Fatalf("first write: %v", err)
	}
	got, err := os.ReadFile(target)
	if err != nil || string(got) != "hello" {
		t.Fatalf("after first write got %q, %v", string(got), err)
	}

	if err := atomicWriteFile(target, []byte("world")); err != nil {
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
