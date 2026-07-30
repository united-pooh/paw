package file

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteToolCreatesNewFile(t *testing.T) {
	root := t.TempDir()
	tool := &WriteTool{Root: root, ReadState: NewReadStateStore()}
	out, err := tool.Run(context.Background(), []byte(`{"file_path":"new.txt","content":"hi\n"}`))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(out, "wrote") {
		t.Fatalf("output = %q", out)
	}
	got, _ := os.ReadFile(filepath.Join(root, "new.txt"))
	if string(got) != "hi\n" {
		t.Fatalf("file = %q", string(got))
	}
}

func TestWriteToolOverwritesAfterUnchangedRead(t *testing.T) {
	root := t.TempDir()
	full := filepath.Join(root, "a.txt")
	_ = os.WriteFile(full, []byte("old\n"), 0o644)

	readState := NewReadStateStore()
	readTool := &ReadTool{Root: root, ReadState: readState}
	if _, err := readTool.Run(context.Background(), []byte(`{"file_path":"a.txt"}`)); err != nil {
		t.Fatal(err)
	}

	writeTool := &WriteTool{Root: root, ReadState: readState}
	_, err := writeTool.Run(context.Background(), []byte(`{"file_path":"a.txt","content":"new\n"}`))
	if err != nil {
		t.Fatalf("overwrite after unchanged read: %v", err)
	}
	got, _ := os.ReadFile(full)
	if string(got) != "new\n" {
		t.Fatalf("file = %q", string(got))
	}
}

func TestWriteToolRejectsStaleWriteAfterExternalModification(t *testing.T) {
	root := t.TempDir()
	full := filepath.Join(root, "a.txt")
	_ = os.WriteFile(full, []byte("old\n"), 0o644)

	readState := NewReadStateStore()
	readTool := &ReadTool{Root: root, ReadState: readState}
	_, _ = readTool.Run(context.Background(), []byte(`{"file_path":"a.txt"}`))

	// User/IDE changes the file after the model's Read.
	_ = os.WriteFile(full, []byte("user-changed\n"), 0o644)

	writeTool := &WriteTool{Root: root, ReadState: readState}
	_, err := writeTool.Run(context.Background(), []byte(`{"file_path":"a.txt","content":"model-rewrite\n"}`))
	if err == nil || !strings.Contains(err.Error(), "modified since last read") {
		t.Fatalf("err = %v, want stale-write error", err)
	}
	// File must be untouched on a stale-write rejection.
	got, _ := os.ReadFile(full)
	if string(got) != "user-changed\n" {
		t.Fatalf("file = %q, want user content preserved", string(got))
	}
}
